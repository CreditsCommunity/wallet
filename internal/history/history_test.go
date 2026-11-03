package history

import (
	"crypto/ed25519"
	"strings"
	"testing"

	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/thrift/api"
	"github.com/CreditsCommunity/wallet/thrift/general"
)

func TestFromSealedAndCSV(t *testing.T) {
	me := ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
	other := ed25519.NewKeyFromSeed(append(make([]byte, 31), 1)).Public().(ed25519.PublicKey)
	sealed := func(src, dst ed25519.PublicKey, typ api.TransactionType) *api.SealedTransaction {
		return &api.SealedTransaction{ID: &api.TransactionId{PoolSeq: 187210331, Index: 2}, Trxn: &api.Transaction{
			ID: 9, Source: general.Address(src), Target: general.Address(dst), Amount: &general.Amount{Integral: 12, Fraction: 5e17},
			Fee: &api.AmountCommission{Commission: 17279}, TimeCreation: 1793714440560, Type: typ}}
	}
	out := FromSealed(me, sealed(me, other, api.TransactionType_TT_Transfer))
	if out.Direction != "out" || out.Amount.String() != "-12.5" || out.Counterparty != keys.Address(other) ||
		out.Fee.String() != "0.008740234375" || out.Block != 187210331 || out.Kind != "transfer" || out.InnerID != 9 {
		t.Errorf("out: %+v", out)
	}
	in := FromSealed(me, sealed(other, me, api.TransactionType_TT_Delegation))
	if in.Direction != "in" || in.Amount.String() != "12.5" || in.Kind != "delegation" {
		t.Errorf("in: %+v", in)
	}
	if k := Kind(api.TransactionType(99)); k != "type 99" {
		t.Error(k)
	}
	var b strings.Builder
	if err := WriteCSV(&b, keys.Address(me), []Entry{out, in}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "time_utc,block,") ||
		!strings.Contains(lines[1], "2026-11-03T") || !strings.Contains(lines[1], ",187210331,2,transfer,out,") ||
		!strings.Contains(lines[1], ",-12.5,0.008740234375,9") {
		t.Errorf("CSV:\n%s", b.String())
	}
}
