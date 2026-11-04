package keys

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mr-tron/base58"
)

// The node's key files are what the node writes (std::ofstream, no newline): base58 of the 64-byte
// secret key, and base58 of the public key.
func TestNodeKeyFilesByteExact(t *testing.T) {
	key, err := FromBase58(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	priv, pub := NodeKeyFiles(key)
	if string(priv) != testSecret || string(pub) != testAddress {
		t.Fatalf("got %q %q", priv, pub)
	}
	// from the node's own seed vectors: words -> key -> the files
	for _, v := range loadVectors(t) {
		k, err := FromWords(SplitPhrase(v.Words))
		if err != nil {
			t.Fatal(err)
		}
		secret, _ := hex.DecodeString(v.Secret)
		public, _ := hex.DecodeString(v.Public)
		priv, pub := NodeKeyFiles(k)
		if string(priv) != base58.Encode(secret) || string(pub) != base58.Encode(public) {
			t.Errorf("seed %s: %s %s", v.MasterSeed, priv, pub)
		}
		if bytes.ContainsAny(priv, "\r\n ") || bytes.ContainsAny(pub, "\r\n ") {
			t.Errorf("a newline or a space in the files")
		}
	}
}

// Read as the node reads: the first line, spaces around it skipped; 64 bytes plain, more encrypted
// by the node, fewer refused; the halves of the key must agree.
func TestParseNodePrivate(t *testing.T) {
	for _, text := range []string{testSecret, testSecret + "\n", testSecret + "\r\n", "  " + testSecret + " \nsomething else\n"} {
		k, err := ParseNodePrivate([]byte(text))
		if err != nil || Base58(k) != testSecret {
			t.Errorf("%q: %v", text, err)
		}
	}
	long := base58.Encode(bytes.Repeat([]byte{7}, 120))
	if _, err := ParseNodePrivate([]byte(long)); !errors.Is(err, ErrNodeKeyEncrypted) {
		t.Errorf("an encrypted key: %v", err)
	}
	for _, bad := range []string{"", testAddress, "0OIl", "\n" + testSecret} {
		if _, err := ParseNodePrivate([]byte(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	raw, _ := base58.Decode(testSecret)
	raw[40] ^= 1 // the public half no longer matches the seed
	if _, err := ParseNodePrivate([]byte(base58.Encode(raw))); err == nil {
		t.Error("a key whose halves disagree was accepted")
	}
}

// NodePublic.txt: its first line must be the public key, as text (the node compares strings).
func TestCheckNodePublic(t *testing.T) {
	key, _ := FromBase58(testSecret)
	pub := key.Public().(ed25519.PublicKey)
	dir := t.TempDir()
	p := filepath.Join(dir, NodePublicFile)
	for text, ok := range map[string]bool{testAddress: true, testAddress + "\n": true, testAddress + " ": false, " " + testAddress: false, "x": false} {
		os.WriteFile(p, []byte(text), 0o600)
		if err := CheckNodePublic(p, pub); (err == nil) != ok {
			t.Errorf("%q: %v", text, err)
		}
	}
}

// With NODE_KEY_CHECK set to testdata/node-key-check.cpp built against the node's cscrypto, the
// files are read by the node's own code (its Config::readKeys steps) and the key signs.
func TestNodeKeyFilesWithTheNodesParser(t *testing.T) {
	check := os.Getenv("NODE_KEY_CHECK")
	if check == "" {
		t.Skip("NODE_KEY_CHECK (testdata/node-key-check.cpp built against the node) not set")
	}
	for _, v := range loadVectors(t) {
		k, _ := FromWords(SplitPhrase(v.Words))
		dir := t.TempDir()
		priv, pub := NodeKeyFiles(k)
		os.WriteFile(filepath.Join(dir, NodePrivateFile), priv, 0o600)
		os.WriteFile(filepath.Join(dir, NodePublicFile), pub, 0o600)
		out, err := exec.Command(check, dir).CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != "ok "+string(pub) {
			t.Errorf("seed %s: %s %v", v.MasterSeed, out, err)
		}
	}
}
