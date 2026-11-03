package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Offline integration: the wallet's commands against testdata/fake_node.py, the node's fake node
// (with the wallet's calls added), which accepts a transfer only when it is well formed and its
// ed25519 signature is valid over the fields received. Nothing leaves the machine.
//
// Needs python3 with thriftpy2, PyNaCl and base58; skipped without them, unless
// CREDITS_WALLET_FAKE_NODE=1 (CI) makes their absence a failure.

const (
	testKey  = "1GMkH3brNXiNNs1tiFZHu4yZSRrzJwxi5wB9bHFtMikjwpAW9DMZzU2Pqakc5it8X3N5vPmqdN7KF4CCUpmKhq" // public throwaway key
	testAddr = "FAe4sisG95oZ42w7buUn5qEE4TAnfTTFPiguZUHmhiF"
	toAddr   = "u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w"
)

type fakeNode struct {
	addr string
	mu   sync.Mutex
	log  bytes.Buffer
	cmd  *exec.Cmd
}

// Log is what the fake node printed so far.
func (f *fakeNode) Log() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.log.String()
}

// waitLog waits until the fake node printed want (its output arrives through a pipe, after the
// answer the wallet already got).
func (f *fakeNode) waitLog(want string) bool {
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if strings.Contains(f.Log(), want) {
			return true
		}
	}
	return false
}

func startFakeNode(t *testing.T, extra ...string) *fakeNode {
	t.Helper()
	if err := exec.Command("python3", "-c", "import thriftpy2, nacl, base58").Run(); err != nil {
		if os.Getenv("CREDITS_WALLET_FAKE_NODE") == "1" {
			t.Fatalf("python3 with thriftpy2, PyNaCl and base58 is needed: %v", err)
		}
		t.Skip("python3 with thriftpy2, PyNaCl and base58 not available")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	cmd := exec.Command("python3", append([]string{"../../testdata/fake_node.py", "--port", fmt.Sprint(port)}, extra...)...)
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	f := &fakeNode{addr: fmt.Sprintf("127.0.0.1:%d", port), cmd: cmd}
	ready := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			f.mu.Lock()
			f.log.WriteString(sc.Text() + "\n")
			f.mu.Unlock()
			if strings.Contains(sc.Text(), "listening") {
				close(ready)
			}
		}
	}()
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	select {
	case <-ready:
	case <-time.After(20 * time.Second):
		t.Fatalf("the fake node did not start:\n%s", f.Log())
	}
	// it prints "listening" before its server binds the port: wait until it accepts connections
	for end := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		c, err := net.DialTimeout("tcp", f.addr, time.Second)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(end) {
			t.Fatalf("the fake node does not accept connections: %v\n%s", err, f.Log())
		}
	}
	return f
}

// runWallet runs the command line in-process and returns its standard output and exit code.
func runWallet(t *testing.T, args ...string) (string, int) {
	t.Helper()
	out, _, code := runWallet2(t, args...)
	return out, code
}

// runWallet2 also returns the standard error.
func runWallet2(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	savedOut, savedErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = wOut, wErr
	var stdout, stderr bytes.Buffer
	done := make(chan struct{})
	go func() { stdout.ReadFrom(rOut); done <- struct{}{} }()
	go func() { stderr.ReadFrom(rErr); done <- struct{}{} }()
	code := run(args)
	wOut.Close()
	wErr.Close()
	<-done
	<-done
	os.Stdout, os.Stderr = savedOut, savedErr
	return stdout.String(), stderr.String(), code
}

func TestOfflineAgainstFakeNode(t *testing.T) {
	fake := startFakeNode(t)
	home := t.TempDir()
	t.Setenv("CREDITS_WALLET_HOME", home)
	pwFile := filepath.Join(home, "pw")
	os.WriteFile(pwFile, []byte("a test password\n"), 0o600)
	keyFile := filepath.Join(home, "key.txt")
	os.WriteFile(keyFile, []byte(testKey+"\n"), 0o600)
	node := "--node=" + fake.addr

	out, code := runWallet(t, "account", "import", "test", keyFile, "--password-file", pwFile, "--json")
	if code != 0 || !strings.Contains(out, testAddr) {
		t.Fatalf("import: %d %s", code, out)
	}
	out, code = runWallet(t, "status", node, "--json")
	var st map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil || st["synced"] != true || st["oldest_block"] != float64(900) {
		t.Fatalf("status: %d %s", code, out)
	}
	out, code = runWallet(t, "balance", "test", node)
	if code != 0 || !strings.Contains(out, "100 CS") {
		t.Fatalf("balance: %d %s", code, out)
	}
	// without --yes and without a terminal: refused before anything is signed
	if _, code = runWallet(t, "send", "test", toAddr, "12.5", node, "--password-file", pwFile); code == 0 {
		t.Fatal("send without --yes and without a terminal was not refused")
	}
	// a wrong password: nothing is sent
	badPw := filepath.Join(home, "bad")
	os.WriteFile(badPw, []byte("wrong\n"), 0o600)
	if _, code = runWallet(t, "send", "test", toAddr, "12.5", node, "--password-file", badPw, "--yes"); code == 0 {
		t.Fatal("send with a wrong password succeeded")
	}
	// more than the balance: refused
	if _, code = runWallet(t, "send", "test", toAddr, "100", node, "--password-file", pwFile, "--yes"); code == 0 {
		t.Fatal("send of the whole balance plus fee was not refused")
	}
	out, code = runWallet(t, "send", "test", toAddr, "12.5", node, "--password-file", pwFile, "--yes", "--json")
	if code != 0 {
		t.Fatalf("send: %d %s\n%s", code, out, fake.Log())
	}
	var sent map[string]any
	if json.Unmarshal([]byte(out), &sent) != nil || sent["inner_id"] != float64(7) || sent["in_block"] != true || sent["fee_cs"] != "0.00874" {
		t.Fatalf("send output: %s", out)
	}
	want := "ACCEPTED: " + testAddr + " -> " + toAddr + " 12.500000000000000000 CS, inner id 7"
	if !fake.waitLog(want) {
		t.Fatalf("the fake node did not accept the transfer:\n%s", fake.Log())
	}
	// a second transfer takes the next inner id
	if out, code = runWallet(t, "send", "test", toAddr, "0.000000000000000001", node, "--password-file", pwFile, "--yes"); code != 0 ||
		!fake.waitLog("0.000000000000000001 CS, inner id 8") {
		t.Fatalf("second send: %d %s\n%s", code, out, fake.Log())
	}
	out, code = runWallet(t, "history", "test", node)
	if code != 0 || !strings.Contains(out, "2 transactions in all") || !strings.Contains(out, "-12.5 CS") {
		t.Fatalf("history: %d %s", code, out)
	}
	csvFile := filepath.Join(home, "h.csv")
	if _, code = runWallet(t, "history", "test", node, "--csv", csvFile); code != 0 {
		t.Fatal("history --csv")
	}
	csv, _ := os.ReadFile(csvFile)
	if lines := strings.Split(strings.TrimSpace(string(csv)), "\n"); len(lines) != 3 || !strings.Contains(lines[2], ",out,"+testAddr+","+toAddr+",-12.5,") {
		t.Fatalf("CSV:\n%s", csv)
	}
	t.Logf("fake node log:\n%s", fake.Log())
}

func TestAccountsOffline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CREDITS_WALLET_HOME", home)
	pwFile := filepath.Join(home, "pw")
	os.WriteFile(pwFile, []byte("pw\n"), 0o600)

	// without a terminal and without saying where the phrase goes: refused, nothing created
	if _, code := runWallet(t, "account", "new", "fresh", "--password-file", pwFile, "--json"); code == 0 {
		t.Fatal("account new without a terminal and without --words-file/--show-words was not refused")
	}
	phraseFile := filepath.Join(home, "phrase")
	out, code := runWallet(t, "account", "new", "fresh", "--password-file", pwFile, "--json", "--words-file", phraseFile)
	var created map[string]string
	if code != 0 || json.Unmarshal([]byte(out), &created) != nil || created["words"] != "" || created["address"] == "" {
		t.Fatalf("new: %d %s (the words must not be in the JSON)", code, out)
	}
	phrase, _ := os.ReadFile(phraseFile)
	if len(strings.Fields(string(phrase))) != 24 {
		t.Fatalf("words file: %q", phrase)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(phraseFile); st.Mode().Perm() != 0o600 {
			t.Errorf("words file mode %v", st.Mode().Perm())
		}
	}
	// --words-file never overwrites; the account is then not created
	if _, code := runWallet(t, "account", "new", "fresh2", "--password-file", pwFile, "--words-file", phraseFile); code == 0 {
		t.Fatal("an existing words file was overwritten")
	}
	freshAddress := created["address"]
	var shown map[string]string
	out, code = runWallet(t, "account", "new", "shown", "--password-file", pwFile, "--json", "--show-words")
	if code != 0 || json.Unmarshal([]byte(out), &shown) != nil || len(strings.Fields(shown["words"])) != 24 {
		t.Fatalf("new --show-words: %d %s", code, out)
	}
	// the phrase restores the same address
	out, code = runWallet(t, "account", "import", "restored", phraseFile, "--password-file", pwFile, "--json")
	if code != 0 || !strings.Contains(out, freshAddress) || !strings.Contains(out, "seed phrase") {
		t.Fatalf("import phrase: %d %s", code, out)
	}
	// official JSON
	jsonFile := filepath.Join(home, "keys.json")
	os.WriteFile(jsonFile, []byte(`{"key":{"public":"`+testAddr+`","private":"`+testKey+`"}}`), 0o600)
	if out, code = runWallet(t, "account", "import", "official", jsonFile, "--password-file", pwFile); code != 0 || !strings.Contains(out, testAddr) {
		t.Fatalf("import JSON: %d %s", code, out)
	}
	if _, code = runWallet(t, "account", "import", "official", jsonFile, "--password-file", pwFile); code == 0 {
		t.Fatal("an existing name was overwritten")
	}
	out, code = runWallet(t, "account", "list")
	if code != 0 || !strings.Contains(out, "fresh") || !strings.Contains(out, "official") || !strings.Contains(out, "restored") {
		t.Fatalf("list: %s", out)
	}
	out, code = runWallet(t, "account", "export", "official", "--format", "base58", "--password-file", pwFile)
	if code != 0 || strings.TrimSpace(out) != testKey {
		t.Fatalf("export base58: %d %q", code, out)
	}
	out, code = runWallet(t, "account", "export", "official", "--format", "official", "--password-file", pwFile, "--yes")
	if code != 0 || !strings.Contains(out, `"private":"`+testKey+`"`) {
		t.Fatalf("export official: %d %q", code, out)
	}
	out, code = runWallet(t, "account", "export", "official")
	if code != 0 || !strings.Contains(out, `"ciphertext"`) || strings.Contains(out, testKey) {
		t.Fatalf("export encrypted: %d %q", code, out)
	}
	if _, code = runWallet(t, "account", "export", "official", "--format", "base58", "--password-file", phraseFile); code == 0 {
		t.Fatal("export with a wrong password")
	}
}

func TestPasswordFileMustBePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix modes")
	}
	home := t.TempDir()
	t.Setenv("CREDITS_WALLET_HOME", home)
	pwFile := filepath.Join(home, "pw")
	os.WriteFile(pwFile, []byte("pw\n"), 0o644)
	keyFile := filepath.Join(home, "key.txt")
	os.WriteFile(keyFile, []byte(testKey+"\n"), 0o600)
	_, stderr, code := runWallet2(t, "account", "import", "x", keyFile, "--password-file", pwFile)
	if code == 0 || !strings.Contains(stderr, "chmod 600") {
		t.Fatalf("a world-readable password file was used: %d %s", code, stderr)
	}
}

// A secret pasted in the wrong place must not come back in an error (text or --json).
func TestErrorsDoNotEchoSecrets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CREDITS_WALLET_HOME", home)
	pwFile := filepath.Join(home, "pw")
	os.WriteFile(pwFile, []byte("pw\n"), 0o600)
	keyFile := filepath.Join(home, "key.txt")
	os.WriteFile(keyFile, []byte(testKey+"\n"), 0o600)
	if _, code := runWallet(t, "account", "import", "test", keyFile, "--password-file", pwFile); code != 0 {
		t.Fatal("import")
	}
	for _, args := range [][]string{
		{"send", "test", testKey, "1", "--node", "127.0.0.1:1", "--yes", "--json"},    // secret as recipient
		{"send", "test", toAddr, testKey, "--node", "127.0.0.1:1", "--yes", "--json"}, // secret as amount
		{"balance", testKey, "--node", "127.0.0.1:1", "--json"},                       // secret as account
		{"history", "abandon ability able about above", "--node", "127.0.0.1:1"},      // words as account
		{testKey}, // secret as a command
	} {
		out, stderr, code := runWallet2(t, args...)
		if code == 0 {
			t.Errorf("%v: no error", args)
		}
		for _, secret := range []string{testKey[:20], "ability able"} {
			if strings.Contains(out+stderr, secret) {
				t.Errorf("%v echoed the secret:\n%s%s", args, out, stderr)
			}
		}
	}
}

func TestHistoryCSVDoesNotOverwrite(t *testing.T) {
	fake := startFakeNode(t)
	home := t.TempDir()
	t.Setenv("CREDITS_WALLET_HOME", home)
	csv := filepath.Join(home, "h.csv")
	os.WriteFile(csv, []byte("keep me"), 0o600)
	if _, code := runWallet(t, "history", toAddr, "--node", fake.addr, "--csv", csv); code == 0 {
		t.Fatal("an existing CSV file was overwritten")
	}
	if data, _ := os.ReadFile(csv); string(data) != "keep me" {
		t.Fatal("the existing file changed")
	}
	if out, stderr, code := runWallet2(t, "history", toAddr, "--node", fake.addr, "--csv", csv, "--force"); code != 0 {
		t.Fatalf("--force: %s %s", out, stderr)
	}
}

func TestFindCommand(t *testing.T) {
	cases := map[string][]string{
		"send":    {"--node", "1.2.3.4:9090", "send", "a", "b", "1"},
		"status":  {"--json", "status"},
		"history": {"--config=x", "history", "a", "--csv", "f"},
		"":        {"--node", "x:1"},
		"account": {"--password-file", "pw", "account", "new", "x"},
	}
	for want, args := range cases {
		if got := findCommand(args); got != want {
			t.Errorf("%v: %q, want %q", args, got, want)
		}
	}
}
