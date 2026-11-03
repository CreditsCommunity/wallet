package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
)

// delegationsScreen lists the delegations the account made and received, and starts a new
// delegation or the withdrawal of the selected one.
type delegationsScreen struct {
	account *keys.File
	d       node.Delegations
	loaded  bool
	sel     int
	err     error
}

type delegationsMsg struct {
	d   node.Delegations
	err error
}

func newDelegationsScreen(f *keys.File) *delegationsScreen { return &delegationsScreen{account: f} }

func (s *delegationsScreen) title() string { return "Delegations  " + s.account.Name }

func (s *delegationsScreen) load(m *model) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		pub, err := s.account.PublicKey()
		if err != nil {
			return delegationsMsg{err: err}
		}
		d, err := m.w.Node.Delegations(ctx, pub)
		return delegationsMsg{d, err}
	}
}

func (s *delegationsScreen) update(m *model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case delegationsMsg:
		s.d, s.err, s.loaded = msg.d, msg.err, true
		s.sel = min(s.sel, max(len(s.d.Out)-1, 0))
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q":
			return m.back()
		case "up", "k":
			s.sel = max(s.sel-1, 0)
		case "down", "j":
			s.sel = min(s.sel+1, max(len(s.d.Out)-1, 0))
		case "n", "d":
			m.screen = newDelegateScreen(s.account, false, "")
		case "w":
			if s.sel >= len(s.d.Out) {
				s.err = errors.New("no delegation of this account to withdraw")
				return nil
			}
			x := s.d.Out[s.sel]
			if !x.Until.IsZero() {
				s.err = errors.New("a delegation with a time limit cannot be withdrawn: it ends by itself")
				return nil
			}
			m.screen = newDelegateScreen(s.account, true, keys.Address(x.Wallet))
		case "r":
			return s.load(m)
		}
	}
	return nil
}

func (s *delegationsScreen) view(m *model) string {
	var b strings.Builder
	b.WriteString(s.account.Public + "\n\n")
	if !s.loaded {
		b.WriteString(stDim.Render("loading…") + "\n")
	} else {
		fmt.Fprintf(&b, "%s  %s CS\n", stTitle.Render("Made"), s.d.OutTotal.Grouped())
		if len(s.d.Out) == 0 {
			b.WriteString(stDim.Render("  none") + "\n")
		}
		for i, x := range s.d.Out {
			line := fmt.Sprintf("→ %s  %s CS  %s", keys.Short(keys.Address(x.Wallet)), x.Amount.Grouped(), untilText(x.Until))
			if i == s.sel {
				b.WriteString(stSel.Render("> "+line) + "\n")
			} else {
				b.WriteString("  " + line + "\n")
			}
		}
		fmt.Fprintf(&b, "\n%s  %s CS\n", stTitle.Render("Received"), s.d.InTotal.Grouped())
		if len(s.d.In) == 0 {
			b.WriteString(stDim.Render("  none") + "\n")
		}
		for i, x := range s.d.In {
			if i >= max(m.height-12-len(s.d.Out), 2) {
				fmt.Fprintf(&b, stDim.Render("  … %d more")+"\n", len(s.d.In)-i)
				break
			}
			fmt.Fprintf(&b, "  ← %s  %s CS  %s\n", keys.Short(keys.Address(x.Wallet)), x.Amount.Grouped(), untilText(x.Until))
		}
	}
	if s.err != nil {
		b.WriteString("\n" + stBad.Render(oneLine(s.err.Error())) + "\n")
	}
	b.WriteString("\n" + keyHelp("n", "delegate", "w", "withdraw selected", "↑/↓", "select", "r", "refresh", "Esc", "back"))
	return b.String()
}
