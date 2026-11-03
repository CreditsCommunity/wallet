package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// waitPort waits until something accepts connections at addr.
func waitPort(t *testing.T, addr string) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			c.Close()
			return
		}
		if time.Now().After(end) {
			t.Fatalf("nothing listens at %s: %v", addr, err)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// Milestone 3 against the fake node: templates, compile (with errors), deploy (the fake node checks
// the address the node would derive and the signature), the contract list, methods, getters, calls,
// a payment to payable(), the state.
func TestM3ContractsAgainstFakeNode(t *testing.T) {
	fake := startFakeNode(t)
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
	must := func(want string, args ...string) string {
		t.Helper()
		out, stderr, code := run(args...)
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("%v: code %d, no %q in\n%s%s\n%s", args, code, want, out, stderr, fake.Log())
		}
		return out
	}
	mustFail := func(want string, args ...string) {
		t.Helper()
		out, stderr, code := run(args...)
		if code == 0 || !strings.Contains(out+stderr, want) {
			t.Errorf("%v: code %d, no %q in\n%s%s", args, code, want, out, stderr)
		}
	}
	if _, _, code := run("account", "import", "test", keyFile, "--password-file", pwFile); code != 0 {
		t.Fatal("import")
	}

	// templates
	out := must(`"name": "TipJar"`, "contract", "templates", "--json")
	var tl struct{ Templates []struct{ Name string } }
	if json.Unmarshal([]byte(out), &tl) != nil || len(tl.Templates) != 5 {
		t.Errorf("templates: %s", out)
	}
	src := filepath.Join(home, "Counter.java")
	must("Counter written to", "contract", "new", "counter", src)
	mustFail("exists", "contract", "new", "Counter", src)
	must("written", "contract", "new", "Counter", src, "--force")

	// compile: errors with their line, then a source that compiles
	bad := filepath.Join(home, "Bad.java")
	os.WriteFile(bad, []byte("public class Bad {\n  int x = COMPILE_ERROR\n}\n"), 0o644)
	_, stderr, code := run("contract", "compile", bad, node)
	if code == 0 || !strings.Contains(stderr, bad+":2: cannot find symbol") || !strings.Contains(stderr, bad+":3: ';' expected") {
		t.Errorf("compile errors: %d %s", code, stderr)
	}
	out, _, code = run("contract", "compile", bad, node, "--json")
	var ce struct {
		Compiled bool
		Errors   []struct{ Line int }
	}
	if code == 0 || json.Unmarshal([]byte(out), &ce) != nil || ce.Compiled || len(ce.Errors) != 2 || ce.Errors[1].Line != 3 {
		t.Errorf("compile errors --json (one JSON document): %d %s", code, out)
	}
	bug := filepath.Join(home, "Bug.java")
	os.WriteFile(bug, []byte("public class Bug { EXECUTOR_BUG }\n"), 0o644)
	mustFail("the node's contract executor failed instead of listing the errors", "contract", "compile", bug, node)
	mustFail("no such file", append([]string{"contract", "deploy", "test", strings.Replace(bad, "Bad", "bad", 1)}, pw...)...)
	must("isOpen() : boolean  getter", "contract", "compile", src, node)

	// deploy: the fake node accepts it only at the address the node derives
	out = must(`"contract":`, append([]string{"contract", "deploy", "test", src, "--json"}, pw...)...)
	var dep struct {
		Contract string
		InnerID  int64 `json:"inner_id"`
		Listed   bool
		InBlock  bool `json:"in_block"`
	}
	if json.Unmarshal([]byte(out), &dep) != nil || dep.InnerID != 7 || !dep.Listed || !dep.InBlock ||
		!fake.waitLog("ACCEPTED: deploy of "+dep.Contract+" by "+testAddr) {
		t.Fatalf("deploy: %s\n%s", out, fake.Log())
	}
	c := dep.Contract

	// the list: on the node and in the local list
	out = must(c, "contract", "list", "test", node, "--json")
	var cl struct {
		Contracts []struct {
			Address, Name, File string
			OnNode              bool `json:"on_node"`
			Local               bool
		}
	}
	if json.Unmarshal([]byte(out), &cl) != nil || len(cl.Contracts) != 1 || !cl.Contracts[0].OnNode || !cl.Contracts[0].Local ||
		cl.Contracts[0].Name != "Counter" || cl.Contracts[0].File != src {
		t.Errorf("contract list: %s", out)
	}
	// another node that does not know it: the local list still has it
	other := startFakeNode(t)
	must("local list only (mainnet): not on this node", "contract", "list", "test", "--node="+other.addr)
	if data, err := os.ReadFile(filepath.Join(home, "contracts", testAddr+".json")); err != nil || strings.Contains(string(data), "secret") {
		t.Errorf("local list file: %v", err)
	}

	// methods, getters, calls
	must("paidBy(String arg0) : BigDecimal  getter", "contract", "methods", c, node)
	must("true", "contract", "get", c, "isOpen", node)
	must("12.50", "contract", "get", c, "paidBy", node, "--", "-starts-with-a-dash")
	mustFail("argument 1 (int arg0)", append([]string{"contract", "call", "test", c, "add", "four"}, pw...)...)
	mustFail("no method", append([]string{"contract", "call", "test", c, "nope"}, pw...)...)
	mustFail("contract pay", append([]string{"contract", "call", "test", c, "payable", "1", "00"}, pw...)...)
	out = must("returned: 4", append([]string{"contract", "call", "test", c, "add", "4"}, pw...)...)
	if !strings.Contains(out, "counter              4") || !fake.waitLog("ACCEPTED: call add(v_int=4)") {
		t.Errorf("call: state not shown:\n%s", out)
	}
	out = must(`"method": "add"`, append([]string{"contract", "call", "test", c, "add", "2", "--json"}, pw...)...)
	if !strings.Contains(out, `"returned": "6"`) || !strings.Contains(out, `"counter": "6"`) {
		t.Errorf("call --json: %s", out)
	}

	// a payment: a CS transfer to the contract runs payable(), with the user data as userData
	out = must("returned: thank you, "+testAddr, append([]string{"contract", "pay", "test", c, "3", "--data", "tip"}, pw...)...)
	if !fake.waitLog("inner id 10, max fee 19558, data 'tip', payable()") {
		t.Errorf("pay: %s\n%s", out, fake.Log())
	}
	must("3", "contract", "get", c, "paidBy", testAddr, node)
	// the node now lists the payment as a deploy and fails on it: the list still has the contract
	out, stderr, code = run("contract", "list", "test", node, "--json")
	if code != 0 || !strings.Contains(out, `"on_node": true`) || !strings.Contains(stderr, "1 entries of the node's list could not be read") {
		t.Errorf("contract list after a payment: %d %s %s\n%s", code, out, stderr, fake.Log())
	}
	mustFail("no contract is deployed", append([]string{"contract", "pay", "test", toAddr, "3"}, pw...)...)
	must("paid", "contract", "info", c, node)
	if strings.Contains(fake.Log(), "REFUSED: ") && !strings.Contains(fake.Log(), "REFUSED: payable method cannot be called") {
		t.Errorf("the fake node refused something:\n%s", fake.Log())
	}
}

// The network identity: a community node's diagnostic API reports its network uuid; the wallet
// refuses to send when it contradicts the profile, and lets the configuration decide without it.
func TestNetworkIdentityAgainstFakeNode(t *testing.T) {
	for _, c := range []struct {
		uuid     string // what the node reports ("": an official node, no field)
		network  string
		pin      string
		sends    bool
		statusIs string
	}{
		{"1234567890123456789", "private", "", true, "not mainnet (uuid 1234567890123456789), community node"},
		{"1234567890123456789", "mainnet", "", false, "MISMATCH"},
		{"1234567890123456789", "private", "42", false, "MISMATCH"},
		{"1234567890123456789", "private", "1234567890123456789", true, "not mainnet"},
		{"mainnet", "testnet", "", false, "MAINNET (uuid 11024959585341937636)"},
		{"", "private", "", true, "node says    : nothing"},
	} {
		t.Run(c.uuid+"-"+c.network+"-"+c.pin, func(t *testing.T) {
			diag := freePort(t)
			args := []string{"--diag-port", fmt.Sprint(diag)}
			if c.uuid != "" {
				args = append(args, "--network-uuid", c.uuid)
			}
			fake := startFakeNode(t, args...)
			waitPort(t, fmt.Sprintf("127.0.0.1:%d", diag))
			home := t.TempDir()
			t.Setenv("CREDITS_WALLET_HOME", home)
			cfg := fmt.Sprintf("node = \"n\"\n[nodes.n]\naddress = %q\nnetwork = %q\ndiag = \"127.0.0.1:%d\"\n", fake.addr, c.network, diag)
			if c.pin != "" {
				cfg += fmt.Sprintf("network_uuid = %q\n", c.pin)
			}
			os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o600)
			pwFile := filepath.Join(home, "pw")
			os.WriteFile(pwFile, []byte("a test password\n"), 0o600)
			keyFile := filepath.Join(home, "key.txt")
			os.WriteFile(keyFile, []byte(testKey+"\n"), 0o600)
			if _, _, code := runWallet2(t, "account", "import", "test", keyFile, "--password-file", pwFile); code != 0 {
				t.Fatal("import")
			}
			out, _, code := runWallet2(t, "status")
			if code != 0 || !strings.Contains(out, c.statusIs) {
				t.Errorf("status: %d\n%s", code, out)
			}
			out, stderr, code := runWallet2(t, "send", "test", toAddr, "1", "--password-file", pwFile, "--yes")
			if (code == 0) != c.sends {
				t.Errorf("send: code %d\n%s%s", code, out, stderr)
			}
			if !c.sends && (strings.Contains(fake.Log(), "ACCEPTED") || !strings.Contains(stderr, "refused")) {
				t.Errorf("sent despite the mismatch:\n%s\n%s", stderr, fake.Log())
			}
			if !strings.Contains(fake.Log(), "DIAG GetNodeInfo") {
				t.Error("the diagnostic API was not asked")
			}
		})
	}
}
