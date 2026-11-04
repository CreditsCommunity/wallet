package tui

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mr-tron/base58"

	"github.com/CreditsCommunity/wallet/internal/amount"
	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/keys/keystest"
	"github.com/CreditsCommunity/wallet/internal/node"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

// clock is the tests' time: it moves only when told.
type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func lightKDF() keys.KDFParams {
	return keys.KDFParams{Name: "argon2id", Time: 1, MemoryKiB: keys.MinKDF.MemoryKiB, Threads: 1}
}
func flat(view string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(view, "│", " ")), " ")
}
func runCmd(m *model, cmd tea.Cmd) { m.Update(cmd()) }
func balanceMsg(name string) accountDataMsg {
	bal, _ := amount.Parse("12345.678")
	return accountDataMsg{name: name, bal: node.Balance{CS: bal}}
}

// lockModel: a wallet with two accounts of the store (main: "main-pw", savings: "other"), locked.
func lockModel(t *testing.T) (*model, *clock, map[string]string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	w := wallet.New(config.Default(), config.Profile{Name: "local", Address: "127.0.0.1:1", Network: config.Mainnet}, filepath.Join(home, ".credits-wallet"))
	w.KDF = lightKDF()
	addr := map[string]string{}
	for name, pw := range map[string]string{"main": "main-pw", "savings": "other"} {
		m, _ := keys.NewMasterSeed()
		f, err := w.CreateAccount(name, keys.DeriveKey(&m), []byte(pw))
		if err != nil {
			t.Fatal(err)
		}
		addr[name] = f.Public
	}
	m := newModel(w)
	c := &clock{t: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)}
	m.now = c.now
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(m.loadAccounts()())
	return m, c, addr
}

// tryPassword types a password on the unlock screen and runs the unlock.
func tryPassword(t *testing.T, m *model, pw string) {
	t.Helper()
	if m.lock.step == lockList {
		press(m, tea.KeyEnter)
	}
	typeText(m, pw)
	cmd := press(m, tea.KeyEnter)
	if cmd == nil {
		t.Fatalf("no unlock started: %q", m.lock.note)
	}
	runCmd(m, cmd)
}

// Locked, the interface shows the accounts' names and addresses only, loads nothing of them and
// opens no screen of an account; a wrong password says nothing more, and the next try waits.
func TestUnlockFlow(t *testing.T) {
	m, c, addr := lockModel(t)
	view := m.View()
	fits80x24(t, "unlock", view)
	if !strings.Contains(view, "main") || !strings.Contains(view, addr["main"]) || !strings.Contains(view, addr["savings"]) {
		t.Fatalf("unlock screen:\n%s", view)
	}
	// nothing of an account while locked
	if m.current() != nil || m.refreshCurrent() != nil {
		t.Fatal("an account is current while locked")
	}
	m.Update(balanceMsg("main"))
	for _, k := range []string{"s", "d", "t", "c", "h", "y"} {
		typeText(m, k)
		if m.screen != nil {
			t.Fatalf("%s opened %T while locked", k, m.screen)
		}
	}
	if v := m.View(); strings.Contains(v, "12,345") || strings.Contains(v, "Balance") || len(m.balances) != 0 {
		t.Fatalf("account data shown while locked:\n%s", v)
	}
	// wrong password: no detail, a delay before the next try
	tryPassword(t, m, "wrong")
	if m.unlocked != nil || m.lock.note != wallet.ErrUnlock.Error() || m.lock.failed != 1 {
		t.Fatalf("wrong password: %v %q", m.unlocked, m.lock.note)
	}
	if v := flat(m.View()); !strings.Contains(v, "could not unlock: wrong password, or a damaged key file") {
		t.Errorf("view:\n%s", m.View())
	}
	typeText(m, "main-pw")
	if cmd := press(m, tea.KeyEnter); cmd != nil || !strings.Contains(m.lock.note, "wait") {
		t.Fatalf("tried again at once: %q", m.lock.note)
	}
	c.add(3 * time.Second)
	tryPassword(t, m, "wrong")
	if m.lock.failed != 2 || m.lock.retryAt.Sub(c.now()) != 4*time.Second {
		t.Fatalf("second failure: %d, wait %v", m.lock.failed, m.lock.retryAt.Sub(c.now()))
	}
	c.add(5 * time.Second)
	tryPassword(t, m, "main-pw")
	if m.unlocked == nil || m.current().Name != "main" || m.lock.failed != 0 {
		t.Fatalf("not unlocked: %q", m.lock.note)
	}
	m.Update(balanceMsg("main"))
	m.Update(balanceMsg("savings")) // another account's data: dropped
	view = m.View()
	fits80x24(t, "main", view)
	if !strings.Contains(view, "12,345.678 CS") || !strings.Contains(view, "[l] lock") || m.balances["savings"].CS.Sign() != 0 {
		t.Fatalf("main after unlock:\n%s", view)
	}
	// the send review asks no password, only "send" on mainnet
	f := m.current()
	pub, _ := keys.ParseAddress(f.Public)
	amt, _ := amount.Parse("1")
	s := newSendScreen(f)
	m.screen = s
	s.step = sendPreparing
	m.Update(preparedMsg{&wallet.SendPlan{Kind: wallet.KindTransfer, Account: f.Name, From: pub, To: pub, Amount: amt, Balance: amt, Network: config.Mainnet, Prepared: c.now()}, nil})
	if len(s.confirm.inputs) != 1 || s.confirm.labels[0] != "Confirm" || strings.Contains(m.View(), "Password") {
		t.Fatalf("review: %v\n%s", s.confirm.labels, m.View())
	}
	s.seenEnd = true
	if cmd := press(m, tea.KeyEnter); cmd != nil || s.err == nil {
		t.Fatal("signed on MAINNET without typing send")
	}
	press(m, tea.KeyEsc)
	press(m, tea.KeyEsc)
	if m.screen != nil {
		t.Fatalf("screen %T", m.screen)
	}

	// manual lock: the key zeroed, the data forgotten, the unlock screen on the same account
	u := m.unlocked
	typeText(m, "l")
	if m.unlocked != nil || !u.Locked() || len(m.balances) != 0 || m.selected().Name != "main" {
		t.Fatal("not locked by l")
	}
	if v := m.View(); strings.Contains(v, "12,345") || !strings.Contains(v, "Unlock an account") {
		t.Fatalf("after l:\n%s", v)
	}
	// switch: a locks first, then another account with its own password
	tryPassword(t, m, "main-pw")
	u = m.unlocked
	typeText(m, "a")
	if m.unlocked != nil || !u.Locked() {
		t.Fatal("a did not lock")
	}
	press(m, tea.KeyDown)
	tryPassword(t, m, "main-pw") // main's password does not open savings
	if m.unlocked != nil {
		t.Fatal("savings opened with main's password")
	}
	c.add(time.Minute)
	tryPassword(t, m, "other")
	if m.current() == nil || m.current().Name != "savings" {
		t.Fatalf("savings: %q", m.lock.note)
	}
}

// The session locks after lock_after_minutes without a key press, never while a transaction is on
// its way, and never with 0.
func TestAutoLock(t *testing.T) {
	m, c, _ := lockModel(t)
	if m.lockAfter != 10*time.Minute {
		t.Fatalf("default %v", m.lockAfter)
	}
	tryPassword(t, m, "main-pw")
	c.add(9 * time.Minute)
	m.Update(tickMsg{})
	if m.unlocked == nil {
		t.Fatal("locked before 10 minutes")
	}
	typeText(m, "?") // a key: the timer starts again
	press(m, tea.KeyEsc)
	c.add(9 * time.Minute)
	m.Update(tickMsg{})
	if m.unlocked == nil {
		t.Fatal("a key press did not count")
	}
	c.add(2 * time.Minute)
	u := m.unlocked
	m.Update(tickMsg{})
	if m.unlocked != nil || !u.Locked() || !strings.Contains(m.lock.note, "locked after 10m0s") {
		t.Fatalf("not locked after 11 minutes: %q", m.lock.note)
	}
	if v := m.View(); !strings.Contains(v, "locked after") {
		t.Errorf("view:\n%s", v)
	}
	// a transaction on its way: not locked under it
	tryPassword(t, m, "main-pw")
	m.screen = &sendScreen{account: m.current(), step: sendSending}
	c.add(time.Hour)
	m.Update(tickMsg{})
	if m.unlocked == nil {
		t.Fatal("locked while sending")
	}
	m.screen = nil
	m.Update(tickMsg{})
	if m.unlocked != nil {
		t.Fatal("not locked once sent")
	}
	// lock_after_minutes = 0: never
	zero := 0
	m.w.Config.LockAfterMinutes = &zero
	m2 := newModel(m.w)
	m2.now = c.now
	m2.Update(m2.loadAccounts()())
	tryPassword(t, m2, "main-pw")
	c.add(48 * time.Hour)
	m2.Update(tickMsg{})
	if m2.unlocked == nil {
		t.Fatal("locked with lock_after_minutes = 0")
	}
}

// After an account is created (or imported) the wallet is on the unlock screen, the session that
// was open is locked, and the new account is selected.
func TestLockedAfterCreation(t *testing.T) {
	m, _, _ := lockModel(t)
	tryPassword(t, m, "main-pw")
	u := m.unlocked
	typeText(m, "n")
	s := m.screen.(*accountScreen)
	typeText(m, "third")
	press(m, tea.KeyEnter)
	typeText(m, "pw3")
	press(m, tea.KeyEnter)
	typeText(m, "pw3")
	press(m, tea.KeyEnter)
	words := append([]string(nil), s.words...)
	press(m, tea.KeyEnter)
	for _, p := range s.positions {
		typeText(m, words[p])
		press(m, tea.KeyEnter)
	}
	finish(t, m, press(m, tea.KeyEnter)) // no recovery file
	if s.step != accSaved || s.err != nil {
		t.Fatalf("saved: %v", s.err)
	}
	if m.unlocked != nil || !u.Locked() {
		t.Fatal("the session stayed open after the creation")
	}
	if v := m.View(); strings.Contains(v, "Balance") {
		t.Errorf("saved:\n%s", v)
	}
	press(m, tea.KeyEnter)
	m.Update(m.loadAccounts()())
	if m.screen != nil || m.unlocked != nil || m.selected() == nil || m.selected().Name != "third" {
		t.Fatalf("not the unlock screen on the new account: %T %v", m.screen, m.selected())
	}
	tryPassword(t, m, "pw3")
	if m.current() == nil || m.current().Name != "third" {
		t.Fatal("the new account does not unlock")
	}
}

// The node's key encrypted by the node: on the unlock screen with its NodePublic.txt's address,
// opened only with the node's password; signing then asks none.
func TestTUIEncryptedNodeKey(t *testing.T) {
	old := keys.NodeKDF
	keys.NodeKDF.Time, keys.NodeKDF.MemoryKiB = 1, 64
	defer func() { keys.NodeKDF = old }()
	m, dir := nodeWalletModel(t, false)
	key, _ := keys.FromBase58(nodeSecret)
	enc, _ := keystest.EncryptNodeKey(key, []byte("node pw"), keys.NodeKDF.Time, keys.NodeKDF.MemoryKiB)
	os.WriteFile(filepath.Join(dir, keys.NodePrivateFile), []byte(base58.Encode(enc)), 0o600)
	address := keys.Address(key.Public().(ed25519.PublicKey))
	os.WriteFile(filepath.Join(dir, keys.NodePublicFile), []byte(address), 0o600)
	m.Update(m.loadAccounts()())
	f := m.selected()
	if f == nil || !f.NodeKeyEncrypted || f.Public != address {
		t.Fatalf("%+v %v", f, m.accErr)
	}
	view := m.View()
	fits80x24(t, "unlock", view)
	if !strings.Contains(view, "node key, enc") {
		t.Errorf("unlock:\n%s", view)
	}
	press(m, tea.KeyEnter)
	if m.lock.step != lockPassword || !strings.Contains(flat(m.View()), "The node's password for its key") {
		t.Fatalf("no password asked:\n%s", m.View())
	}
	tryPassword(t, m, "wrong")
	if m.unlocked != nil || m.lock.note != wallet.ErrUnlock.Error() {
		t.Fatalf("wrong: %q", m.lock.note)
	}
	m.lock.retryAt = time.Time{}
	tryPassword(t, m, "node pw")
	if m.current() == nil || !m.current().NodeKeyEncrypted {
		t.Fatalf("not unlocked: %q", m.lock.note)
	}
	if v := flat(m.View()); !strings.Contains(v, "encrypted by the node: unlocked") {
		t.Errorf("main:\n%s", m.View())
	}
	s := newSendScreen(m.current())
	m.screen = s
	if v := m.View(); !strings.Contains(v, "NODE KEY: encrypted by the node") {
		t.Errorf("send:\n%s", v)
	}
}

// A failure that is not the password (here the files' checks) is said as it is, with no wait added.
func TestUnlockOtherFailure(t *testing.T) {
	m, dir := nodeWalletModel(t, false)
	os.WriteFile(filepath.Join(dir, keys.NodePrivateFile), []byte(nodeSecret), 0o600)
	m.Update(m.loadAccounts()())
	os.Remove(filepath.Join(dir, keys.NodePrivateFile)) // gone between the list and the unlock
	cmd := press(m, tea.KeyEnter)
	if cmd == nil {
		t.Fatal("no unlock started")
	}
	runCmd(m, cmd)
	if m.unlocked != nil || m.lock.failed != 0 || m.lock.note == wallet.ErrUnlock.Error() || m.lock.note == "" {
		t.Fatalf("failed %d, note %q", m.lock.failed, m.lock.note)
	}
}
