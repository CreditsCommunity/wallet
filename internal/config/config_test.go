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
