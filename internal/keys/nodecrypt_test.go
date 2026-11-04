package keys

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mr-tron/base58"
)

// node-encrypted-vectors.json: NodePrivate.txt files encrypted by the node's own code
// (testdata/node-key-crypt.cpp encrypt, i.e. cscrypto's PrivateKey::getEncrypted with the node's
// libsodium) and opened by it (node-key-crypt open) before they were recorded.
type nodeEncVector struct {
	Secret, Public, Password, Encrypted string
}

func loadNodeEncVectors(t *testing.T) []nodeEncVector {
	t.Helper()
	data, err := os.ReadFile("testdata/node-encrypted-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v []nodeEncVector
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// The node's encrypted files open with the node's password, and only with it; the wallet's
// encryption with the same nonce gives the node's bytes exactly.
func TestNodeEncryptedVectors(t *testing.T) {
	if testing.Short() {
		t.Skip("Argon2id with 1 GiB")
	}
	fileKeys := map[string][]byte{} // the salt is fixed: one derivation per password
	for i, v := range loadNodeEncVectors(t) {
		fk := fileKeys[v.Password]
		if fk == nil {
			fk = nodeFileKey([]byte(v.Password))
			fileKeys[v.Password] = fk
		}
		raw, _ := base58.Decode(v.Encrypted)
		key, err := decryptNodeKey(raw, fk)
		if err != nil {
			t.Fatalf("vector %d: %v", i, err)
		}
		if Base58(key) != v.Secret || Address(key.Public().(ed25519.PublicKey)) != v.Public {
			t.Errorf("vector %d: another key", i)
		}
		again, err := encryptNodeKey(key, fk, raw[:8])
		if err != nil || !bytes.Equal(again, raw) {
			t.Errorf("vector %d: the wallet's encryption differs from the node's", i)
		}
		if i == 0 {
			if _, err := OpenNodePrivate([]byte(v.Encrypted), []byte(v.Password+"x")); !errors.Is(err, ErrNodeKeyPassword) {
				t.Errorf("a wrong password: %v", err)
			}
			if _, err := OpenNodePrivate([]byte(v.Encrypted), nil); !errors.Is(err, ErrNodeKeyEncrypted) {
				t.Errorf("no password: %v", err)
			}
			raw[20] ^= 1
			if _, err := decryptNodeKey(raw, fk); !errors.Is(err, ErrNodeKeyPassword) {
				t.Errorf("a changed file: %v", err)
			}
		}
	}
}

// The round trip with the node's code: with NODE_KEY_CRYPT set to testdata/node-key-crypt.cpp
// built against the node, a key the wallet encrypts is opened by the node's readFromEncrypted, and
// a key the node encrypts (a new random nonce) is opened by the wallet.
func TestNodeEncryptedRoundTripWithTheNode(t *testing.T) {
	tool := os.Getenv("NODE_KEY_CRYPT")
	if tool == "" {
		t.Skip("NODE_KEY_CRYPT (testdata/node-key-crypt.cpp built against the node) not set")
	}
	key, _ := FromBase58(testSecret)
	pw := "round trip ~ 42"
	// wallet -> node
	dir := t.TempDir()
	enc, err := EncryptNodeKey(key, []byte(pw), nil)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, NodePrivateFile), []byte(base58.Encode(enc)), 0o600)
	out, err := exec.Command(tool, "open", dir, pw).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ok "+testAddress {
		t.Errorf("the node does not open the wallet's encryption: %s %v", out, err)
	}
	if out, err := exec.Command(tool, "open", dir, pw+"!").CombinedOutput(); err == nil {
		t.Errorf("the node opened it with another password: %s", out)
	}
	// node -> wallet
	dir = t.TempDir()
	p := filepath.Join(dir, NodePrivateFile)
	os.WriteFile(p, []byte(testSecret), 0o600)
	if out, err := exec.Command(tool, "encrypt", dir, pw).CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	data, _ := os.ReadFile(p)
	got, err := OpenNodePrivate(data, []byte(pw))
	if err != nil || Base58(got) != testSecret {
		t.Errorf("the wallet does not open the node's encryption: %v", err)
	}
}

// Light parameters (the tests of the other packages use them too): the format, not the cost.
func TestNodeEncryptedFormat(t *testing.T) {
	old := NodeKDF
	defer func() { NodeKDF = old }()
	NodeKDF.Time, NodeKDF.MemoryKiB = 1, 64
	key, _ := FromBase58(testSecret)
	enc, err := EncryptNodeKey(key, []byte("pw"), nil)
	if err != nil || len(enc) != NodeEncryptedSize || NodeEncryptedSize != 88 {
		t.Fatalf("%d bytes, %v", len(enc), err)
	}
	if _, enc2, _ := DecodeNodePrivate([]byte(base58.Encode(enc) + "\n")); !enc2 {
		t.Error("not seen as encrypted")
	}
	if _, err := ParseNodePrivate([]byte(base58.Encode(enc))); !errors.Is(err, ErrNodeKeyEncrypted) {
		t.Errorf("parse: %v", err)
	}
	k, err := OpenNodePrivate([]byte(base58.Encode(enc)), []byte("pw"))
	if err != nil || Base58(k) != testSecret {
		t.Errorf("open: %v", err)
	}
	if _, err := DecryptNodeKey(enc[:87], []byte("pw")); !errors.Is(err, ErrNodeKeyPassword) {
		t.Errorf("short: %v", err)
	}
}

// The session's key: usable until Destroy, then zeroed and refused.
func TestLockedKey(t *testing.T) {
	key, _ := FromBase58(testSecret)
	lk, err := NewLockedKey(key)
	if err != nil {
		t.Fatal(err)
	}
	var lent ed25519.PrivateKey
	err = lk.Use(func(k ed25519.PrivateKey) error {
		lent = k
		if Base58(k) != testSecret {
			t.Error("another key")
		}
		ed25519.Sign(k, []byte("x"))
		return nil
	})
	if err != nil || Address(lk.Public()) != testAddress {
		t.Fatal(err)
	}
	if !bytes.Equal(lent, make([]byte, 64)) {
		t.Error("the copy lent to Use was not zeroed")
	}
	lk.Destroy()
	lk.Destroy()
	if !lk.Destroyed() || !errors.Is(lk.Use(func(ed25519.PrivateKey) error { return nil }), ErrLocked) {
		t.Error("usable after Destroy")
	}
}
