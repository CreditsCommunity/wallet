package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

func typeText(m *model, text string) {
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
}

func press(m *model, k tea.KeyType) tea.Cmd {
	_, cmd := m.Update(tea.KeyMsg{Type: k})
	return cmd
}

// newAccountUpToFile fills the new-account form and the check, up to the recovery question.
func newAccountUpToFile(t *testing.T, name string) (*model, *accountScreen, string) {
	t.Helper()
	home := t.TempDir()
	data := filepath.Join(home, ".credits-wallet")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	w := wallet.New(config.Default(), config.Profile{Name: "local", Address: "127.0.0.1:1", Network: config.Mainnet}, data)
	w.KDF = keys.KDFParams{Name: "argon2id", Time: keys.MinKDF.Time, MemoryKiB: keys.MinKDF.MemoryKiB, Threads: 1}
	m := newModel(w)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	s := newAccountScreen(false)
	m.screen = s
	typeText(m, name)
	press(m, tea.KeyEnter)
	typeText(m, "pw")
	press(m, tea.KeyEnter)
	typeText(m, "pw")
	press(m, tea.KeyEnter)
	if s.step != accWords {
		t.Fatalf("not at the phrase: step %d, err %v", s.step, s.err)
	}
	words := append([]string(nil), s.words...)
	press(m, tea.KeyEnter)
	for _, p := range s.positions {
		typeText(m, words[p])
		press(m, tea.KeyEnter)
	}
	if s.step != accFileAsk {
		t.Fatalf("not at the question: step %d, err %v", s.step, s.err)
	}
	if v := m.View(); !strings.Contains(v, "Save the 24 words to a file as well? [y/N]") {
		t.Fatalf("the question:\n%s", v)
	}
	return m, s, home
}

// finish runs the background save and hands its result to the screen.
func finish(t *testing.T, m *model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("nothing to save")
	}
	m.Update(cmd())
}

func TestTUIRecoveryDefaultNo(t *testing.T) {
	m, s, home := newAccountUpToFile(t, "main")
	finish(t, m, press(m, tea.KeyEnter))
	if s.step != accSaved || s.err != nil || s.address == "" {
		t.Fatalf("saved: step %d err %v", s.step, s.err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 1 { // only the data folder
		t.Errorf("Enter wrote a file: %v", entries)
	}
	if strings.Contains(m.View(), "credits-recovery") {
		t.Error("a recovery file is mentioned")
	}
}

func TestTUIRecoveryYes(t *testing.T) {
	m, s, home := newAccountUpToFile(t, "main")
	typeText(m, "y")
	want := filepath.Join(home, "credits-recovery-main.txt")
	if s.step != accFilePath || s.file.value(0) != want {
		t.Fatalf("path step %d, default %q", s.step, s.file.value(0))
	}
	view := m.View()
	for i, l := range strings.Split(view, "\n") {
		if w := lipgloss.Width(l); w > 80 {
			t.Errorf("line %d: %d columns", i, w)
		}
	}
	finish(t, m, press(m, tea.KeyEnter))
	if s.step != accSaved || s.err != nil {
		t.Fatalf("saved: step %d err %v", s.step, s.err)
	}
	st, err := os.Stat(want)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	text, _ := os.ReadFile(want)
	if !strings.Contains(string(text), "address: "+s.address) || strings.Count(string(text), ". ") < 24 {
		t.Errorf("file:\n%s", text)
	}
	view = strings.Join(strings.Fields(strings.ReplaceAll(m.View(), "│", " ")), " ")
	if !strings.Contains(view, "copy it to paper or an offline USB key, then delete it from this computer") || !strings.Contains(view, "credits-recovery-main.txt") {
		t.Errorf("no path or advice:\n%s", m.View())
	}
	if s.words != nil || s.key != nil {
		t.Error("the phrase or the key is still held")
	}
}

func TestTUIRecoveryRefusesDataFolder(t *testing.T) {
	m, s, _ := newAccountUpToFile(t, "main")
	typeText(m, "y")
	s.file.inputs[0].SetValue(filepath.Join(m.w.DataDir, "words.txt"))
	if cmd := press(m, tea.KeyEnter); cmd != nil || s.step != accFilePath || s.err == nil || !strings.Contains(s.err.Error(), "data folder") {
		t.Fatalf("not refused: step %d err %v", s.step, s.err)
	}
	if !strings.Contains(m.View(), "refused") {
		t.Errorf("the refusal is not shown:\n%s", m.View())
	}
}
