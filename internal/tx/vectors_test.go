package tx

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/CreditsCommunity/wallet/internal/amount"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/thrift/general"
)

// testdata/sign-vectors.txt is made by testdata/sign-vectors.py, which builds the transactions the
// way the node's ci/testnet/fund.py and samples/python/token_client.py do (Python, PyNaCl,
// thriftpy2), independently of this package.
func readVectors(t *testing.T) map[string][]byte {
	t.Helper()
	data, err := os.ReadFile("../../testdata/sign-vectors.txt")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		k, v, _ := strings.Cut(line, ":")
		b, err := hex.DecodeString(strings.TrimSpace(v))
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		out[strings.TrimSpace(k)] = b
	}
	return out
}

func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	text, err := os.ReadFile("../../testdata/test-key.txt")
	if err != nil {
		t.Fatal(err)
	}
	key, err := keys.FromBase58(string(text))
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func check(t *testing.T, v map[string][]byte, name string, signed, wire, signature []byte) {
	t.Helper()
	if want := v[name+".signed"]; !bytes.Equal(signed, want) {
		t.Errorf("%s signed\n got %x\nwant %x", name, signed, want)
	}
	if want := v[name+".wire"]; !bytes.Equal(wire, want) {
		t.Errorf("%s userFields\n got %x\nwant %x", name, wire, want)
	}
	if want := v[name+".signature"]; !bytes.Equal(signature, want) {
		t.Errorf("%s signature\n got %x\nwant %x", name, signature, want)
	}
}

func TestDelegationsAndDataByteIdenticalToFundPy(t *testing.T) {
	v := readVectors(t)
	key := testKey(t)
	src := key.Public().(ed25519.PublicKey)
	to, _ := keys.ParseAddress("u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w")
	until := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	value, err := DelegationValue(until, time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	if err != nil || value != 1893456000 {
		t.Fatalf("DelegationValue %d %v", value, err)
	}
	for _, c := range []struct {
		name   string
		inner  int64
		amount string
		deleg  uint64
		data   string
	}{
		{"delegate", 8, "1000", Delegate, ""},
		{"delegate-until", 9, "500", value, ""},
		{"withdraw", 10, "1000", DelegateWithdraw, ""},
		{"data", 11, "12.5", 0, "invoice 42 — grüße\n\tpaid"},
	} {
		amt, _ := amount.ParsePositive(c.amount)
		tr := Transfer{Source: src, Target: to, Amount: amt, InnerID: c.inner, MaxFee: MaxFee, Delegation: c.deleg, Data: c.data}
		sent, signed, err := tr.Sign(key)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		check(t, v, c.name, signed, sent.UserFields, sent.Signature)
		if !ed25519.Verify(src, signed, sent.Signature) {
			t.Errorf("%s: signature does not verify", c.name)
		}
	}
}

func TestTokenCallByteIdenticalToTokenClient(t *testing.T) {
	v := readVectors(t)
	key := testKey(t)
	token, _ := keys.ParseAddress("9vX2wH7ytrbbo1KtNa13dUz3nvtJqaCH4oGrr2NEsBqd")
	to, err := Variant("java.lang.String", "u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w")
	if err != nil {
		t.Fatal(err)
	}
	amt, err := Variant("java.math.BigDecimal", "250.5")
	if err != nil {
		t.Fatal(err)
	}
	call := ContractCall{Source: key.Public().(ed25519.PublicKey), Contract: token, Method: "transfer",
		Params: []*general.Variant{to, amt}, InnerID: 12, MaxFee: MaxFee}
	sent, signed, err := call.Sign(key)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := SerializeInvocation(sent.SmartContract)
	check(t, v, "token-transfer", signed, code, sent.Signature)
	if sent.Amount.Integral != 0 || sent.Amount.Fraction != 0 || sent.SmartContract.ForgetNewState || len(sent.UserFields) != 0 {
		t.Errorf("call fields: %+v", sent)
	}
}

func TestUserFieldRefusals(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	other := ed25519.NewKeyFromSeed(append(make([]byte, 31), 1))
	amt, _ := amount.ParsePositive("5")
	base := Transfer{Source: key.Public().(ed25519.PublicKey), Target: other.Public().(ed25519.PublicKey), Amount: amt, InnerID: 1, MaxFee: MaxFee}
	for name, change := range map[string]func(*Transfer){
		"both":      func(t *Transfer) { t.Data, t.Delegation = "x", Delegate },
		"release":   func(t *Transfer) { t.Delegation = 3 },
		"NUL first": func(t *Transfer) { t.Data = "\x00abc" },
		"control":   func(t *Transfer) { t.Data = "a\x1b[2Jb" },
		"not UTF-8": func(t *Transfer) { t.Data = "\xff\xfe" },
		"too long":  func(t *Transfer) { t.Data = strings.Repeat("a", MaxData+1) },
		"negative":  func(t *Transfer) { t.Amount = amt.Neg() },
	} {
		c := base
		change(&c)
		if _, _, err := c.Sign(key); err == nil {
			t.Errorf("%s: signed", name)
		}
	}
	if _, err := DelegationValue(time.Now().Add(-time.Minute), time.Now()); err == nil {
		t.Error("an expiry in the past was accepted")
	}
	// a plain transfer stays the 86 bytes of M1, with empty userFields
	tr, signed, err := base.Sign(key)
	if err != nil || len(signed) != 86 || len(tr.UserFields) != 0 {
		t.Errorf("plain transfer: %d bytes, userFields %x, %v", len(signed), tr.UserFields, err)
	}
}

func TestVariants(t *testing.T) {
	for _, c := range []struct{ typ, text, show string }{
		{"java.lang.String", "abc", "abc"}, {"java.math.BigDecimal", "+12.50", "12.50"}, {"int", "-7", "-7"},
		{"java.lang.Long", "9000000000", "9000000000"}, {"boolean", "TRUE", "true"}, {"double", "2", "2.0"}, {"byte[]", "0aff", "0aff"},
	} {
		v, err := Variant(c.typ, c.text)
		if err != nil || Show(v) != c.show {
			t.Errorf("%s %q: %q %v", c.typ, c.text, Show(v), err)
		}
	}
	for _, c := range [][2]string{{"int", "1.5"}, {"java.math.BigDecimal", "1,5"}, {"boolean", "yes"}, {"java.util.List", "x"}} {
		if _, err := Variant(c[0], c[1]); err == nil {
			t.Errorf("%v accepted", c)
		}
	}
}
