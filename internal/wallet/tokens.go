package wallet

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/CreditsCommunity/wallet/internal/amount"
	"github.com/CreditsCommunity/wallet/internal/history"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
)

// The tokens an account holds. A token is a contract: the chain knows only contracts and their
// states, and the node's token methods answer from a token cache it builds next to the chain
// ([api] tokens_cache=true, off by default). The wallet lists the account's tokens:
//
//  1. from the local node's token cache when it keeps one;
//  2. otherwise from the token cache of the profile's remote history node (read-only: only the
//     token addresses are taken from it, the balances are read from the local node);
//  3. otherwise from the account's own history (the history node and the local node): the
//     contracts it called, deployed, paid or sent tokens of, plus the account's local token list
//     (tokens/<address>.json in the data folder: the tokens the user added, sent, or saw in a
//     token cache). Tokens received from others are not in the account's history: such a list
//     may miss them, and on a pruned node older transactions are gone.
//
// Each balance is then the token contract's balanceOf, run as a free getter on the local node.

// TokenHolding is a token an account holds.
type TokenHolding struct {
	Token    ed25519.PublicKey
	Code     string // what the contract calls itself: anyone can deploy a token with any code
	Name     string
	Decimals int    // -1: unknown
	Balance  string // decimal text, as the contract returns it
	Added    bool   // the user added it to the account's local token list
	Warnings []string
	Err      error // the token or its balance could not be read
}

// TokenSource tells where a token list comes from.
type TokenSource string

const (
	TokensFromCache       TokenSource = "cache"        // the local node's token cache
	TokensFromRemoteCache TokenSource = "remote-cache" // the history node's token cache
	TokensFromHistory     TokenSource = "history"      // the account's history and local list
)

// HistoryNote labels a list found from the history.
const HistoryNote = "found from history (may be incomplete on a pruned node)"

// TokenList is what Tokens found.
type TokenList struct {
	Account  ed25519.PublicKey
	Holdings []TokenHolding
	Source   TokenSource
	Describe string // where the list comes from, for the user
	// Note is set when the list may miss tokens (HistoryNote)
	Note string
	// Zero: tokens found with a balance of 0, not listed (unless added by the user)
	Zero int
	// Scanned: transactions of the history read; Capped: the scan stopped at tokenScanLimit
	Scanned int
	Capped  bool
	// CacheErr: why the local node's token cache was not used (node.ErrNoTokenCache)
	CacheErr error
	// LocalErr: the local token list could not be read or written
	LocalErr error
}

const (
	tokenScanLimit = 1000        // transactions read per history source
	tokenScanTTL   = time.Minute // a list found from history is reused this long
)

type tokenMeta struct {
	code, name string
	decimals   int
	notToken   bool
}

type tokenCaches struct {
	mu   sync.Mutex
	meta map[string]tokenMeta  // by token address: what the contract says of itself
	scan map[string]*TokenList // by account: lists found from history
	at   map[string]time.Time
}

func (c *tokenCaches) getMeta(token ed25519.PublicKey) (tokenMeta, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.meta[string(token)]
	return m, ok
}

func (c *tokenCaches) putMeta(token ed25519.PublicKey, m tokenMeta) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.meta == nil {
		c.meta = map[string]tokenMeta{}
	}
	c.meta[string(token)] = m
}

func (c *tokenCaches) getScan(account ed25519.PublicKey) *TokenList {
	c.mu.Lock()
	defer c.mu.Unlock()
	if l, ok := c.scan[string(account)]; ok && time.Since(c.at[string(account)]) < tokenScanTTL {
		return l
	}
	return nil
}

func (c *tokenCaches) putScan(account ed25519.PublicKey, l *TokenList) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.scan == nil {
		c.scan, c.at = map[string]*TokenList{}, map[string]time.Time{}
	}
	c.scan[string(account)], c.at[string(account)] = l, time.Now()
}

func (c *tokenCaches) forget(account ed25519.PublicKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.scan, string(account))
}

// meta reads what a token says of itself (decimals, code, name), once per run.
func (w *Wallet) meta(ctx context.Context, caller, token ed25519.PublicKey) (tokenMeta, error) {
	if m, ok := w.tokens.getMeta(token); ok {
		if m.notToken {
			return m, ErrNotToken
		}
		return m, nil
	}
	tp, _, err := w.TokenMethods(ctx, caller, token)
	if errors.Is(err, ErrNotToken) || errors.Is(err, node.ErrNotAContract) {
		w.tokens.putMeta(token, tokenMeta{notToken: true})
		return tokenMeta{}, ErrNotToken
	}
	if err != nil {
		return tokenMeta{}, err
	}
	m := tokenMeta{code: tp.Code, name: tp.Name, decimals: tp.Decimals}
	w.tokens.putMeta(token, m)
	return m, nil
}

// Tokens lists the tokens an account holds, each with its full contract address (see above).
func (w *Wallet) Tokens(ctx context.Context, account ed25519.PublicKey) (*TokenList, error) {
	l := &TokenList{Account: account}
	held, err := w.Node.TokenBalances(ctx, account)
	if err == nil {
		l.Source, l.Describe = TokensFromCache, "the token cache of the local node "+w.Node.Address
		local, lerr := w.LocalTokens(account)
		l.LocalErr = lerr
		seen := make([]ed25519.PublicKey, 0, len(held))
		for _, b := range held {
			h := TokenHolding{Token: b.Token, Code: b.Code, Name: b.Name, Balance: b.Balance, Decimals: -1}
			if m, err := w.meta(ctx, account, b.Token); err == nil {
				h.Decimals = m.decimals
			}
			l.Holdings = append(l.Holdings, h)
			seen = append(seen, b.Token)
		}
		// the tokens of the local list the cache does not name: a cache turned on after the node
		// pruned (or started from a snapshot) misses holders whose last call is older; added ones
		// are shown with their balance, 0 included, the others when they hold some
		w.addLocal(ctx, l, local, account, true)
		if err := w.addLocalTokens(account, seen, "seen"); err != nil && l.LocalErr == nil {
			l.LocalErr = err
		}
		finishTokenList(l)
		return l, nil
	}
	if !errors.Is(err, node.ErrNoTokenCache) {
		return nil, err
	}
	if cached := w.tokens.getScan(account); cached != nil {
		return cached, nil
	}
	l.CacheErr = err
	var candidates []ed25519.PublicKey
	local, lerr := w.LocalTokens(account)
	l.LocalErr = lerr
	for _, t := range local {
		if pub, err := keys.ParseAddress(t.Address); err == nil && t.How != "added" {
			candidates = append(candidates, pub)
		}
	}
	if w.historyNode != nil {
		if remote, err := w.historyNode.TokenBalances(ctx, account); err == nil {
			l.Source = TokensFromRemoteCache
			l.Describe = "the token cache of the remote full node " + w.historyNode.Address + " (read-only); balances from the local node"
			for _, b := range remote {
				candidates = append(candidates, b.Token)
			}
		}
	}
	if l.Source == "" {
		l.Source, l.Note = TokensFromHistory, HistoryNote
		sources := []history.Source{w.History()}
		if w.History().Remote() {
			sources = append(sources, history.Local(w.Node))
		}
		l.Describe = "the account's history on " + sources[0].Describe()
		if len(sources) > 1 {
			l.Describe += " and the local node"
		}
		l.Describe += ", and the tokens it sent or added"
		for _, src := range sources {
			found, n, capped, err := scanTokens(ctx, src, account)
			if err != nil {
				return nil, fmt.Errorf("reading the history on %s: %w", src.Describe(), err)
			}
			candidates = append(candidates, found...)
			l.Scanned += n
			l.Capped = l.Capped || capped
		}
	}
	done := map[string]bool{}
	for _, t := range local {
		if pub, err := keys.ParseAddress(t.Address); err == nil && t.How == "added" {
			done[string(pub)] = true
		}
	}
	for _, token := range candidates {
		if done[string(token)] {
			continue
		}
		done[string(token)] = true
		h, ok := w.holding(ctx, account, token, false)
		if !ok {
			continue
		}
		if h.Err == nil && isZero(h.Balance) {
			l.Zero++
			continue
		}
		l.Holdings = append(l.Holdings, h)
	}
	w.addLocal(ctx, l, local, account, false)
	finishTokenList(l)
	w.tokens.putScan(account, l)
	return l, nil
}

// addLocal adds the tokens of the local list that are not in the list yet: the ones the user added
// with their balance (0 included), and with all the others the ones the account holds some of.
func (w *Wallet) addLocal(ctx context.Context, l *TokenList, local []LocalToken, account ed25519.PublicKey, all bool) {
	in := map[string]int{}
	for i, h := range l.Holdings {
		in[string(h.Token)] = i
	}
	for _, t := range local {
		pub, err := keys.ParseAddress(t.Address)
		added := t.How == "added"
		if err != nil || (!added && !all) {
			continue
		}
		if i, ok := in[string(pub)]; ok {
			l.Holdings[i].Added = l.Holdings[i].Added || added
			continue
		}
		h, ok := w.holding(ctx, account, pub, added)
		if !ok || (!added && (h.Err != nil || isZero(h.Balance))) {
			continue
		}
		in[string(pub)] = len(l.Holdings)
		l.Holdings = append(l.Holdings, h)
	}
}

// holding reads a token's description and the account's balance; ok is false for an address that
// is not a token.
func (w *Wallet) holding(ctx context.Context, account, token ed25519.PublicKey, added bool) (TokenHolding, bool) {
	h := TokenHolding{Token: token, Decimals: -1, Added: added}
	m, err := w.meta(ctx, account, token)
	if errors.Is(err, ErrNotToken) {
		if added {
			h.Err = err
			return h, true
		}
		return h, false
	}
	if err != nil {
		h.Err = err
		return h, true
	}
	h.Code, h.Name, h.Decimals = m.code, m.name, m.decimals
	if h.Balance, err = w.TokenBalanceOf(ctx, token, account); err != nil {
		h.Err = err
	}
	return h, true
}

// finishTokenList adds the look-alike warnings, every token compared with the others.
func finishTokenList(l *TokenList) {
	held := make([]node.TokenBalance, 0, len(l.Holdings))
	for _, h := range l.Holdings {
		held = append(held, node.TokenBalance{Token: h.Token, Code: h.Code, Name: h.Name})
	}
	for i := range l.Holdings {
		h := &l.Holdings[i]
		h.Warnings = TokenWarnings(h.Token, h.Code, h.Name, held)
	}
}

func isZero(balance string) bool {
	c, ok := amount.CompareDecimal(balance, "0")
	return ok && c == 0
}

// scanTokens reads up to tokenScanLimit of the account's latest transactions on src and returns
// the contracts among them that may be tokens: the ones the account called, deployed or paid, and
// the tokens of its token transfers.
func scanTokens(ctx context.Context, src history.Source, account ed25519.PublicKey) (found []ed25519.PublicKey, scanned int, capped bool, err error) {
	const page = 100
	seen := map[string]bool{}
	for offset := 0; offset < tokenScanLimit; offset += page {
		p, err := src.Page(ctx, account, offset, page)
		if err != nil {
			return found, scanned, false, err
		}
		scanned += len(p.Entries)
		for _, e := range p.Entries {
			addr := e.Token
			if addr == "" && (e.Direction == "out" || e.Direction == "self") {
				switch e.Kind {
				case "call", "token transfer", "deploy", "token deploy", "contract payment":
					addr = e.Counterparty
				}
			}
			if addr == "" || seen[addr] {
				continue
			}
			if pub, err := keys.ParseAddress(addr); err == nil && !pub.Equal(account) {
				seen[addr] = true
				found = append(found, pub)
			}
		}
		if len(p.Entries) < page || offset+page >= p.Total {
			return found, scanned, false, nil
		}
	}
	return found, scanned, true, nil
}

// ---- the local token list -----------------------------------------------------------------------

// LocalToken is a token in an account's local list.
type LocalToken struct {
	Address string    `json:"address"`
	Network string    `json:"network"`
	How     string    `json:"how"` // "added" by the user, "sent" by the wallet, "seen" in a token cache
	Since   time.Time `json:"since"`
}

type localTokens struct {
	Version int          `json:"version"`
	Account string       `json:"account"`
	Tokens  []LocalToken `json:"tokens"`
}

// TokensDir is the folder of the local token lists.
func TokensDir(dataDir string) string { return filepath.Join(dataDir, "tokens") }

func (w *Wallet) tokenListPath(account ed25519.PublicKey) string {
	return filepath.Join(TokensDir(w.DataDir), keys.Address(account)+".json")
}

// localTokensAll reads the whole local token list of an account (every network).
func (w *Wallet) localTokensAll(account ed25519.PublicKey) ([]LocalToken, error) {
	if w.DataDir == "" {
		return nil, nil
	}
	path := w.tokenListPath(account)
	data, err := readListFile(path)
	if data == nil || err != nil {
		return nil, err
	}
	var l localTokens
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if l.Account != keys.Address(account) {
		return nil, fmt.Errorf("%s is the list of another account", path)
	}
	out := l.Tokens[:0]
	for _, t := range l.Tokens {
		if _, err := keys.ParseAddress(t.Address); err == nil {
			out = append(out, t)
		}
	}
	return out, nil
}

// LocalTokens is the account's local token list on the profile's network.
func (w *Wallet) LocalTokens(account ed25519.PublicKey) ([]LocalToken, error) {
	all, err := w.localTokensAll(account)
	out := all[:0:0]
	for _, t := range all {
		if t.Network == string(w.Profile.Network) {
			out = append(out, t)
		}
	}
	return out, err
}

// writeLocalTokens changes the local token list with edit; nothing is written when edit reports
// no change.
func (w *Wallet) writeLocalTokens(account ed25519.PublicKey, edit func([]LocalToken) ([]LocalToken, bool)) error {
	if w.DataDir == "" {
		return errors.New("no data folder")
	}
	list, err := w.localTokensAll(account)
	if err != nil {
		return err
	}
	list, changed := edit(list)
	if !changed {
		return nil
	}
	w.tokens.forget(account)
	data, err := json.MarshalIndent(localTokens{Version: 1, Account: keys.Address(account), Tokens: list}, "", "  ")
	if err != nil {
		return err
	}
	return writeListFile(TokensDir(w.DataDir), w.tokenListPath(account), data)
}

// addLocalTokens records tokens in the local list ("added" wins over "sent", which wins over "seen").
func (w *Wallet) addLocalTokens(account ed25519.PublicKey, tokens []ed25519.PublicKey, how string) error {
	if len(tokens) == 0 || w.DataDir == "" {
		return nil
	}
	rank := map[string]int{"seen": 0, "sent": 1, "added": 2}
	network := string(w.Profile.Network)
	return w.writeLocalTokens(account, func(list []LocalToken) ([]LocalToken, bool) {
		changed := false
		for _, token := range tokens {
			addr, found := keys.Address(token), false
			for i := range list {
				if list[i].Address == addr && list[i].Network == network {
					found = true
					if rank[how] > rank[list[i].How] {
						list[i].How, changed = how, true
					}
				}
			}
			if !found {
				list = append(list, LocalToken{Address: addr, Network: network, How: how, Since: time.Now().UTC().Truncate(time.Second)})
				changed = true
			}
		}
		return list, changed
	})
}

// AddToken checks that the address is a token and adds it to the account's local list: it is then
// always listed, with its balance (0 included).
func (w *Wallet) AddToken(ctx context.Context, account ed25519.PublicKey, tokenText string) (TokenHolding, error) {
	token, err := keys.ParseAddress(tokenText)
	if err != nil {
		return TokenHolding{}, fmt.Errorf("token: %w", err)
	}
	h, ok := w.holding(ctx, account, token, true)
	if !ok || errors.Is(h.Err, ErrNotToken) {
		return h, ErrNotToken
	}
	if h.Err != nil {
		return h, h.Err
	}
	h.Warnings = TokenWarnings(token, h.Code, h.Name, nil)
	return h, w.addLocalTokens(account, []ed25519.PublicKey{token}, "added")
}

// RemoveToken takes a token off the account's local list (on the profile's network); it tells
// whether it was there. A token found in the history or a token cache is still listed.
func (w *Wallet) RemoveToken(account ed25519.PublicKey, tokenText string) (bool, error) {
	token, err := keys.ParseAddress(tokenText)
	if err != nil {
		return false, fmt.Errorf("token: %w", err)
	}
	addr, network, removed := keys.Address(token), string(w.Profile.Network), false
	err = w.writeLocalTokens(account, func(list []LocalToken) ([]LocalToken, bool) {
		out := list[:0]
		for _, t := range list {
			if t.Address == addr && t.Network == network {
				removed = true
				continue
			}
			out = append(out, t)
		}
		return out, removed
	})
	return removed, err
}
