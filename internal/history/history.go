// Package history reads an account's transactions and token transfers from a history source (the
// local node, or a remote full node read-only) and writes them as CSV.
package history

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"io"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/CreditsCommunity/wallet/internal/amount"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
	"github.com/CreditsCommunity/wallet/thrift/api"
)

// Entry is one transaction seen from the account.
type Entry struct {
	Time         time.Time     `json:"time"`
	Block        int64         `json:"block"`
	Index        int32         `json:"index"`
	Kind         string        `json:"kind"`
	Direction    string        `json:"direction"` // "out", "in" or "self"
	Counterparty string        `json:"counterparty"`
	Amount       amount.Amount `json:"-"` // signed: negative when it leaves the account
	Fee          amount.Amount `json:"-"` // the fee charged (paid by the source)
	InnerID      int64         `json:"inner_id"`
	// Data is the transfer's user data (a text), when it has one.
	Data string `json:"data,omitempty"`
	// Until is the expiry of a delegation with a time limit.
	Until time.Time `json:"delegation_until,omitzero"`
	// Token transfers: the token's address and code, and the amount moved (decimal text, signed
	// like Amount).
	Token       string `json:"token,omitempty"`
	TokenCode   string `json:"token_code,omitempty"`
	TokenAmount string `json:"token_amount,omitempty"`
}

// Page is a page of entries.
type Page struct {
	Entries []Entry
	Offset  int
	Total   int // the account's transaction count on the source (it may hold fewer: pruned)
}

// Source is where the history comes from: only reads.
type Source interface {
	// Describe names the source: "local node 127.0.0.1:9090" or "remote full node ... (read-only)".
	Describe() string
	// Remote tells whether the source is a remote full node rather than the local node.
	Remote() bool
	// Page returns transactions offset..offset+limit-1, newest first.
	Page(ctx context.Context, account ed25519.PublicKey, offset, limit int) (Page, error)
	// OldestBlock is the oldest block the source holds (1 on a full node).
	OldestBlock(ctx context.Context) (int64, error)
	// TokenTransfers returns every token transfer of the account, newest first, for the tokens it
	// holds and the extra ones given. node.ErrNoTokenCache when the source keeps no token cache.
	TokenTransfers(ctx context.Context, account ed25519.PublicKey, extra []ed25519.PublicKey) ([]Entry, error)
}

var kinds = map[api.TransactionType]string{
	api.TransactionType_TT_Transfer:          "transfer",
	api.TransactionType_TT_ContractDeploy:    "deploy",
	api.TransactionType_TT_ContractCall:      "call",
	api.TransactionType_TT_ContractState:     "contract state",
	api.TransactionType_TT_ContractReplenish: "contract payment",
	api.TransactionType_TT_TokenDeploy:       "token deploy",
	api.TransactionType_TT_TokenTransfer:     "token transfer",
	api.TransactionType_TT_Delegation:        "delegation",
	api.TransactionType_TT_RevokeDelegation:  "delegation withdrawn",
	api.TransactionType_TT_Release:           "delegation released",
	api.TransactionType_TT_ContractEmitted:   "contract emitted",
}

// Kind names a transaction type.
func Kind(t api.TransactionType) string {
	if k, ok := kinds[t]; ok {
		return k
	}
	return "type " + strconv.Itoa(int(t))
}

// delegationValue reads the delegated field as the node returns it in userFields (flag 0, count 1,
// id 5, type 1, u64).
func delegationValue(uf []byte) (uint64, bool) {
	if len(uf) != 15 || uf[0] != 0 || uf[1] != 1 || binary.LittleEndian.Uint32(uf[2:6]) != 5 || uf[6] != 1 {
		return 0, false
	}
	return binary.LittleEndian.Uint64(uf[7:]), true
}

// userText is the user data of a transfer: the node returns the text field as it is. Text that is
// not UTF-8 is shown as hex.
func userText(uf []byte) string {
	if len(uf) == 0 || uf[0] == 0 {
		return ""
	}
	if utf8.Valid(uf) {
		return string(uf)
	}
	return "0x" + hex.EncodeToString(uf)
}

// FromSealed reads a transaction of the node as seen from account.
func FromSealed(account ed25519.PublicKey, s *api.SealedTransaction) Entry {
	var e Entry
	if s == nil || s.Trxn == nil {
		return e
	}
	t := s.Trxn
	if s.ID != nil {
		e.Block, e.Index = s.ID.PoolSeq, s.ID.Index
	}
	e.Time = time.UnixMilli(int64(t.TimeCreation)).UTC()
	e.Kind = Kind(t.Type)
	e.InnerID = int64(t.ID)
	a := amount.FromThrift(t.Amount)
	out := bytes.Equal(t.Source, account)
	in := bytes.Equal(t.Target, account)
	switch {
	case out && in:
		e.Direction, e.Counterparty, e.Amount = "self", keys.Address([]byte(t.Target)), amount.Amount{}
	case out:
		e.Direction, e.Counterparty, e.Amount = "out", keys.Address([]byte(t.Target)), a.Neg()
	default:
		e.Direction, e.Counterparty, e.Amount = "in", keys.Address([]byte(t.Source)), a
	}
	switch t.Type {
	case api.TransactionType_TT_Delegation, api.TransactionType_TT_RevokeDelegation, api.TransactionType_TT_Release:
		// a delegation takes the CS from the delegator's balance; a withdrawal gives them back
		if t.Type == api.TransactionType_TT_RevokeDelegation && e.Direction != "self" {
			e.Amount = e.Amount.Neg()
		}
		if v, ok := delegationValue(t.UserFields); ok && v >= 4 {
			e.Until = time.Unix(int64(v), 0).UTC()
		}
	case api.TransactionType_TT_Transfer:
		e.Data = userText(t.UserFields)
	case api.TransactionType_TT_TokenTransfer:
		if info := t.SmartInfo; info != nil && info.VTokenTransfer != nil {
			x := info.VTokenTransfer
			e.Token, e.TokenCode = keys.Address([]byte(t.Target)), string(x.Code)
			e.TokenAmount = signedText(string(x.Amount), bytes.Equal(x.Sender, account) && !bytes.Equal(x.Receiver, account))
			if bytes.Equal(x.Receiver, account) && !bytes.Equal(x.Sender, account) {
				e.Direction, e.Counterparty = "in", keys.Address([]byte(x.Sender))
			} else if len(x.Receiver) == ed25519.PublicKeySize {
				e.Counterparty = keys.Address([]byte(x.Receiver))
			}
		}
	}
	if t.Fee != nil {
		e.Fee = amount.Commission(uint16(t.Fee.Commission)).Amount()
	}
	return e
}

func signedText(amt string, out bool) string {
	if out && amt != "" && amt[0] != '-' {
		return "-" + amt
	}
	return amt
}

var zeroKey = make([]byte, ed25519.PublicKeySize)

// FromTokenTransfer reads a token transfer as seen from account.
func FromTokenTransfer(account ed25519.PublicKey, t *api.TokenTransfer) Entry {
	var e Entry
	if t == nil {
		return e
	}
	sender := t.Sender
	if len(sender) != ed25519.PublicKeySize || bytes.Equal(sender, zeroKey) {
		sender = t.Initiator // transfer(): the initiator sends
	}
	if t.Transaction != nil {
		e.Block, e.Index = t.Transaction.PoolSeq, t.Transaction.Index
	}
	e.Time = time.UnixMilli(int64(t.Time)).UTC()
	e.Kind = "token transfer"
	e.Token, e.TokenCode = keys.Address([]byte(t.Token)), string(t.Code)
	out := bytes.Equal(sender, account)
	in := bytes.Equal(t.Receiver, account)
	switch {
	case out && in:
		e.Direction, e.Counterparty, e.TokenAmount = "self", keys.Address([]byte(t.Receiver)), string(t.Amount)
	case out:
		e.Direction, e.Counterparty, e.TokenAmount = "out", keys.Address([]byte(t.Receiver)), signedText(string(t.Amount), true)
	default:
		e.Direction, e.Counterparty, e.TokenAmount = "in", keys.Address([]byte(sender)), string(t.Amount)
	}
	if out && t.Fee != nil {
		e.Fee = amount.Commission(uint16(t.Fee.Commission)).Amount()
	}
	if bytes.Equal(t.Initiator, account) || out {
		e.Data = userText(t.UserFields)
	}
	return e
}

// Merge adds the token transfers to the CS transactions: a token transfer the account initiated is
// also one of its transactions (the contract call), which gets the token's details; the others
// (received) are added. Newest first.
func Merge(cs, tokens []Entry) []Entry {
	at := map[[2]int64]int{}
	out := append([]Entry(nil), cs...)
	for i, e := range out {
		if e.Block > 0 {
			at[[2]int64{e.Block, int64(e.Index)}] = i
		}
	}
	for _, t := range tokens {
		if i, ok := at[[2]int64{t.Block, int64(t.Index)}]; ok && t.Block > 0 {
			e := &out[i]
			e.Kind, e.Token, e.TokenCode, e.TokenAmount = t.Kind, t.Token, t.TokenCode, t.TokenAmount
			e.Direction, e.Counterparty = t.Direction, t.Counterparty
			continue
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Block != out[j].Block {
			return out[i].Block > out[j].Block
		}
		return out[i].Index > out[j].Index
	})
	return out
}

// TokensSent lists the token contracts of the account's outgoing token transfers among entries.
func TokensSent(entries []Entry) []ed25519.PublicKey {
	var out []ed25519.PublicKey
	seen := map[string]bool{}
	for _, e := range entries {
		addr := e.Token
		if addr == "" && e.Kind == "token transfer" && e.Direction == "out" {
			addr = e.Counterparty
		}
		if addr == "" || seen[addr] {
			continue
		}
		if pub, err := keys.ParseAddress(addr); err == nil {
			seen[addr] = true
			out = append(out, pub)
		}
	}
	return out
}

// CSVHeader is the first line of an export.
var CSVHeader = []string{"time_utc", "block", "index", "kind", "direction", "account", "counterparty", "amount_cs", "fee_cs",
	"inner_id", "token", "token_code", "token_amount", "data", "delegation_until_utc"}

// WriteCSV writes entries as CSV with a header line. Amounts are exact decimals with '.'.
func WriteCSV(w io.Writer, account string, entries []Entry) error {
	c := csv.NewWriter(w)
	if err := c.Write(CSVHeader); err != nil {
		return err
	}
	for _, e := range entries {
		until := ""
		if !e.Until.IsZero() {
			until = e.Until.Format(time.RFC3339)
		}
		if err := c.Write([]string{e.Time.Format(time.RFC3339), strconv.FormatInt(e.Block, 10), strconv.Itoa(int(e.Index)),
			e.Kind, e.Direction, account, e.Counterparty, e.Amount.String(), e.Fee.String(), strconv.FormatInt(e.InnerID, 10),
			e.Token, e.TokenCode, e.TokenAmount, csvSafe(e.Data), until}); err != nil {
			return err
		}
	}
	c.Flush()
	return c.Error()
}

// csvSafe keeps user data, which anyone who sends to the account chooses, from being read as a
// formula by a spreadsheet.
func csvSafe(s string) string {
	if s != "" && (s[0] == '=' || s[0] == '+' || s[0] == '-' || s[0] == '@' || s[0] == '\t' || s[0] == '\r') {
		return "'" + s
	}
	return s
}

// NodeSource is the history a node holds: the local node, or a remote full node (read-only).
type NodeSource struct {
	Node     *node.Client
	IsRemote bool
}

// Local is the local node's history.
func Local(n *node.Client) NodeSource { return NodeSource{Node: n} }

// RemoteNode is a remote full node's history: it is only read.
func RemoteNode(n *node.Client) NodeSource { return NodeSource{Node: n, IsRemote: true} }

// Describe implements Source.
func (s NodeSource) Describe() string {
	if s.IsRemote {
		return "remote full node " + s.Node.Address + " (read-only)"
	}
	return "local node " + s.Node.Address
}

// Remote implements Source.
func (s NodeSource) Remote() bool { return s.IsRemote }

// Page implements Source.
func (s NodeSource) Page(ctx context.Context, account ed25519.PublicKey, offset, limit int) (Page, error) {
	list, total, err := s.Node.Transactions(ctx, account, int64(offset), int64(limit))
	if err != nil {
		return Page{}, err
	}
	p := Page{Offset: offset, Total: total, Entries: make([]Entry, 0, len(list))}
	for _, x := range list {
		p.Entries = append(p.Entries, FromSealed(account, x))
	}
	return p, nil
}

// OldestBlock implements Source.
func (s NodeSource) OldestBlock(ctx context.Context) (int64, error) {
	st, err := s.Node.Status(ctx)
	if err != nil {
		return 0, err
	}
	return s.Node.OldestBlock(ctx, st.LastBlock)
}

// TokenTransfers implements Source.
func (s NodeSource) TokenTransfers(ctx context.Context, account ed25519.PublicKey, extra []ed25519.PublicKey) ([]Entry, error) {
	held, err := s.Node.TokenBalances(ctx, account)
	if err != nil {
		return nil, err
	}
	tokens := append([]ed25519.PublicKey(nil), extra...)
	for _, b := range held {
		tokens = append(tokens, b.Token)
	}
	seen := map[string]bool{}
	var out []Entry
	for _, token := range tokens {
		if seen[string(token)] {
			continue
		}
		seen[string(token)] = true
		for offset := int64(0); ; offset += 100 {
			list, count, err := s.Node.TokenTransfers(ctx, token, account, offset, 100)
			if errors.Is(err, node.ErrNotAToken) {
				break // an extra address that is not a token
			}
			if err != nil {
				return out, err
			}
			for _, t := range list {
				out = append(out, FromTokenTransfer(account, t))
			}
			if len(list) < 100 || offset+100 >= int64(count) {
				break
			}
		}
	}
	return Merge(nil, out), nil
}

// All reads every transaction the source returns for the account, page by page (for CSV).
func All(ctx context.Context, src Source, account ed25519.PublicKey, pageSize int) ([]Entry, error) {
	var all []Entry
	for offset := 0; ; offset += pageSize {
		p, err := src.Page(ctx, account, offset, pageSize)
		if err != nil {
			return all, err
		}
		all = append(all, p.Entries...)
		if len(p.Entries) < pageSize || len(all) >= p.Total {
			return all, nil
		}
	}
}

// AllWithTokens is All with the token transfers merged in. When the source keeps no token cache
// the CS transactions are returned with node.ErrNoTokenCache as tokenErr.
func AllWithTokens(ctx context.Context, src Source, account ed25519.PublicKey, pageSize int, extra []ed25519.PublicKey) (entries []Entry, tokenErr, err error) {
	cs, err := All(ctx, src, account, pageSize)
	if err != nil {
		return cs, nil, err
	}
	tokens, tokenErr := src.TokenTransfers(ctx, account, append(TokensSent(cs), extra...))
	if tokenErr != nil {
		return cs, tokenErr, nil
	}
	return Merge(cs, tokens), nil, nil
}
