package amount

import (
	"math/big"
	"testing"

	"github.com/CreditsCommunity/wallet/thrift/general"
)

func TestParse(t *testing.T) {
	good := map[string]string{ // text -> units
		"12.5":                 "12500000000000000000",
		"1000":                 "1000000000000000000000",
		"0.000000000000000001": "1",
		".5":                   "500000000000000000",
		"7.":                   "7000000000000000000",
		"0":                    "0",
		" 3.25 ":               "3250000000000000000",
	}
	for text, want := range good {
		a, err := Parse(text)
		if err != nil {
			t.Fatalf("Parse(%q): %v", text, err)
		}
		if a.Units().String() != want {
			t.Errorf("Parse(%q) = %s units, want %s", text, a.Units(), want)
		}
	}
	for _, text := range []string{"", ".", "-1", "+1", "1e3", "1,5", "1,000", "0x10", "1.2.3", "abc",
		"0.0000000000000000001", "1 000"} {
		if _, err := Parse(text); err == nil {
			t.Errorf("Parse(%q): no error", text)
		}
	}
	for _, text := range []string{"0", "0.000", "2147483648", "99999999999"} {
		if _, err := ParsePositive(text); err == nil {
			t.Errorf("ParsePositive(%q): no error", text)
		}
	}
	if _, err := ParsePositive("2147483647.999999999999999999"); err != nil {
		t.Errorf("largest amount: %v", err)
	}
}

func TestFormat(t *testing.T) {
	a, _ := Parse("12345.678")
	if a.String() != "12345.678" || a.Grouped() != "12,345.678" || a.Fixed(2) != "12,345.68" || a.Fixed(0) != "12,346" {
		t.Errorf("got %s %s %s %s", a.String(), a.Grouped(), a.Fixed(2), a.Fixed(0))
	}
	var zero Amount
	if zero.String() != "0" || zero.Fixed(3) != "0.000" {
		t.Errorf("zero: %q %q", zero.String(), zero.Fixed(3))
	}
	tiny, _ := Parse("0.000000000000000001")
	if tiny.String() != "0.000000000000000001" || tiny.Neg().String() != "-0.000000000000000001" || tiny.Neg().Fixed(2) != "0.00" {
		t.Errorf("tiny: %q %q %q", tiny.String(), tiny.Neg().String(), tiny.Neg().Fixed(2))
	}
	m, _ := Parse("1000000")
	if m.Neg().Grouped() != "-1,000,000" {
		t.Errorf("million: %q", m.Neg().Grouped())
	}
}

func TestThrift(t *testing.T) {
	a, _ := Parse("12.5")
	th, err := a.Thrift()
	if err != nil || th.Integral != 12 || th.Fraction != 500_000_000_000_000_000 {
		t.Fatalf("Thrift: %+v %v", th, err)
	}
	if FromThrift(th).Cmp(a) != 0 {
		t.Error("round trip")
	}
	// a negative amount from the node: integral -1, fraction 0.5 -> -0.5
	n := FromThrift(&general.Amount{Integral: -1, Fraction: 500_000_000_000_000_000})
	if n.String() != "-0.5" {
		t.Errorf("negative: %s", n)
	}
	if FromThrift(nil).Sign() != 0 {
		t.Error("nil")
	}
	if FromUnits(big.NewInt(5)).String() != "0.000000000000000005" {
		t.Error("FromUnits")
	}
}

func TestCommission(t *testing.T) {
	// "1 CS", the max fee of the node's samples and ci/testnet/fund.py: 0x4c66 (sign-expected.txt
	// "664c"), e 19, m 102: 102/1024 * 10 = 0.99609375 CS
	if c := EncodeCommission(1.0); c != 0x4c66 {
		t.Errorf("EncodeCommission(1) = %#x", c)
	}
	if c := Commission(0x4c66).Amount(); c.String() != "0.99609375" {
		t.Errorf("0x4c66 = %s", c)
	}
	// what mainnet's ActualFeeGet answers for a transfer: 17279 = e 16, m 895 -> 0.008740234375
	if c := Commission(17279).Amount(); c.String() != "0.008740234375" || c.Fixed(6) != "0.008740" {
		t.Errorf("17279 = %s", c)
	}
	if c := EncodeCommission(10.0).Amount(); c.String() != "9.9609375" {
		t.Errorf("10 CS = %s", c)
	}
}
