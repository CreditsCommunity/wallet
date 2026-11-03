package node

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"github.com/CreditsCommunity/wallet/thrift/api"
	"github.com/CreditsCommunity/wallet/thrift/general"
)

func TestNextInnerIDStatuses(t *testing.T) {
	res := func(code int8, last int64) *api.WalletTransactionsCountGetResult_ {
		return &api.WalletTransactionsCountGetResult_{Status: &general.APIResponse{Code: code, Message: "m"}, LastTransactionInnerId: api.TransactionInnerId(last)}
	}
	if n, err := nextInnerID(res(0, 41)); err != nil || n != 42 {
		t.Errorf("success: %d %v", n, err)
	}
	if n, err := nextInnerID(res(3, 0)); err != nil || n != 1 {
		t.Errorf("not found (a new wallet): %d %v", n, err)
	}
	for _, code := range []int8{1, 2, 4, 9} {
		if n, err := nextInnerID(res(code, 41)); err == nil {
			t.Errorf("code %d gave inner id %d", code, n)
		}
	}
	if _, err := nextInnerID(&api.WalletTransactionsCountGetResult_{}); err == nil {
		t.Error("missing status accepted")
	}
	if _, err := nextInnerID(nil); err == nil {
		t.Error("nil answer accepted")
	}
	if _, err := nextInnerID(res(0, MaxInnerID)); err == nil {
		t.Error("an inner id past 46 bits")
	}
	if _, err := nextInnerID(res(0, -5)); err == nil {
		t.Error("a negative inner id")
	}
}

func TestCheckMissingStatus(t *testing.T) {
	if check("X", nil) == nil {
		t.Error("a missing status is success")
	}
	if check("X", &general.APIResponse{Code: 1}) == nil {
		t.Error("failure is success")
	}
	if check("X", &general.APIResponse{}) != nil {
		t.Error("success is an error")
	}
}

func TestHasTransaction(t *testing.T) {
	me := ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
	other := ed25519.NewKeyFromSeed(append(make([]byte, 31), 1)).Public().(ed25519.PublicKey)
	list := []*api.SealedTransaction{nil, {Trxn: &api.Transaction{ID: 7, Source: general.Address(other)}},
		{Trxn: &api.Transaction{ID: 8, Source: general.Address(me)}}}
	if !HasTransaction(list, me, 8) || HasTransaction(list, me, 7) || HasTransaction(list, me, 9) {
		t.Error("HasTransaction")
	}
}

func TestJudge(t *testing.T) {
	now := time.Now()
	if ok, _ := Judge(100, 101, now, now); !ok {
		t.Error("head one round behind is synced")
	}
	if ok, _ := Judge(100, 200, now, now); ok {
		t.Error("100 rounds behind is synced")
	}
	if ok, _ := Judge(100, 101, now.Add(-time.Hour), now); ok {
		t.Error("an hour-old head is synced")
	}
	if ok, _ := Judge(0, 0, time.Time{}, now); ok {
		t.Error("an empty node is synced")
	}
}

// A message from the node is untrusted text: it reaches the terminal escaped.
func TestAPIErrorEscapesTheNodeMessage(t *testing.T) {
	err := check("TransactionFlow", &general.APIResponse{Code: 1, Message: "bad\x1b]0;owned\a\u009b2J"})
	if msg := err.Error(); strings.ContainsAny(msg, "\x1b\a\u009b") || !strings.Contains(msg, `bad\x1b]0;owned\a\u009b2J`) {
		t.Errorf("%q", msg)
	}
}
