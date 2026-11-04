package wallet

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/keys"
)

// the node's sample clients' throwaway key (public, never fund it)
const (
	nkSecret  = "1GMkH3brNXiNNs1tiFZHu4yZSRrzJwxi5wB9bHFtMikjwpAW9DMZzU2Pqakc5it8X3N5vPmqdN7KF4CCUpmKhq"
	nkAddress = "FAe4sisG95oZ42w7buUn5qEE4TAnfTTFPiguZUHmhiF"
)

// nodeDir makes a node's folder (the node program and config.ini, API port 0: off).
func nodeDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "csnode")
	os.Mkdir(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "node"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "config.ini"), []byte("[api]\nport=0\n"), 0o644)
	return dir
}

func newKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	m, err := keys.NewMasterSeed()
	if err != nil {
		t.Fatal(err)
	}
	return keys.DeriveKey(&m)
}

func TestResolveNodeKey(t *testing.T) {
	dir := nodeDir(t)
	other := t.TempDir()
	cfg := filepath.Join(dir, "wallet.toml")
	cases := []struct {
		nodeKey, cwd, want string
	}{
		{"", dir, filepath.Join(dir, "NodePrivate.txt")}, // in the node's folder
		{"", other, ""},  // elsewhere: not a node's wallet
		{"off", dir, ""}, // turned off
		{"NodePrivate.txt", other, filepath.Join(dir, "NodePrivate.txt")}, // relative to wallet.toml
		{dir, other, filepath.Join(dir, "NodePrivate.txt")},               // a folder
		{filepath.Join(dir, "key.txt"), other, filepath.Join(dir, "key.txt")},
	}
	t.Setenv(NodeKeyEnv, "")
	for _, c := range cases {
		nk, err := ResolveNodeKey(config.Profile{NodeKey: c.nodeKey}, cfg, c.cwd)
		got := ""
		if nk != nil {
			got = nk.Private
			if nk.Public != filepath.Join(dir, "NodePublic.txt") {
				t.Errorf("%+v: public %s", c, nk.Public)
			}
		}
		if err != nil || got != c.want {
			t.Errorf("%+v: %q %v", c, got, err)
		}
	}
}

// $CREDITS_WALLET_NODE_KEY (the Docker image): when the profile names no key; "off" wins.
func TestResolveNodeKeyEnv(t *testing.T) {
	dir := nodeDir(t)
	t.Setenv(NodeKeyEnv, filepath.Join(dir, "keys", "NodePrivate.txt"))
	nk, err := ResolveNodeKey(config.Profile{}, "", t.TempDir())
	if err != nil || nk == nil || nk.Private != filepath.Join(dir, "keys", "NodePrivate.txt") || nk.Public != filepath.Join(dir, "keys", "NodePublic.txt") || !strings.Contains(nk.Source, NodeKeyEnv) {
		t.Fatalf("%+v %v", nk, err)
	}
	if nk, err := ResolveNodeKey(config.Profile{NodeKey: "off"}, "", t.TempDir()); nk != nil || err != nil {
		t.Fatalf("off: %+v %v", nk, err)
	}
	t.Setenv(NodeKeyEnv, "keys/NodePrivate.txt")
	if _, err := ResolveNodeKey(config.Profile{}, "", dir); err == nil {
		t.Fatal("a relative path")
	}
}

func TestNodeAccount(t *testing.T) {
	dir := nodeDir(t)
	w := New(config.Default(), config.Profile{Address: config.DefaultAddress}, t.TempDir())
	w.KDF = keys.MinKDF
	w.KDF.Name = "argon2id"
	w.NodeKey = NodeKeyIn(dir, "test")
	if !w.NodeKeyMissing() {
		t.Fatal("no key yet: missing")
	}
	if _, err := w.LoadAccount("node"); err == nil || !strings.Contains(err.Error(), "has no key yet") {
		t.Fatalf("missing key: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "NodePrivate.txt"), []byte(nkSecret), 0o600)
	other := newKey(t)
	if _, err := w.CreateAccount("savings", other, []byte("pw")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.CreateAccount("node", other, []byte("pw")); err == nil {
		t.Fatal("an account of the store named like the node's key account")
	}
	files, problems, err := w.Accounts()
	if err != nil || len(problems) > 0 || len(files) != 2 || files[0].Name != "node" || !files[0].IsNodeKey() || files[0].Public != nkAddress || files[1].Name != "savings" {
		t.Fatalf("accounts: %v %v %v", files, problems, err)
	}
	f, err := w.LoadAccount("node")
	if err != nil {
		t.Fatal(err)
	}
	key, err := f.Decrypt(nil) // no password: the node's key is in clear
	if err != nil || keys.Base58(key) != nkSecret {
		t.Fatalf("decrypt: %v", err)
	}
	// the file changed after the account was read: refused
	os.WriteFile(filepath.Join(dir, "NodePrivate.txt"), []byte(keys.Base58(other)), 0o600)
	if _, err := f.Decrypt(nil); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed key: %v", err)
	}
	// an encrypted node key is reported, not used
	os.WriteFile(filepath.Join(dir, "NodePrivate.txt"), []byte(strings.Repeat("z", 150)), 0o600)
	if _, problems, _ := w.Accounts(); len(problems) != 1 || !strings.Contains(problems[0].Error(), "encrypted by the node") {
		t.Fatalf("encrypted: %v", problems)
	}
}

// An account of the store named "node" (an earlier import of the node's key) keeps its name: the
// node's key account is then "node-key".
func TestNodeAccountNameTaken(t *testing.T) {
	dir := nodeDir(t)
	w := New(config.Default(), config.Profile{Address: config.DefaultAddress}, t.TempDir())
	w.KDF = keys.MinKDF
	w.KDF.Name = "argon2id"
	if _, err := w.CreateAccount("node", newKey(t), []byte("pw")); err != nil {
		t.Fatal(err)
	}
	w.NodeKey = NodeKeyIn(dir, "test")
	os.WriteFile(filepath.Join(dir, "NodePrivate.txt"), []byte(nkSecret), 0o600)
	files, _, _ := w.Accounts()
	if len(files) != 2 || files[0].Name != "node-key" || !files[0].IsNodeKey() || files[1].Name != "node" || files[1].IsNodeKey() {
		t.Fatalf("%v", files)
	}
}

func TestWriteNodeKey(t *testing.T) {
	dir := nodeDir(t)
	key := newKey(t)
	if err := WriteNodeKey(dir, key); err != nil {
		t.Fatal(err)
	}
	priv, pub := keys.NodeKeyFiles(key)
	for name, want := range map[string][]byte{"NodePrivate.txt": priv, "NodePublic.txt": pub} {
		got, _ := os.ReadFile(filepath.Join(dir, name))
		if string(got) != string(want) {
			t.Errorf("%s: %q", name, got)
		}
		if st, _ := os.Stat(filepath.Join(dir, name)); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %v", name, st.Mode().Perm())
		}
	}
	// never over a key the node has
	if err := WriteNodeKey(dir, newKey(t)); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("over a key: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "NodePrivate.txt"))
	if string(got) != string(priv) {
		t.Fatal("the key was changed")
	}
	// NodePublic.txt alone is enough to refuse
	dir2 := nodeDir(t)
	os.WriteFile(filepath.Join(dir2, "NodePublic.txt"), pub, 0o644)
	if err := WriteNodeKey(dir2, key); err == nil || !strings.Contains(err.Error(), "NodePublic.txt already exists") {
		t.Fatalf("public only: %v", err)
	}
	// a file left by an interrupted write
	dir3 := nodeDir(t)
	os.WriteFile(filepath.Join(dir3, "NodePrivate.txt.partial"), []byte("x"), 0o600)
	if err := WriteNodeKey(dir3, key); err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("partial: %v", err)
	}
	// not a folder
	if err := WriteNodeKey(filepath.Join(dir, "config.ini"), key); err == nil {
		t.Fatal("a file as the folder")
	}
}

func TestWriteNodeKeySymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	key := newKey(t)
	target := filepath.Join(t.TempDir(), "elsewhere")
	for _, name := range []string{"NodePrivate.txt", "NodePublic.txt"} {
		dir := nodeDir(t)
		os.Symlink(target, filepath.Join(dir, name)) // dangling: writing through it would create target
		for _, replace := range []bool{false, true} {
			if _, err := CheckNodeKeyTarget(dir, replace); err == nil || !strings.Contains(err.Error(), "symbolic link") {
				t.Errorf("%s (replace %v): %v", name, replace, err)
			}
		}
		if err := WriteNodeKey(dir, key); err == nil {
			t.Errorf("%s: written through a link", name)
		}
		if _, err := ReplaceNodeKey(dir, key, time.Now()); err == nil {
			t.Errorf("%s: replaced through a link", name)
		}
		if _, err := os.Lstat(target); err == nil {
			t.Fatalf("%s: the link's target was created", name)
		}
	}
	// the folder may be a link to the node's folder: its real path is used
	dir := nodeDir(t)
	link := filepath.Join(t.TempDir(), "csnode-link")
	os.Symlink(dir, link)
	if err := WriteNodeKey(link, key); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "NodePrivate.txt")); err != nil {
		t.Fatal(err)
	}
	// a folder every user can write to
	open := nodeDir(t)
	os.Chmod(open, 0o777)
	if err := WriteNodeKey(open, key); err == nil || !strings.Contains(err.Error(), "every user") {
		t.Fatalf("o+w folder: %v", err)
	}
}

// The node runs: its lock on db/.lock, its API port, refused.
func TestWriteNodeKeyNodeRunning(t *testing.T) {
	dir := nodeDir(t)
	os.Mkdir(filepath.Join(dir, "db"), 0o755)
	unlock := holdLock(t, filepath.Join(dir, "db", ".lock"))
	if why := NodeRunning(dir); !strings.Contains(why, ".lock") {
		t.Fatalf("lock: %q", why)
	}
	if err := WriteNodeKey(dir, newKey(t)); err == nil || !strings.Contains(err.Error(), "the node is running") {
		t.Fatalf("with the lock held: %v", err)
	}
	unlock()
	if why := NodeRunning(dir); why != "" {
		t.Fatalf("the lock released: %q", why)
	}
	// the API port of its config.ini answers
	os.WriteFile(filepath.Join(dir, "config.ini"), []byte("[params]\nport=1\n[api]\nport=9090\n"), 0o644)
	saved := apiAnswers
	t.Cleanup(func() { apiAnswers = saved })
	asked := ""
	apiAnswers = func(addr string) bool { asked = addr; return true }
	if err := WriteNodeKey(dir, newKey(t)); err == nil || !strings.Contains(err.Error(), "127.0.0.1:9090") || asked != "127.0.0.1:9090" {
		t.Fatalf("API answers: %v (%s)", err, asked)
	}
	apiAnswers = func(string) bool { return false }
	if err := WriteNodeKey(dir, newKey(t)); err != nil {
		t.Fatal(err)
	}
	// replacing: also refused while it runs
	unlock = holdLock(t, filepath.Join(dir, "db", ".lock"))
	defer unlock()
	if _, err := ReplaceNodeKey(dir, newKey(t), time.Now()); err == nil || !strings.Contains(err.Error(), "the node is running") {
		t.Fatalf("replace while running: %v", err)
	}
}

// The Docker layout: the keys in /data/keys, the node (its db/.lock, its config.ini) in /data.
func TestNodeRunningDockerLayout(t *testing.T) {
	data := nodeDir(t)
	keysDir := filepath.Join(data, "keys")
	os.Mkdir(keysDir, 0o700)
	os.Mkdir(filepath.Join(data, "db"), 0o755)
	unlock := holdLock(t, filepath.Join(data, "db", ".lock"))
	defer unlock()
	if why := NodeRunning(keysDir); !strings.Contains(why, ".lock") {
		t.Fatalf("%q", why)
	}
}

func TestReplaceNodeKey(t *testing.T) {
	dir := nodeDir(t)
	old := newKey(t)
	if err := WriteNodeKey(dir, old); err != nil {
		t.Fatal(err)
	}
	oldPriv, oldPub := keys.NodeKeyFiles(old)
	if _, err := ReplaceNodeKey(dir, old, time.Now()); err == nil || !strings.Contains(err.Error(), "already has this key") {
		t.Fatalf("the same key: %v", err)
	}
	key := newKey(t)
	now := time.Date(2026, 11, 4, 12, 30, 5, 0, time.Local)
	backups, err := ReplaceNodeKey(dir, key, now)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "NodePrivate.txt.bak-20261104-123005"), filepath.Join(dir, "NodePublic.txt.bak-20261104-123005")}
	if strings.Join(backups, " ") != strings.Join(want, " ") {
		t.Fatalf("backups %v", backups)
	}
	for i, b := range backups {
		got, _ := os.ReadFile(b)
		if string(got) != string([][]byte{oldPriv, oldPub}[i]) {
			t.Errorf("%s: %q", b, got)
		}
		if st, _ := os.Stat(b); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %v", b, st.Mode().Perm())
		}
	}
	if err := checkNodeFiles(dir, key); err != nil {
		t.Fatal(err)
	}
	// a second replacement in the same second: the backup exists, nothing is replaced
	if _, err := ReplaceNodeKey(dir, newKey(t), now); err == nil || !strings.Contains(err.Error(), "nothing was replaced") {
		t.Fatalf("backup exists: %v", err)
	}
	if err := checkNodeFiles(dir, key); err != nil {
		t.Fatal(err)
	}
	// a folder without a key: written, no backup
	empty := nodeDir(t)
	if b, err := ReplaceNodeKey(empty, key, now); err != nil || len(b) != 0 {
		t.Fatalf("no key before: %v %v", b, err)
	}
}

// The recovery file never goes in the node's folder, even before the node's key is written there.
func TestRecoveryNotInNodeFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	data := filepath.Join(home, ".credits-wallet")
	dir := nodeDir(t)
	os.Mkdir(filepath.Join(dir, "logs"), 0o755)
	for _, p := range []string{filepath.Join(dir, "words.txt"), filepath.Join(dir, "logs", "words.txt")} {
		if _, err := CheckRecoveryPath(p, data, filepath.Join(data, "accounts"), dir); err == nil || !strings.Contains(err.Error(), "node's folder") {
			t.Errorf("%s: %v", p, err)
		}
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(home, "via")
		os.Symlink(dir, link)
		if _, err := CheckRecoveryPath(filepath.Join(link, "w.txt"), data, filepath.Join(data, "accounts"), dir); err == nil || !strings.Contains(err.Error(), "node's folder") {
			t.Errorf("through a link: %v", err)
		}
	}
	if _, err := CheckRecoveryPath(filepath.Join(home, "w.txt"), data, filepath.Join(data, "accounts"), dir); err != nil {
		t.Errorf("outside: %v", err)
	}
	// the wallet of the node refuses its own node's folder
	w := New(config.Default(), config.Profile{Address: config.DefaultAddress}, data)
	w.NodeKey = NodeKeyIn(dir, "test")
	if _, err := w.CheckRecoveryPath(filepath.Join(dir, "w.txt")); err == nil {
		t.Error("the node's folder of a node's wallet")
	}
}

func TestAPIPort(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.ini")
	for text, want := range map[string]string{
		"[api]\nport=9090\n":                        "9090",
		"[params]\nport=1\n[api]\n port = 9101\r\n": "9101",
		"[api]\nport=0\n":                           "",
		"[api]\nexecutor_port=9080\n":               "",
		"":                                          "",
	} {
		os.WriteFile(p, []byte(text), 0o644)
		if got := apiPort(p); got != want {
			t.Errorf("%q: %q", text, got)
		}
	}
}
