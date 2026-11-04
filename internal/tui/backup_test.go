package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

func backupModel(t *testing.T, home string) *model {
	t.Helper()
	w := wallet.New(config.Default(), config.Profile{Name: "local", Address: "127.0.0.1:1", Network: config.Mainnet}, filepath.Join(home, ".credits-wallet"))
	w.KDF = lightKDF()
	m := newModel(w)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

func run(t *testing.T, m *model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("no background work")
	}
	m.Update(cmd())
}

func TestTUIBackupExportImport(t *testing.T) {
	old := keys.BackupKDF
	keys.BackupKDF = lightKDF()
	defer func() { keys.BackupKDF = old }()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	m := backupModel(t, home)
	newSeed := func() []byte { s, _ := keys.NewMasterSeed(); return keys.DeriveKey(&s) }
	if _, err := m.w.CreateAccount("main", newSeed(), []byte("pw-main")); err != nil {
		t.Fatal(err)
	}
	other := newSeed()
	m.w.CreateAccount("other", other, []byte("pw-other"))

	// b on the unlock screen, e: the default file in the home, the password twice
	typeText(m, "b")
	s, ok := m.screen.(*backupScreen)
	if !ok || !strings.Contains(m.View(), "export a backup") {
		t.Fatalf("no backup screen:\n%s", m.View())
	}
	typeText(m, "e")
	want, _ := wallet.DefaultBackupPath(time.Now())
	if s.form.value(0) != want {
		t.Fatalf("default path %q, want %q", s.form.value(0), want)
	}
	press(m, tea.KeyEnter)
	typeText(m, "short")
	if !strings.Contains(m.View(), "hint: a short password") {
		t.Errorf("no strength hint:\n%s", m.View())
	}
	press(m, tea.KeyEnter)
	typeText(m, "differs")
	press(m, tea.KeyEnter)
	if s.step != bkExportForm || s.err == nil {
		t.Fatalf("different passwords accepted: %d", s.step)
	}
	// into the data folder: refused
	s.form.inputs[0].SetValue(filepath.Join(home, ".credits-wallet", "b.dat"))
	s.form.inputs[2].SetValue("short")
	press(m, tea.KeyEnter) // (focus is on the last field)
	if s.err == nil || !strings.Contains(s.err.Error(), "data folder") {
		t.Fatalf("data folder: %v", s.err)
	}
	s.form.inputs[0].SetValue(want)
	s.form.inputs[2].SetValue("short")
	run(t, m, press(m, tea.KeyEnter))
	v := m.View()
	if s.step != bkDone || s.err != nil || !strings.Contains(v, "backup written") || !strings.Contains(v, "2 accounts") || !strings.Contains(v, "USB key") {
		t.Fatalf("export: %v\n%s", s.err, v)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatal(err)
	}
	typeText(m, "x")
	if m.screen != nil {
		t.Fatal("not back")
	}

	// another wallet with main's name taken by another address and other's address already there
	home2 := t.TempDir()
	m2 := backupModel(t, home2)
	m2.w.CreateAccount("main", newSeed(), []byte("x"))
	m2.w.CreateAccount("mine", other, []byte("y"))
	typeText(m2, "b")
	typeText(m2, "i")
	s2 := m2.screen.(*backupScreen)
	typeText(m2, want)
	press(m2, tea.KeyEnter)
	typeText(m2, "wrong")
	run(t, m2, press(m2, tea.KeyEnter))
	if s2.step != bkImportForm || s2.err == nil || !strings.Contains(m2.View(), "wrong backup password, or a damaged file") {
		t.Fatalf("wrong password: %v\n%s", s2.err, m2.View())
	}
	typeText(m2, "short")
	run(t, m2, press(m2, tea.KeyEnter))
	if s2.step != bkRename || !strings.Contains(m2.View(), "is another account's here") {
		t.Fatalf("no rename: %d %v\n%s", s2.step, s2.err, m2.View())
	}
	typeText(m2, "mine") // taken
	press(m2, tea.KeyEnter)
	if s2.step != bkRename || s2.err == nil {
		t.Fatal("a taken name was accepted")
	}
	s2.form.inputs[0].SetValue("main-old")
	press(m2, tea.KeyEnter)
	v = m2.View()
	if s2.step != bkConfirm || !strings.Contains(v, "restore as main-old") || !strings.Contains(v, "here as mine: skipped") || !strings.Contains(v, "restore 1") {
		t.Fatalf("confirm:\n%s", v)
	}
	run(t, m2, press(m2, tea.KeyEnter))
	v = m2.View()
	if s2.step != bkDone || !strings.Contains(v, "as main-old") || !strings.Contains(v, "keep their own passwords") {
		t.Fatalf("done:\n%s", v)
	}
	f, err := m2.w.Store.Load("main-old")
	if err != nil {
		t.Fatal(err)
	}
	if k, err := f.Decrypt([]byte("pw-main")); err != nil {
		t.Fatalf("main-old does not open with main's password: %v", err)
	} else {
		keys.Zero(k)
	}
	if m2.w.Store.Exists("other") {
		t.Fatal("an address already here was restored")
	}
	for _, line := range strings.Split(v, "\n") {
		if w := len([]rune(line)); w > 80 {
			t.Errorf("line wider than 80: %q", line)
		}
	}
}
