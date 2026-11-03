package history

import (
	"crypto/ed25519"
	"reflect"
	"strings"
	"testing"

	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
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

func TestUserFieldsTokensAndMerge(t *testing.T) {
	me := ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
	other := ed25519.NewKeyFromSeed(append(make([]byte, 31), 1)).Public().(ed25519.PublicKey)
	token := ed25519.NewKeyFromSeed(append(make([]byte, 31), 2)).Public().(ed25519.PublicKey)
	tx := func(seq int64, typ api.TransactionType, src, dst ed25519.PublicKey, uf []byte) *api.SealedTransaction {
		return &api.SealedTransaction{ID: &api.TransactionId{PoolSeq: seq, Index: 0}, Trxn: &api.Transaction{ID: 1,
			Source: general.Address(src), Target: general.Address(dst), Amount: &general.Amount{Integral: 1000},
			Fee: &api.AmountCommission{Commission: 17279}, Type: typ, UserFields: uf}}
	}
	// a delegation until 2030-01-01, as the node returns its user field
	until := []byte{0, 1, 5, 0, 0, 0, 1, 0x80, 0xd8, 0xdb, 0x70, 0, 0, 0, 0}
	d := FromSealed(me, tx(10, api.TransactionType_TT_Delegation, me, other, until))
	if d.Until.Year() != 2030 || d.Amount.String() != "-1000" {
		t.Errorf("delegation: %+v", d)
	}
	w := FromSealed(me, tx(11, api.TransactionType_TT_RevokeDelegation, me, other, []byte{0, 1, 5, 0, 0, 0, 1, 2, 0, 0, 0, 0, 0, 0, 0}))
	if w.Amount.String() != "1000" || !w.Until.IsZero() || w.Kind != "delegation withdrawn" {
		t.Errorf("withdrawal: %+v", w)
	}
	data := FromSealed(me, tx(12, api.TransactionType_TT_Transfer, other, me, []byte("=HYPERLINK(\"x\")")))
	if data.Data != `=HYPERLINK("x")` {
		t.Errorf("data: %+v", data)
	}
	call := FromSealed(me, tx(13, api.TransactionType_TT_TokenTransfer, me, token, nil))
	sent := FromTokenTransfer(me, &api.TokenTransfer{Token: general.Address(token), Code: "CTT", Sender: general.Address(me),
		Receiver: general.Address(other), Amount: "250.5", Initiator: general.Address(me), Transaction: &api.TransactionId{PoolSeq: 13},
		Fee: &api.AmountCommission{Commission: 17279}})
	got := FromTokenTransfer(me, &api.TokenTransfer{Token: general.Address(token), Code: "CTT", Sender: general.Address(other),
		Receiver: general.Address(me), Amount: "7", Initiator: general.Address(other), Transaction: &api.TransactionId{PoolSeq: 14}})
	if sent.TokenAmount != "-250.5" || sent.Direction != "out" || got.TokenAmount != "7" || got.Direction != "in" || !got.Fee.IsZero() {
		t.Errorf("token transfers: %+v %+v", sent, got)
	}
	if ts := TokensSent([]Entry{call}); len(ts) != 1 || !ts[0].Equal(token) {
		t.Errorf("tokens sent: %v", ts)
	}
	all := Merge([]Entry{data, call, w, d}, []Entry{got, sent})
	// the CS entry (the call to the token) keeps its own fields; only the token columns are filled
	if len(all) != 5 || all[0].Block != 14 || all[1].Block != 13 || all[1].TokenAmount != "-250.5" || all[1].TokenCode != "CTT" ||
		all[1].Counterparty != keys.Address(token) || all[1].Kind != call.Kind || all[1].Direction != "out" || all[1].InnerID != 1 {
		t.Fatalf("merge: %+v", all)
	}
	var b strings.Builder
	WriteCSV(&b, keys.Address(me), all)
	if !strings.Contains(b.String(), `,"'=HYPERLINK(""x"")",`) || !strings.Contains(b.String(), ",CTT,-250.5,") || !strings.Contains(b.String(), "2030-01-01T00:00:00Z") {
		t.Errorf("CSV:\n%s", b.String())
	}
}

func TestMergeNeverOverwritesTheCSFields(t *testing.T) {
	cs := Entry{Block: 5, Index: 1, Kind: "call", Direction: "out", Counterparty: "CONTRACT", InnerID: 3}
	spoof := Entry{Block: 5, Index: 1, Kind: "token transfer", Direction: "in", Counterparty: "SOMEONE", Token: "T", TokenCode: "X", TokenAmount: "9"}
	got := Merge([]Entry{cs}, []Entry{spoof})
	if len(got) != 1 || got[0].Kind != "call" || got[0].Direction != "out" || got[0].Counterparty != "CONTRACT" || got[0].InnerID != 3 ||
		got[0].Token != "T" || got[0].TokenCode != "X" || got[0].TokenAmount != "9" {
		t.Errorf("merge: %+v", got)
	}
}

func TestCSVGuardsEveryColumnChosenByOthers(t *testing.T) {
	e := Entry{Kind: "token transfer", Direction: "in", Counterparty: "A", TokenCode: "=cmd|' /C calc'!A0", TokenAmount: "+1+1", Data: "@SUM(1)"}
	var b strings.Builder
	if err := WriteCSV(&b, "ME", []Entry{e}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`,'=cmd|' /C calc'!A0,`, ",'+1+1,", ",'@SUM(1),"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("no %s in\n%s", want, b.String())
		}
	}
	for _, field := range strings.Split(strings.Split(strings.TrimSpace(b.String()), "\n")[1], ",") {
		if f := strings.TrimPrefix(field, `"`); f != "" && strings.ContainsAny(f[:1], "=+@") {
			t.Errorf("a field starts like a formula: %q", field)
		}
	}
	// a negative token amount stays a number
	b.Reset()
	WriteCSV(&b, "ME", []Entry{{TokenAmount: "-250.5", TokenCode: "CTT"}})
	if !strings.Contains(b.String(), ",CTT,-250.5,") {
		t.Errorf("negative amount: %s", b.String())
	}
}

// A history source reaches its node only through Reader, which has no way to send.
func TestHistorySourceCannotSend(t *testing.T) {
	r := reflect.TypeOf((*Reader)(nil)).Elem()
	for _, name := range []string{"Send", "WaitInBlock"} {
		if _, ok := r.MethodByName(name); ok {
			t.Errorf("Reader has %s", name)
		}
	}
	src := reflect.TypeOf(NodeSource{})
	for i := 0; i < src.NumField(); i++ {
		if src.Field(i).IsExported() {
			t.Errorf("NodeSource exports %s", src.Field(i).Name)
		}
	}
	var _ Reader = (*node.Client)(nil) // the node client satisfies it
	var _ Source = NodeSource{}
}
