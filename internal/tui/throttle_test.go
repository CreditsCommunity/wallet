package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

// Every screen that checks the wallet password while the wallet is closed shares the unlock
// screen's counter and delay: a wrong password anywhere delays the next try everywhere, and no
// screen tries while the delay runs.
func TestWalletPasswordThrottleOnEveryPath(t *testing.T) {
	old := keys.BackupKDF
	keys.BackupKDF = lightKDF()
	defer func() { keys.BackupKDF = old }()
	m, c, _ := lockModel(t)
	legacyAccount(t, m.w, "legacy", "own") // so that the migration screen asks the wallet password

	// a backup from another wallet whose password is the guess: importing it tries the guess as
	// this wallet's password
	other := backupModel(t, t.TempDir())
	other.w.CreateAccount("x", newTestKey(), []byte("guess"))
	b, _, err := other.w.CollectBackup(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	bk := filepath.Join(t.TempDir(), "guess.dat")
	if err := wallet.WriteBackup(bk, b, []byte("guess")); err != nil {
		t.Fatal(err)
	}

	// each path types a wrong wallet password; it returns the work started (nil: refused) and
	// the screen's message
	paths := map[string]func() (tea.Cmd, string){
		"unlock": func() (tea.Cmd, string) {
			m.screen = nil
			m.lock.reset("")
			press(m, tea.KeyEnter)
			typeText(m, "guess")
			return press(m, tea.KeyEnter), m.lock.note
		},
		"new account": func() (tea.Cmd, string) {
			s := m.newAccountScreen(true)
			m.screen = s
			s.form.inputs[0].SetValue("imported")
			s.form.inputs[1].SetValue(keys.Base58(newTestKey()))
			s.form.inputs[2].SetValue("guess")
			s.form.setFocus(2)
			cmd := press(m, tea.KeyEnter)
			return cmd, errText(s.err)
		},
		"backup export": func() (tea.Cmd, string) {
			s := newBackupScreen()
			m.screen = s
			typeText(m, "e")
			s.form.inputs[0].SetValue(filepath.Join(t.TempDir(), "b.dat"))
			s.form.inputs[1].SetValue("guess")
			s.form.setFocus(1)
			cmd := press(m, tea.KeyEnter)
			return cmd, errText(s.err)
		},
		"backup import": func() (tea.Cmd, string) {
			s := newBackupScreen()
			m.screen = s
			typeText(m, "i")
			s.form.inputs[0].SetValue(bk)
			s.form.inputs[1].SetValue("guess")
			s.form.setFocus(1)
			cmd := press(m, tea.KeyEnter)
			return cmd, errText(s.err)
		},
		"migration": func() (tea.Cmd, string) {
			s := m.newMigrateScreen()
			m.screen = s
			typeText(m, "guess")
			cmd := press(m, tea.KeyEnter)
			return cmd, errText(s.err)
		},
		"change of password": func() (tea.Cmd, string) {
			s := newPasswordScreen()
			m.screen = s
			s.form.inputs[0].SetValue("guess")
			s.form.inputs[1].SetValue("new one")
			s.form.inputs[2].SetValue("new one")
			s.form.setFocus(2)
			cmd := press(m, tea.KeyEnter)
			return cmd, errText(s.err)
		},
	}
	order := []string{"unlock", "new account", "backup export", "backup import", "migration", "change of password"}
	for i, name := range order {
		// a wrong password on this path counts, and starts the delay
		cmd, msg := paths[name]()
		if cmd == nil {
			t.Fatalf("%s: no try (%q)", name, msg)
		}
		m.Update(cmd())
		if m.lock.failed != i+1 || m.lock.retryAt.Sub(c.now()) != unlockDelay(i+1) {
			t.Fatalf("%s: counter %d, wait %v", name, m.lock.failed, m.lock.retryAt.Sub(c.now()))
		}
		if m.session != nil {
			t.Fatalf("%s: a wrong password opened the wallet", name)
		}
		// while the delay runs, no path tries
		for _, other := range order {
			if cmd, msg := paths[other](); cmd != nil || !strings.Contains(msg, "wait") {
				t.Fatalf("after %s, %s tried during the delay (%q)", name, other, msg)
			}
		}
		c.add(unlockDelay(i+1) + time.Second)
	}
	// the right password resets the counter
	m.screen = nil
	m.lock.reset("")
	tryPassword(t, m, "main-pw")
	if m.unlocked == nil || m.lock.failed != 0 {
		t.Fatalf("not unlocked: %q", m.lock.note)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
