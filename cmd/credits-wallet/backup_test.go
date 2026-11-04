package main

import (
	"bufio"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

var lightKDF = keys.KDFParams{Name: "argon2id", Time: keys.MinKDF.Time, MemoryKiB: keys.MinKDF.MemoryKiB, Threads: 1}

// lightKeys makes the backup's key derivation light for a test (the wallet key and the accounts
// written by the tests use lightKDF through testWalletAt).
func lightKeys(t *testing.T) {
	old := keys.BackupKDF
	keys.BackupKDF = lightKDF
	t.Cleanup(func() { keys.BackupKDF = old })
}

func testWalletAt(data string) *wallet.Wallet {
	w := wallet.New(config.Default(), config.Profile{Address: config.DefaultAddress}, data)
	w.KDF = lightKDF
	return w
}

func pwFileWith(t *testing.T, pw string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pw")
	os.WriteFile(p, []byte(pw+"\n"), 0o600)
	return p
}

// backupEnv is a home with a data folder holding the given accounts under the wallet password
// "wallet pw" (light key derivation), and a file with that password.
func backupEnv(t *testing.T, names ...string) (home, data, pwFile string, addr map[string]string) {
	t.Helper()
	lightKeys(t)
	home, data, _ = recoveryEnv(t)
	pwFile = pwFileWith(t, "wallet pw")
	addr = map[string]string{}
	for _, name := range names {
		addr[name] = addAccount(t, data, name, nil, "wallet pw")
	}
	return
}

// addAccount writes an account under the wallet password pw (a new key, or key) and returns its
// address.
func addAccount(t *testing.T, data, name string, key ed25519.PrivateKey, pw string) string {
	t.Helper()
	if key == nil {
		m, _ := keys.NewMasterSeed()
		key = keys.DeriveKey(&m)
	}
	f, err := testWalletAt(data).CreateAccount(name, key, []byte(pw))
	if err != nil {
		t.Fatal(err)
	}
	return f.Public
}

// openKey opens an account of the wallet in data with password pw.
func openKey(data, name, pw string) (ed25519.PrivateKey, error) {
	w := testWalletAt(data)
	f, err := w.Store.Load(name)
	if err != nil {
		return nil, err
	}
	return w.OpenKey(f, []byte(pw))
}

func TestBackupExportImportCLI(t *testing.T) {
	home, data, pwFile, addr := backupEnv(t, "main", "savings")
	path := filepath.Join(home, "wallet.dat")

	// only the wallet password encrypts the backup
	if _, errOut, code := runWallet2(t, "backup", "export", path, "--password-file", pwFileWith(t, "not it")); code == 0 || !strings.Contains(errOut, "wrong wallet password") {
		t.Fatalf("another password: %d %s", code, errOut)
	}
	out, errOut, code := runWallet2(t, "backup", "export", path, "--password-file", pwFile, "--json")
	var got struct {
		File        string              `json:"file"`
		Count       int                 `json:"count"`
		Accounts    []map[string]string `json:"accounts"`
		NotIncluded []string            `json:"not_included"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || got.File != path || got.Count != 2 || len(got.NotIncluded) != 2 {
		t.Fatalf("export: %d %s %s", code, out, errOut)
	}
	if strings.Contains(out, "ciphertext") || strings.Contains(out, "key_file") {
		t.Fatalf("the JSON holds the content: %s", out)
	}
	// never over a file, never in the data folder
	if _, errOut, code := runWallet2(t, "backup", "export", path, "--password-file", pwFile); code == 0 || !strings.Contains(errOut, "already exists") {
		t.Errorf("overwrite: %d %s", code, errOut)
	}
	if _, errOut, code := runWallet2(t, "backup", "export", filepath.Join(data, "w.dat"), "--password-file", pwFile); code == 0 || !strings.Contains(errOut, "data folder") {
		t.Errorf("data folder: %d %s", code, errOut)
	}
	// the text output: path, count, the advice, what is not in it, the old password's backups
	out, errOut, code = runWallet2(t, "backup", "export", filepath.Join(home, "w2.dat"), "--password-file", pwFile)
	if code != 0 || !strings.Contains(out, "backup written: "+filepath.Join(home, "w2.dat")) || !strings.Contains(out, "accounts: 2") ||
		!strings.Contains(errOut, "off this computer, e.g. on a USB key") || !strings.Contains(errOut, "NodePrivate.txt") ||
		!strings.Contains(errOut, "only as strong as that password") {
		t.Fatalf("export text: %d\n%s\n%s", code, out, errOut)
	}

	// another wallet with another password: savings is already there, main's name is another's
	data2 := filepath.Join(t.TempDir(), "wallet2")
	t.Setenv("CREDITS_WALLET_HOME", data2)
	savingsKey, err := openKey(data, "savings", "wallet pw")
	if err != nil {
		t.Fatal(err)
	}
	addAccount(t, data2, "old-savings", savingsKey, "pw2")
	addAccount(t, data2, "main", nil, "pw2")

	if _, errOut, code := runWallet2(t, "backup", "import", path, "--password-file", pwFileWith(t, "not it")); code == 0 || !strings.Contains(errOut, "wrong backup password, or a damaged file") {
		t.Fatalf("wrong password: %d %s", code, errOut)
	}
	// this wallet's password differs: it must be given
	if _, errOut, code := runWallet2(t, "backup", "import", path, "--password-file", pwFile, "--yes"); code == 0 || !strings.Contains(errOut, "--wallet-password-file") {
		t.Fatalf("no wallet password: %d %s", code, errOut)
	}
	pw2 := pwFileWith(t, "pw2")
	// without a terminal: --yes, or nothing
	if _, errOut, code := runWallet2(t, "backup", "import", path, "--password-file", pwFile, "--wallet-password-file", pw2); code == 0 || !strings.Contains(errOut, "add --yes") {
		t.Fatalf("no terminal: %d %s", code, errOut)
	}
	out, errOut, code = runWallet2(t, "backup", "import", path, "--password-file", pwFile, "--wallet-password-file", pw2, "--yes", "--rename-suffix", "-b", "--json")
	var res struct {
		Restored int `json:"restored"`
		Skipped  int `json:"skipped"`
		Accounts []struct {
			Name, Address, Result, Reason string
			RestoredAs                    string `json:"restored_as"`
		} `json:"accounts"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil || res.Restored != 1 || res.Skipped != 1 {
		t.Fatalf("import: %d %s %s", code, out, errOut)
	}
	for _, a := range res.Accounts {
		switch a.Name {
		case "main":
			if a.Result != "restored" || a.RestoredAs != "main-b" || a.Address != addr["main"] {
				t.Errorf("main: %+v", a)
			}
		case "savings":
			if a.Result != "skipped" || !strings.Contains(a.Reason, "old-savings") {
				t.Errorf("savings: %+v", a)
			}
		}
	}
	// restored under this wallet's password, not the backup's
	if k, err := openKey(data2, "main-b", "pw2"); err != nil {
		t.Fatalf("main-b does not open with this wallet's password: %v", err)
	} else {
		keys.Zero(k)
	}
	if _, err := openKey(data2, "main-b", "wallet pw"); err == nil {
		t.Fatal("main-b opens with the backup's password")
	}
}

func TestBackupImportInteractive(t *testing.T) {
	home, _, pwFile, addr := backupEnv(t, "main", "spare")
	path := filepath.Join(home, "wallet.dat")
	if _, errOut, code := runWallet2(t, "backup", "export", path, "--password-file", pwFile); code != 0 {
		t.Fatal(errOut)
	}
	// a wallet with the same password: nothing more is asked
	data2 := filepath.Join(t.TempDir(), "wallet2")
	t.Setenv("CREDITS_WALLET_HOME", data2)
	addAccount(t, data2, "main", nil, "wallet pw")

	savedTerm, savedReader := stdinIsTerminal, stdinReader
	t.Cleanup(func() { stdinIsTerminal, stdinReader = savedTerm, savedReader })
	stdinIsTerminal = func() bool { return true }
	// a taken name, then a free one, then the confirmation
	stdinReader = bufio.NewReader(strings.NewReader("spare\nmain-laptop\ny\n"))
	out, errOut, code := runWallet2(t, "backup", "import", path, "--password-file", pwFile)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	for _, want := range []string{"holds 2 accounts", addr["main"], "the name is another account's here", `"spare" is taken too`,
		"main will be restored as main-laptop", "Restore 2 accounts? [y/N]", "under this wallet's password"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("missing %q in\n%s", want, errOut)
		}
	}
	if strings.Contains(errOut, "Password of this wallet") {
		t.Error("this wallet's password was asked although it is the backup's")
	}
	if !strings.Contains(out, "restored  main") || !strings.Contains(out, "as main-laptop") || !strings.Contains(out, "restored: 2, skipped: 0") {
		t.Errorf("output:\n%s", out)
	}
	// answered no: nothing restored; an empty wallet would take the backup's password
	data3 := filepath.Join(t.TempDir(), "wallet3")
	t.Setenv("CREDITS_WALLET_HOME", data3)
	stdinReader = bufio.NewReader(strings.NewReader("\n"))
	_, errOut, code = runWallet2(t, "backup", "import", path, "--password-file", pwFile)
	if code == 0 || !strings.Contains(errOut, "nothing was restored") || !strings.Contains(errOut, "the backup's password becomes its wallet password") {
		t.Fatalf("no: %d %s", code, errOut)
	}
	if entries, _ := os.ReadDir(filepath.Join(data3, "accounts")); len(entries) != 0 {
		t.Fatalf("restored after no: %v", entries)
	}
}

// legacyAccount writes an account with a password of its own (version 1), as before.
func legacyAccount(t *testing.T, data, name, pw string) {
	t.Helper()
	m, _ := keys.NewMasterSeed()
	f, err := keys.Encrypt(name, keys.DeriveKey(&m), []byte(pw), lightKDF)
	if err != nil {
		t.Fatal(err)
	}
	if err := (keys.Store{Dir: filepath.Join(data, "accounts")}).Save(f); err != nil {
		t.Fatal(err)
	}
}

// The migration on the command line: refused operations before it, the wallet password (one of
// the accounts'), each other password asked once, one kept for later, then 'wallet' says so.
func TestWalletMigrateCLI(t *testing.T) {
	lightKeys(t)
	_, data, _ := recoveryEnv(t)
	legacyAccount(t, data, "a", "p1")
	legacyAccount(t, data, "b", "p2")
	legacyAccount(t, data, "c", "p3")
	p1 := pwFileWith(t, "p1")
	out, _, _ := runWallet2(t, "account", "list")
	if !strings.Contains(out, "old password") {
		t.Errorf("list:\n%s", out)
	}
	if _, errOut, code := runWallet2(t, "account", "import", "x", "--password-file", p1); code == 0 || !strings.Contains(errOut, "wallet migrate") {
		t.Fatalf("import before the migration: %d %s", code, errOut)
	}
	if _, errOut, code := runWallet2(t, "backup", "export", filepath.Join(t.TempDir(), "b.dat"), "--password-file", p1); code == 0 || !strings.Contains(errOut, "wallet migrate") {
		t.Fatalf("backup before the migration: %d %s", code, errOut)
	}
	// an old-password account still signs with its own password
	if out, code := runWallet(t, "account", "export", "b", "--format", "base58", "--password-file", pwFileWith(t, "p2")); code != 0 || len(strings.TrimSpace(out)) < 80 {
		t.Fatalf("old password: %d %s", code, out)
	}
	savedTerm, savedSecret := stdinIsTerminal, readSecret
	t.Cleanup(func() { stdinIsTerminal, readSecret = savedTerm, savedSecret })
	stdinIsTerminal = func() bool { return true }
	answers := []string{"wrong", "p2", ""} // b: a wrong one, then its own; c: kept
	var asked []string
	readSecret = func(prompt string) (string, error) {
		asked = append(asked, prompt)
		if len(answers) == 0 {
			return "", errors.New("no more")
		}
		a := answers[0]
		answers = answers[1:]
		return a, nil
	}
	out, errOut, code := runWallet2(t, "wallet", "migrate", "--password-file", p1)
	if code != 0 || !strings.Contains(out, "under the wallet password: 2 (a, b)") || !strings.Contains(out, "old password kept: 1 (c)") {
		t.Fatalf("migrate: %d\n%s\n%s", code, out, errOut)
	}
	if len(asked) != 3 || !strings.Contains(asked[0], "Password of b") || !strings.Contains(asked[2], "Password of c") {
		t.Fatalf("asked: %q", asked)
	}
	for _, n := range []string{"a", "b"} {
		if k, err := openKey(data, n, "p1"); err != nil {
			t.Fatalf("%s: %v", n, err)
		} else {
			keys.Zero(k)
		}
	}
	out, _, _ = runWallet2(t, "wallet")
	if !strings.Contains(out, "wallet password: yes") || !strings.Contains(out, "old password   : 1 account (c)") {
		t.Fatalf("status:\n%s", out)
	}
	// now an account is added under the wallet password, without choosing one again
	stdinIsTerminal = func() bool { return false }
	if _, errOut, code := runWallet2(t, "account", "new", "d", "--password-file", p1, "--words-file", filepath.Join(t.TempDir(), "w")); code != 0 {
		t.Fatalf("new: %s", errOut)
	}
	if k, err := openKey(data, "d", "p1"); err != nil {
		t.Fatal(err)
	} else {
		keys.Zero(k)
	}
}

// The change of the wallet password, and an encrypted export imported into another wallet.
func TestWalletPasswordAndSingleKeyCLI(t *testing.T) {
	_, data, pwFile, addr := backupEnv(t, "main")
	newPw := pwFileWith(t, "new wallet pw")
	if _, errOut, code := runWallet2(t, "wallet", "password", "--password-file", pwFileWith(t, "wrong"), "--new-password-file", newPw); code == 0 || !strings.Contains(errOut, "wrong wallet password") {
		t.Fatalf("wrong current password: %d %s", code, errOut)
	}
	out, errOut, code := runWallet2(t, "wallet", "password", "--password-file", pwFile, "--new-password-file", newPw)
	if code != 0 || !strings.Contains(out, "wallet password changed") || !strings.Contains(errOut, "keep opening with the old password") {
		t.Fatalf("change: %d %s %s", code, out, errOut)
	}
	if _, err := openKey(data, "main", "wallet pw"); err == nil {
		t.Fatal("the old password still opens the account")
	}
	// an encrypted export (its password: by default the wallet's) into another wallet
	exp := filepath.Join(t.TempDir(), "main.key")
	if _, errOut, code := runWallet2(t, "account", "export", "main", "--out", exp, "--password-file", newPw); code != 0 {
		t.Fatalf("export: %s", errOut)
	}
	data2 := filepath.Join(t.TempDir(), "wallet2")
	t.Setenv("CREDITS_WALLET_HOME", data2)
	out, errOut, code = runWallet2(t, "account", "import", "main", exp, "--password-file", newPw, "--json")
	if code != 0 || !strings.Contains(out, addr["main"]) || !strings.Contains(out, "encrypted key file") {
		t.Fatalf("import: %d %s %s", code, out, errOut)
	}
	if k, err := openKey(data2, "main", "new wallet pw"); err != nil {
		t.Fatal(err)
	} else {
		keys.Zero(k)
	}
	// an account file of another wallet (under its wallet key) is refused with the way out
	raw := filepath.Join(data, "accounts", "main.json")
	if _, errOut, code := runWallet2(t, "account", "import", "other", raw, "--password-file", newPw); code == 0 || !strings.Contains(errOut, "another wallet's password") {
		t.Fatalf("v2 import: %d %s", code, errOut)
	}
}
