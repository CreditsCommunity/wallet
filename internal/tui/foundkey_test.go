package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

// foundModel: the wallet started in a folder holding NodePrivate.txt, no node's key configured;
// the configuration file has comments and two profiles.
func foundModel(t *testing.T, mode os.FileMode) (*model, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(wallet.NodeKeyEnv, "")
	dir := filepath.Join(home, "csnode")
	os.Mkdir(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "NodePrivate.txt"), []byte(nodeSecret), mode)
	os.Chmod(filepath.Join(dir, "NodePrivate.txt"), mode) // whatever the umask
	data := filepath.Join(home, ".credits-wallet")
	os.MkdirAll(data, 0o700)
	cfgPath := filepath.Join(data, "config.toml")
	os.WriteFile(cfgPath, []byte("# mine\nnode = \"local\"\n\n[nodes.local]   # here\naddress = \"127.0.0.1:1\"\nhistory_node = \"10.0.0.9:9090\"\n\n[nodes.lab]\naddress = \"10.0.0.5:9090\"\nnetwork = \"testnet\"\n"), 0o600)
	cfg, err := config.Load(cfgPath, true)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := cfg.Resolve("", "")
	w := wallet.New(cfg, p, data)
	found := wallet.FindLocalNodeKey(dir, data, nil, false)
	if found == nil {
		t.Fatal("not found")
	}
	m := newModel(w)
	m.screen = newFoundKeyScreen(found)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(m.loadAccounts()())
	return m, dir, cfgPath
}

// A key the node encrypted: its address is tagged as NodePublic.txt's, unverified until unlocked.
func TestFoundKeyEncryptedTagged(t *testing.T) {
	m := testModel(t)
	m.screen = newFoundKeyScreen(&wallet.FoundNodeKey{Path: "/n/NodePrivate.txt", Address: "FAe4sisG95oZ42w7buUn5qEE4TAnfTTFPiguZUHmhiF", Encrypted: true})
	v := flat(m.View())
	if !strings.Contains(v, "(from NodePublic.txt, unverified until unlocked)") || !strings.Contains(v, "its password is asked") {
		t.Errorf("encrypted:\n%s", m.View())
	}
	fits80x24(t, "found encrypted", m.View())
}

// The found key is said with its address and full path and never used before a choice.
func TestFoundKeyUseForSession(t *testing.T) {
	m, dir, cfgPath := foundModel(t, 0o600)
	before, _ := os.ReadFile(cfgPath)
	view := m.View()
	fits80x24(t, "found", view)
	v := flat(view)
	for _, want := range []string{"This folder holds a node's key", "FAe4sisG95oZ42w7buUn5qEE4TAnfTTFPiguZUHmhiF", "[u] use it for this session", "[r] use it and remember it", "[i] ignore it", "[x] ignore, never ask"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(strings.ReplaceAll(v, " ", ""), filepath.Join(dir, "NodePrivate.txt")) {
		t.Errorf("no full path:\n%s", view)
	}
	if m.w.NodeKey != nil || len(m.accounts) != 0 {
		t.Fatal("used before a choice")
	}
	runCmd(m, m.screen.update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}}))
	if m.screen != nil || m.w.NodeKey == nil || m.selected() == nil || !m.selected().IsNodeKey() {
		t.Fatalf("not used: %T %v", m.screen, m.selected())
	}
	if m.unlocked != nil {
		t.Fatal("unlocked without going through the unlock screen")
	}
	if after, _ := os.ReadFile(cfgPath); string(after) != string(before) {
		t.Error("the configuration changed for a session-only choice")
	}
	unlockSelected(t, m, "") // a plain node's key: no password
	if f := m.current(); f == nil || f.Public != "FAe4sisG95oZ42w7buUn5qEE4TAnfTTFPiguZUHmhiF" {
		t.Fatalf("%+v", f)
	}
}

// Use and remember: node_key with the absolute path in the active profile, everything else kept.
func TestFoundKeyRemember(t *testing.T) {
	m, dir, cfgPath := foundModel(t, 0o600)
	before, _ := os.ReadFile(cfgPath)
	runCmd(m, m.screen.update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}))
	if m.screen != nil || m.w.NodeKey == nil {
		t.Fatalf("not used: %v", m.screen)
	}
	after, _ := os.ReadFile(cfgPath)
	key := filepath.Join(dir, "NodePrivate.txt")
	want := strings.Replace(string(before), "[nodes.local]   # here\n", "[nodes.local]   # here\nnode_key = \""+strings.ReplaceAll(key, `\`, `\\`)+"\"\n", 1)
	if string(after) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", after, want)
	}
	cfg, _ := config.Load(cfgPath, true)
	p, _ := cfg.Resolve("", "")
	if nk, err := wallet.ResolveNodeKey(p, cfgPath, "", t.TempDir()); err != nil || nk == nil || nk.Private != key {
		t.Fatalf("next start: %+v %v", nk, err)
	}
	if wallet.FindLocalNodeKey(dir, m.w.DataDir, &wallet.NodeKey{}, false) != nil {
		t.Error("offered again once configured")
	}
}

// Ignore: nothing used, asked again next time; don't ask again: never offered for that file.
func TestFoundKeyIgnore(t *testing.T) {
	m, dir, _ := foundModel(t, 0o600)
	m.screen.update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if m.screen != nil || m.w.NodeKey != nil {
		t.Fatal("ignore used the key")
	}
	if wallet.FindLocalNodeKey(dir, m.w.DataDir, nil, false) == nil {
		t.Fatal("not offered again after a plain ignore")
	}
	m.screen = newFoundKeyScreen(wallet.FindLocalNodeKey(dir, m.w.DataDir, nil, false))
	m.screen.update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.screen != nil || m.w.NodeKey != nil {
		t.Fatal("x used the key")
	}
	if wallet.FindLocalNodeKey(dir, m.w.DataDir, nil, false) != nil {
		t.Fatal("offered again after don't ask again")
	}
}

// A key file or folder others can write to: refused, only ignoring is offered; one others can read:
// offered with the chmod 600 warning.
func TestFoundKeyUnsafe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the owner and mode checks are not done on Windows")
	}
	m, _, cfgPath := foundModel(t, 0o666)
	s := m.screen.(*foundKeyScreen)
	if s.found.Refused == nil {
		t.Fatal("not refused")
	}
	view := m.View()
	fits80x24(t, "refused", view)
	if v := flat(view); !strings.Contains(v, "Refused") || !strings.Contains(v, "written by other users") || strings.Contains(v, "[u]") || strings.Contains(v, "FAe4sisG") {
		t.Errorf("refused view:\n%s", view)
	}
	before, _ := os.ReadFile(cfgPath)
	for _, k := range []rune{'u', 'r'} {
		m.screen.update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{k}})
		if m.w.NodeKey != nil || m.screen == nil {
			t.Fatalf("%c used a refused key", k)
		}
	}
	if after, _ := os.ReadFile(cfgPath); string(after) != string(before) {
		t.Error("remembered a refused key")
	}
	m2, _, _ := foundModel(t, 0o644)
	if v := flat(m2.View()); !strings.Contains(v, "chmod 600") || !strings.Contains(v, "[u] use it") {
		t.Errorf("readable:\n%s", m2.View())
	}
}
