package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// account address prints the address alone (for xclip, pbcopy, clip).
func TestAccountAddress(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CREDITS_WALLET_HOME", home)
	keyFile := filepath.Join(home, "key.txt")
	os.WriteFile(keyFile, []byte(testKey+"\n"), 0o600)
	pwFile := filepath.Join(home, "pw")
	os.WriteFile(pwFile, []byte("pw\n"), 0o600)
	if _, code := runWallet(t, "account", "import", "main", keyFile, "--password-file", pwFile); code != 0 {
		t.Fatal("import")
	}
	out, code := runWallet(t, "account", "address", "main")
	if code != 0 || out != testAddr+"\n" {
		t.Fatalf("account address: %d %q", code, out)
	}
	out, code = runWallet(t, "account", "address", "main", "--json")
	var got map[string]string
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || got["address"] != testAddr || got["name"] != "main" {
		t.Fatalf("--json: %d %s", code, out)
	}
	if _, errOut, code := runWallet2(t, "account", "address", "nobody"); code == 0 || !strings.Contains(errOut, "no account named") {
		t.Fatalf("unknown account: %d %s", code, errOut)
	}
}
