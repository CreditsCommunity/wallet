package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

// tokensScreen lists every token the account holds (the node's token cache, or found from the
// history), with a detail of the selected one: code, name, decimals, balance, the full contract
// address (which alone identifies a token) and the look-alike warnings. It sends tokens, adds a
// token by address to the account's list, and copies the selected address.
type tokensScreen struct {
	account *keys.File
	list    *wallet.TokenList
	loaded  bool
	sel     int
	err     error
	note    string // the outcome of the last action (copy, add)
	adding  *form  // the address of a token to add
}

type tokensMsg struct {
	list *wallet.TokenList
	err  error
}

type tokenAddedMsg struct {
	h   wallet.TokenHolding
	err error
}

// copyTo is where the copy sequence goes (the terminal; a buffer in tests).
var copyTo io.Writer = os.Stderr

func newTokensScreen(f *keys.File) *tokensScreen { return &tokensScreen{account: f} }

func (s *tokensScreen) title() string { return "Tokens  " + s.account.Name }

func (s *tokensScreen) load(m *model) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*callTimeout)
		defer cancel()
		pub, err := s.account.PublicKey()
		if err != nil {
			return tokensMsg{err: err}
		}
		list, err := m.w.Tokens(ctx, pub)
		return tokensMsg{list, err}
	}
}

func (s *tokensScreen) holdings() []wallet.TokenHolding {
	if s.list == nil {
		return nil
	}
	return s.list.Holdings
}

func (s *tokensScreen) selected() *wallet.TokenHolding {
	if h := s.holdings(); s.sel < len(h) {
		return &h[s.sel]
	}
	return nil
}

func (s *tokensScreen) update(m *model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tokensMsg:
		s.list, s.err, s.loaded = msg.list, msg.err, true
		s.sel = min(s.sel, max(len(s.holdings())-1, 0))
		return nil
	case tokenAddedMsg:
		if msg.err != nil {
			s.note = stBad.Render(oneLine(msg.err.Error()))
			return nil
		}
		s.note = stOK.Render("✓ added " + keys.Address(msg.h.Token))
		return s.load(m)
	}
	if s.adding != nil {
		if k, ok := msg.(tea.KeyMsg); ok && k.String() == "esc" {
			s.adding = nil
			return nil
		}
		cmd, done := s.adding.update(msg)
		if !done {
			return cmd
		}
		text := s.adding.value(0)
		s.adding = nil
		s.note = stDim.Render("checking the token…")
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
			defer cancel()
			pub, err := s.account.PublicKey()
			if err != nil {
				return tokenAddedMsg{err: err}
			}
			h, err := m.w.AddToken(ctx, pub, text)
			return tokenAddedMsg{h, err}
		}
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	s.note = ""
	switch k.String() {
	case "esc", "q":
		return m.back()
	case "up", "k":
		s.sel = max(s.sel-1, 0)
	case "down", "j":
		s.sel = min(s.sel+1, max(len(s.holdings())-1, 0))
	case "s", "enter":
		if h := s.selected(); h != nil {
			m.screen = newTokenSendScreen(s.account, keys.Address(h.Token))
		} else {
			m.screen = newTokenSendScreen(s.account, "")
		}
	case "a":
		s.adding = newForm("Token").address(0)
		s.adding.inputs[0].Placeholder = "the token's contract address"
	case "y":
		if h := s.selected(); h != nil {
			copyAddress(keys.Address(h.Token))
			s.note = stOK.Render("address sent to the clipboard (terminals with OSC 52); or select it with Shift+drag")
		}
	case "x":
		if h := s.selected(); h != nil {
			addr := keys.Address(h.Token)
			pub, err := s.account.PublicKey()
			if err == nil {
				var removed bool
				if removed, err = m.w.RemoveToken(pub, addr); err == nil && !removed {
					s.note = stDim.Render("not in your list (it comes from the node or the history): nothing to remove")
					return nil
				}
			}
			if err != nil {
				s.note = stBad.Render(oneLine(err.Error()))
				return nil
			}
			s.note = stOK.Render("✓ removed from your list: " + addr)
			return s.load(m)
		}
	case "r":
		return s.load(m)
	}
	return nil
}

// copyOwnAddress copies an account's own address and returns the note to show.
func copyOwnAddress(addr string) string {
	if _, err := keys.ParseAddress(addr); err != nil {
		return "not an address: nothing copied"
	}
	copyAddress(addr)
	return "address copied (terminals with OSC 52); or select it with Shift+drag"
}

// copyAddress asks the terminal to put text on the clipboard (OSC 52; through tmux or screen
// when the wallet runs in one).
func copyAddress(text string) {
	seq := osc52.New(text)
	switch {
	case os.Getenv("TMUX") != "":
		seq = seq.Tmux()
	case strings.HasPrefix(os.Getenv("TERM"), "screen"):
		seq = seq.Screen()
	}
	seq.WriteTo(copyTo)
}

func (s *tokensScreen) view(m *model) string {
	var b strings.Builder
	b.WriteString(s.account.Public + "\n")
	l := s.list
	switch {
	case !s.loaded:
		b.WriteString("\n" + stDim.Render("loading… (without a token cache the wallet reads the account's history)") + "\n")
	case s.err != nil:
		b.WriteString("\n" + stBad.Render(oneLine(s.err.Error())) + "\n")
	default:
		b.WriteString(stDim.Render(truncate("from "+l.Describe, m.width)) + "\n")
		if l.Note != "" {
			b.WriteString(stWarn.Render(truncate(l.Note+"; tokens_cache=true on the node lists them all", m.width)) + "\n")
		}
		if l.LocalErr != nil {
			b.WriteString(stWarn.Render(truncate("local token list: "+oneLine(l.LocalErr.Error()), m.width)) + "\n")
		}
	}
	hs := s.holdings()
	if s.loaded && s.err == nil && len(hs) == 0 {
		b.WriteString("\n" + stDim.Render("no tokens found") + "\n")
		if l != nil && l.Zero > 0 {
			b.WriteString(stDim.Render(fmt.Sprintf("(%d with a balance of 0 not shown)", l.Zero)) + "\n")
		}
		b.WriteString(stDim.Render("[a] adds a token by its contract address.") + "\n")
	}
	// the list: as many rows as the detail below leaves room for
	rows := max(m.height-17, 3)
	first := max(0, min(s.sel-rows+1, len(hs)-rows))
	if len(hs) > 0 {
		b.WriteString("\n")
	}
	for i := first; i < len(hs) && i < first+rows; i++ {
		h := hs[i]
		bal := printable(h.Balance)
		if h.Err != nil {
			bal = "?"
		}
		mark := " "
		if len(h.Warnings) > 0 || h.Origin != "" {
			mark = "!"
		}
		line := truncate(fmt.Sprintf("%s %-8s %24s  %s", mark, printable(h.Code), bal, printable(h.Name)), m.width-4)
		if i == s.sel {
			b.WriteString(stSel.Render("> "+line) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	if len(hs) > rows {
		b.WriteString(stDim.Render(fmt.Sprintf("  %d of %d tokens, ↑/↓ to scroll", rows, len(hs))) + "\n")
	}
	if h := s.selected(); h != nil {
		b.WriteString("\n" + tokenDetail(h, m.width))
	}
	if s.adding != nil {
		b.WriteString("\n" + s.adding.view() + keyHelp("Enter", "add", "Esc", "cancel") + "\n")
	} else {
		if s.note != "" {
			b.WriteString("\n" + truncate(s.note, m.width) + "\n")
		}
		b.WriteString("\n" + keyHelp("s", "send", "a", "add", "x", "remove", "y", "copy address", "r", "refresh", "Esc", "back"))
	}
	return b.String()
}

// tokenDetail shows the selected token; the contract address stands alone on its line, whole,
// so that it can be selected and copied.
func tokenDetail(h *wallet.TokenHolding, width int) string {
	var b strings.Builder
	dec := "?"
	if h.Decimals >= 0 {
		dec = fmt.Sprint(h.Decimals)
	}
	b.WriteString(stTitle.Render("Contract") + "  " + keys.Address(h.Token) + "\n")
	b.WriteString(truncate(fmt.Sprintf("Code      %s   Name  %s   (what the contract says of itself)", printable(h.Code), printable(h.Name)), width) + "\n")
	if h.Err != nil {
		b.WriteString(stBad.Render(truncate("Balance   not read: "+oneLine(h.Err.Error()), width)) + "\n")
	} else {
		b.WriteString(truncate(fmt.Sprintf("Balance   %s   Decimals  %s", printable(h.Balance), dec), width) + "\n")
	}
	if h.Origin != "" {
		b.WriteString(stWarn.Render(truncate("! "+printable(h.Origin), width)) + "\n")
	}
	for i, w := range h.Warnings {
		if i == 2 {
			b.WriteString(stWarn.Render(fmt.Sprintf("! and %d more warnings", len(h.Warnings)-2)) + "\n")
			break
		}
		b.WriteString(stWarn.Render(truncate("! "+printable(w), width)) + "\n")
	}
	return b.String()
}
