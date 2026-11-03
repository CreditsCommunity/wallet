package wallet

import (
	"context"
	"crypto/ed25519"
	"testing"

	"github.com/CreditsCommunity/wallet/internal/history"
	"github.com/CreditsCommunity/wallet/internal/keys"
)

type fakeSource struct{ entries []history.Entry }

func (fakeSource) Describe() string                           { return "fake" }
func (fakeSource) Remote() bool                               { return false }
func (fakeSource) OldestBlock(context.Context) (int64, error) { return 1, nil }
func (fakeSource) TokenTransfers(context.Context, ed25519.PublicKey, []ed25519.PublicKey) ([]history.Entry, error) {
	return nil, nil
}
func (f fakeSource) Page(_ context.Context, _ ed25519.PublicKey, offset, limit int) (history.Page, error) {
	end := min(offset+limit, len(f.entries))
	return history.Page{Entries: f.entries[min(offset, end):end], Offset: offset, Total: len(f.entries)}, nil
}

// The history scan: contracts the account called, deployed or paid, the tokens of its transfers;
// a token it only received is marked as received, not chosen.
func TestScanTokens(t *testing.T) {
	account, _ := keys.ParseAddress("FAe4sisG95oZ42w7buUn5qEE4TAnfTTFPiguZUHmhiF")
	const called, bought, gift, sender, wallet = "9vX2wH7ytrbbo1KtNa13dUz3nvtJqaCH4oGrr2NEsBqd",
		"GPbiGQqMaovXRdz8d5pyCeKyAy7jZcnF9dmoGugvRnjS", "BfuatFLd9C7uhA2e6VEoqP2LCNnC5WVG8TujnZo6S5d6",
		"CW27vNj37fjEcTVEJfy7WKUmzjk6VdMu2z1888Y53JXj", "u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w"
	src := fakeSource{entries: []history.Entry{
		{Kind: "call", Direction: "out", Counterparty: called},
		{Kind: "contract payment", Direction: "out", Counterparty: bought},
		{Kind: "transfer", Direction: "out", Counterparty: wallet}, // a plain transfer: not a candidate
		{Kind: "token transfer", Direction: "in", Counterparty: sender, Token: gift},
		{Kind: "token transfer", Direction: "in", Counterparty: sender, Token: called}, // also called: chosen
	}}
	found, received, n, capped, err := scanTokens(context.Background(), src, account)
	if err != nil || n != 5 || capped {
		t.Fatal(err, n, capped)
	}
	got := map[string]bool{}
	for _, f := range found {
		got[keys.Address(f)] = true
	}
	if len(found) != 3 || !got[called] || !got[bought] || !got[gift] || got[wallet] {
		t.Errorf("candidates: %v", got)
	}
	g, _ := keys.ParseAddress(gift)
	c, _ := keys.ParseAddress(called)
	if received[string(g)] != "received from "+sender+": not added by you" || received[string(c)] != "" {
		t.Errorf("origins: %q / %q", received[string(g)], received[string(c)])
	}
}

// The local list keeps added and sent tokens only.
func TestLocalTokensKinds(t *testing.T) {
	w := &Wallet{DataDir: t.TempDir()}
	account, _ := keys.ParseAddress("FAe4sisG95oZ42w7buUn5qEE4TAnfTTFPiguZUHmhiF")
	tok, _ := keys.ParseAddress("9vX2wH7ytrbbo1KtNa13dUz3nvtJqaCH4oGrr2NEsBqd")
	if err := w.addLocalTokens(account, []ed25519.PublicKey{tok}, "sent"); err != nil {
		t.Fatal(err)
	}
	if l, err := w.LocalTokens(account); err != nil || len(l) != 1 || l[0].How != "sent" {
		t.Fatal(l, err)
	}
	if removed, err := w.RemoveToken(account, keys.Address(tok)); err != nil || !removed {
		t.Fatal(removed, err)
	}
	if l, _ := w.LocalTokens(account); len(l) != 0 {
		t.Fatal(l)
	}
}
