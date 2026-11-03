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

// tokensScreen lists the account's token balances (the node's token cache) and sends tokens.
type tokensScreen struct {
	account *keys.File
	list    []node.TokenBalance
	loaded  bool
	sel     int
	err     error
}

type tokensMsg struct {
	list []node.TokenBalance
	err  error
}

func newTokensScreen(f *keys.File) *tokensScreen { return &tokensScreen{account: f} }

func (s *tokensScreen) title() string { return "Tokens  " + s.account.Name }

func (s *tokensScreen) load(m *model) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		pub, err := s.account.PublicKey()
		if err != nil {
			return tokensMsg{err: err}
		}
		list, err := m.w.Node.TokenBalances(ctx, pub)
		return tokensMsg{list, err}
	}
}

func (s *tokensScreen) update(m *model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tokensMsg:
		s.list, s.err, s.loaded = msg.list, msg.err, true
		s.sel = min(s.sel, max(len(s.list)-1, 0))
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q":
			return m.back()
		case "up", "k":
			s.sel = max(s.sel-1, 0)
		case "down", "j":
			s.sel = min(s.sel+1, max(len(s.list)-1, 0))
		case "s", "enter":
			if s.sel < len(s.list) {
				m.screen = newTokenSendScreen(s.account, keys.Address(s.list[s.sel].Token))
			} else {
				m.screen = newTokenSendScreen(s.account, "")
			}
		case "a":
			m.screen = newTokenSendScreen(s.account, "")
		case "r":
			return s.load(m)
		}
	}
	return nil
}

func (s *tokensScreen) view(m *model) string {
	var b strings.Builder
	b.WriteString(s.account.Public + "\n\n")
	switch {
	case !s.loaded:
		b.WriteString(stDim.Render("loading…") + "\n")
	case errors.Is(s.err, node.ErrNoTokenCache):
		b.WriteString(stWarn.Render("The node does not list token balances: it needs tokens_cache=true in the [api]") + "\n")
		b.WriteString(stWarn.Render("section of its config.ini (and a restart).") + "\n")
		b.WriteString(stDim.Render("You can still send a token you know: [a] and its contract address.") + "\n")
	case s.err != nil:
		b.WriteString(stBad.Render(oneLine(s.err.Error())) + "\n")
	case len(s.list) == 0:
		b.WriteString(stDim.Render("no tokens") + "\n")
	}
	for i, t := range s.list {
		if i >= max(m.height-8, 3) {
			break
		}
		line := fmt.Sprintf("%-8s %24s  %s  %s", printable(t.Code), printable(t.Balance), keys.Short(keys.Address(t.Token)), printable(t.Name))
		if i == s.sel {
			b.WriteString(stSel.Render("> "+line) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	b.WriteString("\n" + keyHelp("s", "send selected", "a", "send another token", "r", "refresh", "Esc", "back"))
	return b.String()
}
