package wallet

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/keys"
)

// failAt makes crashAt fail at one step (a crash there), for one test.
func failAt(t *testing.T, step string) {
	t.Helper()
	old := crashAt
	crashAt = func(s string) error {
		if s == step {
			return errors.New("crash at " + s)
		}
		return nil
	}
	t.Cleanup(func() { crashAt = old })
}

func noCrash() { crashAt = func(string) error { return nil } }

// opens tells whether every named account opens with the wallet password pw.
func opens(t *testing.T, w *Wallet, pw string, names ...string) bool {
	t.Helper()
	for _, n := range names {
		f, err := w.Store.Load(n)
		if err != nil {
			t.Fatal(err)
		}
		k, err := w.OpenKey(f, []byte(pw))
		if err != nil {
			return false
		}
		keys.Zero(k)
	}
	return true
}

// One password: the first account chooses it, every other account is encrypted under it and
// opens with it; the files are the master key's (version 2, no key derivation of their own).
func TestOneWalletPassword(t *testing.T) {
	w, _ := testWallet(t)
	if w.HasWalletPassword() {
		t.Fatal("a wallet password before the first account")
	}
	if _, err := w.OpenWallet([]byte("x")); !errors.Is(err, ErrNoWalletPassword) {
		t.Fatalf("open: %v", err)
	}
	if _, err := w.CreateAccount("main", newKey(t), []byte("the pw")); err != nil {
		t.Fatal(err)
	}
	if !w.HasWalletPassword() {
		t.Fatal("no wallet key after the first account")
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(w.Store.WalletKeyPath()); st.Mode().Perm() != 0o600 {
			t.Errorf("wallet.key mode %v", st.Mode().Perm())
		}
	}
	if _, err := w.CreateAccount("second", newKey(t), []byte("another pw")); !IsWrongPassword(err) {
		t.Fatalf("a second password was accepted: %v", err)
	}
	if _, err := w.CreateAccount("second", newKey(t), []byte("the pw")); err != nil {
		t.Fatal(err)
	}
	if !opens(t, w, "the pw", "main", "second") || opens(t, w, "another pw", "main") {
		t.Fatal("not one password")
	}
	raw, _ := os.ReadFile(w.Store.FilePath("main"))
	if !bytes.Contains(raw, []byte(`"version": 2`)) || bytes.Contains(raw, []byte(`"kdf"`)) || !bytes.Contains(raw, []byte(`"key_id"`)) {
		t.Fatalf("account file:\n%s", raw)
	}
	s, err := w.OpenWallet([]byte("the pw"))
	if err != nil {
		t.Fatal(err)
	}
	f, _ := w.Store.Load("second")
	u, err := w.UnlockWith(s, f) // the session: no password per account
	if err != nil {
		t.Fatal(err)
	}
	u.Lock()
	s.Lock()
	if _, err := w.UnlockWith(s, f); err == nil {
		t.Fatal("a locked wallet opened an account")
	}
}

// A change of the wallet password re-encrypts every account under a new key; interrupted at any
// step, opening the wallet with the new password finishes it, and the old one says so.
func TestChangeWalletPasswordResumable(t *testing.T) {
	for _, step := range []string{"", "change:b", "change:wallet.key", "change:next"} {
		t.Run(step, func(t *testing.T) {
			w, _ := testWallet(t)
			for _, n := range []string{"a", "b", "c"} {
				w.CreateAccount(n, newKey(t), []byte("old"))
			}
			if err := w.ChangeWalletPassword([]byte("wrong"), []byte("new")); !IsWrongPassword(err) {
				t.Fatalf("changed with a wrong password: %v", err)
			}
			if step != "" {
				failAt(t, step)
				if err := w.ChangeWalletPassword([]byte("old"), []byte("new")); err == nil {
					t.Fatal("no crash")
				}
				noCrash()
				_, err := w.OpenWallet([]byte("old"))
				switch step {
				case "change:next": // wallet.key is the new one already
					if !IsWrongPassword(err) {
						t.Fatalf("old password after the new key: %v", err)
					}
				default:
					if !errors.Is(err, ErrChangeInterrupted) {
						t.Fatalf("old password, change interrupted: %v", err)
					}
				}
				if err := w.ChangeWalletPassword([]byte("old"), []byte("other")); err == nil {
					t.Fatal("a second change over an interrupted one")
				}
				s, err := w.OpenWallet([]byte("new")) // finishes it
				if err != nil {
					t.Fatal(err)
				}
				s.Lock()
			} else if err := w.ChangeWalletPassword([]byte("old"), []byte("new")); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(w.Store.WalletKeyNextPath()); err == nil {
				t.Fatal("wallet.key.next left")
			}
			if !opens(t, w, "new", "a", "b", "c") || opens(t, w, "old", "a") {
				t.Fatal("not every account under the new password")
			}
			entries, _ := os.ReadDir(w.Store.Dir)
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".tmp-") {
					t.Errorf("left: %s", e.Name())
				}
			}
		})
	}
}

// legacy writes an account file of version 1 (a password of its own).
func legacy(t *testing.T, w *Wallet, name, pw string) ed25519.PublicKey {
	t.Helper()
	k := newKey(t)
	f, err := keys.Encrypt(name, k, []byte(pw), testKDF)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Store.Save(f); err != nil {
		t.Fatal(err)
	}
	return k.Public().(ed25519.PublicKey)
}

// The migration: the wallet password moves the accounts that have it, the others with their own
// password; one not given stays as it is. A crash in the middle leaves every account usable, the
// copies are checked and removed when the wallet is opened, and the migration goes on.
func TestMigrationResumable(t *testing.T) {
	w, _ := testWallet(t)
	legacy(t, w, "a", "p1")
	pubB := legacy(t, w, "b", "p2")
	legacy(t, w, "c", "p3")
	if !w.NeedsMigration() {
		t.Fatal("no migration needed")
	}
	if _, err := w.OpenWallet([]byte("p1")); !errors.Is(err, ErrMigrateFirst) {
		t.Fatalf("open: %v", err)
	}
	if _, err := w.CreateAccount("new", newKey(t), []byte("p1")); !errors.Is(err, ErrMigrateFirst) {
		t.Fatalf("a new account before the migration: %v", err)
	}
	s, err := w.CreateWalletPassword([]byte("p1")) // one of the accounts' passwords
	if err != nil {
		t.Fatal(err)
	}
	moved, left, err := w.MigrateWith(s, []byte("p1"))
	if err != nil || strings.Join(moved, ",") != "a" || len(left) != 2 {
		t.Fatalf("%v %v %v", moved, left, err)
	}
	// a crash while b is moved: b is still whole with its old password, its copy kept
	failAt(t, "migrate:b")
	fb, _ := w.Store.Load("b")
	if err := w.MigrateAccount(s, fb, []byte("p2")); err == nil {
		t.Fatal("no crash")
	}
	noCrash()
	s.Lock()
	for _, n := range []string{"a", "b"} {
		if _, err := os.Stat(filepath.Join(w.MigrationDir(), n+".json")); err != nil {
			t.Fatalf("no copy of %s during the migration", n)
		}
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(filepath.Join(w.MigrationDir(), "b.json")); st.Mode().Perm() != 0o600 {
			t.Errorf("copy mode %v", st.Mode().Perm())
		}
	}
	if fb, _ := w.Store.Load("b"); !fb.OldPassword() {
		t.Fatal("b changed by the crash")
	}
	// a file lost meanwhile comes back from its copy
	os.Remove(w.Store.FilePath("b"))
	s, err = w.OpenWallet([]byte("p1")) // checks the copies: a is fine, b is put back
	if err != nil {
		t.Fatal(err)
	}
	defer s.Lock()
	if _, err := os.Stat(w.MigrationDir()); err == nil {
		t.Fatal("the copies were not removed")
	}
	fb, err = w.Store.Load("b")
	if err != nil || !fb.OldPassword() || fb.Public != keys.Address(pubB) {
		t.Fatalf("b not put back: %v", err)
	}
	// resumed: b with its own password, c left with its old one
	if err := w.MigrateAccount(s, fb, []byte("wrong")); !errors.Is(err, keys.ErrWrongPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	if err := w.MigrateAccount(s, fb, []byte("p2")); err != nil {
		t.Fatal(err)
	}
	w.FinishMigration(s)
	if !opens(t, w, "p1", "a", "b") {
		t.Fatal("a and b are not under the wallet password")
	}
	old := w.OldPasswordAccounts()
	if len(old) != 1 || old[0].Name != "c" || w.NeedsMigration() {
		t.Fatalf("left: %v", old)
	}
	if !opens(t, w, "p3", "c") { // an old-password account still opens with its own password
		t.Fatal("c")
	}
}

// One key from elsewhere: an encrypted key file asks its own password and goes under the wallet
// password; an export is a key file of its own, under the wallet password or another one.
func TestSingleKeyExportImport(t *testing.T) {
	w, home := testWallet(t)
	k := newKey(t)
	w.CreateAccount("main", k, []byte("wallet pw"))
	f, _ := w.Store.Load("main")
	if _, err := w.ExportEncrypted(f, []byte("wrong"), []byte("x")); err == nil {
		t.Fatal("exported with a wrong wallet password")
	}
	data, err := w.ExportEncrypted(f, []byte("wallet pw"), []byte("file pw"))
	if err != nil {
		t.Fatal(err)
	}
	kf, ok := keys.EncryptedKeyFile(string(data))
	if !ok || !kf.OldPassword() || kf.KeyID != "" {
		t.Fatalf("export: %s", data)
	}
	got, err := kf.Decrypt([]byte("file pw"))
	if err != nil || !bytes.Equal(got, k) {
		t.Fatalf("the export does not open with its password: %v", err)
	}
	// into another wallet: re-encrypted under its password
	w2, _ := testWallet(t)
	if _, err := w2.CreateAccount("imported", got, []byte("w2 pw")); err != nil {
		t.Fatal(err)
	}
	if !opens(t, w2, "w2 pw", "imported") {
		t.Fatal("the import is not under the wallet password")
	}
	// an account file of another wallet (version 2) is not a key file of its own
	raw, _ := os.ReadFile(w.Store.FilePath("main"))
	if f2, ok := keys.EncryptedKeyFile(string(raw)); !ok || !f2.UnderWalletKey() {
		t.Fatal("a version 2 file not recognised")
	}
	_ = home
}

// The node's key keeps its own rules: a plain one needs no password, one the node encrypted opens
// with the node's password only, never the wallet's, and the open wallet does not open it.
func TestNodeKeyException(t *testing.T) {
	lightNodeKDF(t)
	dir := nodeDir(t)
	writeEncryptedNodeKey(t, dir, nkSecret, "node pw")
	w := New(config.Default(), config.Profile{Address: config.DefaultAddress}, t.TempDir())
	w.KDF = testKDF
	w.NodeKey = NodeKeyIn(dir, "test")
	w.CreateAccount("savings", newKey(t), []byte("wallet pw"))
	s, err := w.OpenWallet([]byte("wallet pw"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Lock()
	f, err := w.LoadAccount("node")
	if err != nil || !f.NodeKeyEncrypted {
		t.Fatalf("%v %v", f, err)
	}
	if _, err := w.UnlockWith(s, f); err == nil {
		t.Fatal("the wallet password opened the node's encrypted key")
	}
	if _, err := w.OpenKey(f, []byte("wallet pw")); err == nil {
		t.Fatal("the wallet password opened the node's key")
	}
	key, err := w.OpenKey(f, []byte("node pw"))
	if err != nil || keys.Base58(key) != nkSecret {
		t.Fatalf("the node's password: %v", err)
	}
	// a plain node key: no password, the wallet open or not
	os.WriteFile(filepath.Join(dir, keys.NodePrivateFile), []byte(nkSecret), 0o600)
	f, _ = w.LoadAccount("node")
	u, err := w.UnlockWith(s, f)
	if err != nil {
		t.Fatal(err)
	}
	u.Lock()
	if key, err := w.OpenKey(f, nil); err != nil || keys.Base58(key) != nkSecret {
		t.Fatalf("plain: %v", err)
	}
	// and the node's key is never in a backup
	b, _, err := w.CollectBackup(time.Now())
	if err != nil || len(b.Accounts) != 1 || b.Accounts[0].Name != "savings" {
		t.Fatalf("backup: %v", err)
	}
}
