package wallet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A node's key file is never to be deleted; any other plain key file is.
func TestPlainKeyAdvice(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "NodePrivate.txt")
	if a := PlainKeyAdvice(node); !strings.Contains(a, "keep it, the node needs it") || strings.Contains(a, "delete") {
		t.Errorf("node key: %s", a)
	}
	os.WriteFile(filepath.Join(dir, "NodePublic.txt"), []byte("x"), 0o600)
	if a := PlainKeyAdvice(filepath.Join(dir, "renamed.txt")); !strings.Contains(a, "node needs it") {
		t.Errorf("a key next to NodePublic.txt: %s", a)
	}
	if a := PlainKeyAdvice(filepath.Join(t.TempDir(), "keys.json")); !strings.Contains(a, "delete it") {
		t.Errorf("keys JSON: %s", a)
	}
}
