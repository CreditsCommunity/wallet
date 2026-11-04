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

var testKDF = keys.KDFParams{Name: "argon2id", Time: keys.MinKDF.Time, MemoryKiB: keys.MinKDF.MemoryKiB, Threads: 1}

func lightBackupKDF(t *testing.T) {
	old := keys.BackupKDF
	keys.BackupKDF = testKDF
	t.Cleanup(func() { keys.BackupKDF = old })
}

// testWallet is a wallet on its own data folder (in a home of its own) with light key files.
func testWallet(t *testing.T) (*Wallet, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	data := filepath.Join(home, ".credits-wallet")
	w := New(config.Default(), config.Profile{Name: "local", Address: "127.0.0.1:1", Network: config.Mainnet}, data)
	w.KDF = testKDF
	return w, home
}

func randomAddress(t *testing.T) ed25519.PublicKey {
	return newKey(t).Public().(ed25519.PublicKey)
}

func TestBackupRoundTrip(t *testing.T) {
	lightBackupKDF(t)
	src, home := testWallet(t)
	pws := map[string]string{"main": "pw-main", "savings": "pw-savings", "test.1": "pw-test"}
	addr := map[string]ed25519.PublicKey{}
	for name, pw := range pws {
		k := newKey(t)
		if _, err := src.CreateAccount(name, k, []byte(pw)); err != nil {
			t.Fatal(err)
		}
		addr[name] = k.Public().(ed25519.PublicKey)
	}
	token, contract := randomAddress(t), randomAddress(t)
	if err := src.addLocalTokens(addr["main"], []ed25519.PublicKey{token}, "added"); err != nil {
		t.Fatal(err)
	}
	if err := src.AddLocalContract(addr["savings"], LocalContract{Address: keys.Address(contract), Name: "Counter", Network: "mainnet", Deployed: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(home, "wallet.dat")
	b, problems, err := src.CollectBackup(time.Now())
	if err != nil || len(problems) != 0 || len(b.Accounts) != 3 {
		t.Fatalf("collect: %v %v", err, problems)
	}
	if err := WriteBackup(path, b, []byte("the backup password")); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", st.Mode().Perm())
		}
	}
	data, err := ReadBackupFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name := range pws { // nothing of the accounts in clear, not even the names
		if bytes.Contains(data, []byte(name)) || bytes.Contains(data, []byte(keys.Address(addr[name]))) {
			t.Fatalf("%s is in clear in the backup", name)
		}
	}
	if _, err := OpenBackup(data, []byte("wrong")); !errors.Is(err, keys.ErrBackupOpen) {
		t.Fatalf("wrong password: %v", err)
	}

	// into another wallet
	dst, _ := testWallet(t)
	opened, err := OpenBackup(data, []byte("the backup password"))
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Wipe()
	plans, err := dst.PlanRestore(opened)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(plans))
	for i, p := range plans {
		if p.State != RestoreNew {
			t.Fatalf("%s: state %d", p.Account.Name, p.State)
		}
		names[i] = p.Account.Name
	}
	results, restored, failed := dst.Restore(plans, names)
	if restored != 3 || failed != 0 {
		t.Fatalf("restore: %+v", results)
	}
	for name, pw := range pws {
		// the key file exactly as stored, and it opens with its own password
		orig, _ := os.ReadFile(src.Store.FilePath(name))
		got, _ := os.ReadFile(dst.Store.FilePath(name))
		if !bytes.Equal(orig, got) {
			t.Errorf("%s: the key file differs\n%s\n%s", name, orig, got)
		}
		f, err := dst.Store.Load(name)
		if err != nil {
			t.Fatal(err)
		}
		k, err := f.Decrypt([]byte(pw))
		if err != nil || !bytes.Equal(k.Public().(ed25519.PublicKey), addr[name]) {
			t.Fatalf("%s does not open with its password: %v", name, err)
		}
		if _, err := f.Decrypt([]byte("the backup password")); err == nil {
			t.Fatalf("%s opens with the backup password", name)
		}
	}
	if l, err := dst.localTokensAll(addr["main"]); err != nil || len(l) != 1 || l[0].Address != keys.Address(token) {
		t.Errorf("token list: %v %v", l, err)
	}
	if l, err := dst.LocalContracts(addr["savings"]); err != nil || len(l) != 1 || l[0].Address != keys.Address(contract) {
		t.Errorf("contract list: %v %v", l, err)
	}
	if _, err := os.Stat(filepath.Join(dst.DataDir, "config.toml")); err == nil {
		t.Error("a configuration was restored")
	}
}

func TestBackupExistingAndNameClash(t *testing.T) {
	lightBackupKDF(t)
	src, home := testWallet(t)
	kMain, kOther := newKey(t), newKey(t)
	src.CreateAccount("main", kMain, []byte("a"))
	src.CreateAccount("other", kOther, []byte("b"))
	token := randomAddress(t)
	src.addLocalTokens(kMain.Public().(ed25519.PublicKey), []ed25519.PublicKey{token}, "added")
	b, _, _ := src.CollectBackup(time.Now())
	path := filepath.Join(home, "b.dat")
	if err := WriteBackup(path, b, []byte("x")); err != nil {
		t.Fatal(err)
	}

	dst, _ := testWallet(t)
	dst.CreateAccount("old-main", kMain, []byte("c"))  // the same address, another name
	dst.CreateAccount("other", newKey(t), []byte("d")) // the same name, another address
	data, _ := ReadBackupFile(path)
	opened, err := OpenBackup(data, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	plans, _ := dst.PlanRestore(opened)
	states := map[string]RestorePlan{}
	for _, p := range plans {
		states[p.Account.Name] = p
	}
	if p := states["main"]; p.State != RestoreExists || p.Existing != "old-main" {
		t.Fatalf("main: %+v", p)
	}
	if p := states["other"]; p.State != RestoreNameTaken {
		t.Fatalf("other: %+v", p)
	}
	if err := dst.FreeRestoreName("other", nil); err == nil {
		t.Fatal("a taken name was free")
	}
	if err := dst.FreeRestoreName("other-2", map[string]bool{"other-2": true}); err == nil {
		t.Fatal("a name used by the restore was free")
	}
	names := make([]string, len(plans))
	for i, p := range plans {
		if p.Account.Name == "other" {
			names[i] = "other-restored"
		}
	}
	results, restored, failed := dst.Restore(plans, names)
	if restored != 1 || failed != 0 {
		t.Fatalf("%+v", results)
	}
	f, err := dst.Store.Load("other-restored")
	if err != nil || f.Public != keys.Address(kOther.Public().(ed25519.PublicKey)) {
		t.Fatalf("renamed: %v %v", f, err)
	}
	if k, err := f.Decrypt([]byte("b")); err != nil {
		t.Fatalf("the renamed account does not open with its password: %v", err)
	} else {
		keys.Zero(k)
	}
	if f, _ := dst.Store.Load("old-main"); f == nil {
		t.Fatal("the existing account is gone")
	}
	if dst.Store.Exists("main") {
		t.Fatal("an address already here was restored again")
	}
	// the skipped account's token list was merged into the existing one's
	if l, _ := dst.localTokensAll(kMain.Public().(ed25519.PublicKey)); len(l) != 1 {
		t.Errorf("token list of the existing address: %v", l)
	}
	// skipped without a name
	opened2, _ := OpenBackup(data, []byte("x"))
	plans2, _ := dst.PlanRestore(opened2)
	results, restored, _ = dst.Restore(plans2, make([]string, len(plans2)))
	if restored != 0 {
		t.Fatalf("restored without a name: %+v", results)
	}
}

func TestBackupFileRefusals(t *testing.T) {
	lightBackupKDF(t)
	w, home := testWallet(t)
	w.CreateAccount("main", newKey(t), []byte("a"))
	existing := filepath.Join(home, "exists.dat")
	os.WriteFile(existing, []byte("x"), 0o600)
	keysDir := filepath.Join(home, "keys")
	os.Mkdir(keysDir, 0o700)
	os.WriteFile(filepath.Join(keysDir, "NodePrivate.txt"), []byte("x"), 0o600)
	refused := map[string]string{
		existing:                                "already exists",
		filepath.Join(w.DataDir, "b.dat"):       "data folder",
		filepath.Join(w.Store.Dir, "b.dat"):     "data folder",
		filepath.Join(keysDir, "b.dat"):         "holds key files",
		filepath.Join(home, "missing", "b.dat"): "not an existing folder",
	}
	if runtime.GOOS != "windows" {
		os.Symlink(filepath.Join(home, "elsewhere.dat"), filepath.Join(home, "link.dat"))
		refused[filepath.Join(home, "link.dat")] = "symbolic link"
		os.Symlink(w.DataDir, filepath.Join(home, "via"))
		refused[filepath.Join(home, "via", "b.dat")] = "data folder"
	}
	for path, want := range refused {
		if _, err := w.CheckBackupPath(path); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", path, err, want)
		}
	}
	// the writer itself never overwrites nor follows a link
	b, _, _ := w.CollectBackup(time.Now())
	if err := WriteBackup(existing, b, []byte("x")); err == nil {
		t.Error("an existing file was overwritten")
	}
	if got, _ := os.ReadFile(existing); string(got) != "x" {
		t.Error("the existing file changed")
	}
	if runtime.GOOS != "windows" {
		if err := WriteBackup(filepath.Join(home, "link.dat"), b, []byte("x")); err == nil {
			t.Error("written through a link")
		}
	}
	// not a backup, too large
	if _, err := ReadBackupFile(existing); !errors.Is(err, keys.ErrNotBackup) {
		t.Errorf("not a backup: %v", err)
	}
	// nothing to back up: the node's key alone is never in a backup
	empty, _ := testWallet(t)
	if _, _, err := empty.CollectBackup(time.Now()); err == nil {
		t.Error("an empty backup")
	}
}

func TestBackupContentChecked(t *testing.T) {
	lightBackupKDF(t)
	w, _ := testWallet(t)
	k := newKey(t)
	w.CreateAccount("main", k, []byte("a"))
	b, _, _ := w.CollectBackup(time.Now())
	// a key file of another address, a name twice: refused even under the right password
	bad := []func(*Backup){
		func(b *Backup) { b.Accounts[0].Address = keys.Address(randomAddress(t)) },
		func(b *Backup) { b.Accounts = append(b.Accounts, b.Accounts[0]) },
		func(b *Backup) { b.Accounts[0].Name = "../x" },
		func(b *Backup) { b.Accounts[0].Tokens = []byte(`{"version":1,"account":"someone else"}`) },
		func(b *Backup) { b.Version = 2 },
	}
	for i, change := range bad {
		c := *b
		c.Accounts = append([]BackupAccount(nil), b.Accounts...)
		change(&c)
		path := filepath.Join(t.TempDir(), "b.dat")
		if err := WriteBackup(path, &c, []byte("x")); err != nil {
			t.Fatal(err)
		}
		data, _ := ReadBackupFile(path)
		if _, err := OpenBackup(data, []byte("x")); err == nil {
			t.Errorf("change %d was accepted", i)
		}
	}
}

func TestPasswordHint(t *testing.T) {
	if PasswordHint([]byte("short")) == "" || PasswordHint([]byte("correct horse battery")) != "" {
		t.Error("the hint")
	}
}
