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
	t.Setenv(NodeKeyEnv, "")
	cases := []struct {
		nodeKey, flag, cwd, want string
	}{
		{"", "", dir, ""}, // in the node's folder, nothing named: never guessed
		{"", "", other, ""},
		{"off", "", dir, ""},
		{"NodePrivate.txt", "", other, filepath.Join(dir, "NodePrivate.txt")}, // relative to wallet.toml
		{dir, "", other, filepath.Join(dir, "NodePrivate.txt")},               // a folder
		{filepath.Join(dir, "key.txt"), "", other, filepath.Join(dir, "key.txt")},
		{"off", dir, other, filepath.Join(dir, "NodePrivate.txt")},          // --node-key wins
		{"", "NodePrivate.txt", dir, filepath.Join(dir, "NodePrivate.txt")}, // relative to the current folder
	}
	for _, c := range cases {
		nk, err := ResolveNodeKey(config.Profile{NodeKey: c.nodeKey}, cfg, c.flag, c.cwd)
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

// $CREDITS_WALLET_NODE_KEY (the Docker image): when neither the flag nor the profile names a key.
func TestResolveNodeKeyEnv(t *testing.T) {
	dir := nodeDir(t)
	t.Setenv(NodeKeyEnv, filepath.Join(dir, "keys", "NodePrivate.txt"))
	nk, err := ResolveNodeKey(config.Profile{}, "", "", t.TempDir())
	if err != nil || nk == nil || nk.Private != filepath.Join(dir, "keys", "NodePrivate.txt") || nk.Public != filepath.Join(dir, "keys", "NodePublic.txt") || !strings.Contains(nk.Source, NodeKeyEnv) {
		t.Fatalf("%+v %v", nk, err)
	}
	if nk, err := ResolveNodeKey(config.Profile{NodeKey: "off"}, "", "", t.TempDir()); nk != nil || err != nil {
		t.Fatalf("off: %+v %v", nk, err)
	}
	t.Setenv(NodeKeyEnv, "keys/NodePrivate.txt")
	if _, err := ResolveNodeKey(config.Profile{}, "", "", dir); err == nil {
		t.Fatal("a relative path")
	}
}

// The node's key and its folder: the user's (or root's), writable by nobody else, a regular file
// or a link whose target passes the same checks; otherwise refused, the key not used.
func TestNodeKeySafety(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ACLs are not checked on Windows")
	}
	w := New(config.Default(), config.Profile{Address: config.DefaultAddress}, t.TempDir())
	use := func(dir, private string) error {
		w.NodeKey = NodeKeyIn(dir, "test")
		w.NodeKey.Private = private
		_, err := w.LoadAccount("node")
		return err
	}
	dir := nodeDir(t)
	key := filepath.Join(dir, "NodePrivate.txt")
	os.WriteFile(key, []byte(nkSecret), 0o600)
	if err := use(dir, key); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o620, 0o602, 0o666} {
		os.Chmod(key, mode)
		if err := use(dir, key); err == nil || !strings.Contains(err.Error(), "written by other users") {
			t.Errorf("key mode %v: %v", mode, err)
		}
	}
	os.Chmod(key, 0o644) // readable by others is the node's own default: accepted
	if err := use(dir, key); err != nil {
		t.Errorf("0644: %v", err)
	}
	for _, mode := range []os.FileMode{0o775, 0o757, 0o777} {
		os.Chmod(dir, mode)
		if err := use(dir, key); err == nil || !strings.Contains(err.Error(), "the node's folder") {
			t.Errorf("folder mode %v: %v", mode, err)
		}
	}
	os.Chmod(dir, 0o755)
	// owned by another user (root is accepted)
	saved := fileOwner
	t.Cleanup(func() { fileOwner = saved })
	fileOwner = func(st os.FileInfo) int { return os.Geteuid() + 4242 }
	if err := use(dir, key); err == nil || !strings.Contains(err.Error(), "belongs to another user") {
		t.Errorf("another user's: %v", err)
	}
	fileOwner = func(st os.FileInfo) int { return 0 }
	if err := use(dir, key); err != nil {
		t.Errorf("root's: %v", err)
	}
	fileOwner = saved
	// a link: its target is checked, and the target's folder
	safe := filepath.Join(t.TempDir(), "real")
	os.Mkdir(safe, 0o700)
	os.WriteFile(filepath.Join(safe, "k.txt"), []byte(nkSecret), 0o600)
	linkDir := nodeDir(t)
	link := filepath.Join(linkDir, "NodePrivate.txt")
	os.Symlink(filepath.Join(safe, "k.txt"), link)
	if err := use(linkDir, link); err != nil {
		t.Errorf("a link to a safe key: %v", err)
	}
	os.Chmod(safe, 0o777)
	if err := use(linkDir, link); err == nil || !strings.Contains(err.Error(), "the node's folder") {
		t.Errorf("a link into an open folder: %v", err)
	}
	os.Chmod(safe, 0o700)
	os.Chmod(filepath.Join(safe, "k.txt"), 0o666)
	if err := use(linkDir, link); err == nil || !strings.Contains(err.Error(), "written by other users") {
		t.Errorf("a link to an open key: %v", err)
	}
	os.Remove(filepath.Join(safe, "k.txt"))
	if err := use(linkDir, link); err == nil || !strings.Contains(err.Error(), "leads nowhere") {
		t.Errorf("a dangling link: %v", err)
	}
	// not a regular file
	fifoDir := nodeDir(t)
	os.Mkdir(filepath.Join(fifoDir, "NodePrivate.txt"), 0o700)
	if err := use(fifoDir, filepath.Join(fifoDir, "NodePrivate.txt")); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("a folder as the key: %v", err)
	}
}

// The node's key account is announced once per session, with its address and file.
func TestNodeKeyNotice(t *testing.T) {
	dir := nodeDir(t)
	os.WriteFile(filepath.Join(dir, "NodePrivate.txt"), []byte(nkSecret), 0o600)
	w := New(config.Default(), config.Profile{Address: config.DefaultAddress}, t.TempDir())
	w.NodeKey = NodeKeyIn(dir, "node_key in wallet.toml")
	var said []string
	w.NodeKeyNotice = func(s string) { said = append(said, s) }
	w.Accounts()
	w.LoadAccount("node")
	if len(said) != 1 || !strings.Contains(said[0], nkAddress) || !strings.Contains(said[0], filepath.Join(dir, "NodePrivate.txt")) || !strings.Contains(said[0], "wallet.toml") {
		t.Fatalf("%q", said)
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

// A half-written key (NodePrivate.txt without a matching NodePublic.txt): node-key new points to
// the repair, not to --replace; RepairNodePublic writes the public file (the node stopped), a
// mismatching one kept as a backup.
func TestRepairNodePublic(t *testing.T) {
	dir := nodeDir(t)
	os.WriteFile(filepath.Join(dir, "NodePrivate.txt"), []byte(nkSecret), 0o600)
	if _, err := CheckNodeKeyTarget(dir, false); err == nil || !strings.Contains(err.Error(), "NodePublic.txt is missing") ||
		!strings.Contains(err.Error(), "node-key repair") || strings.Contains(err.Error(), "--replace") {
		t.Fatalf("half-written: %v", err)
	}
	if _, err := RepairNodePublic(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "NodePublic.txt")); string(got) != nkAddress {
		t.Fatalf("%q", got)
	}
	if _, err := RepairNodePublic(dir, time.Now()); err == nil || !strings.Contains(err.Error(), "nothing to repair") {
		t.Fatalf("again: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "NodePublic.txt"), []byte("wrong"), 0o644)
	if _, err := CheckNodeKeyTarget(dir, false); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatch: %v", err)
	}
	backup, err := RepairNodePublic(dir, time.Date(2026, 11, 4, 9, 0, 0, 0, time.Local))
	if err != nil || !strings.HasSuffix(backup, "NodePublic.txt.bak-20261104-090000") {
		t.Fatalf("%q %v", backup, err)
	}
	if got, _ := os.ReadFile(backup); string(got) != "wrong" {
		t.Fatalf("backup %q", got)
	}
	if b := NodeKeyBackups(dir); len(b) != 1 || b[0] != backup {
		t.Fatalf("backups %v", b)
	}
	// the node running: refused
	os.Remove(filepath.Join(dir, "NodePublic.txt"))
	os.Mkdir(filepath.Join(dir, "db"), 0o755)
	unlock := holdLock(t, filepath.Join(dir, "db", ".lock"))
	defer unlock()
	if _, err := RepairNodePublic(dir, time.Now()); err == nil || !strings.Contains(err.Error(), "the node is running") {
		t.Fatalf("running: %v", err)
	}
}

// A key file other users can read is used, with a warning naming chmod 600.
func TestNodeKeyReadableWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ACLs are not checked on Windows")
	}
	dir := nodeDir(t)
	key := filepath.Join(dir, "NodePrivate.txt")
	os.WriteFile(key, []byte(nkSecret), 0o644)
	os.Chmod(key, 0o644)
	k, warning, err := ReadNodeKeySafe(key)
	if err != nil || k == nil || !strings.Contains(warning, "chmod 600 "+key) {
		t.Fatalf("%q %v", warning, err)
	}
	w := New(config.Default(), config.Profile{Address: config.DefaultAddress}, t.TempDir())
	w.NodeKey = NodeKeyIn(dir, "test")
	var said []string
	w.NodeKeyNotice = func(s string) { said = append(said, s) }
	w.Accounts()
	if len(said) != 2 || !strings.HasPrefix(said[1], "warning: ") {
		t.Fatalf("%q", said)
	}
	os.Chmod(key, 0o600)
	if _, warning, _ := ReadNodeKeySafe(key); warning != "" || NodeKeyModeWarning(key) != "" {
		t.Fatalf("0600 warned: %q", warning)
	}
}

// The key that signs must be the one the plan was made for.
func TestSignRefusesAnotherKey(t *testing.T) {
	dir := nodeDir(t)
	os.WriteFile(filepath.Join(dir, "NodePrivate.txt"), []byte(nkSecret), 0o600)
	w := New(config.Default(), config.Profile{Address: config.DefaultAddress}, t.TempDir())
	w.NodeKey = NodeKeyIn(dir, "test")
	other := newKey(t)
	p := &SendPlan{Kind: KindTransfer, Account: "node", NodeKey: true, From: other.Public().(ed25519.PublicKey), To: other.Public().(ed25519.PublicKey)}
	if _, err := w.sign(p, nil); err == nil || !strings.Contains(err.Error(), "no longer the one") {
		t.Fatalf("%v", err)
	}
}
