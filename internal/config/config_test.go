package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExampleParses(t *testing.T) {
	c, err := Parse(Example, "example")
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Resolve("", "")
	if err != nil || p.Address != DefaultAddress || p.Network != Mainnet || !p.Network.IsMainnet() || c.HistoryPage != 20 {
		t.Errorf("%+v %v", p, err)
	}
}

func TestDefaultsWithoutFile(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "none.toml"), false)
	if err != nil || c.Node != "local" || c.Nodes["local"].Network != Mainnet {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "none.toml"), true); err == nil {
		t.Error("a missing --config file is not an error")
	}
}

func TestProfilesAndOverride(t *testing.T) {
	text := `
node = "lab"
[nodes.local]
address = "127.0.0.1:9090"
[nodes.lab]
address = "10.0.0.5:9090"
network = "testnet"
[nodes.priv]
address = "10.0.0.6:9090"
network = "private"
`
	c, err := Parse(text, "t")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := c.Resolve("", "")
	if p.Name != "lab" || p.Network != Testnet || p.Network.IsMainnet() {
		t.Errorf("default profile: %+v", p)
	}
	if p, _ := c.Resolve("local", ""); p.Network != Mainnet {
		t.Errorf("local: %+v", p)
	}
	// --node to an address no profile declares: mainnet, even with a testnet profile chosen
	if p, _ := c.Resolve("lab", "192.168.1.1:9090"); p.Network != Mainnet || p.Address != "192.168.1.1:9090" {
		t.Errorf("unknown address: %+v", p)
	}
	// --node to the address of a declared profile takes that profile's network
	if p, _ := c.Resolve("local", "10.0.0.6:9090"); p.Network != Private || p.Name != "priv" {
		t.Errorf("declared address: %+v", p)
	}
	if _, err := c.Resolve("nope", ""); err == nil {
		t.Error("unknown profile")
	}
	if _, err := c.Resolve("", "nohostport"); err == nil {
		t.Error("bad --node")
	}
}

func TestBadConfigs(t *testing.T) {
	for _, text := range []string{
		`[nodes.a]` + "\n" + `network = "devnet"`,
		`[nodes.a]` + "\n" + `address = "nohost"`,
		`node = "b"` + "\n" + `[nodes.a]`,
		`nodez = 1`,
		`[nodes.a]` + "\n" + `adress = "127.0.0.1:1"`,
		`not toml`,
	} {
		if _, err := Parse(text, "t"); err == nil {
			t.Errorf("accepted:\n%s", text)
		}
	}
}

func TestDataDir(t *testing.T) {
	t.Setenv("CREDITS_WALLET_HOME", "/x/y")
	if d, _ := DataDir(); d != "/x/y" {
		t.Errorf("CREDITS_WALLET_HOME: %s", d)
	}
	t.Setenv("CREDITS_WALLET_HOME", "")
	d, err := DataDir()
	if err != nil || !strings.HasSuffix(d, "credits-wallet") {
		t.Errorf("%s %v", d, err)
	}
	if AccountsDir("/a") != filepath.Join("/a", "accounts") {
		t.Error("AccountsDir")
	}
}

func TestPermissionWarnings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix modes")
	}
	dir := t.TempDir()
	os.Chmod(dir, 0o700)
	os.Mkdir(AccountsDir(dir), 0o700)
	if w := PermissionWarnings(dir); len(w) != 0 {
		t.Errorf("private folders: %v", w)
	}
	os.Chmod(AccountsDir(dir), 0o755)
	if w := PermissionWarnings(dir); len(w) != 1 || !strings.Contains(w[0], "accounts") {
		t.Errorf("open accounts folder: %v", w)
	}
	if !isWithin("/home/u/x", "/home/u") || isWithin("/home/ux", "/home/u") || isWithin("/tmp", "/home/u") {
		t.Error("isWithin")
	}
}

func TestHistoryNode(t *testing.T) {
	c, err := Parse("[nodes.local]\naddress = \"127.0.0.1:9090\"\nhistory_node = \"10.1.2.3:9090\"\n", "x")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := c.Resolve("", "")
	if p.HistoryNode != "10.1.2.3:9090" {
		t.Fatalf("history node %q", p.HistoryNode)
	}
	if q, _ := c.Resolve("", "10.9.9.9:9090"); q.HistoryNode != "" {
		t.Errorf("another --node kept the profile's history node: %q", q.HistoryNode)
	}
	if q, err := ResolveHistory(p, "local"); err != nil || q.HistoryNode != "" {
		t.Errorf("local: %q %v", q.HistoryNode, err)
	}
	if q, err := ResolveHistory(p, "1.1.1.1:1"); err != nil || q.HistoryNode != "1.1.1.1:1" {
		t.Errorf("override: %q %v", q.HistoryNode, err)
	}
	if _, err := ResolveHistory(p, "nonsense"); err == nil {
		t.Error("a bad --history-node was accepted")
	}
	if _, err := Parse("[nodes.local]\nhistory_node = \"nope\"\n", "x"); err == nil {
		t.Error("a bad history_node was accepted")
	}
}

func TestDiagAndNetworkUUID(t *testing.T) {
	c, err := Parse(`
node = "a"
[nodes.a]
address = "10.0.0.5:9090"
[nodes.b]
address = "127.0.0.1:9090"
diag = "127.0.0.1:9401"
network = "private"
network_uuid = "1234"
[nodes.c]
address = "[::1]:9090"
diag = "off"
`, "x")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Nodes["a"].DiagAddress(); got != "10.0.0.5:9088" {
		t.Errorf("default diag: %s", got)
	}
	if got := c.Nodes["b"].DiagAddress(); got != "127.0.0.1:9401" || c.Nodes["b"].PinnedUUID() != 1234 {
		t.Errorf("b: %s %d", got, c.Nodes["b"].PinnedUUID())
	}
	if got := c.Nodes["c"].DiagAddress(); got != "" {
		t.Errorf("off: %q", got)
	}
	for _, bad := range []string{`diag = "nope"`, `network_uuid = "-1"`, `network_uuid = "0"`, `network_uuid = "abc"`} {
		if _, err := Parse("[nodes.a]\n"+bad+"\n", "x"); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	// --node with an address a profile declares takes that profile's diag and pin; another one not
	p, err := c.Resolve("a", "127.0.0.1:9090")
	if err != nil || p.Diag != "127.0.0.1:9401" || p.NetworkUUID != "1234" || p.Network != Private {
		t.Errorf("resolve to b: %+v %v", p, err)
	}
	p, err = c.Resolve("b", "10.9.9.9:9090")
	if err != nil || p.Diag != "" || p.NetworkUUID != "" || p.Network != Mainnet || p.DiagAddress() != "10.9.9.9:9088" {
		t.Errorf("unknown address: %+v %v", p, err)
	}
}
