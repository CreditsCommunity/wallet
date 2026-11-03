package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/CreditsCommunity/wallet/internal/amount"
	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/history"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

// Every screen fits 80x24 (spec 6) and shows the network.
func TestScreensFit80x24(t *testing.T) {
	for _, network := range []config.Network{config.Mainnet, config.Testnet} {
		cfg := config.Default()
		w := wallet.New(cfg, config.Profile{Name: "local", Address: "127.0.0.1:1", Network: network}, t.TempDir())
		m := newModel(w)
		m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		f := &keys.File{Name: "main", Public: "u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w"}
		m.Update(accountsMsg{files: []*keys.File{f, {Name: "savings", Public: f.Public}}})
		m.Update(statusMsg{st: node.Status{LastBlock: 187210331, Round: 187210332, Synced: true, HeadTime: time.Now()}, oldest: 186699000})
		bal, _ := amount.Parse("12345.678")
		amt, _ := amount.Parse("12.5")
		e := history.Entry{Time: time.Now(), Block: 187210331, Kind: "transfer", Direction: "out", Counterparty: f.Public, Amount: amt.Neg()}
		m.Update(accountDataMsg{name: "main", bal: node.Balance{CS: bal}, entries: []history.Entry{e, e, e}, total: 3})

		screens := map[string]screen{"main": nil, "send": newSendScreen(f), "new": newAccountScreen(false),
			"import": newAccountScreen(true), "history": newHistoryScreen(f, 20), "status": &statusScreen{}, "help": &helpScreen{}}
		for name, s := range screens {
			m.screen = s
			view := m.View()
			if testing.Verbose() && network == config.Mainnet {
				t.Logf("%s:\n%s", name, view)
			}
			lines := strings.Split(view, "\n")
			if len(lines) > 24 {
				t.Errorf("%s %s: %d lines", network, name, len(lines))
			}
			for i, l := range lines {
				if w := lipgloss.Width(l); w > 80 {
					t.Errorf("%s %s line %d: %d columns: %q", network, name, i, w, l)
				}
			}
			if !strings.Contains(lines[0], network.Label()) {
				t.Errorf("%s %s: the network is not on the first line: %q", network, name, lines[0])
			}
			if name == "main" && !strings.Contains(view, "12,345.678 CS") {
				t.Errorf("main screen without the balance:\n%s", view)
			}
		}
	}
}
