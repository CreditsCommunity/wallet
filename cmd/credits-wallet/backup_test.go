package main

import (
	"bufio"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CreditsCommunity/wallet/internal/keys"
)

var lightKDF = keys.KDFParams{Name: "argon2id", Time: keys.MinKDF.Time, MemoryKiB: keys.MinKDF.MemoryKiB, Threads: 1}

// backupEnv is a home with a data folder holding the given accounts (light key files, password
// "pw-NAME"), and a backup password file.
func backupEnv(t *testing.T, names ...string) (home, data, pwFile string, addr map[string]string) {
	t.Helper()
	old := keys.BackupKDF
	keys.BackupKDF = lightKDF
	t.Cleanup(func() { keys.BackupKDF = old })
	home, data, _ = recoveryEnv(t)
	pwFile = filepath.Join(t.TempDir(), "backup-pw")
	os.WriteFile(pwFile, []byte("backup password\n"), 0o600)
	addr = map[string]string{}
	for _, name := range names {
		addr[name] = addAccount(t, data, name, nil)
	}
	return
}

// addAccount writes a key file into data (a new key, or key) and returns its address.
func addAccount(t *testing.T, data, name string, key ed25519.PrivateKey) string {
	t.Helper()
	if key == nil {
		m, _ := keys.NewMasterSeed()
		key = keys.DeriveKey(&m)
	}
	f, err := keys.Encrypt(name, key, []byte("pw-"+name), lightKDF)
	if err != nil {
		t.Fatal(err)
	}
	if err := (keys.Store{Dir: filepath.Join(data, "accounts")}).Save(f); err != nil {
		t.Fatal(err)
	}
	return f.Public
}

func TestBackupExportImportCLI(t *testing.T) {
	home, data, pwFile, addr := backupEnv(t, "main", "savings")
	path := filepath.Join(home, "wallet.dat")

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
	// the text output: path, count, the advice, what is not in it
	out, errOut, code = runWallet2(t, "backup", "export", filepath.Join(home, "w2.dat"), "--password-file", pwFile)
	if code != 0 || !strings.Contains(out, "backup written: "+filepath.Join(home, "w2.dat")) || !strings.Contains(out, "accounts: 2") ||
		!strings.Contains(errOut, "keep it off this computer, e.g. on a USB key") || !strings.Contains(errOut, "NodePrivate.txt") {
		t.Fatalf("export text: %d\n%s\n%s", code, out, errOut)
	}

	// another wallet: savings is already there (another name), main's name is another address's
	data2 := filepath.Join(t.TempDir(), "wallet2")
	t.Setenv("CREDITS_WALLET_HOME", data2)
	savingsFile, _ := keys.Store{Dir: filepath.Join(data, "accounts")}.Load("savings")
	savingsKey, _ := savingsFile.Decrypt([]byte("pw-savings"))
	addAccount(t, data2, "old-savings", savingsKey)
	addAccount(t, data2, "main", nil)

	wrong := filepath.Join(t.TempDir(), "wrong")
	os.WriteFile(wrong, []byte("not it\n"), 0o600)
	if _, errOut, code := runWallet2(t, "backup", "import", path, "--password-file", wrong); code == 0 || !strings.Contains(errOut, "wrong backup password, or a damaged file") {
		t.Fatalf("wrong password: %d %s", code, errOut)
	}
	// without a terminal: --yes, or nothing
	if _, errOut, code := runWallet2(t, "backup", "import", path, "--password-file", pwFile); code == 0 || !strings.Contains(errOut, "add --yes") {
		t.Fatalf("no terminal: %d %s", code, errOut)
	}
	out, errOut, code = runWallet2(t, "backup", "import", path, "--password-file", pwFile, "--yes", "--rename-suffix", "-b", "--json")
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
	f, err := keys.Store{Dir: filepath.Join(data2, "accounts")}.Load("main-b")
	if err != nil || f.Public != addr["main"] {
		t.Fatalf("main-b: %v", err)
	}
	if k, err := f.Decrypt([]byte("pw-main")); err != nil {
		t.Fatalf("the restored account does not open with its own password: %v", err)
	} else {
		keys.Zero(k)
	}
}

func TestBackupImportInteractive(t *testing.T) {
	home, _, pwFile, addr := backupEnv(t, "main", "spare")
	path := filepath.Join(home, "wallet.dat")
	if _, errOut, code := runWallet2(t, "backup", "export", path, "--password-file", pwFile); code != 0 {
		t.Fatal(errOut)
	}
	data2 := filepath.Join(t.TempDir(), "wallet2")
	t.Setenv("CREDITS_WALLET_HOME", data2)
	addAccount(t, data2, "main", nil)

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
		"main will be restored as main-laptop", "Restore 2 accounts? [y/N]", "keep their own passwords"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("missing %q in\n%s", want, errOut)
		}
	}
	if !strings.Contains(out, "restored  main") || !strings.Contains(out, "as main-laptop") || !strings.Contains(out, "restored: 2, skipped: 0") {
		t.Errorf("output:\n%s", out)
	}
	// answered no: nothing restored
	data3 := filepath.Join(t.TempDir(), "wallet3")
	t.Setenv("CREDITS_WALLET_HOME", data3)
	stdinReader = bufio.NewReader(strings.NewReader("\n"))
	if _, errOut, code := runWallet2(t, "backup", "import", path, "--password-file", pwFile); code == 0 || !strings.Contains(errOut, "nothing was restored") {
		t.Fatalf("no: %d %s", code, errOut)
	}
	if entries, _ := os.ReadDir(filepath.Join(data3, "accounts")); len(entries) != 0 {
		t.Fatalf("restored after no: %v", entries)
	}
}
