// Package history reads an account's transactions from a history source and writes them as CSV.
// Milestone 1 has one source, the local node; a remote full node comes in milestone 2.
package history

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/csv"
	"io"
	"strconv"
	"time"

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
}

// Page is a page of entries.
type Page struct {
	Entries []Entry
	Offset  int
	Total   int // the account's transaction count on the source (it may hold fewer: pruned)
}

// Source is where the history comes from.
type Source interface {
	// Describe names the source: "local node 127.0.0.1:9090".
	Describe() string
	// Page returns transactions offset..offset+limit-1, newest first.
	Page(ctx context.Context, account ed25519.PublicKey, offset, limit int) (Page, error)
	// OldestBlock is the oldest block the source holds (1 on a full node).
	OldestBlock(ctx context.Context) (int64, error)
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
	if t.Fee != nil {
		e.Fee = amount.Commission(uint16(t.Fee.Commission)).Amount()
	}
	return e
}

// WriteCSV writes entries as CSV with a header line. Amounts are exact decimals with '.'.
func WriteCSV(w io.Writer, account string, entries []Entry) error {
	c := csv.NewWriter(w)
	if err := c.Write([]string{"time_utc", "block", "index", "kind", "direction", "account", "counterparty", "amount_cs", "fee_cs", "inner_id"}); err != nil {
		return err
	}
	for _, e := range entries {
		if err := c.Write([]string{e.Time.Format(time.RFC3339), strconv.FormatInt(e.Block, 10), strconv.Itoa(int(e.Index)),
			e.Kind, e.Direction, account, e.Counterparty, e.Amount.String(), e.Fee.String(), strconv.FormatInt(e.InnerID, 10)}); err != nil {
			return err
		}
	}
	c.Flush()
	return c.Error()
}

// LocalNode is the history the local node holds.
type LocalNode struct{ Node *node.Client }

// Describe implements Source.
func (l LocalNode) Describe() string { return "node " + l.Node.Address }

// Page implements Source.
func (l LocalNode) Page(ctx context.Context, account ed25519.PublicKey, offset, limit int) (Page, error) {
	list, total, err := l.Node.Transactions(ctx, account, int64(offset), int64(limit))
	if err != nil {
		return Page{}, err
	}
	p := Page{Offset: offset, Total: total, Entries: make([]Entry, 0, len(list))}
	for _, s := range list {
		p.Entries = append(p.Entries, FromSealed(account, s))
	}
	return p, nil
}

// OldestBlock implements Source.
func (l LocalNode) OldestBlock(ctx context.Context) (int64, error) {
	st, err := l.Node.Status(ctx)
	if err != nil {
		return 0, err
	}
	return l.Node.OldestBlock(ctx, st.LastBlock)
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
