package wallet

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCheckRecoveryPath(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, ".credits-wallet")
	accounts := filepath.Join(data, "accounts")
	os.MkdirAll(accounts, 0o700)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if p, err := CheckRecoveryPath("~/credits-recovery-main.txt", data, accounts); err != nil || p != filepath.Join(home, "credits-recovery-main.txt") {
		t.Fatalf("the default place: %q %v", p, err)
	}
	nodeDir := filepath.Join(home, "node")
	os.Mkdir(nodeDir, 0o700)
	os.WriteFile(filepath.Join(nodeDir, "NodePrivate.txt"), []byte("x"), 0o600)
	keysDir := filepath.Join(home, "keys")
	os.MkdirAll(filepath.Join(keysDir, "sub"), 0o700)
	os.WriteFile(filepath.Join(keysDir, "main.json"), []byte(`{"version":1,"kdf":{"name":"argon2id"},"ciphertext":"x"}`), 0o600)
	os.WriteFile(filepath.Join(home, "other.json"), []byte(`{"a":1}`), 0o600) // not a key file
	existing := filepath.Join(home, "existing.txt")
	os.WriteFile(existing, nil, 0o600)
	refused := map[string]string{
		filepath.Join(data, "w.txt"):            "data folder",
		filepath.Join(accounts, "w.txt"):        "data folder",
		filepath.Join(nodeDir, "w.txt"):         "holds key files (NodePrivate.txt)",
		filepath.Join(keysDir, "sub", "w.txt"):  "holds key files (main.json)",
		existing:                                "already exists",
		filepath.Join(home, "missing", "w.txt"): "not an existing folder",
		"":                                      "no file name",
	}
	if runtime.GOOS != "windows" {
		// a link to the data folder does not get around the check
		os.Symlink(data, filepath.Join(home, "via"))
		refused[filepath.Join(home, "via", "w.txt")] = "data folder"
		os.Symlink(filepath.Join(home, "nowhere"), filepath.Join(home, "dangling"))
		refused[filepath.Join(home, "dangling")] = "symbolic link"
	}
	for path, want := range refused {
		if _, err := CheckRecoveryPath(path, data, accounts); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", path, err, want)
		}
	}
}

func TestWriteSecretFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := WriteSecretFile(path, []byte("secret\n")); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	if err := WriteSecretFile(path, []byte("other\n")); err == nil {
		t.Error("overwritten")
	}
	if b, _ := os.ReadFile(path); string(b) != "secret\n" {
		t.Errorf("content %q", b)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(dir, "link")
		os.Symlink(filepath.Join(dir, "target"), link)
		if err := WriteSecretFile(link, []byte("x")); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Errorf("through a link: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(dir, "target")); err == nil {
			t.Error("the link's target was created")
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) > 2 {
		t.Errorf("temporary files left: %v", entries)
	}
}

func TestRecoveryText(t *testing.T) {
	words := strings.Fields(strings.Repeat("abandon ", 23) + "art")
	text := string(RecoveryText("main", "ADDR", time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC), words))
	for _, want := range []string{"account: main\n", "address: ADDR\n", "date:    2026-11-04 10:00 UTC\n", " 1. abandon\n", "24. art\n",
		"WARNING: anyone with these words controls the funds"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
}

func TestUserHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if h, err := UserHome(); err != nil || h != home {
		t.Errorf("UserHome: %q %v, want %q", h, err, home)
	}
	if p, _ := DefaultRecoveryPath("main"); p != filepath.Join(home, "credits-recovery-main.txt") {
		t.Errorf("default path %q", p)
	}
}
