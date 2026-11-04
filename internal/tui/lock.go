package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CreditsCommunity/wallet/internal/echo"
	"github.com/CreditsCommunity/wallet/internal/history"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

// The unlock screen (spec 6, "Unlock"): the interface opens on it, and returns to it on a manual
// lock, a switch of account, the inactivity timer and after an account is created or imported.
// It shows the accounts' names and addresses only; the chosen account's password opens it for the
// session (a plain node's key needs none). Nothing of an account (balance, history, tokens,
// contracts) is loaded or shown before it is unlocked.

type lockStep int

const (
	lockList lockStep = iota
	lockPassword
	lockOpening
)

type lockScreen struct {
	step    lockStep
	pw      *form
	note    string // why the wallet is locked, or the last failure
	failed  int    // wrong passwords in a row
	retryAt time.Time
}

type unlockedMsg struct {
	u   *wallet.Unlocked
	err error
}
type retryMsg struct{}

// unlockDelay is the wait after the n-th wrong password in a row: 2 s, doubling, at most 30 s.
// There is no limit on tries; Argon2id already costs a second or more each.
func unlockDelay(n int) time.Duration {
	if n <= 0 {
		return 0
	}
	d := 2 * time.Second << min(n-1, 4)
	return min(d, 30*time.Second)
}

func (m *model) lockedNow() bool { return m.unlocked == nil }

// lockSession zeroes the unlocked key and forgets everything shown of the account; the interface
// goes back to the unlock screen, on the account named selectName ("": the same one).
func (m *model) lockSession(why, selectName string) {
	if m.unlocked != nil {
		if selectName == "" {
			selectName = m.unlocked.Account.Name
		}
		m.unlocked.Lock()
		m.unlocked = nil
	}
	m.balances, m.balErr = map[string]node.Balance{}, map[string]error{}
	m.recent, m.total = map[string][]history.Entry{}, map[string]int{}
	m.tokens, m.tokErr = map[string]*wallet.TokenList{}, map[string]error{}
	if selectName != "" {
		m.selectName = selectName
		m.selectByName()
	}
	m.screen = nil
	m.lock.reset(why)
}

func (m *model) selectByName() {
	for i, f := range m.accounts {
		if f.Name == m.selectName {
			m.sel = i
			m.selectName = ""
			return
		}
	}
}

func (s *lockScreen) reset(note string) {
	if s.pw != nil {
		s.pw.wipe()
	}
	s.step, s.pw, s.note = lockList, nil, note
}

func (m *model) updateLock(msg tea.Msg) tea.Cmd {
	s := m.lock
	switch msg := msg.(type) {
	case unlockedMsg:
		if s.step != lockOpening {
			msg.u.Lock()
			return nil
		}
		if msg.err != nil {
			s.step = lockPassword
			if s.pw == nil { // a plain node's key: no password was asked
				s.step = lockList
			}
			if !wallet.IsWrongPassword(msg.err) {
				// not the password (the file's checks, the memory for a node's key): said as it is
				s.note = oneLine(msg.err.Error())
				return nil
			}
			s.failed++
			s.retryAt = m.now().Add(unlockDelay(s.failed))
			s.note = wallet.ErrUnlock.Error()
			return tea.Tick(unlockDelay(s.failed), func(time.Time) tea.Msg { return retryMsg{} })
		}
		s.failed, s.note = 0, ""
		s.reset("")
		m.unlocked = msg.u
		m.lastInput = m.now()
		if w := msg.u.Warning(); w != "" && !m.memWarned {
			m.memWarned = true // once
			m.flash = strings.TrimPrefix(m.flash+"; "+w, "; ")
		}
		return m.refreshCurrent()
	case retryMsg:
		return nil
	case tea.MouseMsg:
		if s.step == lockList && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if i := msg.Y - 3; i >= 0 && i < len(m.accounts) { // the header, the title and a blank line above the list
				m.sel = i
			}
		}
		return nil
	case tea.KeyMsg:
		switch s.step {
		case lockOpening:
			return nil
		case lockPassword:
			if msg.String() == "esc" {
				s.reset("")
				return nil
			}
			cmd, done := s.pw.update(msg)
			if !done {
				return cmd
			}
			if wait := s.retryAt.Sub(m.now()); wait > 0 {
				s.note = fmt.Sprintf("wait %d s before the next try", int(wait.Seconds()+0.99))
				return nil
			}
			pw := []byte(s.pw.inputs[0].Value())
			s.pw.wipe()
			if len(pw) == 0 {
				s.note = "type the password"
				return nil
			}
			return s.open(m, pw)
		}
		switch msg.String() {
		case "q", "esc":
			return tea.Quit
		case "up", "k":
			if m.sel > 0 {
				m.sel--
			}
		case "down", "j":
			if m.sel < len(m.accounts)-1 {
				m.sel++
			}
		case "enter":
			f := m.selected()
			if f == nil {
				return nil
			}
			s.note = ""
			if !wallet.NeedsPassword(f) {
				return s.open(m, nil)
			}
			s.pw = newForm("Password").secret(0)
			s.step = lockPassword
		case "n":
			m.screen = newAccountScreen(false)
		case "i":
			m.screen = newAccountScreen(true)
		case "g":
			if m.w.NodeKeyMissing() {
				m.screen = newNodeKeyScreen(m.w.NodeKey.Dir)
			}
		case "S":
			m.screen = &statusScreen{}
			return m.loadStatus(true)
		case "?":
			m.screen = &helpScreen{}
		case "r":
			return m.loadAccounts()
		}
	}
	return nil
}

// open unlocks the selected account in the background (Argon2id takes a second, a node's
// encrypted key some more). The password is zeroed afterwards.
func (s *lockScreen) open(m *model, pw []byte) tea.Cmd {
	f := m.selected()
	if f == nil {
		keys.Zero(pw)
		return nil
	}
	s.step = lockOpening
	w := m.w
	return func() tea.Msg {
		defer keys.Zero(pw)
		u, err := w.Unlock(f, pw)
		return unlockedMsg{u, err}
	}
}

// selected is the account chosen on the unlock screen.
func (m *model) selected() *keys.File {
	if m.sel >= 0 && m.sel < len(m.accounts) {
		return m.accounts[m.sel]
	}
	return nil
}

func (m *model) lockView() string {
	s := m.lock
	var b strings.Builder
	b.WriteString(stTitle.Render("Unlock an account") + "  " + stDim.Render("nothing of an account is shown before its password") + "\n\n")
	if m.accErr != nil {
		b.WriteString(stBad.Render(truncate(oneLine(m.accErr.Error()), m.width-2)) + "\n")
	}
	if len(m.accounts) == 0 {
		b.WriteString("Welcome. Create an account with " + stKey.Render("[n]") + " or import one with " + stKey.Render("[i]") + ".\n")
		b.WriteString(stDim.Render(truncate("Keys are stored encrypted in "+echo.Terminal(m.w.Store.Dir), m.width-2)) + "\n")
	}
	// the list: name and address, nothing else
	rows := max(m.height-14, 3)
	first := 0
	if m.sel >= rows {
		first = m.sel - rows + 1
	}
	for i := first; i < len(m.accounts) && i < first+rows; i++ {
		f := m.accounts[i]
		name := f.Name
		if len(name) > 18 {
			name = name[:17] + "…"
		}
		tag := ""
		switch {
		case f.NodeKeyEncrypted:
			tag = " " + stWarn.Render("node key, enc")
		case f.IsNodeKey():
			tag = " " + stWarn.Render("node key")
		}
		line := fmt.Sprintf("%-18s %s", name, f.Public)
		if i == m.sel {
			b.WriteString(stSel.Render("> "+line) + tag + "\n")
		} else {
			b.WriteString("  " + line + tag + "\n")
		}
	}
	if len(m.accounts) > rows {
		b.WriteString(stDim.Render(fmt.Sprintf("%d accounts: ↑/↓ to see them all", len(m.accounts))) + "\n")
	}
	b.WriteString("\n")
	f := m.selected()
	switch s.step {
	case lockList:
		if f != nil && f.IsNodeKey() && !f.NodeKeyEncrypted {
			b.WriteString(stWarn.Render(truncate("NODE KEY in clear in "+echo.Terminal(f.NodeKey)+": no password", m.width-2)) + "\n")
		}
		if m.w.NodeKeyMissing() {
			b.WriteString(keyHelp("g", "make the node's key from 24 words:") + stWarn.Render(truncate(" the node has none yet in "+echo.Terminal(m.w.NodeKey.Dir), m.width-40)) + "\n")
		}
		b.WriteString(keyHelp("Enter", "unlock", "n", "new", "i", "import", "S", "node", "?", "help", "q", "quit") + "\n")
	case lockPassword, lockOpening:
		what := "Password of " + f.Name
		if f.NodeKeyEncrypted {
			what = "The node's password for its key (" + echo.Terminal(f.NodeKey) + ")"
		}
		if s.pw != nil {
			b.WriteString(truncate(what, m.width-2) + "\n")
			b.WriteString(s.pw.view())
		}
		if s.step == lockOpening {
			if f.NodeKeyEncrypted {
				b.WriteString(stDim.Render("opening the node's key: Argon2id with 1 GiB, as the node does (some seconds)…") + "\n")
			} else {
				b.WriteString(stDim.Render("opening…") + "\n")
			}
		} else {
			b.WriteString(keyHelp("Enter", "unlock", "Esc", "back") + "\n")
		}
	}
	if s.note != "" {
		b.WriteString(stBad.Render(truncate(s.note, 2*m.width)) + "\n")
	}
	return b.String()
}
