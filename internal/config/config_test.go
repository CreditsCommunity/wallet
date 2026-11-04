package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
require_network_uuid = true
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
	if err != nil || p.Diag != "127.0.0.1:9401" || p.NetworkUUID != "1234" || p.Network != Private || !p.RequireNetworkUUID {
		t.Errorf("resolve to b: %+v %v", p, err)
	}
	p, err = c.Resolve("b", "10.9.9.9:9090")
	if err != nil || p.Diag != "" || p.NetworkUUID != "" || p.RequireNetworkUUID || p.Network != Mainnet || p.DiagAddress() != "10.9.9.9:9088" {
		t.Errorf("unknown address: %+v %v", p, err)
	}
}

// node_key: a path (or "off") in a node profile.
func TestNodeKeySetting(t *testing.T) {
	c, err := Parse("[nodes.local]\naddress = \"127.0.0.1:9090\"\nnode_key = \"NodePrivate.txt\"\n[nodes.other]\nnode_key = \"off\"\n", "wallet.toml")
	if err != nil || c.Nodes["local"].NodeKey != "NodePrivate.txt" || c.Nodes["other"].NodeKey != NodeKeyOff {
		t.Fatalf("%v %+v", err, c)
	}
	if _, err := Parse(Example, "example"); err != nil {
		t.Fatal(err)
	}
}

// "Use it and remember it": node_key written into the active profile, every other line kept.
func TestRememberNodeKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	text := "# my wallet\nnode = \"lab\"\nhistory_page = 30   # keep\n\n[nodes.local]\naddress = \"127.0.0.1:9090\"\n# node_key = \"old\"\n\n[nodes.lab]  # the lab\naddress = \"10.0.0.5:9090\"\nnetwork = \"testnet\"\n\n[nodes.\"x y\"]\naddress = \"10.0.0.6:9090\"\n"
	os.WriteFile(path, []byte(text), 0o640)
	cfg, _ := Load(path, true)
	p, _ := cfg.Resolve("", "")
	key := `/home/me/csnode "a"\NodePrivate.txt`
	if err := RememberNodeKey(path, p, key); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := strings.Replace(text, "[nodes.lab]  # the lab\n", "[nodes.lab]  # the lab\nnode_key = \"/home/me/csnode \\\"a\\\"\\\\NodePrivate.txt\"\n", 1)
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	c2, err := Load(path, true)
	if err != nil || c2.Nodes["lab"].NodeKey != key || c2.HistoryPage != 30 || c2.Nodes["local"].NodeKey != "" {
		t.Fatalf("%+v %v", c2, err)
	}
	// again: the line is replaced, not added
	if err := RememberNodeKey(path, p, "/k2"); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if strings.Count(string(got), "\nnode_key = ") != 1 || !strings.Contains(string(got), "node_key = \"/k2\"") || !strings.Contains(string(got), "# node_key = \"old\"") {
		t.Fatalf("replaced:\n%s", got)
	}
	// a quoted profile name
	q := *cfg.Nodes["x y"]
	if err := RememberNodeKey(path, q, "/k3"); err != nil {
		t.Fatal(err)
	}
	if c3, _ := Load(path, true); c3.Nodes["x y"].NodeKey != "/k3" || c3.Nodes["lab"].NodeKey != "/k2" {
		t.Fatal("quoted profile")
	}
	// --node: no profile
	if err := RememberNodeKey(path, Profile{Address: "1.2.3.4:9090"}, "/k"); err == nil {
		t.Error("written without a profile")
	}
	// a profile written as an inline table: refused, nothing changed
	inline := "[nodes]\nlocal = { address = \"127.0.0.1:9090\" }\n"
	os.WriteFile(path, []byte(inline), 0o600)
	cfg, _ = Load(path, true)
	p, _ = cfg.Resolve("", "")
	if err := RememberNodeKey(path, p, "/k"); err == nil || !strings.Contains(err.Error(), "by hand") {
		t.Errorf("inline table: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != inline {
		t.Errorf("changed:\n%s", got)
	}
	// no file yet: created with the profile (mode 600)
	fresh := filepath.Join(dir, "new", "config.toml")
	if err := RememberNodeKey(fresh, Profile{Name: "local", Address: DefaultAddress, Network: Mainnet}, "/n/NodePrivate.txt"); err != nil {
		t.Fatal(err)
	}
	c4, err := Load(fresh, true)
	if err != nil || c4.Nodes["local"].NodeKey != "/n/NodePrivate.txt" || c4.Nodes["local"].Address != DefaultAddress {
		t.Fatalf("%+v %v", c4, err)
	}
	if st, _ := os.Stat(fresh); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	// a file without [nodes] (the default profile): the table is added at the end
	bare := filepath.Join(dir, "bare.toml")
	os.WriteFile(bare, []byte("# only this\nhistory_page = 5"), 0o600)
	cfg, _ = Load(bare, true)
	p, _ = cfg.Resolve("", "")
	if err := RememberNodeKey(bare, p, "/b"); err != nil {
		t.Fatal(err)
	}
	if c5, _ := Load(bare, true); c5.Nodes["local"].NodeKey != "/b" || c5.HistoryPage != 5 {
		t.Fatal("bare")
	}
	if got, _ := os.ReadFile(bare); !strings.HasPrefix(string(got), "# only this\nhistory_page = 5\n") {
		t.Errorf("bare:\n%s", got)
	}
}

func TestLockAfter(t *testing.T) {
	if Default().LockAfter() != 10*time.Minute {
		t.Error("default")
	}
	c, err := Parse("lock_after_minutes = 0", "x")
	if err != nil || c.LockAfter() != 0 {
		t.Errorf("0: %v", err)
	}
	c, _ = Parse("lock_after_minutes = 3", "x")
	if c.LockAfter() != 3*time.Minute {
		t.Error("3")
	}
	for _, bad := range []string{"lock_after_minutes = -1", "lock_after_minutes = 100000"} {
		if _, err := Parse(bad, "x"); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}
