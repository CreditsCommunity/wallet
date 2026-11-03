// Package tui is the wallet's full-screen terminal interface (Bubble Tea): the main screen with
// the accounts, their balance and last transactions, and the screens to create or import an
// account, send CS, page through the history and see the node's status.
package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/history"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

// Run starts the interface and returns when the user quits.
func Run(w *wallet.Wallet, dataDir string) error {
	m := newModel(w)
	_, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

type screen interface {
	update(m *model, msg tea.Msg) tea.Cmd
	view(m *model) string
	title() string
}

type model struct {
	w             *wallet.Wallet
	width, height int

	accounts []*keys.File
	sel      int
	accErr   error

	status    node.Status
	statusErr error
	oldest    int64 // oldest block on the node (0: unknown)

	balances map[string]node.Balance
	balErr   map[string]error
	recent   map[string][]history.Entry
	total    map[string]int
	tokens   map[string][]node.TokenBalance
	tokErr   map[string]error

	screen screen // nil: the main screen
	flash  string // a one-line message on the main screen
}

func newModel(w *wallet.Wallet) *model {
	return &model{flash: strings.Join(config.PermissionWarnings(filepath.Dir(w.Store.Dir)), "; "), w: w, width: 80, height: 24, balances: map[string]node.Balance{}, balErr: map[string]error{},
		recent: map[string][]history.Entry{}, total: map[string]int{}, tokens: map[string][]node.TokenBalance{}, tokErr: map[string]error{}}
}

// ---- messages and commands -----------------------------------------------------------------------

type accountsMsg struct {
	files []*keys.File
	err   error
}
type statusMsg struct {
	st     node.Status
	oldest int64
	err    error
}
type accountDataMsg struct {
	name    string
	bal     node.Balance
	entries []history.Entry
	total   int
	tokens  []node.TokenBalance
	tokErr  error
	err     error
}
type tickMsg struct{}

const callTimeout = 30 * time.Second

func (m *model) loadAccounts() tea.Cmd {
	return func() tea.Msg {
		files, problems, err := m.w.Store.List()
		if err == nil && len(problems) > 0 {
			err = fmt.Errorf("key files not readable: %w", errors.Join(problems...))
		}
		return accountsMsg{files, err}
	}
}

func (m *model) loadStatus(withOldest bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		st, err := m.w.Node.Status(ctx)
		var oldest int64
		if err == nil && withOldest {
			oldest, _ = m.w.Node.OldestBlock(ctx, st.LastBlock)
		}
		return statusMsg{st, oldest, err}
	}
}

func (m *model) loadAccount(f *keys.File) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		pub, err := f.PublicKey()
		if err != nil {
			return accountDataMsg{name: f.Name, err: err}
		}
		bal, err := m.w.Node.Balance(ctx, pub)
		if err != nil {
			return accountDataMsg{name: f.Name, err: err}
		}
		page, err := m.w.History().Page(ctx, pub, 0, 8)
		tokens, tokErr := m.w.Node.TokenBalances(ctx, pub)
		return accountDataMsg{name: f.Name, bal: bal, entries: page.Entries, total: page.Total, tokens: tokens, tokErr: tokErr, err: err}
	}
}

func tick() tea.Cmd { return tea.Tick(10*time.Second, func(time.Time) tea.Msg { return tickMsg{} }) }

func (m *model) current() *keys.File {
	if m.sel >= 0 && m.sel < len(m.accounts) {
		return m.accounts[m.sel]
	}
	return nil
}

func (m *model) refreshCurrent() tea.Cmd {
	if f := m.current(); f != nil {
		return m.loadAccount(f)
	}
	return nil
}

// ---- Bubble Tea --------------------------------------------------------------------------------

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.loadAccounts(), m.loadStatus(true), tick())
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case accountsMsg:
		m.accounts, m.accErr = msg.files, msg.err
		if m.sel >= len(m.accounts) {
			m.sel = max(0, len(m.accounts)-1)
		}
		return m, m.refreshCurrent()
	case statusMsg:
		m.status, m.statusErr = msg.st, msg.err
		if msg.oldest > 0 {
			m.oldest = msg.oldest
		}
		return m, nil
	case accountDataMsg:
		if msg.err != nil {
			m.balErr[msg.name] = msg.err
		} else {
			delete(m.balErr, msg.name)
			m.balances[msg.name], m.recent[msg.name], m.total[msg.name] = msg.bal, msg.entries, msg.total
			m.tokens[msg.name], m.tokErr[msg.name] = msg.tokens, msg.tokErr
		}
		return m, nil
	case tickMsg:
		return m, tea.Batch(m.loadStatus(m.oldest == 0), m.refreshCurrent(), tick())
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}
	if m.screen != nil {
		return m, m.screen.update(m, msg)
	}
	return m, m.updateMain(msg)
}

func (m *model) back(cmds ...tea.Cmd) tea.Cmd {
	m.screen = nil
	return tea.Batch(append(cmds, m.loadAccounts())...)
}

func (m *model) updateMain(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && msg.X < 22 {
			if i := msg.Y - 2; i >= 0 && i < len(m.accounts) {
				m.sel = i
				return m.refreshCurrent()
			}
		}
	case tea.KeyMsg:
		m.flash = ""
		switch msg.String() {
		case "q", "esc":
			return tea.Quit
		case "up", "k":
			if m.sel > 0 {
				m.sel--
				return m.refreshCurrent()
			}
		case "down", "j":
			if m.sel < len(m.accounts)-1 {
				m.sel++
				return m.refreshCurrent()
			}
		case "n":
			m.screen = newAccountScreen(false)
		case "i":
			m.screen = newAccountScreen(true)
		case "s":
			if m.current() == nil {
				m.flash = "create or import an account first"
				return nil
			}
			m.screen = newSendScreen(m.current())
		case "d":
			if f := m.current(); f != nil {
				s := newDelegationsScreen(f)
				m.screen = s
				return s.load(m)
			}
		case "t":
			if f := m.current(); f != nil {
				s := newTokensScreen(f)
				m.screen = s
				return s.load(m)
			}
		case "h":
			if f := m.current(); f != nil {
				s := newHistoryScreen(f, m.w.Config.HistoryPage)
				m.screen = s
				return s.load(m)
			}
		case "S":
			m.screen = &statusScreen{}
			return m.loadStatus(true)
		case "?":
			m.screen = &helpScreen{}
		case "r":
			return tea.Batch(m.loadStatus(true), m.loadAccounts())
		}
	}
	return nil
}

// ---- views -------------------------------------------------------------------------------------

func (m *model) networkBadge() string {
	n := m.w.Profile.Network
	if n.IsMainnet() {
		return stMainnet.Render(n.Label())
	}
	return stTestnet.Render(n.Label())
}

func (m *model) nodeLine() string {
	s := "node " + m.w.Profile.Address + " "
	switch {
	case m.statusErr != nil:
		return s + stBad.Render("✗ unreachable")
	case m.status.Round == 0:
		return s + stDim.Render("…")
	case m.status.Synced:
		return s + stOK.Render("✓ synced")
	default:
		return s + stWarn.Render("! not synced")
	}
}

func (m *model) header(title string) string {
	left := stTitle.Render(title)
	right := m.networkBadge() + " " + m.nodeLine()
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *model) View() string {
	title := "credits-wallet"
	var body, footer string
	if m.screen != nil {
		title = m.screen.title()
		body = m.screen.view(m)
		footer = ""
	} else {
		body = m.mainView()
		footer = keyHelp("s", "send", "d", "delegate", "t", "tokens", "h", "history", "n", "new", "S", "status", "?", "help", "q", "quit")
	}
	h := m.height - 2
	if footer == "" {
		h++
	}
	body = lipgloss.NewStyle().Height(h).MaxHeight(h).Width(m.width).MaxWidth(m.width).Render(body)
	out := m.header(title) + "\n" + body
	if footer != "" {
		out += "\n" + lipgloss.NewStyle().MaxWidth(m.width).Render(footer)
	}
	return out
}

func (m *model) mainView() string {
	// accounts column
	var left strings.Builder
	left.WriteString(stTitle.Render("Accounts") + "\n")
	if m.accErr != nil {
		left.WriteString(stBad.Render(m.accErr.Error()) + "\n")
	}
	if len(m.accounts) == 0 {
		left.WriteString(stDim.Render("none yet") + "\n")
	}
	for i, f := range m.accounts {
		name := f.Name
		if len(name) > 18 {
			name = name[:17] + "…"
		}
		if i == m.sel {
			left.WriteString(stSel.Render("> "+name) + "\n")
		} else {
			left.WriteString("  " + name + "\n")
		}
	}
	left.WriteString("\n" + keyHelp("n", "new") + "\n" + keyHelp("i", "import"))
	leftCol := lipgloss.NewStyle().Width(21).Render(left.String())

	// details of the selected account
	rightW := max(m.width-23, 30)
	var right strings.Builder
	if f := m.current(); f != nil {
		right.WriteString(stTitle.Render(f.Name) + "  " + f.Public + "\n")
		if err := m.balErr[f.Name]; err != nil {
			right.WriteString(stBad.Render(oneLine(err.Error())) + "\n")
		} else if b, ok := m.balances[f.Name]; ok {
			right.WriteString(fmt.Sprintf("Balance    %s CS\n", b.CS.Grouped()))
			if b.DelegatedOut.Sign() != 0 || b.Delegations > 0 {
				right.WriteString(fmt.Sprintf("Delegated  %s CS (%d delegations)\n", b.DelegatedOut.Grouped(), b.Delegations))
			}
			if b.DelegatedIn.Sign() != 0 {
				right.WriteString(fmt.Sprintf("Delegated to it  %s CS\n", b.DelegatedIn.Grouped()))
			}
			right.WriteString(m.tokenLine(f.Name, rightW) + "\n")
			right.WriteString("\n" + stTitle.Render("Last transactions") + " " + m.historyNote() + "\n")
			entries := m.recent[f.Name]
			if len(entries) == 0 {
				if m.total[f.Name] > 0 {
					right.WriteString(stDim.Render(fmt.Sprintf("%d transactions, none on this node (pruned)", m.total[f.Name])) + "\n")
				} else {
					right.WriteString(stDim.Render("none") + "\n")
				}
			}
			for _, e := range entries {
				right.WriteString(entryLine(e, rightW) + "\n")
			}
		} else {
			right.WriteString(stDim.Render("loading…") + "\n")
		}
	} else {
		right.WriteString("Welcome. Create an account with " + stKey.Render("[n]") + " or import one with " + stKey.Render("[i]") + ".\n")
		right.WriteString(stDim.Render("Keys are stored encrypted in "+m.w.Store.Dir) + "\n")
	}
	if m.statusErr != nil {
		right.WriteString("\n" + stBad.Render(oneLine(m.statusErr.Error())) + "\n")
	} else if m.status.Round > 0 && !m.status.Synced {
		right.WriteString("\n" + stWarn.Render("The node is not synced: "+m.status.Why+". Sending is refused until it is.") + "\n")
	}
	if m.flash != "" {
		right.WriteString("\n" + stWarn.Render(m.flash) + "\n")
	}
	rightCol := lipgloss.NewStyle().Width(rightW).Render(right.String())
	sep := lipgloss.NewStyle().Foreground(colDim).Render(strings.Repeat("│\n", max(m.height-3, 1)))
	return lipgloss.JoinHorizontal(lipgloss.Top, leftCol, sep, " ", rightCol)
}

// tokenLine is "Tokens     CTT 998,900.50  XYZ 3" for the main screen.
func (m *model) tokenLine(name string, width int) string {
	if err := m.tokErr[name]; err != nil {
		if errors.Is(err, node.ErrNoTokenCache) {
			return stDim.Render("Tokens     the node has no token cache ([api] tokens_cache=true)")
		}
		return stDim.Render("Tokens     " + oneLine(err.Error()))
	}
	list := m.tokens[name]
	if len(list) == 0 {
		return stDim.Render("Tokens     none")
	}
	parts := make([]string, 0, len(list))
	for _, t := range list {
		parts = append(parts, printable(t.Code)+" "+printable(t.Balance))
	}
	return ansi.Truncate("Tokens     "+strings.Join(parts, "  "), width, "…")
}

func (m *model) historyNote() string {
	if m.w.History().Remote() {
		return stDim.Render("(from " + m.w.History().Describe() + ")")
	}
	if m.oldest > 1 {
		return stDim.Render(fmt.Sprintf("(from block %s on this node)", groupInt(m.oldest)))
	}
	return ""
}

// entryLine renders a transaction in one line: "11-03 14:02  #187,210,331  → u6PJ8D…EsynXq2w  -12.5 CS".
// When the width is short the block number goes first, then the line is cut.
func entryLine(e history.Entry, width int) string {
	arrow := map[string]string{"out": "→", "in": "←", "self": "↺"}[e.Direction]
	amt := e.Amount.Grouped()
	if e.Amount.Sign() > 0 {
		amt = "+" + amt
	}
	what := arrow + " " + keys.Short(e.Counterparty)
	if e.Kind != "transfer" {
		what += " " + e.Kind
	}
	when := e.Time.Local().Format("01-02 15:04")
	value := amt + " CS"
	if e.TokenAmount != "" {
		value = printable(e.TokenAmount) + " " + printable(e.TokenCode)
	}
	line := fmt.Sprintf("%s  #%s  %s  %s", when, groupInt(e.Block), what, value)
	if lipgloss.Width(line) > width {
		line = fmt.Sprintf("%s  %s  %s", when, what, value)
	}
	line = ansi.Truncate(line, width, "…")
	if e.Amount.Sign() < 0 || strings.HasPrefix(e.TokenAmount, "-") {
		return line
	}
	return stOK.Render(line)
}

func groupInt(n int64) string {
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// ---- status and help ---------------------------------------------------------------------------

type statusScreen struct{}

func (statusScreen) title() string { return "Node status" }
func (statusScreen) update(m *model, msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc", "q", "enter":
			return m.back()
		case "r":
			return m.loadStatus(true)
		}
	}
	return nil
}
func (statusScreen) view(m *model) string {
	var b strings.Builder
	p := m.w.Profile
	fmt.Fprintf(&b, "Network     %s", m.networkBadge())
	if p.Network.IsMainnet() {
		b.WriteString(stDim.Render("  assumed: no test network declared for this node"))
	} else {
		b.WriteString(stDim.Render("  declared in the configuration"))
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "Node         %s %s\n", p.Address, stDim.Render(p.Name))
	if m.statusErr != nil {
		b.WriteString(stBad.Render(oneLine(m.statusErr.Error())) + "\n")
	} else {
		st := m.status
		fmt.Fprintf(&b, "Last block   %s\n", groupInt(st.LastBlock))
		fmt.Fprintf(&b, "Round        %s\n", groupInt(st.Round))
		if !st.HeadTime.IsZero() {
			fmt.Fprintf(&b, "Head time    %s (%s ago)\n", st.HeadTime.Local().Format("2006-01-02 15:04:05"), time.Since(st.HeadTime).Round(time.Second))
		}
		if st.Synced {
			b.WriteString("Synced       " + stOK.Render("yes") + "\n")
		} else {
			b.WriteString("Synced       " + stWarn.Render("no: "+st.Why) + "\n")
		}
		if m.oldest > 0 {
			note := "full history"
			if m.oldest > 1 {
				note = "pruned: older history is not on this node"
			}
			fmt.Fprintf(&b, "History from block %s %s\n", groupInt(m.oldest), stDim.Render("("+note+")"))
		}
	}
	b.WriteString("\n" + keyHelp("r", "refresh", "Esc", "back"))
	return b.String()
}

type helpScreen struct{}

func (helpScreen) title() string { return "Help" }
func (helpScreen) update(m *model, msg tea.Msg) tea.Cmd {
	if _, ok := msg.(tea.KeyMsg); ok {
		return m.back()
	}
	return nil
}
func (helpScreen) view(m *model) string {
	return strings.Join([]string{
		stTitle.Render("Main screen"),
		"  ↑/↓ or j/k   select an account (or click it)",
		"  s            send CS from the selected account (optional user data: public text)",
		"  d            delegations: made and received; n delegate, w withdraw",
		"  t            tokens: balances; s send a token, a send a token by its address",
		"  h            history of the selected account (e: export CSV)",
		"  n / i        new account / import a key, a 24-word phrase or a keys JSON",
		"  S            node status        r  refresh        q  quit",
		"",
		stTitle.Render("Forms"),
		"  Tab, ↑/↓     next or previous field    Enter  next / confirm    Esc  cancel",
		"",
		stTitle.Render("Safety"),
		"  The network is shown at the top. MAINNET is assumed unless the configuration declares",
		"  network = \"testnet\" or \"private\" for this node; on mainnet a transfer asks you to type",
		"  'send' and your password. Keys never leave this machine: transactions are signed here.",
		"  The wallet refuses to send while the node is not synced.",
		"",
		"  Scriptable commands: credits-wallet help",
		"",
		stDim.Render("any key: back"),
	}, "\n")
}
