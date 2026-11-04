package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mr-tron/base58"

	"github.com/CreditsCommunity/wallet/internal/keys"
)

// testNodeDir makes a node's folder (the node program and config.ini, the API off).
func testNodeDir(t *testing.T, parent string) string {
	t.Helper()
	dir := filepath.Join(parent, "csnode")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "node"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "config.ini"), []byte("[api]\nport=0\n"), 0o644)
	return dir
}

// interactive runs the command line as on a terminal, with the master seed fixed and words 1, 2
// and 3 typed back, then typed.
func interactive(t *testing.T, typed string, args ...string) ([]string, string, string, int) {
	t.Helper()
	seed := fixedSeed
	words := keys.Words(&seed)
	savedTerm, savedReader, savedSeed, savedPos := stdinIsTerminal, stdinReader, newMasterSeed, checkPositions
	t.Cleanup(func() {
		stdinIsTerminal, stdinReader, newMasterSeed, checkPositions = savedTerm, savedReader, savedSeed, savedPos
	})
	stdinIsTerminal = func() bool { return true }
	newMasterSeed = func() ([keys.MasterSeedSize]byte, error) { return fixedSeed, nil }
	checkPositions = func(int) []int { return []int{0, 1, 2} }
	stdinReader = bufio.NewReader(strings.NewReader(words[0] + "\n" + words[1] + "\n" + words[2] + "\n" + typed))
	out, errOut, code := runWallet2(t, args...)
	return words, out, errOut, code
}

// checkNodeFiles: the folder holds the key of words, in the node's format.
func checkNodeFiles(t *testing.T, dir string, words []string) string {
	t.Helper()
	want, err := keys.FromWords(words)
	if err != nil {
		t.Fatal(err)
	}
	priv, pub := keys.NodeKeyFiles(want)
	for name, content := range map[string][]byte{"NodePrivate.txt": priv, "NodePublic.txt": pub} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(got) != string(content) {
			t.Fatalf("%s: %q %v", name, got, err)
		}
		if st, _ := os.Stat(filepath.Join(dir, name)); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %v", name, st.Mode().Perm())
		}
	}
	return string(pub)
}

// account node-key new on a terminal: the words shown once and checked, the recovery file refused
// in the node's folder and written outside it, the node's key written in the node's format.
func TestNodeKeyNewInteractive(t *testing.T) {
	home, _, _ := recoveryEnv(t)
	dir := testNodeDir(t, t.TempDir())
	typed := "y\n" + filepath.Join(dir, "words.txt") + "\n\n"
	words, out, errOut, code := interactive(t, typed, "account", "node-key", "new", "--out", dir)
	if code != 0 {
		t.Fatalf("node-key new: %d\n%s\n%s", code, out, errOut)
	}
	address := checkNodeFiles(t, dir, words)
	if !strings.Contains(out, "address : "+address) {
		t.Errorf("output: %s", out)
	}
	for _, want := range []string{"in the node's folder", "The node's key is kept in clear on this server", "Save the 24 words to a file as well?"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("missing %q:\n%s", want, errOut)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "words.txt")); err == nil {
		t.Error("the recovery file was written in the node's folder")
	}
	text, err := os.ReadFile(filepath.Join(home, "credits-recovery-node.txt"))
	if err != nil || !strings.Contains(string(text), "address: "+address) || !strings.Contains(string(text), "24. "+words[23]+"\n") {
		t.Fatalf("recovery file: %v\n%s", err, text)
	}
	assertNoLeak(t, words, out, errOut)
	// a second time: the node has a key, refused before any word is shown
	_, out, errOut, code = interactive(t, "", "account", "node-key", "new", "--out", dir)
	if code == 0 || !strings.Contains(errOut, "already exists") || strings.Contains(errOut, "seed phrase") {
		t.Fatalf("over a key: %d\n%s\n%s", code, out, errOut)
	}
}

// Without a terminal: --words-file (never in the node's folder) or --show-words.
func TestNodeKeyNewScripted(t *testing.T) {
	recoveryEnv(t)
	parent := t.TempDir()
	dir := testNodeDir(t, parent)
	if _, errOut, code := runWallet2(t, "account", "node-key", "new", "--out", dir); code == 0 || !strings.Contains(errOut, "no terminal") {
		t.Fatalf("no terminal: %d %s", code, errOut)
	}
	if _, errOut, code := runWallet2(t, "account", "node-key", "new", "--out", dir, "--words-file", filepath.Join(dir, "w.txt")); code == 0 || !strings.Contains(errOut, "node's folder") {
		t.Fatalf("words in the node's folder: %d %s", code, errOut)
	}
	if len(filesIn(t, dir)) != 2 {
		t.Fatalf("files written: %v", filesIn(t, dir))
	}
	wordsFile := filepath.Join(parent, "words.txt")
	out, errOut, code := runWallet2(t, "account", "node-key", "new", "--out", dir, "--words-file", wordsFile, "--json")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	text, _ := os.ReadFile(wordsFile)
	address := checkNodeFiles(t, dir, strings.Fields(string(text)))
	if !strings.Contains(out, `"address": "`+address+`"`) || strings.Contains(out, strings.Fields(string(text))[0]+" ") {
		t.Fatalf("output: %s", out)
	}
	// not a node's wallet and no --out
	if _, errOut, code := runWallet2(t, "account", "node-key", "new", "--words-file", filepath.Join(parent, "w2.txt")); code == 0 || !strings.Contains(errOut, "not a node's wallet") {
		t.Fatalf("no folder: %d %s", code, errOut)
	}
}

// The node runs (its API answers on the port of its config.ini): refused, no word shown.
func TestNodeKeyNewNodeRunning(t *testing.T) {
	recoveryEnv(t)
	dir := testNodeDir(t, t.TempDir())
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	os.WriteFile(filepath.Join(dir, "config.ini"), []byte(fmt.Sprintf("[api]\nport=%d\n", l.Addr().(*net.TCPAddr).Port)), 0o644)
	_, out, errOut, code := interactive(t, "", "account", "node-key", "new", "--out", dir)
	if code == 0 || !strings.Contains(errOut, "the node is running") || strings.Contains(errOut, "seed phrase") {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	if len(filesIn(t, dir)) != 2 {
		t.Fatalf("files written: %v", filesIn(t, dir))
	}
}

// The wallet of a node: the node's key is the account "node", marked, the default, and it signs
// without a password; the fake node checks the signature.
func TestNodeKeyAccountSends(t *testing.T) {
	fake := startFakeNode(t)
	home := t.TempDir()
	t.Setenv("CREDITS_WALLET_HOME", filepath.Join(home, "data"))
	dir := testNodeDir(t, home)
	os.WriteFile(filepath.Join(dir, "NodePrivate.txt"), []byte(testKey), 0o600)
	os.WriteFile(filepath.Join(dir, "NodePublic.txt"), []byte(testAddr), 0o600)
	cfg := filepath.Join(dir, "wallet.toml")
	os.WriteFile(cfg, []byte(fmt.Sprintf("node = \"local\"\n[nodes.local]\naddress = %q\nnetwork = \"private\"\ndiag = \"off\"\nnode_key = \"NodePrivate.txt\"\n", fake.addr)), 0o644)
	c := "--config=" + cfg

	out, errOut, code := runWallet2(t, "account", "list", c)
	if code != 0 || !strings.Contains(out, "node                 "+testAddr+"  NODE KEY (default;") || !strings.Contains(errOut, "kept in clear on this server") {
		t.Fatalf("list: %d\n%s\n%s", code, out, errOut)
	}
	if out, code := runWallet(t, "account", "address", c); code != 0 || strings.TrimSpace(out) != testAddr {
		t.Fatalf("default address: %d %s", code, out)
	}
	if out, code := runWallet(t, "account", "node-key", c); code != 0 || !strings.Contains(out, "public file : ") || !strings.Contains(out, "matches") || !strings.Contains(out, "not seen running") {
		t.Fatalf("node-key: %d %s", code, out)
	}
	if out, code := runWallet(t, "balance", c); code != 0 || !strings.Contains(out, "node ") {
		t.Fatalf("balance: %d %s", code, out)
	}
	// no --password-file: the node's key needs none
	out, errOut, code = runWallet2(t, "send", "node", toAddr, "1.5", c, "--yes")
	if code != 0 || !strings.Contains(out, "sent 1.5 CS to "+toAddr) || !strings.Contains(errOut, "NODE KEY") {
		t.Fatalf("send: %d\n%s\n%s\n%s", code, out, errOut, fake.Log())
	}
	// the store's own export formats: plain ones without a password, never "encrypted"
	if out, code := runWallet(t, "account", "export", "node", "--format", "base58", c); code != 0 || strings.TrimSpace(out) != testKey {
		t.Fatalf("export base58: %d", code)
	}
	if _, errOut, code := runWallet2(t, "account", "export", "node", c); code == 0 || !strings.Contains(errOut, "not in the key store") {
		t.Fatalf("export encrypted: %d %s", code, errOut)
	}
	if _, errOut, code := runWallet2(t, "account", "export", "node", "--format", "node", "--out", t.TempDir(), c); code == 0 || !strings.Contains(errOut, "the node's key already") {
		t.Fatalf("export node of the node: %d %s", code, errOut)
	}
	// the account is announced once with its address and file
	if !strings.Contains(errOut, "node key account node: "+testAddr+", read from "+filepath.Join(dir, "NodePrivate.txt")) {
		t.Errorf("no notice of the node's key:\n%s", errOut)
	}
	// never guessed from the current folder (a planted key pair would become the default account);
	// --node-key names it
	t.Chdir(dir)
	out, errOut, code = runWallet2(t, "account", "list", "--node="+fake.addr)
	if code != 0 || strings.Contains(out, testAddr) {
		t.Fatalf("guessed from the current folder: %d %s", code, out)
	}
	// only a one-line hint on stderr
	if strings.Count(errOut, "This folder holds a node key: to use it, --node-key NodePrivate.txt") != 1 {
		t.Errorf("no hint:\n%s", errOut)
	}
	if out, code := runWallet(t, "account", "address", "--node-key", "NodePrivate.txt"); code != 0 || strings.TrimSpace(out) != testAddr {
		t.Fatalf("--node-key: %d %s", code, out)
	}
	// a key file others can write to is refused, not used
	os.Chmod(filepath.Join(dir, "NodePrivate.txt"), 0o666)
	if runtime.GOOS != "windows" {
		if _, errOut, code := runWallet2(t, "account", "address", "--node-key", "NodePrivate.txt"); code == 0 || !strings.Contains(errOut, "written by other users") {
			t.Fatalf("an open key: %d %s", code, errOut)
		}
	}
	os.Chmod(filepath.Join(dir, "NodePrivate.txt"), 0o600)
	// the same profile with node_key = "off": no node account
	os.WriteFile(cfg, []byte(fmt.Sprintf("[nodes.local]\naddress = %q\nnetwork = \"private\"\nnode_key = \"off\"\n", fake.addr)), 0o644)
	if out, code := runWallet(t, "account", "list", c); code != 0 || strings.Contains(out, testAddr) {
		t.Fatalf("off: %d %s", code, out)
	}
}

// account export NAME --format node: never over the node's key; --replace keeps backups.
func TestExportNodeFormat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CREDITS_WALLET_HOME", filepath.Join(home, "data"))
	pwFile := filepath.Join(home, "pw")
	os.WriteFile(pwFile, []byte("a test password\n"), 0o600)
	keyFile := filepath.Join(home, "key.txt")
	os.WriteFile(keyFile, []byte(testKey+"\n"), 0o600)
	if _, code := runWallet(t, "account", "import", "main", keyFile, "--password-file", pwFile); code != 0 {
		t.Fatal("import")
	}
	dir := testNodeDir(t, home)
	if _, errOut, code := runWallet2(t, "account", "export", "main", "--format", "node", "--out", dir, "--password-file", pwFile); code == 0 || !strings.Contains(errOut, "--yes") {
		t.Fatalf("no terminal, no --yes: %d %s", code, errOut)
	}
	out, errOut, code := runWallet2(t, "account", "export", "main", "--format", "node", "--out", dir, "--password-file", pwFile, "--yes")
	if code != 0 || !strings.Contains(errOut, "identity follows its key") {
		t.Fatalf("export: %d %s %s", code, out, errOut)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "NodePrivate.txt")); string(got) != testKey {
		t.Fatalf("NodePrivate.txt %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "NodePublic.txt")); string(got) != testAddr {
		t.Fatalf("NodePublic.txt %q", got)
	}
	// another account over the node's key: refused without --replace
	os.WriteFile(keyFile+"2", []byte(strings.Repeat("abandon ", 23)+"craft\n"), 0o600)
	if _, code := runWallet(t, "account", "import", "other", keyFile+"2", "--password-file", pwFile); code != 0 {
		t.Fatal("import other")
	}
	if _, errOut, code := runWallet2(t, "account", "export", "other", "--format", "node", "--out", dir, "--password-file", pwFile, "--yes"); code == 0 || !strings.Contains(errOut, "already exists") {
		t.Fatalf("over the key: %d %s", code, errOut)
	}
	if _, _, code := runWallet2(t, "account", "export", "other", "--replace", "--out", dir, "--password-file", pwFile, "--yes"); code != 2 {
		t.Fatalf("--replace without --format node: %d", code)
	}
	out, errOut, code = runWallet2(t, "account", "export", "other", "--format", "node", "--out", filepath.Join(dir, "NodePrivate.txt"), "--replace", "--password-file", pwFile, "--yes")
	if code != 0 || !strings.Contains(errOut, "REPLACES the node's key") || !strings.Contains(errOut, testAddr) || strings.Count(out, "backup  : ") != 2 {
		t.Fatalf("replace: %d\n%s\n%s", code, out, errOut)
	}
	checkNodeFiles(t, dir, strings.Fields(strings.Repeat("abandon ", 23)+"craft"))
	backups, _ := filepath.Glob(filepath.Join(dir, "NodePrivate.txt.bak-*"))
	if len(backups) != 1 {
		t.Fatalf("backups %v", backups)
	}
	if got, _ := os.ReadFile(backups[0]); string(got) != testKey {
		t.Fatalf("backup %q", got)
	}
	if st, _ := os.Stat(backups[0]); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode %v", st.Mode().Perm())
	}
	if !strings.Contains(errOut, "STOP THE NODE FIRST") || !strings.Contains(errOut, "best effort") {
		t.Errorf("no stop-the-node warning:\n%s", errOut)
	}
	// account node-key lists the backups and says to move them offline
	out, errOut, code = runWallet2(t, "account", "node-key", "--node-key", dir)
	if code != 0 || strings.Count(out, "backup      : ") != 2 || !strings.Contains(errOut, "move them offline") {
		t.Fatalf("status: %d\n%s\n%s", code, out, errOut)
	}
	// a half-written key: node-key new points to the repair, which writes NodePublic.txt
	os.Remove(filepath.Join(dir, "NodePublic.txt"))
	if _, errOut, code := runWallet2(t, "account", "node-key", "new", "--out", dir, "--show-words"); code == 0 || !strings.Contains(errOut, "node-key repair") {
		t.Fatalf("half-written: %d %s", code, errOut)
	}
	if out, code := runWallet(t, "account", "node-key", "repair", "--out", dir); code != 0 || !strings.Contains(out, "NodePublic.txt written") {
		t.Fatalf("repair: %d %s", code, out)
	}
	checkNodeFiles(t, dir, strings.Fields(strings.Repeat("abandon ", 23)+"craft"))
}

// The node's key encrypted by the node (--encryptkey): listed with its NodePublic.txt's address,
// the node's password asked when a command signs (here --password-file), never exported in clear.
func TestEncryptedNodeKeySends(t *testing.T) {
	old := keys.NodeKDF
	keys.NodeKDF.Time, keys.NodeKDF.MemoryKiB = 1, 64
	defer func() { keys.NodeKDF = old }()
	fake := startFakeNode(t)
	home := t.TempDir()
	t.Setenv("CREDITS_WALLET_HOME", filepath.Join(home, "data"))
	dir := testNodeDir(t, home)
	key, _ := keys.FromBase58(testKey)
	enc, _ := keys.EncryptNodeKey(key, []byte("node pw"), nil)
	os.WriteFile(filepath.Join(dir, "NodePrivate.txt"), []byte(base58.Encode(enc)), 0o600)
	os.WriteFile(filepath.Join(dir, "NodePublic.txt"), []byte(testAddr), 0o600)
	cfg := filepath.Join(dir, "wallet.toml")
	os.WriteFile(cfg, []byte(fmt.Sprintf("node = \"local\"\n[nodes.local]\naddress = %q\nnetwork = \"private\"\ndiag = \"off\"\nnode_key = \"NodePrivate.txt\"\n", fake.addr)), 0o644)
	c := "--config=" + cfg
	out, errOut, code := runWallet2(t, "account", "list", c)
	if code != 0 || !strings.Contains(out, testAddr+"  NODE KEY") || !strings.Contains(out, "encrypted by the node") || !strings.Contains(errOut, "encrypted with the node's password") {
		t.Fatalf("list: %d\n%s\n%s", code, out, errOut)
	}
	wrong := filepath.Join(home, "wrong")
	os.WriteFile(wrong, []byte("nope\n"), 0o600)
	if _, errOut, code := runWallet2(t, "send", "node", toAddr, "1.5", c, "--yes", "--password-file", wrong); code == 0 || !strings.Contains(errOut, "wrong password") {
		t.Fatalf("wrong password: %d %s", code, errOut)
	}
	if _, errOut, code := runWallet2(t, "send", "node", toAddr, "1.5", c, "--yes"); code == 0 {
		t.Fatalf("sent without the node's password: %s", errOut)
	}
	pw := filepath.Join(home, "pw")
	os.WriteFile(pw, []byte("node pw\n"), 0o600)
	out, errOut, code = runWallet2(t, "send", "node", toAddr, "1.5", c, "--yes", "--password-file", pw)
	if code != 0 || !strings.Contains(out, "sent 1.5 CS to "+toAddr) || !strings.Contains(errOut, "encrypted by the node (its password signs)") {
		t.Fatalf("send: %d\n%s\n%s\n%s", code, out, errOut, fake.Log())
	}
	for _, format := range []string{"base58", "official"} {
		if _, errOut, code := runWallet2(t, "account", "export", "node", "--format", format, c, "--yes", "--password-file", pw); code == 0 || !strings.Contains(errOut, "never writes it out in clear") {
			t.Fatalf("export %s: %d %s", format, code, errOut)
		}
	}
	if out, code := runWallet(t, "account", "node-key", c); code != 0 || !strings.Contains(out, "encrypted by the node") {
		t.Fatalf("node-key: %d %s", code, out)
	}
}
