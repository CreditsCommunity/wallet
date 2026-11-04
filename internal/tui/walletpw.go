package tui

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

// The migration screen: the accounts that still have a password each move under the wallet
// password. Offered at the first start after the upgrade (no wallet password yet), and with M
// while accounts with an old password are left. The wallet password (chosen now, it may be one of
// the accounts' passwords; or typed) moves every account that has it; each other account's
// password is then asked once (empty: it keeps its old password, for later).

type migStep int

const (
	migPassword migStep = iota // choose (Password, Repeat) or type (Wallet pw) the wallet password
	migWorking
	migAccount // an account's own password
	migDone
)

type migrateScreen struct {
	step    migStep
	choose  bool // no wallet password yet: it is chosen
	form    *form
	session *wallet.Session // opened here (handed to the model at the end)
	left    []*keys.File
	moved   []string
	kept    []string
	err     error
}

type migratedMsg struct {
	epoch   int
	tried   bool // the wallet password was checked (it existed)
	session *wallet.Session
	moved   []string
	left    []*keys.File
	err     error
}

type migratedOneMsg struct {
	epoch int
	name  string
	err   error
}

func (m *model) newMigrateScreen() *migrateScreen {
	s := &migrateScreen{choose: !m.w.HasWalletPassword()}
	switch {
	case s.choose:
		s.form = newForm("Password", "Repeat").secret(0).secret(1)
	case m.session != nil:
		// the wallet is open: only the accounts' own passwords are asked
		s.left = m.w.OldPasswordAccounts()
		s.nextAccount()
		return s
	default:
		s.form = newForm("Wallet pw").secret(0)
	}
	return s
}

func (s *migrateScreen) title() string { return "One password for the wallet" }

// close zeroes the wallet the screen opened, unless it is the model's.
func (s *migrateScreen) close(m *model) {
	if s.form != nil {
		s.form.wipe()
	}
	if s.session != nil && s.session != m.session {
		s.session.Lock()
	}
	s.session = nil
}

func (s *migrateScreen) nextAccount() {
	if len(s.left) == 0 {
		s.step = migDone
		return
	}
	s.form = newForm("Password").secret(0)
	s.step = migAccount
}

func (s *migrateScreen) finish(m *model) tea.Cmd {
	if s.form != nil {
		s.form.wipe()
	}
	sess := s.session
	if sess == nil {
		sess = m.session
	}
	if sess != nil {
		for _, p := range m.w.FinishMigration(sess) {
			m.flash = strings.TrimPrefix(m.flash+"; "+oneLine(p.Error()), "; ")
		}
	}
	if s.session != nil && m.session == nil {
		m.session = s.session // the wallet is open: choosing an account asks nothing
	} else if s.session != nil && s.session != m.session {
		s.session.Lock()
	}
	s.session = nil
	return m.back()
}

func (s *migrateScreen) update(m *model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case migratedMsg:
		if msg.tried {
			m.pwTried(msg.err)
		}
		if msg.err != nil && msg.session == nil {
			s.step, s.err = migPassword, wrongPassword(msg.err)
			return nil
		}
		s.session, s.moved, s.left, s.err = msg.session, msg.moved, msg.left, msg.err
		for _, w := range msg.session.Warnings {
			m.addFlash(w)
		}
		s.nextAccount()
		return nil
	case migratedOneMsg:
		f := s.left[0]
		switch {
		case msg.err == nil:
			s.moved, s.left, s.err = append(s.moved, f.Name), s.left[1:], nil
			s.nextAccount()
		case errors.Is(msg.err, keys.ErrWrongPassword):
			s.step, s.err = migAccount, errors.New("not that password: again, or Enter with nothing to keep it for later")
		default:
			s.step, s.err = migAccount, msg.err
		}
		return nil
	case tea.KeyMsg:
		if msg.String() == "esc" && s.step != migWorking {
			return s.finish(m)
		}
	}
	switch s.step {
	case migPassword:
		cmd, done := s.form.update(msg)
		if !done {
			return cmd
		}
		pw := []byte(s.form.inputs[0].Value())
		if s.choose {
			again := []byte(s.form.inputs[1].Value())
			ok := len(pw) > 0 && bytes.Equal(pw, again)
			keys.Zero(again)
			if !ok {
				keys.Zero(pw)
				s.err = errors.New("the passwords are empty or differ")
				return nil
			}
		} else if len(pw) == 0 {
			s.err = errors.New("type the wallet password")
			return nil
		} else if err := m.pwThrottle(); err != nil {
			keys.Zero(pw)
			s.err = err
			return nil
		}
		s.form.wipe()
		s.step, s.err = migWorking, nil
		w, choose, epoch := m.w, s.choose, m.epoch
		return func() tea.Msg {
			defer keys.Zero(pw)
			var ws *wallet.Session
			var err error
			if choose {
				ws, err = w.CreateWalletPassword(pw)
			} else {
				ws, err = w.OpenWallet(pw)
			}
			if err != nil {
				return migratedMsg{epoch: epoch, tried: !choose, err: err}
			}
			moved, left, err := w.MigrateWith(ws, pw)
			return migratedMsg{epoch: epoch, tried: !choose, session: ws, moved: moved, left: left, err: err}
		}
	case migAccount:
		cmd, done := s.form.update(msg)
		if !done {
			return cmd
		}
		pw := []byte(s.form.inputs[0].Value())
		s.form.wipe()
		f := s.left[0]
		if len(pw) == 0 { // kept for later
			s.kept, s.left, s.err = append(s.kept, f.Name), s.left[1:], nil
			s.nextAccount()
			return nil
		}
		sess := s.session
		if sess == nil {
			sess = m.session
		}
		s.step = migWorking
		w, epoch := m.w, m.epoch
		return func() tea.Msg {
			defer keys.Zero(pw)
			return migratedOneMsg{epoch: epoch, name: f.Name, err: w.MigrateAccount(sess, f, pw)}
		}
	case migDone:
		if _, ok := msg.(tea.KeyMsg); ok {
			return s.finish(m)
		}
	}
	return nil
}

func (s *migrateScreen) view(m *model) string {
	var b strings.Builder
	switch s.step {
	case migPassword:
		n := len(m.w.OldPasswordAccounts())
		if s.choose {
			fmt.Fprintf(&b, "The wallet now has one password for every account. Your %d accounts have a\n", n)
			b.WriteString("password each: choose the wallet password. It may be one of theirs: the accounts\n")
			b.WriteString("that have it move at once; each other account's password is asked once.\n\n")
		} else {
			fmt.Fprintf(&b, "%d accounts still have a password of their own. The wallet password moves those\n", n)
			b.WriteString("that have it; each other account's password is asked once.\n\n")
		}
		b.WriteString(s.form.view())
		if s.choose {
			if pw := s.form.inputs[0].Value(); pw != "" {
				if hint := wallet.PasswordHint([]byte(pw)); hint != "" {
					b.WriteString(stWarn.Render(hint) + "\n")
				}
			}
		}
		b.WriteString("\n" + keyHelp("Enter", "next", "Esc", "later (the accounts keep their passwords)") + "\n")
	case migWorking:
		b.WriteString(stDim.Render("Argon2id for each account: a second or two each…") + "\n")
	case migAccount:
		f := s.left[0]
		if len(s.moved) > 0 {
			b.WriteString(stOK.Render("✓ under the wallet password: "+strings.Join(s.moved, ", ")) + "\n\n")
		}
		fmt.Fprintf(&b, "%s (%s) has another password: type it once to move it under\n", printable(f.Name), keys.Short(f.Public))
		b.WriteString("the wallet password, or Enter with nothing to keep its old password for later.\n\n")
		b.WriteString(s.form.view())
		b.WriteString("\n" + keyHelp("Enter", "move it / keep it", "Esc", "stop here") + "\n")
	case migDone:
		b.WriteString(stOK.Render("✓ under the wallet password: "+strings.Join(s.moved, ", ")) + "\n")
		if len(s.kept) > 0 {
			b.WriteString(stWarn.Render("old password kept: "+strings.Join(s.kept, ", ")+" (M moves them later)") + "\n")
		}
		b.WriteString("\n" + stDim.Render("any key: the wallet is open, choose an account") + "\n")
	}
	if s.err != nil {
		b.WriteString("\n" + stBad.Render(oneLine(s.err.Error())) + "\n")
	}
	return stBox.Width(min(m.width-2, 82)).Render(strings.TrimRight(b.String(), "\n"))
}

// ---- change of the wallet password -----------------------------------------------------------------

type passwordScreen struct {
	form      *form
	abandon   bool // a change was interrupted: it is abandoned here (with the old password)
	abandoned bool
	busy      bool
	done      bool
	err       error
}

type passwordChangedMsg struct {
	epoch   int
	session *wallet.Session
	err     error
}

func newPasswordScreen() *passwordScreen {
	return &passwordScreen{form: newForm("Current", "New", "Repeat").secret(0).secret(1).secret(2)}
}

// newPasswordScreenFor is the change of password, or, when a change was interrupted, its
// abandon (with the old password: possible while no account is under the new key yet).
func (m *model) newPasswordScreenFor() *passwordScreen {
	if m.w.ChangeInterrupted() {
		return &passwordScreen{abandon: true, form: newForm("Old pw").secret(0)}
	}
	return newPasswordScreen()
}

func (s *passwordScreen) close(*model) { s.form.wipe() }

func (s *passwordScreen) title() string { return "Change the wallet password" }

func (s *passwordScreen) update(m *model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case passwordChangedMsg:
		s.busy = false
		m.pwTried(msg.err)
		if msg.err != nil {
			s.err = wrongPassword(msg.err)
			return nil
		}
		if msg.session == nil { // abandoned: the wallet as it was
			s.done, s.abandoned, s.err = true, true, nil
			return nil
		}
		// the wallet's key changed: the session goes on with the new one
		if m.session != nil {
			m.session.Lock()
		}
		m.session, s.done, s.err = msg.session, true, nil
		for _, w := range msg.session.Warnings {
			m.addFlash(w)
		}
		return nil
	case tea.KeyMsg:
		if s.busy {
			return nil
		}
		if s.done || msg.String() == "esc" {
			s.form.wipe()
			return m.back()
		}
	}
	cmd, done := s.form.update(msg)
	if !done {
		return cmd
	}
	if err := m.pwThrottle(); err != nil {
		s.err = err
		return nil
	}
	if s.abandon {
		old := []byte(s.form.inputs[0].Value())
		s.form.wipe()
		s.busy, s.err = true, nil
		w, epoch := m.w, m.epoch
		return func() tea.Msg {
			defer keys.Zero(old)
			return passwordChangedMsg{epoch: epoch, err: w.AbandonPasswordChange(old)}
		}
	}
	old, pw, again := []byte(s.form.inputs[0].Value()), []byte(s.form.inputs[1].Value()), []byte(s.form.inputs[2].Value())
	s.form.wipe()
	s.form.setFocus(0)
	ok := len(pw) > 0 && bytes.Equal(pw, again)
	keys.Zero(again)
	if !ok {
		keys.Zero(old)
		keys.Zero(pw)
		s.err = errors.New("the new passwords are empty or differ")
		return nil
	}
	s.busy, s.err = true, nil
	w, epoch := m.w, m.epoch
	return func() tea.Msg {
		defer keys.Zero(old)
		defer keys.Zero(pw)
		if err := w.ChangeWalletPassword(old, pw); err != nil {
			return passwordChangedMsg{epoch: epoch, err: err}
		}
		ws, err := w.OpenWallet(pw)
		return passwordChangedMsg{epoch: epoch, session: ws, err: err}
	}
}

func (s *passwordScreen) view(m *model) string {
	var b strings.Builder
	switch {
	case s.abandoned:
		b.WriteString(stOK.Render("✓ the interrupted change is abandoned: the wallet password is the old one") + "\n")
		b.WriteString(stDim.Render("any key: back") + "\n")
	case s.abandon && !s.busy:
		b.WriteString("A change of the wallet password was interrupted. Type the OLD password to abandon it\n")
		b.WriteString("(possible while no account is under the new password yet), or lock (l) and open\n")
		b.WriteString("the wallet with the NEW password, which finishes it.\n\n")
		b.WriteString(s.form.view())
		b.WriteString("\n" + keyHelp("Enter", "abandon the change", "Esc", "cancel") + "\n")
	case s.done:
		b.WriteString(stOK.Render("✓ wallet password changed: every account is under a new wallet key") + "\n")
		b.WriteString(stWarn.Render(wallet.BackupOldPasswordNote) + "\n")
		b.WriteString(stDim.Render("Make a new backup (b)."+" Any key: back") + "\n")
	case s.busy:
		b.WriteString(stDim.Render("re-encrypting every account…") + "\n")
	default:
		b.WriteString("Every account is re-encrypted under a new wallet key. If the wallet is stopped\n")
		b.WriteString("half-way, opening it with the new password finishes the change.\n\n")
		b.WriteString(s.form.view())
		if pw := s.form.inputs[1].Value(); pw != "" {
			if hint := wallet.PasswordHint([]byte(pw)); hint != "" {
				b.WriteString(stWarn.Render(hint) + "\n")
			}
		}
		b.WriteString("\n" + keyHelp("Enter", "next / change", "Esc", "cancel") + "\n")
	}
	if s.err != nil {
		b.WriteString("\n" + stBad.Render(oneLine(s.err.Error())) + "\n")
	}
	return stBox.Width(min(m.width-2, 82)).Render(strings.TrimRight(b.String(), "\n"))
}

// ---- the delay after a wrong wallet password ----------------------------------------------------------

// Every screen that checks the wallet password (the unlock screen, the migration, the backup's
// export and import, the change of password, a new account while the wallet is closed) shares
// the unlock screen's counter of wrong passwords and its delay before the next try (unlockDelay:
// 2 s, doubling, at most 30 s): no screen is a faster way to guess the password.

// pwThrottle refuses a try of the wallet password while the delay runs.
func (m *model) pwThrottle() error {
	if wait := m.lock.retryAt.Sub(m.now()); wait > 0 {
		return fmt.Errorf("wait %d s before the next try", int(wait.Seconds()+0.99))
	}
	return nil
}

// pwTried records the result of a try of the wallet password: a wrong one adds to the counter
// and starts the delay, a right one resets the counter; other errors leave it as it is.
func (m *model) pwTried(err error) {
	switch {
	case err == nil:
		m.lock.failed = 0
	case wallet.IsWrongPassword(err):
		m.lock.failed++
		m.lock.retryAt = m.now().Add(unlockDelay(m.lock.failed))
	}
}

// wrongPassword is what a wrong wallet password says on every screen (as on the unlock screen).
func wrongPassword(err error) error {
	if wallet.IsWrongPassword(err) {
		return wallet.ErrUnlock
	}
	return err
}

// stale drops the end of work started before the wallet was locked (epoch): a wallet it opened
// is zeroed, never installed; a wrong wallet password it found still counts (pwTried).
func stale(m *model, msg tea.Msg) bool {
	var epoch int
	var carried []*wallet.Session
	switch msg := msg.(type) {
	case passwordChangedMsg:
		epoch, carried = msg.epoch, []*wallet.Session{msg.session}
		if epoch != m.epoch {
			m.pwTried(msg.err)
		}
	case migratedMsg:
		epoch, carried = msg.epoch, []*wallet.Session{msg.session}
		if epoch != m.epoch && msg.tried {
			m.pwTried(msg.err)
		}
	case migratedOneMsg:
		epoch = msg.epoch
	case backupDstMsg:
		epoch, carried = msg.epoch, []*wallet.Session{msg.dst}
		if epoch != m.epoch {
			m.pwTried(msg.err)
		}
	case backupOpenedMsg:
		epoch, carried = msg.epoch, []*wallet.Session{msg.src, msg.dst}
		if epoch != m.epoch {
			if msg.dstTried {
				m.pwTried(msg.dstErr)
			}
			msg.b.Wipe()
			wallet.WipePlans(msg.plans)
			keys.Zero(msg.pw)
		}
	default:
		return false
	}
	if epoch == m.epoch {
		return false
	}
	for _, c := range carried {
		if c != m.session {
			c.Lock()
		}
	}
	return true
}
