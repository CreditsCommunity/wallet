package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakeToken = "9vX2wH7ytrbbo1KtNa13dUz3nvtJqaCH4oGrr2NEsBqd" // testdata/fake_node.py TOKEN

// Milestone 2 against the fake node: user data, delegations, tokens, the remote history node.
// The fake node checks every signature over the bytes fund.py would sign.
func TestM2AgainstFakeNode(t *testing.T) {
	fake := startFakeNode(t)
	remote := startFakeNode(t)
	home := t.TempDir()
	t.Setenv("CREDITS_WALLET_HOME", home)
	pwFile := filepath.Join(home, "pw")
	os.WriteFile(pwFile, []byte("a test password\n"), 0o600)
	keyFile := filepath.Join(home, "key.txt")
	os.WriteFile(keyFile, []byte(testKey+"\n"), 0o600)
	node := "--node=" + fake.addr
	pw := []string{node, "--password-file", pwFile, "--yes"}
	run := func(args ...string) (string, string, int) {
		t.Helper()
		return runWallet2(t, args...)
	}
	mustFail := func(why string, args ...string) {
		t.Helper()
		if out, stderr, code := run(args...); code == 0 {
			t.Errorf("%s: accepted\n%s%s", why, out, stderr)
		}
	}
	if _, _, code := run("account", "import", "test", keyFile, "--password-file", pwFile); code != 0 {
		t.Fatal("import")
	}

	// user data
	out, _, code := run(append([]string{"send", "test", toAddr, "1", "--data", "invoice 42 — paid", "--json"}, pw...)...)
	if code != 0 || !strings.Contains(out, `"data": "invoice 42 — paid"`) || !fake.waitLog(`inner id 7, max fee 19558, data 'invoice 42 — paid'`) {
		t.Fatalf("send --data: %d %s\n%s", code, out, fake.Log())
	}
	mustFail("data with an escape sequence", append([]string{"send", "test", toAddr, "1", "--data", "a\x1b[2Jb"}, pw...)...)
	mustFail("data too long", append([]string{"send", "test", toAddr, "1", "--data", strings.Repeat("x", 4097)}, pw...)...)

	// delegations
	if out, stderr, code := run(append([]string{"delegate", "test", toAddr, "10"}, pw...)...); code != 0 || !fake.waitLog("inner id 8, max fee 19558, delegated 1") {
		t.Fatalf("delegate: %d %s %s\n%s", code, out, stderr, fake.Log())
	}
	if out, stderr, code := run(append([]string{"delegate", "test", toAddr, "6", "--until", "2030-01-01"}, pw...)...); code != 0 ||
		!fake.waitLog("inner id 9, max fee 19558, delegated 1893456000") || !strings.Contains(out, "until 2030-01-01 00:00:00 UTC") {
		t.Fatalf("delegate --until: %d %s %s\n%s", code, out, stderr, fake.Log())
	}
	mustFail("a delegation below 5 CS", append([]string{"delegate", "test", toAddr, "4.99"}, pw...)...)
	mustFail("an expiry in the past", append([]string{"delegate", "test", toAddr, "5", "--until", "2020-01-01"}, pw...)...)
	mustFail("a bad date", append([]string{"delegate", "test", toAddr, "5", "--until", "tomorrow"}, pw...)...)
	mustFail("delegating to itself", append([]string{"delegate", "test", testAddr, "5"}, pw...)...)
	out, _, code = run("delegations", "test", node, "--json")
	var d struct {
		Out  string `json:"delegated_out_cs"`
		Made []struct {
			Wallet, Amount_cs string
			Until             *string
		} `json:"made"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &d) != nil || d.Out != "16" || len(d.Made) != 2 {
		t.Fatalf("delegations: %d %s", code, out)
	}
	if out, _, code = run("delegations", toAddr, node); code != 0 || !strings.Contains(out, "delegations received: 16 CS in 2 delegations") {
		t.Fatalf("delegations received: %d %s", code, out)
	}
	mustFail("withdrawing more than the delegation without a time limit", append([]string{"undelegate", "test", toAddr, "11"}, pw...)...)
	mustFail("undelegate --until", append([]string{"undelegate", "test", toAddr, "5", "--until", "2030-01-01"}, pw...)...)
	if out, stderr, code := run(append([]string{"undelegate", "test", toAddr, "10"}, pw...)...); code != 0 || !fake.waitLog("inner id 10, max fee 19558, delegated 2") {
		t.Fatalf("undelegate: %d %s %s\n%s", code, out, stderr, fake.Log())
	}

	// tokens
	if out, _, code = run("tokens", "test", node); code != 0 || !strings.Contains(out, "FTK") || !strings.Contains(out, "1000.00") {
		t.Fatalf("tokens: %d %s", code, out)
	}
	if out, _, code = run("token", "info", fakeToken, node); code != 0 || !strings.Contains(out, "decimals  : 2") || !strings.Contains(out, "Fake Token") {
		t.Fatalf("token info: %d %s", code, out)
	}
	mustFail("more decimals than the token's", append([]string{"token", "send", "test", fakeToken, toAddr, "0.001"}, pw...)...)
	mustFail("more tokens than the balance", append([]string{"token", "send", "test", fakeToken, toAddr, "1000.01"}, pw...)...)
	mustFail("a token that is not a contract", append([]string{"token", "send", "test", toAddr, toAddr, "1"}, pw...)...)
	out, stderr, code := run(append([]string{"token", "send", "test", fakeToken, toAddr, "250.50", "--json"}, pw...)...)
	if code != 0 || !strings.Contains(out, `"token_amount": "250.5"`) || !strings.Contains(out, `"returned": "true"`) ||
		!fake.waitLog("ACCEPTED: token call transfer(v_string='"+toAddr+"', v_big_decimal='250.5')") {
		t.Fatalf("token send: %d %s %s\n%s", code, out, stderr, fake.Log())
	}
	if out, _, code = run("token", "balance", fakeToken, "test", node); code != 0 || strings.TrimSpace(out) != "749.50 FTK" {
		t.Fatalf("token balance: %d %q", code, out)
	}
	if out, _, code = run("history", "test", node, "--tokens"); code != 0 || !strings.Contains(out, "-250.5 FTK") || !strings.Contains(out, "1 token transfer") {
		t.Fatalf("history --tokens: %d %s", code, out)
	}
	if out, _, code = run("history", toAddr, node, "--tokens"); code != 0 || !strings.Contains(out, "+250.5 FTK") {
		t.Fatalf("history --tokens of the receiver: %d %s", code, out)
	}
	if out, _, code = run("history", "test", node); code != 0 || !strings.Contains(out, `data "invoice 42 — paid"`) ||
		!strings.Contains(out, "delegation until 2030-01-01 00:00 UTC") || !strings.Contains(out, "delegation withdrawn") ||
		!strings.Contains(out, "on local node "+fake.addr) {
		t.Fatalf("history: %d %s", code, out)
	}
	csvFile := filepath.Join(home, "all.csv")
	if _, stderr, code := run("history", "test", node, "--csv", csvFile); code != 0 {
		t.Fatalf("csv: %s", stderr)
	}
	csv, _ := os.ReadFile(csvFile)
	if !strings.Contains(string(csv), ",token transfer,out,"+testAddr+","+fakeToken+",0,") || !strings.Contains(string(csv), ","+fakeToken+",FTK,-250.5,,") ||
		!strings.Contains(string(csv), ",invoice 42 — paid,") || !strings.Contains(string(csv), "2030-01-01T00:00:00Z") {
		t.Fatalf("CSV:\n%s", csv)
	}

	// the history from a remote node: read there, sending still goes to the local node
	before := strings.Count(fake.Log(), "HISTORY")
	out, stderr, code = run("history", "test", node, "--history-node", remote.addr)
	if code != 0 || !strings.Contains(out, "remote full node "+remote.addr+" (read-only)") || !strings.Contains(stderr, "history source: remote full node") ||
		!remote.waitLog("HISTORY TransactionsGet "+testAddr) || strings.Count(fake.Log(), "HISTORY") != before {
		t.Fatalf("remote history: %d %s %s", code, out, stderr)
	}
	if out, _, code = run(append([]string{"send", "test", toAddr, "1", "--history-node", remote.addr}, pw...)...); code != 0 || strings.Contains(remote.Log(), "ACCEPTED") {
		t.Fatalf("a send with --history-node went to the history node: %d %s\n%s", code, out, remote.Log())
	}
	os.WriteFile(filepath.Join(home, "config.toml"), []byte("[nodes.local]\naddress = \""+fake.addr+"\"\nnetwork = \"private\"\nhistory_node = \""+remote.addr+"\"\n"), 0o600)
	if out, _, code = run("history", "test", "--json"); code != 0 || !strings.Contains(out, `"remote": true`) {
		t.Fatalf("history_node from the configuration: %d %s", code, out)
	}
	if out, _, code = run("history", "test", "--history-node", "local"); code != 0 || !strings.Contains(out, "local node "+fake.addr) {
		t.Fatalf("--history-node local: %d %s", code, out)
	}
	t.Logf("fake node log:\n%s", fake.Log())
}

func TestTokensWithoutTokenCache(t *testing.T) {
	fake := startFakeNode(t, "--no-token-cache")
	home := t.TempDir()
	t.Setenv("CREDITS_WALLET_HOME", home)
	_, stderr, code := runWallet2(t, "tokens", testAddr, "--node", fake.addr)
	if code == 0 || !strings.Contains(stderr, "tokens_cache=true") {
		t.Fatalf("tokens without the cache: %d %s", code, stderr)
	}
	// a token is still read from its contract
	if out, _, code := runWallet2(t, "token", "balance", fakeToken, testAddr, "--node", fake.addr); code != 0 || strings.TrimSpace(out) != "1000.00 FTK" {
		t.Fatalf("token balance without the cache: %d %s", code, out)
	}
	csv := filepath.Join(home, "h.csv")
	if _, stderr, code := runWallet2(t, "history", testAddr, "--node", fake.addr, "--csv", csv); code != 0 || !strings.Contains(stderr, "without token transfers") {
		t.Fatalf("csv without the cache: %d %s", code, stderr)
	}
}

func TestJSONOutputEscapesTerminalControls(t *testing.T) {
	var b strings.Builder
	if err := writeJSON(&b, map[string]string{"code": "X\u009b2J\u202eY"}, "  "); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(b.String(), "\u009b\u202e") || !strings.Contains(b.String(), "X\\u009b2J\\u202eY") {
		t.Errorf("JSON: %s", b.String())
	}
}

// A token that calls itself CS is sent only after a warning, and the review shows its address.
func TestTokenCallingItselfCSIsWarned(t *testing.T) {
	fake := startFakeNode(t, "--token-code", "CS")
	home := t.TempDir()
	t.Setenv("CREDITS_WALLET_HOME", home)
	pwFile := filepath.Join(home, "pw")
	os.WriteFile(pwFile, []byte("pw\n"), 0o600)
	keyFile := filepath.Join(home, "key.txt")
	os.WriteFile(keyFile, []byte(testKey+"\n"), 0o600)
	if _, _, code := runWallet2(t, "account", "import", "test", keyFile, "--password-file", pwFile); code != 0 {
		t.Fatal("import")
	}
	_, stderr, code := runWallet2(t, "token", "send", "test", fakeToken, toAddr, "1", "--node", fake.addr, "--password-file", pwFile, "--yes")
	if code != 0 || !strings.Contains(stderr, "WARNING  this token calls itself CS: it is NOT CS") ||
		!strings.Contains(stderr, "Token    "+fakeToken) || !strings.Contains(stderr, "as the contract calls itself") {
		t.Fatalf("token send of a token calling itself CS: %d\n%s", code, stderr)
	}
}

// Errors reach the terminal with control characters escaped, whatever text they carry.
func TestErrorOutputEscapesControls(t *testing.T) {
	t.Setenv("CREDITS_WALLET_HOME", t.TempDir())
	_, stderr, code := runWallet2(t, "status", "--config", "missing\x1b]0;owned\a\u202e.toml")
	if code == 0 || strings.ContainsAny(stderr, "\x1b\a\u202e") || !strings.Contains(stderr, `missing\x1b]0;owned\a\u202e.toml`) {
		t.Fatalf("%d %q", code, stderr)
	}
}
