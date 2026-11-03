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
		pub, _ := keys.ParseAddress(f.Public)

		m.Update(accountDataMsg{name: "main", bal: node.Balance{CS: bal}, entries: []history.Entry{e, e, e}, total: 3,
			tokens: []node.TokenBalance{{Token: pub, Code: "CTT", Name: "Community Test Token", Balance: "998900.50"}}})
		other, _ := amount.Parse("1000")
		dels := &delegationsScreen{account: f, loaded: true, d: node.Delegations{OutTotal: other, InTotal: other,
			Out: []node.Delegation{{Wallet: pub, Amount: other}, {Wallet: pub, Amount: other, Until: time.Now().Add(time.Hour)}},
			In:  []node.Delegation{{Wallet: pub, Amount: other}}}}
		toks := &tokensScreen{account: f, loaded: true, list: []node.TokenBalance{{Token: pub, Code: "CTT\x1b[2J", Name: "x", Balance: "1"}}}
		noCache := &tokensScreen{account: f, loaded: true, err: node.ErrNoTokenCache}
		tokHist := newHistoryScreen(f, 20)
		tokHist.tokens = true
		tokHist.tokenList = []history.Entry{{Time: time.Now(), Block: 5, Kind: "token transfer", Direction: "in", Counterparty: f.Public, TokenAmount: "7", TokenCode: "CTT"}}
		screens := map[string]screen{"main": nil, "send": newSendScreen(f), "new": newAccountScreen(false),
			"import": newAccountScreen(true), "history": newHistoryScreen(f, 20), "status": &statusScreen{}, "help": &helpScreen{},
			"delegate": newDelegateScreen(f, false, ""), "withdraw": newDelegateScreen(f, true, f.Public),
			"token send": newTokenSendScreen(f, f.Public), "delegations": dels, "tokens": toks, "tokens without cache": noCache,
			"token history": tokHist}
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
			if strings.Contains(view, "\x1b[2J") {
				t.Errorf("%s: a terminal escape from a token name reached the screen", name)
			}
			if name == "main" && (!strings.Contains(view, "12,345.678 CS") || !strings.Contains(view, "CTT 998900.50")) {
				t.Errorf("main screen without the balance:\n%s", view)
			}
		}
	}
}

// The review of every kind of transaction fits 80x24 and escapes text chosen by others.
func TestReviewScreensFit80x24(t *testing.T) {
	w := wallet.New(config.Default(), config.Profile{Name: "local", Address: "127.0.0.1:1", Network: config.Mainnet}, t.TempDir())
	m := newModel(w)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	f := &keys.File{Name: "main", Public: "u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w"}
	pub, _ := keys.ParseAddress(f.Public)
	amt, _ := amount.Parse("1234.5")
	fee, _ := amount.Parse("0.00874")
	base := wallet.SendPlan{Account: "main", From: pub, To: pub, Amount: amt, Fee: fee, Balance: amt, InnerID: 7, Network: config.Mainnet}
	for _, p := range []wallet.SendPlan{
		func() wallet.SendPlan { p := base; p.Kind, p.Data = wallet.KindTransfer, "invoice\x1b[2J 42"; return p }(),
		func() wallet.SendPlan {
			p := base
			p.Kind, p.Until = wallet.KindDelegate, time.Now().Add(time.Hour)
			return p
		}(),
		func() wallet.SendPlan { p := base; p.Kind, p.Delegated = wallet.KindUndelegate, amt; return p }(),
		func() wallet.SendPlan {
			p := base
			other, _ := keys.ParseAddress("9vX2wH7ytrbbo1KtNa13dUz3nvtJqaCH4oGrr2NEsBqd")
			p.Kind, p.Token = wallet.KindToken, &wallet.TokenPlan{Contract: pub, Code: "CS\x1b]0;x\a", Name: "Credits", Decimals: 2, Amount: "250.5", Balance: "998900.50",
				Warnings: wallet.TokenWarnings(pub, "CS", "Credits", []node.TokenBalance{{Token: other, Code: "CS"}})}
			return p
		}(),
	} {
		s := newSendScreen(f)
		s.kind, s.plan, s.step = p.Kind, &p, sendReview
		s.confirm = newForm("Confirm", "Password").secret(1)
		m.screen = s
		view := m.View()
		lines := strings.Split(view, "\n")
		if len(lines) > 24 {
			t.Errorf("%s: %d lines", p.Kind, len(lines))
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w > 80 {
				t.Errorf("%s line %d: %d columns: %q", p.Kind, i, w, l)
			}
		}
		if p.Kind == wallet.KindToken && (!strings.Contains(view, "WARNING") || !strings.Contains(view, f.Public) || !strings.Contains(view, "calls itself")) {
			t.Errorf("token review without the full address, the self-declared name or the warnings:\n%s", view)
		}
		if strings.Contains(view, "\x1b[2J") || strings.Contains(view, "\x1b]0;") || !strings.Contains(view, "MAINNET") {
			t.Errorf("%s:\n%s", p.Kind, view)
		}
		if testing.Verbose() {
			t.Logf("%s:\n%s", p.Kind, view)
		}
	}
}
