package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/echo"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

// foundKeyScreen: the wallet started in a folder that holds a node's key (NodePrivate.txt) and has
// no node's key configured (spec 4.4). The key is never used by itself (it could have been planted
// there): the screen says so, shows its address and full path, and offers to use it for this
// session, to use it and remember it (node_key in the active profile of the configuration file),
// or to ignore it (and not be asked again for this file). A key that fails the ownership and mode
// checks can only be ignored.
type foundKeyScreen struct {
	found *wallet.FoundNodeKey
	err   error
}

func newFoundKeyScreen(f *wallet.FoundNodeKey) *foundKeyScreen { return &foundKeyScreen{found: f} }

func (s *foundKeyScreen) title() string { return "A node's key in this folder" }

// configPath is where "remember" writes: the configuration file in use, or the data folder's.
func (m *model) configPath() string {
	if m.w.Config.Path != "" {
		return m.w.Config.Path
	}
	return config.DefaultPath(m.w.DataDir)
}

func (s *foundKeyScreen) update(m *model, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	usable := s.found.Refused == nil
	switch strings.ToLower(k.String()) {
	case "u":
		if usable {
			return s.use(m, "found in the current folder, chosen for this session")
		}
	case "r":
		if usable {
			path := m.configPath()
			if err := config.RememberNodeKey(path, m.w.Profile, s.found.Path); err != nil {
				s.err = err
				return nil
			}
			return s.use(m, "node_key in "+path+", chosen at start")
		}
	case "i", "esc":
		m.screen = nil
		return nil
	case "x":
		if err := wallet.IgnoreNodeKey(m.w.DataDir, s.found.Path); err != nil {
			s.err = err
			return nil
		}
		m.screen = nil
	}
	return nil
}

// use makes the found key the node's key account and goes to the unlock screen on it.
func (s *foundKeyScreen) use(m *model, source string) tea.Cmd {
	m.w.UseFoundNodeKey(s.found, source)
	m.selectName = m.w.NodeAccountName()
	m.screen = nil
	return m.loadAccounts()
}

func (s *foundKeyScreen) view(m *model) string {
	f := s.found
	var b strings.Builder
	b.WriteString(stWarn.Render("This folder holds a node's key. It is used only if you choose it:") + "\n")
	b.WriteString(stWarn.Render("a key someone else put here would make their address yours.") + "\n\n")
	b.WriteString("File     " + echo.Terminal(f.Path) + "\n")
	if f.Refused != nil {
		b.WriteString("\n" + stBad.Width(min(m.width-6, 74)).Render("Refused: "+oneLine(f.Refused.Error())) + "\n")
		b.WriteString("\n" + keyHelp("i", "ignore", "x", "ignore, never ask for this file") + "\n")
	} else {
		b.WriteString("Address  " + highlightAddress(f.Address) + "\n")
		if f.Encrypted {
			b.WriteString(stWarn.Render("         "+wallet.UnverifiedAddressNote) + "\n")
		}
		if f.Encrypted {
			b.WriteString(stDim.Render("encrypted by the node: its password is asked to unlock it") + "\n")
		} else {
			b.WriteString(stDim.Render("in clear, as the node needs it: no password") + "\n")
		}
		if f.Warning != "" {
			b.WriteString(stWarn.Width(min(m.width-6, 74)).Render("warning: "+oneLine(f.Warning)) + "\n")
		}
		b.WriteString("\nCheck that this is your node's address before using it.\n\n")
		b.WriteString(keyHelp("u", "use it for this session") + "\n")
		b.WriteString(keyHelp("r", "use it and remember it: node_key in the profile "+m.w.Profile.Name+" of") + "\n")
		b.WriteString("    " + stDim.Render(truncate(echo.Terminal(m.configPath()), min(m.width-8, 72))) + "\n")
		b.WriteString(keyHelp("i", "ignore it", "x", "ignore, never ask for this file") + "\n")
	}
	if s.err != nil {
		b.WriteString("\n" + stBad.Render(oneLine(s.err.Error())) + "\n")
	}
	return stBox.Width(min(m.width-2, 78)).Render(strings.TrimRight(b.String(), "\n"))
}
