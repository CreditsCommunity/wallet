package wallet

import (
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mr-tron/base58"

	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/tx"
)

// lightNodeKDF makes the node's 1 GiB Argon2id light for a test (the format is proven in package
// keys with the node's own vectors).
func lightNodeKDF(t *testing.T) {
	t.Helper()
	old := keys.NodeKDF
	keys.NodeKDF.Time, keys.NodeKDF.MemoryKiB = 1, 64
	t.Cleanup(func() { keys.NodeKDF = old })
}

// writeEncryptedNodeKey writes NodePrivate.txt as the node encrypts it, and NodePublic.txt.
func writeEncryptedNodeKey(t *testing.T, dir, secret, password string) {
	t.Helper()
	key, err := keys.FromBase58(secret)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := keys.EncryptNodeKey(key, []byte(password), nil)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, keys.NodePrivateFile), []byte(base58.Encode(enc)), 0o600)
	os.WriteFile(filepath.Join(dir, keys.NodePublicFile), []byte(keys.Address(key.Public().(ed25519.PublicKey))), 0o600)
}

// The node's key encrypted by the node: listed with its NodePublic.txt's address, opened only with
// the node's password, signs; a changed file or another NodePublic.txt is refused.
func TestEncryptedNodeKeyAccount(t *testing.T) {
	lightNodeKDF(t)
	dir := nodeDir(t)
	writeEncryptedNodeKey(t, dir, nkSecret, "node pw")
	w := New(config.Default(), config.Profile{Address: config.DefaultAddress}, t.TempDir())
	w.NodeKey = NodeKeyIn(dir, "test")
	files, problems, err := w.Accounts()
	if err != nil || len(problems) > 0 || len(files) != 1 || !files[0].NodeKeyEncrypted || files[0].Public != nkAddress {
		t.Fatalf("%v %v %v", files, problems, err)
	}
	f := files[0]
	if !NeedsPassword(f) {
		t.Fatal("no password asked")
	}
	if _, err := w.Unlock(f, []byte("wrong")); !errors.Is(err, ErrUnlock) || err.Error() != ErrUnlock.Error() {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := w.Unlock(f, nil); !errors.Is(err, ErrUnlock) {
		t.Fatalf("no password: %v", err)
	}
	if _, err := w.OpenKey(f, []byte("wrong")); !errors.Is(err, keys.ErrNodeKeyPassword) {
		t.Fatalf("OpenKey wrong: %v", err)
	}
	key, err := w.OpenKey(f, []byte("node pw")) // the commands: password asked when they sign
	if err != nil || keys.Base58(key) != nkSecret {
		t.Fatalf("OpenKey: %v", err)
	}
	u, err := w.Unlock(f, []byte("node pw"))
	if err != nil {
		t.Fatal(err)
	}
	pub := key.Public().(ed25519.PublicKey)
	p := &SendPlan{Kind: KindTransfer, Account: "node", NodeKey: true, NodeKeyEncrypted: true, From: pub, To: pub, InnerID: 1, MaxFee: tx.MaxFee}
	if !p.NeedsPassword() {
		t.Error("the plan needs no password")
	}
	tr, err := u.sign(w, p)
	if err != nil || tr == nil || len(tr.Signature) != ed25519.SignatureSize {
		t.Fatalf("sign: %v", err)
	}
	other := &SendPlan{Kind: KindTransfer, Account: "savings", From: pub, To: pub, InnerID: 1, MaxFee: tx.MaxFee}
	if _, err := u.sign(w, other); err == nil {
		t.Error("signed for another account")
	}
	// the node re-encrypted its key (another password, same key): the session refuses until unlocked again
	writeEncryptedNodeKey(t, dir, nkSecret, "new pw")
	if _, err := u.sign(w, p); err == nil || !strings.Contains(err.Error(), "changed since the account was unlocked") {
		t.Fatalf("changed file: %v", err)
	}
	u.Lock()
	if !u.Locked() {
		t.Fatal("not locked")
	}
	if _, err := u.sign(w, p); !errors.Is(err, keys.ErrLocked) && (err == nil || !strings.Contains(err.Error(), "changed")) {
		t.Fatalf("after Lock: %v", err)
	}
	// a NodePublic.txt of another key: the address shown is not the key's: refused once opened
	otherKey := newKey(t)
	os.WriteFile(filepath.Join(dir, keys.NodePublicFile), []byte(keys.Address(otherKey.Public().(ed25519.PublicKey))), 0o600)
	files, _, _ = w.Accounts()
	if _, err := w.Unlock(files[0], []byte("new pw")); err == nil || !strings.Contains(err.Error(), "not the one of NodePublic.txt") {
		t.Fatalf("another NodePublic.txt: %v", err)
	}
	// a NodePublic.txt others can write would choose the address shown: refused
	if runtime.GOOS != "windows" {
		os.Chmod(filepath.Join(dir, keys.NodePublicFile), 0o666)
		if _, problems, _ := w.Accounts(); len(problems) != 1 || !strings.Contains(problems[0].Error(), "can be written by other users") {
			t.Fatalf("NodePublic.txt open to others: %v", problems)
		}
	}
	// no NodePublic.txt: no address, not used
	os.Remove(filepath.Join(dir, keys.NodePublicFile))
	if _, problems, _ := w.Accounts(); len(problems) != 1 || !strings.Contains(problems[0].Error(), "does not give its address") {
		t.Fatalf("no NodePublic.txt: %v", problems)
	}
}

// A store account: unlocked with its password, signs without it, zeroed by Lock.
func TestUnlockStoreAccount(t *testing.T) {
	w := New(config.Default(), config.Profile{Address: config.DefaultAddress}, t.TempDir())
	w.KDF = keys.MinKDF
	w.KDF.Name = "argon2id"
	key := newKey(t)
	f, err := w.CreateAccount("main", key, []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Unlock(f, []byte("nope")); err != ErrUnlock {
		t.Fatalf("wrong: %v", err)
	}
	u, err := w.Unlock(f, []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	pub := key.Public().(ed25519.PublicKey)
	p := &SendPlan{Kind: KindTransfer, Account: "main", From: pub, To: pub, InnerID: 1, MaxFee: tx.MaxFee}
	tr, err := u.sign(w, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Signature) != ed25519.SignatureSize {
		t.Fatal("signature")
	}
	u.Lock()
	if _, err := u.sign(w, p); !errors.Is(err, keys.ErrLocked) {
		t.Fatalf("after Lock: %v", err)
	}
	u.Lock() // twice: fine
}

// A plain node's key: unlocked without a password, nothing kept in memory.
func TestUnlockPlainNodeKey(t *testing.T) {
	dir := nodeDir(t)
	os.WriteFile(filepath.Join(dir, "NodePrivate.txt"), []byte(nkSecret), 0o600)
	w := New(config.Default(), config.Profile{Address: config.DefaultAddress}, t.TempDir())
	w.NodeKey = NodeKeyIn(dir, "test")
	f, err := w.NodeAccount()
	if err != nil || NeedsPassword(f) {
		t.Fatalf("%v", err)
	}
	u, err := w.Unlock(f, nil)
	if err != nil || u.key != nil {
		t.Fatalf("%v", err)
	}
	pub, _ := f.PublicKey()
	if _, err := u.sign(w, &SendPlan{Kind: KindTransfer, Account: "node", NodeKey: true, From: pub, To: pub, InnerID: 1, MaxFee: tx.MaxFee}); err != nil {
		t.Fatal(err)
	}
}
