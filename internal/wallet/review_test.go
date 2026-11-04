package wallet

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// M1: the data folder is recognised by identity, not by its name.
func TestCheckRecoveryPathAliases(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "Data")
	accounts := filepath.Join(data, "accounts")
	os.MkdirAll(accounts, 0o700)
	// a case variant, where the file system ignores case (macOS, Windows)
	upper := filepath.Join(home, "DATA")
	if a, err := os.Stat(upper); err == nil {
		if b, _ := os.Stat(data); os.SameFile(a, b) {
			if _, err := CheckRecoveryPath(filepath.Join(upper, "w.txt"), data, accounts); err == nil || !strings.Contains(err.Error(), "data folder") {
				t.Errorf("a case variant of the data folder: %v", err)
			}
		}
	} else {
		t.Log("case-sensitive file system: no case variant to try")
	}
	if runtime.GOOS == "windows" {
		return
	}
	// a link above the data folder, and the data folder given through a link
	os.Symlink(home, filepath.Join(t.TempDir(), "x"))
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Skip(err)
	}
	for _, p := range []string{filepath.Join(alias, "Data", "w.txt"), filepath.Join(alias, "Data", "accounts", "w.txt")} {
		if _, err := CheckRecoveryPath(p, data, accounts); err == nil || !strings.Contains(err.Error(), "data folder") {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := CheckRecoveryPath(filepath.Join(home, "w.txt"), alias+"/Data", alias+"/Data/accounts"); err != nil {
		t.Errorf("outside the data folder (given through a link): %v", err)
	}
	if _, err := CheckRecoveryPath(filepath.Join(data, "w.txt"), alias+"/Data", alias+"/Data/accounts"); err == nil {
		t.Error("the data folder given through a link was not recognised")
	}
}

// L4: more kinds of key files, and a folder that cannot be listed.
func TestKeyFileDetection(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, ".credits-wallet")
	cases := map[string]func(dir string){
		"base58 secret": func(dir string) {
			os.WriteFile(filepath.Join(dir, "backup.txt"), []byte("1GMkH3brNXiNNs1tiFZHu4yZSRrzJwxi5wB9bHFtMikjwpAW9DMZzU2Pqakc5it8X3N5vPmqdN7KF4CCUpmKhq\n"), 0o600)
		},
		"NodePrivate copy": func(dir string) { os.WriteFile(filepath.Join(dir, "NodePrivate.txt.bak"), []byte("x"), 0o600) },
		"encrypted export": func(dir string) {
			os.WriteFile(filepath.Join(dir, "main.key"), []byte(`{"version":1,"kdf":{"name":"argon2id"},"ciphertext":"x"}`), 0o600)
		},
		"official JSON": func(dir string) {
			os.WriteFile(filepath.Join(dir, "keys"), []byte(`{"key":{"public":"a","private":"b"}}`), 0o600)
		},
	}
	if runtime.GOOS != "windows" {
		cases["link to a key file"] = func(dir string) {
			target := filepath.Join(t.TempDir(), "k.json")
			os.WriteFile(target, []byte(`{"key":{"private":"b"}}`), 0o600)
			os.Symlink(target, filepath.Join(dir, "innocent"))
		}
	}
	n := 0
	for name, plant := range cases {
		n++
		dir := filepath.Join(home, fmt.Sprintf("case%d", n))
		os.Mkdir(dir, 0o700)
		plant(dir)
		if _, err := CheckRecoveryPath(filepath.Join(dir, "w.txt"), data, ""); err == nil || !strings.Contains(err.Error(), "holds key files") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// an address (32 bytes) and ordinary text are not key files
	plain := filepath.Join(home, "plain")
	os.Mkdir(plain, 0o700)
	os.WriteFile(filepath.Join(plain, "address.txt"), []byte("u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w\n"), 0o600)
	os.WriteFile(filepath.Join(plain, "notes.json"), []byte(`{"a":1}`), 0o600)
	if _, err := CheckRecoveryPath(filepath.Join(plain, "w.txt"), data, ""); err != nil {
		t.Errorf("a folder without keys: %v", err)
	}
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		closed := filepath.Join(home, "closed")
		os.Mkdir(closed, 0o300) // writable, not listable
		defer os.Chmod(closed, 0o700)
		if _, err := CheckRecoveryPath(filepath.Join(closed, "w.txt"), data, ""); err == nil || !strings.Contains(err.Error(), "cannot be listed") {
			t.Errorf("a folder that cannot be listed: %v", err)
		}
	}
}

// L1: a file swapped at the path after the link is removed, and the write fails.
func TestSameFileSwap(t *testing.T) {
	dir := t.TempDir()
	mine, _ := os.Create(filepath.Join(dir, "mine"))
	defer mine.Close()
	other := filepath.Join(dir, "other")
	os.WriteFile(other, []byte("planted"), 0o600)
	if err := sameFile(mine, other); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("a swapped file: %v", err)
	}
	if _, err := os.Lstat(other); err == nil {
		t.Error("the swapped file was not removed")
	}
	if err := sameFile(mine, filepath.Join(dir, "mine")); err != nil {
		t.Errorf("the same file: %v", err)
	}
}

// L2: the named fallback (PATH.partial) — used where O_TMPFILE is not — leaves nothing behind,
// and refuses while a .partial from an interrupted write is there.
func TestWriteSecretFileNamedFallback(t *testing.T) {
	saved := unnamedWrite
	defer func() { unnamedWrite = saved }()
	unnamedWrite = func(string, []byte) (bool, error) { return false, nil }
	dir := t.TempDir()
	path := filepath.Join(dir, "credits-recovery-main.txt")
	if err := WriteSecretFile(path, []byte("secret\n")); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("left behind: %v", entries)
	}
	if st, _ := os.Stat(path); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	path2 := filepath.Join(dir, "second.txt")
	os.WriteFile(path2+".partial", []byte("old"), 0o600)
	if err := WriteSecretFile(path2, []byte("x")); err == nil || !strings.Contains(err.Error(), "interrupted write") {
		t.Errorf("with a .partial there: %v", err)
	}
	if _, err := os.Lstat(path2); err == nil {
		t.Error("written although a .partial was there")
	}
}

// L2 on Linux: the unnamed file leaves nothing but the file.
func TestWriteSecretFileUnnamed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	done, err := writeUnnamed(path, []byte("secret\n"))
	if runtime.GOOS != "linux" {
		if done {
			t.Error("unnamed write outside Linux")
		}
		return
	}
	if !done {
		t.Skip("O_TMPFILE not supported here")
	}
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "secret\n" {
		t.Errorf("content %q", b)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("left behind: %v", entries)
	}
	if done, err := writeUnnamed(path, []byte("x")); !done || err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("over an existing file: %v %v", done, err)
	}
}
