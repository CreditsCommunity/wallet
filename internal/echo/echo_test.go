package echo

import (
	"strings"
	"testing"
)

func TestQuote(t *testing.T) {
	if q := Quote("12,5"); q != `"12,5"` {
		t.Error(q)
	}
	addr := "u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w"
	if q := Quote(addr); !strings.Contains(q, addr) {
		t.Error(q)
	}
	secret := "1GMkH3brNXiNNs1tiFZHu4yZSRrzJwxi5wB9bHFtMikjwpAW9DMZzU2Pqakc5it8X3N5vPmqdN7KF4CCUpmKhq"
	for _, s := range []string{secret, "abandon ability able", secret[:45]} {
		if q := Quote(s); strings.Contains(q, s[:10]) {
			t.Errorf("echoed: %s", q)
		}
	}
}

func TestPrintable(t *testing.T) {
	for in, want := range map[string]string{
		"CTT": "CTT", "Grüße — ok": "Grüße — ok", "a\x1b[2Jb": `a\x1b[2Jb`, "x\ay": `x\ay`, "\xff": `\xff`, `say "hi"`: `say "hi"`, "line\nnext": `line\nnext`,
	} {
		if got := Printable(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if got := Printable(strings.Repeat("a", MaxPrintable+5)); len(got) != MaxPrintable+len("…") {
		t.Errorf("not cut: %d", len(got))
	}
}
