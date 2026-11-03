package tx

import (
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/CreditsCommunity/wallet/internal/amount"
	"github.com/CreditsCommunity/wallet/internal/keys"
)

// testdata/sign-expected.txt and testdata/test-key.txt come from the node's
// samples/clients/testdata: what every sample client (and ci/testnet/fund.py) signs for
// "sign test-key.txt u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w 12.5 7".
func TestSignByteIdenticalToTheNodeSamples(t *testing.T) {
	keyText, err := os.ReadFile("../../testdata/test-key.txt")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("../../testdata/sign-expected.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(expected)), "\n") {
		k, v, _ := strings.Cut(line, ":")
		want[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	key, err := keys.FromBase58(string(keyText))
	if err != nil {
		t.Fatal(err)
	}
	to, _ := keys.ParseAddress("u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w")
	amt, _ := amount.ParsePositive("12.5")
	tr := Transfer{Source: key.Public().(ed25519.PublicKey), Target: to, Amount: amt, InnerID: 7, MaxFee: MaxFee}
	sent, signed, err := tr.Sign(key)
	if err != nil {
		t.Fatal(err)
	}
	if got := keys.Address([]byte(sent.Source)); got != want["source"] {
		t.Errorf("source %s, want %s", got, want["source"])
	}
	if got := hex.EncodeToString(signed); got != want["signed"] {
		t.Errorf("signed\n got %s\nwant %s", got, want["signed"])
	}
	if got := hex.EncodeToString(sent.Signature); got != want["signature"] {
		t.Errorf("signature\n got %s\nwant %s", got, want["signature"])
	}
	if !ed25519.Verify([]byte(sent.Source), signed, sent.Signature) || len(signed) != 86 {
		t.Error("signature does not verify")
	}
	if sent.ID != 7 || sent.Amount.Integral != 12 || sent.Amount.Fraction != 5e17 || sent.Currency != 1 || int(sent.Fee.Commission) != 0x4c66 {
		t.Errorf("transaction fields: %+v", sent)
	}
}

func TestSignRefusals(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	other := ed25519.NewKeyFromSeed(append(make([]byte, 31), 1))
	amt, _ := amount.ParsePositive("1")
	base := Transfer{Source: key.Public().(ed25519.PublicKey), Target: other.Public().(ed25519.PublicKey), Amount: amt, InnerID: 1, MaxFee: MaxFee}
	if _, _, err := base.Sign(other); err == nil {
		t.Error("signed with another key")
	}
	for name, change := range map[string]func(*Transfer){
		"inner id 0":   func(t *Transfer) { t.InnerID = 0 },
		"inner id big": func(t *Transfer) { t.InnerID = MaxInnerID + 1 },
		"short target": func(t *Transfer) { t.Target = t.Target[:31] },
	} {
		c := base
		change(&c)
		if _, _, err := c.Sign(key); err == nil {
			t.Errorf("%s: signed", name)
		}
	}
}
