package keys

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/CreditsCommunity/wallet/internal/keys/keystest"
	"github.com/mr-tron/base58"
)

// node-encrypted-vectors.json: NodePrivate.txt files encrypted by the node's own code
// (testdata/node-key-crypt.cpp encrypt) and opened by it (node-key-crypt open) before they were
// recorded: the first four by the legacy code (cscrypto's PrivateKey::getEncrypted, 88 bytes, the
// fixed salt), the others by the node's salted format (cs::keyfile::encrypt, 104 bytes).
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

// The node's encrypted files of both formats open with the node's password, and only with it; the
// wallet's encryption with the same salt and nonce gives the node's bytes exactly.
func TestNodeEncryptedVectors(t *testing.T) {
	if testing.Short() {
		t.Skip("Argon2id with 1 GiB")
	}
	legacyKeys := map[string][]byte{} // the legacy salt is fixed: one derivation per password
	seen := map[int]int{}
	for i, v := range loadNodeEncVectors(t) {
		raw, _ := base58.Decode(v.Encrypted)
		var salt, fk []byte
		switch len(raw) {
		case NodeEncryptedSize:
			if fk = legacyKeys[v.Password]; fk == nil {
				fk = nodeFileKey([]byte(v.Password), nil)
				legacyKeys[v.Password] = fk
			}
		case NodeSaltedEncryptedSize:
			salt = raw[:nodeSaltSize]
			fk = nodeFileKey([]byte(v.Password), salt)
		default:
			t.Fatalf("vector %d: %d bytes", i, len(raw))
		}
		first := seen[len(raw)] == 0
		seen[len(raw)]++
		sealed := raw[len(salt):]
		key, err := decryptNodeKey(sealed, fk)
		if err != nil {
			t.Fatalf("vector %d: %v", i, err)
		}
		if Base58(key) != v.Secret || Address(key.Public().(ed25519.PublicKey)) != v.Public {
			t.Errorf("vector %d: another key", i)
		}
		again, err := keystest.Seal(key, fk, sealed[:nodeNonceSize])
		if err != nil || !bytes.Equal(again, sealed) {
			t.Errorf("vector %d: the wallet's encryption differs from the node's", i)
		}
		if !first {
			continue
		}
		if _, err := OpenNodePrivate([]byte(v.Encrypted), []byte(v.Password+"x")); !errors.Is(err, ErrNodeKeyPassword) {
			t.Errorf("vector %d: a wrong password: %v", i, err)
		}
		if _, err := OpenNodePrivate([]byte(v.Encrypted), nil); !errors.Is(err, ErrNodeKeyEncrypted) {
			t.Errorf("vector %d: no password: %v", i, err)
		}
		changed := append([]byte(nil), raw...)
		changed[len(salt)+20] ^= 1
		if _, err := decryptNodeKey(changed[len(salt):], fk); !errors.Is(err, ErrNodeKeyPassword) {
			t.Errorf("vector %d: a changed file: %v", i, err)
		}
		if salt != nil {
			changed = append([]byte(nil), raw...)
			changed[3] ^= 1
			if _, err := DecryptNodeKey(changed, []byte(v.Password)); !errors.Is(err, ErrNodeKeyPassword) {
				t.Errorf("vector %d: a changed salt: %v", i, err)
			}
		}
	}
	if seen[NodeEncryptedSize] == 0 || seen[NodeSaltedEncryptedSize] == 0 {
		t.Errorf("vectors of both formats expected: %v", seen)
	}
}

// The round trip with the node's code: with NODE_KEY_CRYPT set to testdata/node-key-crypt.cpp
// built against the node, a key the wallet encrypts is opened by the node, and a key the node
// encrypts (a new random salt and nonce: 104 bytes) is opened by the wallet.
func TestNodeEncryptedRoundTripWithTheNode(t *testing.T) {
	tool := os.Getenv("NODE_KEY_CRYPT")
	if tool == "" {
		t.Skip("NODE_KEY_CRYPT (testdata/node-key-crypt.cpp built against the node) not set")
	}
	key, _ := FromBase58(testSecret)
	pw := "round trip ~ 42"
	// wallet -> node
	dir := t.TempDir()
	enc, err := keystest.EncryptNodeKey(key, []byte(pw), NodeKDF.Time, NodeKDF.MemoryKiB)
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
	if raw, _ := base58.Decode(string(data)); len(raw) != NodeSaltedEncryptedSize {
		t.Errorf("the node wrote %d bytes, not the salted format", len(raw))
	}
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
	enc, err := keystest.EncryptNodeKey(key, []byte("pw"), NodeKDF.Time, NodeKDF.MemoryKiB)
	if err != nil || len(enc) != NodeSaltedEncryptedSize || NodeSaltedEncryptedSize != 104 || NodeEncryptedSize != 88 {
		t.Fatalf("%d bytes, %v", len(enc), err)
	}
	enc2, _ := keystest.EncryptNodeKey(key, []byte("pw"), NodeKDF.Time, NodeKDF.MemoryKiB)
	if bytes.Equal(enc[:nodeSaltSize], enc2[:nodeSaltSize]) || bytes.Equal(enc[nodeSaltSize:nodeSaltSize+nodeNonceSize], enc2[nodeSaltSize:nodeSaltSize+nodeNonceSize]) {
		t.Error("the salt and the nonce are not random")
	}
	if _, encrypted, _ := DecodeNodePrivate([]byte(base58.Encode(enc) + "\n")); !encrypted {
		t.Error("not seen as encrypted")
	}
	if _, err := ParseNodePrivate([]byte(base58.Encode(enc))); !errors.Is(err, ErrNodeKeyEncrypted) {
		t.Errorf("parse: %v", err)
	}
	k, err := OpenNodePrivate([]byte(base58.Encode(enc)), []byte("pw"))
	if err != nil || Base58(k) != testSecret {
		t.Errorf("open: %v", err)
	}
	// the legacy format with the light parameters: the fixed salt, 88 bytes
	fk := nodeFileKey([]byte("pw"), nil)
	legacy, err := keystest.Seal(key, fk, enc[nodeSaltSize:nodeSaltSize+nodeNonceSize])
	if err != nil || len(legacy) != NodeEncryptedSize {
		t.Fatalf("legacy: %d bytes, %v", len(legacy), err)
	}
	if k, err := DecryptNodeKey(legacy, []byte("pw")); err != nil || Base58(k) != testSecret {
		t.Errorf("legacy open: %v", err)
	}
	// another salt is another key: the salted file does not open as legacy, nor with another salt
	if _, err := DecryptNodeKey(enc[nodeSaltSize:], []byte("pw")); !errors.Is(err, ErrNodeKeyPassword) {
		t.Errorf("salted read as legacy: %v", err)
	}
	for _, n := range []int{0, 64, 87, 89, 103, 105} {
		b := make([]byte, n)
		copy(b, enc)
		if _, err := DecryptNodeKey(b, []byte("pw")); !errors.Is(err, ErrNodeKeyPassword) {
			t.Errorf("%d bytes: %v", n, err)
		}
	}
}

// Too little free memory for the node's Argon2id: refused before the derivation, which would
// otherwise end the process.
func TestNodeKeyMemoryCheck(t *testing.T) {
	oldMem, oldKDF := memoryAvailable, NodeKDF
	defer func() { memoryAvailable, NodeKDF = oldMem, oldKDF }()
	NodeKDF.Time, NodeKDF.MemoryKiB = 1, 64
	key, _ := FromBase58(testSecret)
	enc, err := keystest.EncryptNodeKey(key, []byte("pw"), NodeKDF.Time, NodeKDF.MemoryKiB)
	if err != nil {
		t.Fatal(err)
	}
	memoryAvailable = func() (uint64, bool) { return 70 * 1024, true } // less than 64 KiB + a fifth
	if _, err := DecryptNodeKey(enc, []byte("pw")); !errors.Is(err, ErrNodeKeyMemory) || err.Error() != "the node's key needs 1 GiB of free memory to open (as the node does)" {
		t.Fatalf("short of memory: %v", err)
	}
	if _, err := OpenNodePrivate([]byte(base58.Encode(enc)), []byte("pw")); !errors.Is(err, ErrNodeKeyMemory) {
		t.Fatalf("open: %v", err)
	}
	memoryAvailable = func() (uint64, bool) { return 0, false } // unknown: tried
	if _, err := DecryptNodeKey(enc, []byte("pw")); err != nil {
		t.Fatal(err)
	}
	memoryAvailable = func() (uint64, bool) { return 1 << 30, true }
	if _, err := DecryptNodeKey(enc, []byte("pw")); err != nil {
		t.Fatal(err)
	}
	if avail, ok := availableMemory(); runtime.GOOS == "linux" && (!ok || avail == 0) {
		t.Errorf("this system's memory: %d %v", avail, ok)
	}
}
