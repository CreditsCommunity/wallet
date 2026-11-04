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

func newTestKey() []byte { s, _ := keys.NewMasterSeed(); return keys.DeriveKey(&s) }

// opensUnder tells whether the account is under the wallet password pw of w.
func opensUnder(t *testing.T, w *wallet.Wallet, name, pw string) bool {
	t.Helper()
	f, err := w.Store.Load(name)
	if err != nil || !f.UnderWalletKey() {
		return false
	}
	s, err := w.OpenWallet([]byte(pw))
	if err != nil {
		return false
	}
	defer s.Lock()
	k, err := s.Open(f)
	if err != nil {
		return false
	}
	keys.Zero(k)
	return true
}

func TestTUIBackupExportImport(t *testing.T) {
	old := keys.BackupKDF
	keys.BackupKDF = lightKDF()
	defer func() { keys.BackupKDF = old }()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	m := backupModel(t, home)
	if _, err := m.w.CreateAccount("main", newTestKey(), []byte("pw-a")); err != nil {
		t.Fatal(err)
	}
	other := newTestKey()
	m.w.CreateAccount("other", other, []byte("pw-a"))

	// b on the unlock screen, e: the default file in the home, the wallet password
	typeText(m, "b")
	s, ok := m.screen.(*backupScreen)
	if !ok || !strings.Contains(m.View(), "export a backup") {
		t.Fatalf("no backup screen:\n%s", m.View())
	}
	typeText(m, "e")
	want, _ := wallet.DefaultBackupPath(time.Now())
	if s.form.value(0) != want || len(s.form.inputs) != 2 {
		t.Fatalf("default path %q, want %q; %v", s.form.value(0), want, s.form.labels)
	}
	// into the data folder: refused
	s.form.inputs[0].SetValue(filepath.Join(home, ".credits-wallet", "b.dat"))
	press(m, tea.KeyEnter)
	typeText(m, "pw-a")
	press(m, tea.KeyEnter)
	if s.err == nil || !strings.Contains(s.err.Error(), "data folder") {
		t.Fatalf("data folder: %v", s.err)
	}
	// not the wallet password: refused
	s.form.inputs[0].SetValue(want)
	s.form.inputs[1].SetValue("another")
	run(t, m, press(m, tea.KeyEnter))
	if s.step != bkExportForm || s.err == nil || !wallet.IsWrongPassword(s.err) {
		t.Fatalf("a backup under another password: %v", s.err)
	}
	if _, err := os.Stat(want); err == nil {
		t.Fatal("written with a wrong password")
	}
	s.form.inputs[1].SetValue("pw-a")
	run(t, m, press(m, tea.KeyEnter))
	v := m.View()
	if s.step != bkDone || s.err != nil || !strings.Contains(v, "backup written") || !strings.Contains(v, "2 accounts") || !strings.Contains(v, "USB key") {
		t.Fatalf("export: %v\n%s", s.err, v)
	}
	typeText(m, "x")
	if m.screen != nil {
		t.Fatal("not back")
	}

	// another wallet, another password, main's name taken by another address, other's address here
	m2 := backupModel(t, t.TempDir())
	m2.w.CreateAccount("main", newTestKey(), []byte("pw-b"))
	m2.w.CreateAccount("mine", other, []byte("pw-b"))
	typeText(m2, "b")
	typeText(m2, "i")
	s2 := m2.screen.(*backupScreen)
	typeText(m2, want)
	press(m2, tea.KeyEnter)
	typeText(m2, "wrong")
	run(t, m2, press(m2, tea.KeyEnter))
	if s2.step != bkImportForm || !strings.Contains(m2.View(), "wrong backup password, or a damaged file") {
		t.Fatalf("wrong password: %v\n%s", s2.err, m2.View())
	}
	typeText(m2, "pw-a")
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
	// this wallet's password is not the backup's: asked once
	if s2.step != bkWalletPw || !strings.Contains(m2.View(), "This wallet has another password") {
		t.Fatalf("this wallet's password not asked: %d\n%s", s2.step, m2.View())
	}
	typeText(m2, "pw-b")
	run(t, m2, press(m2, tea.KeyEnter))
	v = m2.View()
	if s2.step != bkConfirm || !strings.Contains(v, "restore as main-old") || !strings.Contains(v, "here as mine: skipped") || !strings.Contains(v, "restore 1") {
		t.Fatalf("confirm:\n%s", v)
	}
	run(t, m2, press(m2, tea.KeyEnter))
	v = m2.View()
	if s2.step != bkDone || !strings.Contains(v, "as main-old") || !strings.Contains(v, "under this wallet's password") {
		t.Fatalf("done:\n%s", v)
	}
	if !opensUnder(t, m2.w, "main-old", "pw-b") {
		t.Fatal("main-old is not under this wallet's password")
	}
	if m2.w.Store.Exists("other") {
		t.Fatal("an address already here was restored")
	}

	// an empty wallet: the backup's password becomes its wallet password, nothing more asked
	m3 := backupModel(t, t.TempDir())
	typeText(m3, "b")
	typeText(m3, "i")
	s3 := m3.screen.(*backupScreen)
	typeText(m3, want)
	press(m3, tea.KeyEnter)
	typeText(m3, "pw-a")
	run(t, m3, press(m3, tea.KeyEnter))
	if s3.step != bkConfirm || !strings.Contains(m3.View(), "the backup's becomes its wallet password") {
		t.Fatalf("empty wallet: %d %v\n%s", s3.step, s3.err, m3.View())
	}
	run(t, m3, press(m3, tea.KeyEnter))
	if !opensUnder(t, m3.w, "main", "pw-a") || !opensUnder(t, m3.w, "other", "pw-a") {
		t.Fatal("not restored under the backup's password")
	}
}

// legacyAccount writes an account file of version 1 (a password of its own), as before the wallet
// password.
func legacyAccount(t *testing.T, w *wallet.Wallet, name, pw string) {
	t.Helper()
	f, err := keys.Encrypt(name, newTestKey(), []byte(pw), lightKDF())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Store.Save(f); err != nil {
		t.Fatal(err)
	}
}

// The first start after the upgrade: the wallet password is chosen (here one of the accounts'),
// the accounts that have it move at once, each other's password is asked once, an account whose
// password is not given keeps it.
func TestTUIMigration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := backupModel(t, home)
	legacyAccount(t, m.w, "a", "p1")
	legacyAccount(t, m.w, "a2", "p1")
	legacyAccount(t, m.w, "b", "p2")
	legacyAccount(t, m.w, "c", "p3")
	m.Update(m.loadAccounts()())
	s, ok := m.screen.(*migrateScreen)
	if !ok || !strings.Contains(m.View(), "choose the wallet password") {
		t.Fatalf("no migration at start: %T\n%s", m.screen, m.View())
	}
	fits80x24(t, "migration", m.View())
	typeText(m, "p1")
	if !strings.Contains(m.View(), "hint: a short password") {
		t.Errorf("no hint:\n%s", m.View())
	}
	press(m, tea.KeyEnter)
	typeText(m, "p1")
	run(t, m, press(m, tea.KeyEnter))
	if s.step != migAccount || strings.Join(s.moved, ",") != "a,a2" || s.left[0].Name != "b" {
		t.Fatalf("after the wallet password: %d %v %v", s.step, s.moved, s.err)
	}
	typeText(m, "wrong")
	run(t, m, press(m, tea.KeyEnter))
	if s.step != migAccount || s.err == nil || s.left[0].Name != "b" {
		t.Fatalf("wrong password: %v", s.err)
	}
	typeText(m, "p2")
	run(t, m, press(m, tea.KeyEnter))
	press(m, tea.KeyEnter) // c: nothing typed, it keeps its password
	if s.step != migDone || !strings.Contains(m.View(), "old password kept: c") {
		t.Fatalf("done:\n%s", m.View())
	}
	typeText(m, "x")
	m.Update(m.loadAccounts()())
	if m.session == nil || m.screen != nil {
		t.Fatal("the wallet is not open after the migration")
	}
	for _, n := range []string{"a", "a2", "b"} {
		if !opensUnder(t, m.w, n, "p1") {
			t.Errorf("%s is not under the wallet password", n)
		}
	}
	if f, _ := m.w.Store.Load("c"); !f.OldPassword() {
		t.Fatal("c changed")
	}
	if _, err := os.Stat(m.w.MigrationDir()); err == nil {
		t.Error("the migration's copies are left")
	}
	// c: its own password, asked when it is chosen, and M offers to move it
	if v := m.View(); !strings.Contains(v, "old password") || !strings.Contains(v, "[M]") {
		t.Errorf("picker:\n%s", v)
	}
	for m.selected().Name != "c" {
		press(m, tea.KeyDown)
	}
	press(m, tea.KeyEnter)
	if m.lock.step != lockPassword || !strings.Contains(flat(m.View()), "Old password of c") {
		t.Fatalf("c's password not asked:\n%s", m.View())
	}
	tryPassword(t, m, "p3")
	if m.current() == nil || m.current().Name != "c" {
		t.Fatalf("c: %q", m.lock.note)
	}
}

// P changes the wallet password: every account under a new key, the session goes on.
func TestTUIChangePassword(t *testing.T) {
	m, _, _ := lockModel(t)
	tryPassword(t, m, "main-pw")
	typeText(m, "P")
	s, ok := m.screen.(*passwordScreen)
	if !ok {
		t.Fatalf("no password screen: %T", m.screen)
	}
	typeText(m, "main-pw")
	press(m, tea.KeyEnter)
	typeText(m, "new password")
	press(m, tea.KeyEnter)
	typeText(m, "new password")
	old := m.session
	run(t, m, press(m, tea.KeyEnter))
	if !s.done || s.err != nil || m.session == nil || m.session == old || !old.Locked() {
		t.Fatalf("not changed: %v", s.err)
	}
	if !strings.Contains(flat(m.View()), "keep opening with the old password") {
		t.Errorf("view:\n%s", m.View())
	}
	for _, n := range []string{"main", "savings"} {
		if !opensUnder(t, m.w, n, "new password") || opensUnder(t, m.w, n, "main-pw") {
			t.Errorf("%s", n)
		}
	}
	typeText(m, "x")
	typeText(m, "a")
	press(m, tea.KeyDown)
	runCmd(m, press(m, tea.KeyEnter))
	if m.current() == nil || m.current().Name != "savings" {
		t.Fatalf("the session does not open accounts after the change: %q %T %d", m.lock.note, m.screen, m.sel)
	}
}
