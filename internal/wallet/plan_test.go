package wallet

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
)

func TestTokenWarnings(t *testing.T) {
	token := ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
	other := ed25519.NewKeyFromSeed(append(make([]byte, 31), 1)).Public().(ed25519.PublicKey)
	if w := TokenWarnings(token, "CTT", "Community Test Token", []node.TokenBalance{{Token: token, Code: "CTT"}}); len(w) != 0 {
		t.Errorf("a plain token warned: %v", w)
	}
	if w := TokenWarnings(token, " cs ", "x", nil); len(w) != 1 || !strings.Contains(w[0], "NOT CS") {
		t.Errorf("code CS: %v", w)
	}
	if w := TokenWarnings(token, "XYZ", "Credits", nil); len(w) != 1 {
		t.Errorf("name Credits: %v", w)
	}
	w := TokenWarnings(token, "ctt", "x", []node.TokenBalance{{Token: other, Code: "CTT"}, {Token: token, Code: "ctt"}})
	if len(w) != 1 || !strings.Contains(w[0], keys.Address(other)) {
		t.Errorf("same code as another held token: %v", w)
	}
}

func TestParseUntil(t *testing.T) {
	for in, want := range map[string]string{
		"2027-01-31":                "2027-01-31T00:00:00Z",
		"2027-01-31 18:00":          "2027-01-31T18:00:00Z",
		"2027-01-31T18:00:00+01:00": "2027-01-31T17:00:00Z",
	} {
		got, err := ParseUntil(in)
		if err != nil || got.Format(time.RFC3339) != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	for _, in := range []string{"tomorrow", "31/01/2027", "9999-01-01"} {
		if _, err := ParseUntil(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestTokenWarningsCatchLookAlikes(t *testing.T) {
	token := ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
	other := ed25519.NewKeyFromSeed(append(make([]byte, 31), 1)).Public().(ed25519.PublicKey)
	for _, code := range []string{
		"\u0421\u0405",           // Cyrillic Es, Dze
		"\uff23\uff33",           // fullwidth CS
		"C\u200bS",               // zero-width space
		"C\u202eS",               // right-to-left override
		"\u03f9S",                // Greek lunate sigma
		"cs\u0301",               // a combining accent
		"\u0421R\u0415D\u0406TS", // CREDITS with Cyrillic letters
	} {
		w := TokenWarnings(token, code, "x", nil)
		if len(w) == 0 || !strings.Contains(w[0], "NOT CS") {
			t.Errorf("%q not taken for CS: %v", code, w)
		}
	}
	// any character outside printable ASCII is warned about by itself
	if w := TokenWarnings(token, "T\u00d6K", "ok", nil); len(w) != 1 || !strings.Contains(w[0], "outside plain ASCII") {
		t.Errorf("non-ASCII code: %v", w)
	}
	if w := TokenWarnings(token, "TOK", "na\u200dme", nil); len(w) != 1 || !strings.Contains(w[0], "outside plain ASCII") {
		t.Errorf("non-ASCII name: %v", w)
	}
	// a held token whose code only looks the same
	if w := TokenWarnings(token, "CTT", "x", []node.TokenBalance{{Token: other, Code: "\u0421TT"}}); len(w) != 1 || !strings.Contains(w[0], keys.Address(other)) {
		t.Errorf("look-alike held code: %v", w)
	}
	if got := Skeleton("\uff23\u200b\u0405"); got != "CS" {
		t.Errorf("skeleton %q", got)
	}
}
