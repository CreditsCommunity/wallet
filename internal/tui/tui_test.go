package tui

import (
	"encoding/base64"
	"fmt"
	"os"
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

		m.Update(accountDataMsg{name: "main", bal: node.Balance{CS: bal}, entries: []history.Entry{e, e, e}, total: 3})
		m.Update(accountTokensMsg{name: "main", list: &wallet.TokenList{Source: wallet.TokensFromCache, Describe: "the token cache of the local node",
			Holdings: []wallet.TokenHolding{{Token: pub, Code: "CTT", Name: "Community Test Token", Balance: "998900.50", Decimals: 2}}}})
		other, _ := amount.Parse("1000")
		dels := &delegationsScreen{account: f, loaded: true, d: node.Delegations{OutTotal: other, InTotal: other,
			Out: []node.Delegation{{Wallet: pub, Amount: other}, {Wallet: pub, Amount: other, Until: time.Now().Add(time.Hour)}},
			In:  []node.Delegation{{Wallet: pub, Amount: other}}}}
		toks := &tokensScreen{account: f, loaded: true, list: &wallet.TokenList{Describe: "the token cache of the local node 127.0.0.1:9090",
			Holdings: []wallet.TokenHolding{{Token: pub, Code: "CTT\x1b[2J", Name: "x", Balance: "1", Decimals: 2,
				Warnings: []string{"w1", "w2", "w3"}}}}}
		many := make([]wallet.TokenHolding, 30)
		for i := range many {
			many[i] = wallet.TokenHolding{Token: pub, Code: fmt.Sprintf("T%d", i), Name: strings.Repeat("long name ", 12), Balance: "123456789012345678901234567890.123456"}
		}
		noCache := &tokensScreen{account: f, loaded: true, sel: 29, list: &wallet.TokenList{Source: wallet.TokensFromHistory,
			Describe: "the account's history on local node 127.0.0.1:9090, and the tokens it sent or added", Note: wallet.HistoryNote, Zero: 2,
			Holdings: many}}
		adding := &tokensScreen{account: f, loaded: true, list: noCache.list, adding: newForm("Token")}
		tokHist := newHistoryScreen(f, 20)
		tokHist.tokens = true
		tokHist.tokenList = []history.Entry{{Time: time.Now(), Block: 5, Kind: "token transfer", Direction: "in", Counterparty: f.Public, TokenAmount: "7", TokenCode: "CTT"}}
		screens := map[string]screen{"main": nil, "send": newSendScreen(f), "new": newAccountScreen(false),
			"import": newAccountScreen(true), "history": newHistoryScreen(f, 20), "status": &statusScreen{}, "help": &helpScreen{},
			"delegate": newDelegateScreen(f, false, ""), "withdraw": newDelegateScreen(f, true, f.Public),
			"token send": newTokenSendScreen(f, f.Public), "delegations": dels, "tokens": toks, "tokens without cache": noCache, "token add": adding,
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
			if strings.HasPrefix(name, "tok") && name != "token history" && name != "token send" && !strings.Contains(view, f.Public) {
				t.Errorf("%s: the full contract address is not shown:\n%s", name, view)
			}
			if name == "main" && (!strings.Contains(view, "12,345.678 CS") || !strings.Contains(view, "CTT 998900.50") || !strings.Contains(view, "[t]")) {
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

// The token screen: the selected token's full address on a line of its own, the source of the
// list, the note of a list found from history, and the copy sequence (OSC 52) of the address.
func TestTokensScreenFullAddressAndCopy(t *testing.T) {
	w := wallet.New(config.Default(), config.Profile{Name: "local", Address: "127.0.0.1:1", Network: config.Mainnet}, t.TempDir())
	m := newModel(w)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	f := &keys.File{Name: "main", Public: "u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w"}
	tok1, _ := keys.ParseAddress("9vX2wH7ytrbbo1KtNa13dUz3nvtJqaCH4oGrr2NEsBqd")
	tok2, _ := keys.ParseAddress("FAe4sisG95oZ42w7buUn5qEE4TAnfTTFPiguZUHmhiF")
	s := newTokensScreen(f)
	m.screen = s
	m.Update(tokensMsg{list: &wallet.TokenList{Source: wallet.TokensFromHistory, Note: wallet.HistoryNote, Describe: "the account's history",
		Holdings: []wallet.TokenHolding{{Token: tok1, Code: "FTK", Name: "Fake Token", Balance: "1000.00", Decimals: 2},
			{Token: tok2, Code: "CS", Name: "Credits", Balance: "5", Decimals: 0, Warnings: []string{"the code looks like CS"}}}}})
	view := m.View()
	for _, want := range []string{"Contract  9vX2wH7ytrbbo1KtNa13dUz3nvtJqaCH4oGrr2NEsBqd", "Decimals  2", "Balance   1000.00",
		"found from history (may be incomplete on a pruned node)"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	view = m.View()
	if !strings.Contains(view, "Contract  FAe4sisG95oZ42w7buUn5qEE4TAnfTTFPiguZUHmhiF") || !strings.Contains(view, "the code looks like CS") {
		t.Errorf("the second token's detail:\n%s", view)
	}
	var buf strings.Builder
	copyTo = &buf
	defer func() { copyTo = os.Stderr }()
	t.Setenv("TMUX", "")
	t.Setenv("TERM", "xterm-256color")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("FAe4sisG95oZ42w7buUn5qEE4TAnfTTFPiguZUHmhiF")) + "\a"; buf.String() != want {
		t.Errorf("copy: %q, want %q", buf.String(), want)
	}
}

// x takes the selected token off the account's local list.
func TestTokensScreenRemove(t *testing.T) {
	w := wallet.New(config.Default(), config.Profile{Name: "local", Address: "127.0.0.1:1", Network: config.Mainnet}, t.TempDir())
	m := newModel(w)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	f := &keys.File{Name: "main", Public: "FAe4sisG95oZ42w7buUn5qEE4TAnfTTFPiguZUHmhiF"}
	tok, _ := keys.ParseAddress("9vX2wH7ytrbbo1KtNa13dUz3nvtJqaCH4oGrr2NEsBqd")
	s := newTokensScreen(f)
	m.screen = s
	m.Update(tokensMsg{list: &wallet.TokenList{Describe: "x", Holdings: []wallet.TokenHolding{{Token: tok, Code: "FTK", Origin: "received from u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w: not added by you"}}}})
	if v := m.View(); !strings.Contains(v, "not added by you") || !strings.Contains(v, "[x] remove") {
		t.Errorf("origin or key help missing:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if !strings.Contains(m.View(), "not in your list") {
		t.Errorf("x on a token not in the list:\n%s", m.View())
	}
}
