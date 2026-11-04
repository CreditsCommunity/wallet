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
	session *wallet.Session
	moved   []string
	left    []*keys.File
	err     error
}

type migratedOneMsg struct {
	name string
	err  error
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
		if msg.err != nil {
			s.step, s.err = migPassword, msg.err
			return nil
		}
		s.session, s.moved, s.left, s.err = msg.session, msg.moved, msg.left, nil
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
		}
		s.form.wipe()
		s.step, s.err = migWorking, nil
		w, choose := m.w, s.choose
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
				return migratedMsg{err: err}
			}
			moved, left, err := w.MigrateWith(ws, pw)
			return migratedMsg{session: ws, moved: moved, left: left, err: err}
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
		w := m.w
		return func() tea.Msg {
			defer keys.Zero(pw)
			return migratedOneMsg{name: f.Name, err: w.MigrateAccount(sess, f, pw)}
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
	form *form
	busy bool
	done bool
	err  error
}

type passwordChangedMsg struct {
	session *wallet.Session
	err     error
}

func newPasswordScreen() *passwordScreen {
	return &passwordScreen{form: newForm("Current", "New", "Repeat").secret(0).secret(1).secret(2)}
}

func (s *passwordScreen) title() string { return "Change the wallet password" }

func (s *passwordScreen) update(m *model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case passwordChangedMsg:
		s.busy = false
		if msg.err != nil {
			s.err = msg.err
			return nil
		}
		// the wallet's key changed: the session goes on with the new one
		if m.session != nil {
			m.session.Lock()
		}
		m.session, s.done, s.err = msg.session, true, nil
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
	w := m.w
	return func() tea.Msg {
		defer keys.Zero(old)
		defer keys.Zero(pw)
		if err := w.ChangeWalletPassword(old, pw); err != nil {
			return passwordChangedMsg{err: err}
		}
		ws, err := w.OpenWallet(pw)
		return passwordChangedMsg{session: ws, err: err}
	}
}

func (s *passwordScreen) view(m *model) string {
	var b strings.Builder
	switch {
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
