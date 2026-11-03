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
