package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/CreditsCommunity/wallet/internal/amount"
	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

const nodeSecret = "1GMkH3brNXiNNs1tiFZHu4yZSRrzJwxi5wB9bHFtMikjwpAW9DMZzU2Pqakc5it8X3N5vPmqdN7KF4CCUpmKhq" // public throwaway key

func nodeWalletModel(t *testing.T, withKey bool) (*model, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, "csnode")
	os.Mkdir(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.ini"), []byte("[api]\nport=0\n"), 0o644)
	if withKey {
		os.WriteFile(filepath.Join(dir, "NodePrivate.txt"), []byte(nodeSecret), 0o600)
	}
	w := wallet.New(config.Default(), config.Profile{Name: "local", Address: "127.0.0.1:1", Network: config.Mainnet}, filepath.Join(home, ".credits-wallet"))
	w.NodeKey = wallet.NodeKeyIn(dir, "test")
	m := newModel(w)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(m.loadAccounts()())
	if withKey {
		unlockSelected(t, m, "")
	}
	return m, dir
}

// unlockSelected unlocks the account selected on the unlock screen with pw ("": none asked).
func unlockSelected(t *testing.T, m *model, pw string) {
	t.Helper()
	cmd := press(m, tea.KeyEnter)
	if pw != "" {
		typeText(m, pw)
		cmd = press(m, tea.KeyEnter)
	}
	if cmd == nil {
		t.Fatalf("nothing to unlock: %q", m.lock.note)
	}
	m.Update(cmd())
	if m.unlocked == nil {
		t.Fatalf("not unlocked: %q", m.lock.note)
	}
}

func fits80x24(t *testing.T, name, view string) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > 24 {
		t.Errorf("%s: %d lines", name, len(lines))
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w > 80 {
			t.Errorf("%s line %d: %d columns: %q", name, i, w, l)
		}
	}
}

// The node's key: the first account (selected by default), marked, with the warning shown once.
func TestTUINodeKeyAccount(t *testing.T) {
	m, _ := nodeWalletModel(t, true)
	f := m.current()
	if f == nil || !f.IsNodeKey() || f.Name != "node" {
		t.Fatalf("selected: %+v", f)
	}
	view := m.View()
	fits80x24(t, "main", view)
	flat := strings.Join(strings.Fields(strings.ReplaceAll(view, "│", " ")), " ")
	for _, want := range []string{"node node key", "NODE KEY: the node's own key", "kept in clear on this server"} {
		if !strings.Contains(flat, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
	// once: a key press clears it, a reload does not bring it back
	typeText(m, "?")
	press(m, tea.KeyEsc)
	m.Update(m.loadAccounts()())
	if strings.Contains(m.View(), "kept in clear on this server") {
		t.Error("the warning came back")
	}
	// the review asks no password for the node's key: Confirm only (type send on mainnet)
	s := newSendScreen(f)
	m.screen = s
	amt, _ := amount.Parse("1")
	pub, _ := keys.ParseAddress(f.Public)
	plan := &wallet.SendPlan{Kind: wallet.KindTransfer, Account: f.Name, NodeKey: true, From: pub, To: pub, Amount: amt, Balance: amt, Network: config.Mainnet}
	s.step = sendPreparing
	m.Update(preparedMsg{plan, nil})
	if len(s.confirm.inputs) != 1 || s.confirm.labels[0] != "Confirm" {
		t.Fatalf("confirm form: %v", s.confirm.labels)
	}
	v := m.View()
	fits80x24(t, "review", v)
	if !strings.Contains(v, "NODE KEY") || strings.Contains(v, "Password") {
		t.Errorf("review:\n%s", v)
	}
	s.seenEnd = true
	typeText(m, "nope")
	if cmd := press(m, tea.KeyEnter); cmd != nil || s.err == nil {
		t.Fatal("sent without typing send")
	}
}

// A node's wallet whose node has no key: [g] makes it, words shown and checked, written in the
// node's format; the recovery file is refused in the node's folder.
func TestTUINodeKeyGenerate(t *testing.T) {
	m, dir := nodeWalletModel(t, false)
	if m.current() != nil {
		t.Fatal("an account without a key")
	}
	if v := strings.Join(strings.Fields(strings.ReplaceAll(m.View(), "│", " ")), " "); !strings.Contains(v, "has none yet") || !strings.Contains(v, "[g] make the node's key") {
		t.Fatalf("main:\n%s", v)
	}
	typeText(m, "g")
	s, ok := m.screen.(*accountScreen)
	if !ok || s.nodeDir == "" {
		t.Fatalf("screen %T", m.screen)
	}
	fits80x24(t, "node key", m.View())
	if v := strings.Join(strings.Fields(m.View()), " "); !strings.Contains(v, "kept in clear on this server") {
		t.Errorf("no warning:\n%s", m.View())
	}
	press(m, tea.KeyEnter)
	if s.step != accWords {
		t.Fatalf("step %d err %v", s.step, s.err)
	}
	fits80x24(t, "words", m.View())
	words := append([]string(nil), s.words...)
	press(m, tea.KeyEnter)
	for _, p := range s.positions {
		typeText(m, words[p])
		press(m, tea.KeyEnter)
	}
	typeText(m, "y")
	s.file.inputs[0].SetValue(filepath.Join(dir, "words.txt"))
	if cmd := press(m, tea.KeyEnter); cmd != nil || s.err == nil || !strings.Contains(s.err.Error(), "node's folder") {
		t.Fatalf("recovery in the node's folder: %v", s.err)
	}
	s.file.inputs[0].SetValue("")
	press(m, tea.KeyEsc) // cancel: nothing written
	if _, err := os.Stat(filepath.Join(dir, "NodePrivate.txt")); err == nil {
		t.Fatal("written after Esc")
	}
	// again, without the file
	typeText(m, "g")
	s = m.screen.(*accountScreen)
	press(m, tea.KeyEnter)
	words = append([]string(nil), s.words...)
	press(m, tea.KeyEnter)
	for _, p := range s.positions {
		typeText(m, words[p])
		press(m, tea.KeyEnter)
	}
	finish(t, m, press(m, tea.KeyEnter))
	if s.step != accSaved || s.err != nil {
		t.Fatalf("saved: %v", s.err)
	}
	fits80x24(t, "saved", m.View())
	key, _ := keys.FromWords(words)
	priv, pub := keys.NodeKeyFiles(key)
	if got, _ := os.ReadFile(filepath.Join(dir, "NodePrivate.txt")); string(got) != string(priv) {
		t.Fatal("NodePrivate.txt")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "NodePublic.txt")); string(got) != string(pub) {
		t.Fatal("NodePublic.txt")
	}
	if v := strings.Join(strings.Fields(m.View()), " "); !strings.Contains(v, "the node's key is written") || !strings.Contains(v, string(pub)) {
		t.Errorf("saved:\n%s", m.View())
	}
	press(m, tea.KeyEnter)
	m.Update(m.loadAccounts()())
	if m.unlocked != nil || m.screen != nil {
		t.Fatal("not on the unlock screen after the key was made")
	}
	if f := m.selected(); f == nil || !f.IsNodeKey() || f.Public != string(pub) {
		t.Fatalf("the new key is not the account: %+v", f)
	}
	unlockSelected(t, m, "")
	if f := m.current(); f == nil || f.Public != string(pub) {
		t.Fatalf("unlocked: %+v", f)
	}
}
