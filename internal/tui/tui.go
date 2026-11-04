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
	"github.com/CreditsCommunity/wallet/internal/echo"
	"github.com/CreditsCommunity/wallet/internal/history"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

// Run starts the interface and returns when the user quits. found is a node's key in the current
// folder, offered on the first screen (nil: none). The unlocked key is zeroed on the way out.
func Run(w *wallet.Wallet, dataDir string, found *wallet.FoundNodeKey) error {
	m := newModel(w)
	if found != nil {
		m.screen = newFoundKeyScreen(found)
	}
	defer m.lockSession("", "")
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
	sel      int // the account chosen on the unlock screen
	accErr   error
	// selectName: the account to select once the list is loaded (a new one, the found node key)
	selectName string

	// the session (spec 6, "Unlock"): nil while locked, then nothing of any account is loaded
	unlocked  *wallet.Unlocked
	lock      *lockScreen
	lockAfter time.Duration    // inactivity before the session locks (0: never)
	lastInput time.Time        // the last key press or click
	now       func() time.Time // the clock (tests)

	status    node.Status
	statusErr error
	oldest    int64 // oldest block on the node (0: unknown)
	netID     node.NetworkID
	netIDErr  error

	balances map[string]node.Balance
	balErr   map[string]error
	recent   map[string][]history.Entry
	total    map[string]int
	tokens   map[string]*wallet.TokenList
	tokErr   map[string]error

	screen screen // nil: the main screen
	flash  string // a one-line message on the main screen

	nodeWarned bool // the node key's warning was shown (once)
}

func newModel(w *wallet.Wallet) *model {
	return &model{lock: &lockScreen{}, now: time.Now, lockAfter: w.Config.LockAfter(), flash: strings.Join(config.PermissionWarnings(filepath.Dir(w.Store.Dir)), "; "), w: w, width: 80, height: 24, balances: map[string]node.Balance{}, balErr: map[string]error{},
		recent: map[string][]history.Entry{}, total: map[string]int{}, tokens: map[string]*wallet.TokenList{}, tokErr: map[string]error{}}
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
	id     *node.NetworkID // asked with the oldest block (the status screen)
	idErr  error
}
type accountDataMsg struct {
	name    string
	bal     node.Balance
	entries []history.Entry
	total   int
	err     error
}

// accountTokensMsg: the tokens of an account, read apart (without a token cache the wallet reads
// the history, which takes longer than the balance)
type accountTokensMsg struct {
	name string
	list *wallet.TokenList
	err  error
}
type tickMsg struct{}

const callTimeout = 30 * time.Second

func (m *model) loadAccounts() tea.Cmd {
	return func() tea.Msg {
		files, problems, err := m.w.Accounts()
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
		msg := statusMsg{st: st, err: err}
		if err == nil && withOldest {
			msg.oldest, _ = m.w.Node.OldestBlock(ctx, st.LastBlock)
			id, idErr := m.w.Node.NetworkIdentity(ctx)
			msg.id, msg.idErr = &id, idErr
		}
		return msg
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
		return accountDataMsg{name: f.Name, bal: bal, entries: page.Entries, total: page.Total, err: err}
	}
}

func (m *model) loadAccountTokens(f *keys.File) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*callTimeout)
		defer cancel()
		pub, err := f.PublicKey()
		if err != nil {
			return accountTokensMsg{name: f.Name, err: err}
		}
		list, err := m.w.Tokens(ctx, pub)
		return accountTokensMsg{name: f.Name, list: list, err: err}
	}
}

func tick() tea.Cmd { return tea.Tick(10*time.Second, func(time.Time) tea.Msg { return tickMsg{} }) }

// current is the unlocked account (nil while locked).
func (m *model) current() *keys.File {
	if m.unlocked == nil {
		return nil
	}
	return m.unlocked.Account
}

func (m *model) refreshCurrent() tea.Cmd {
	if f := m.current(); f != nil {
		return tea.Batch(m.loadAccount(f), m.loadAccountTokens(f))
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
		if m.selectName != "" {
			m.selectByName()
		}
		if !m.nodeWarned && len(m.accounts) > 0 && m.accounts[0].IsNodeKey() {
			m.nodeWarned = true
			warning := wallet.NodeKeyWarning
			if m.accounts[0].NodeKeyEncrypted {
				warning = wallet.NodeKeyEncryptedWarning
			}
			if w := wallet.NodeKeyModeWarning(m.accounts[0].NodeKey); w != "" {
				warning = w + "; " + warning
			}
			m.flash = strings.TrimPrefix(m.flash+"; "+warning, "; ")
		}
		if m.sel >= len(m.accounts) {
			m.sel = max(0, len(m.accounts)-1)
		}
		return m, m.refreshCurrent()
	case statusMsg:
		m.status, m.statusErr = msg.st, msg.err
		if msg.oldest > 0 {
			m.oldest = msg.oldest
		}
		if msg.id != nil {
			m.netID, m.netIDErr = *msg.id, msg.idErr
		}
		return m, nil
	case accountDataMsg:
		if f := m.current(); f == nil || f.Name != msg.name {
			return m, nil // locked, or another account since: dropped
		}
		if msg.err != nil {
			m.balErr[msg.name] = msg.err
		} else {
			delete(m.balErr, msg.name)
			m.balances[msg.name], m.recent[msg.name], m.total[msg.name] = msg.bal, msg.entries, msg.total
		}
		return m, nil
	case accountTokensMsg:
		if f := m.current(); f == nil || f.Name != msg.name {
			return m, nil
		}
		if msg.err != nil {
			m.tokErr[msg.name] = msg.err
		} else {
			delete(m.tokErr, msg.name)
			m.tokens[msg.name] = msg.list
		}
		return m, nil
	case tickMsg:
		if m.unlocked != nil && m.lockAfter > 0 && m.now().Sub(m.lastInput) >= m.lockAfter && !m.sending() {
			m.lockSession(fmt.Sprintf("locked after %s without a key press", m.lockAfter), "")
		}
		return m, tea.Batch(m.loadStatus(m.oldest == 0), m.refreshCurrent(), tick())
	case tea.KeyMsg:
		m.lastInput = m.now()
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	case tea.MouseMsg:
		m.lastInput = m.now()
	}
	if m.screen != nil {
		return m, m.screen.update(m, msg)
	}
	if m.unlocked == nil {
		return m, m.updateLock(msg)
	}
	return m, m.updateMain(msg)
}

// sending tells whether a transaction is on its way (the session does not lock under it).
func (m *model) sending() bool {
	s, ok := m.screen.(*sendScreen)
	return ok && s.step == sendSending
}

func (m *model) back(cmds ...tea.Cmd) tea.Cmd {
	m.screen = nil
	return tea.Batch(append(cmds, m.loadAccounts())...)
}

func (m *model) updateMain(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		m.flash = ""
		switch msg.String() {
		case "q", "esc":
			return tea.Quit
		case "l":
			m.lockSession("locked", "")
		case "a":
			// another account: the session locks first, its password is asked
			m.lockSession("", "")
		case "up", "down", "k", "j":
			m.flash = "[a] another account: the session locks first"
		case "n":
			m.screen = newAccountScreen(false)
		case "g":
			if m.w.NodeKeyMissing() {
				m.screen = newNodeKeyScreen(m.w.NodeKey.Dir)
			}
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
		case "c":
			if f := m.current(); f != nil {
				s := newContractsScreen(f)
				m.screen = s
				return s.load(m)
			}
		case "h":
			if f := m.current(); f != nil {
				s := newHistoryScreen(f, m.w.Config.HistoryPage)
				m.screen = s
				return s.load(m)
			}
		case "y":
			if f := m.current(); f != nil {
				m.flash = copyOwnAddress(f.Public)
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
	} else if m.unlocked == nil {
		title = "credits-wallet: locked"
		body = m.lockView()
	} else {
		body = m.mainView()
		footer = keyHelp("s", "send", "d", "delegate", "c", "contracts", "h", "history", "t", "tokens", "l", "lock", "?", "help")
		if lipgloss.Width(footer) > m.width {
			footer = strings.ReplaceAll(footer, "  ", " ")
		}
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
	cur := m.current()
	for _, f := range m.accounts {
		name := f.Name
		if len(name) > 18 {
			name = name[:17] + "…"
		}
		tag := ""
		if f.IsNodeKey() && len(name) <= 9 {
			tag = " " + stWarn.Render("node key")
		}
		if cur != nil && f.Name == cur.Name {
			left.WriteString(stSel.Render("> "+name) + tag + "\n")
		} else {
			left.WriteString(stDim.Render("  "+name) + tag + "\n")
		}
	}
	left.WriteString("\n" + keyHelp("a", "switch") + "\n" + keyHelp("n", "new") + "\n" + keyHelp("i", "import") + "\n" + keyHelp("q", "quit"))
	leftCol := lipgloss.NewStyle().Width(21).Render(left.String())

	// details of the selected account
	rightW := max(m.width-23, 30)
	var right strings.Builder
	if f := m.current(); f != nil {
		right.WriteString(stTitle.Render(f.Name) + "  " + stDim.Render("[y] copy the address") + "\n")
		right.WriteString(f.Public + "\n") // whole, alone on its line: it can also be selected
		if f.NodeKeyEncrypted {
			right.WriteString(stWarn.Render(truncate("NODE KEY, encrypted by the node: unlocked", rightW)) + "\n")
			right.WriteString(stDim.Render(truncate("read from "+f.NodeKey, rightW)) + "\n")
		} else if f.IsNodeKey() {
			right.WriteString(stWarn.Render(truncate("NODE KEY: the node's own key, in clear in its folder (no password)", rightW)) + "\n")
			right.WriteString(stDim.Render(truncate("read from "+f.NodeKey, rightW)) + "\n")
		}
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
	if m.w.NodeKeyMissing() {
		right.WriteString("\n" + stWarn.Render("The node in "+m.w.NodeKey.Dir+" has no key yet.") + "\n")
		right.WriteString(stKey.Render("[g]") + " make the node's key from a 24-word phrase: the node uses it at its first start.\n")
	}
	if m.statusErr != nil {
		right.WriteString("\n" + stBad.Render(oneLine(m.statusErr.Error())) + "\n")
	} else if m.status.Round > 0 && !m.status.Synced {
		right.WriteString("\n" + stWarn.Render("The node is not synced: "+m.status.Why+". Sending is refused until it is.") + "\n")
	}
	if m.flash != "" {
		right.WriteString("\n" + stWarn.Render(truncate(m.flash, rightW)) + "\n")
	}
	rightCol := lipgloss.NewStyle().Width(rightW).Render(right.String())
	sep := lipgloss.NewStyle().Foreground(colDim).Render(strings.Repeat("│\n", max(m.height-3, 1)))
	return lipgloss.JoinHorizontal(lipgloss.Top, leftCol, sep, " ", rightCol)
}

// tokenLine is "Tokens     CTT 998,900.50  XYZ 3  [t]" for the main screen: every token held, as
// many as fit, and how many there are.
func (m *model) tokenLine(name string, width int) string {
	l := m.tokens[name]
	if err := m.tokErr[name]; err != nil && l == nil {
		return stDim.Render(truncate("Tokens     "+oneLine(err.Error()), width))
	}
	if l == nil {
		return stDim.Render("Tokens     loading…")
	}
	where := ""
	if l.Note != "" {
		where = " (found from history)"
	}
	if len(l.Holdings) == 0 {
		return stDim.Render("Tokens     none" + where)
	}
	parts := make([]string, 0, len(l.Holdings))
	for _, h := range l.Holdings {
		bal := printable(h.Balance)
		if h.Err != nil {
			bal = "?"
		}
		parts = append(parts, printable(h.Code)+" "+bal)
	}
	n := fmt.Sprintf("%d tokens", len(l.Holdings))
	if len(l.Holdings) == 1 {
		n = "1 token"
	}
	tail := stDim.Render("  · "+n+where+" ") + stKey.Render("[t]")
	head := "Tokens     " + strings.Join(parts, "  ")
	room := max(width-2-lipgloss.Width(tail), 20)
	return ansi.Truncate(head, room, "…") + tail
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
		ta := printable(e.TokenAmount)
		if !strings.HasPrefix(ta, "-") {
			ta = "+" + ta
		}
		value = ta + " " + printable(e.TokenCode)
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

// oneLine puts a message (an error, which may carry text from the node) on one line, with what
// could drive the terminal escaped.
func oneLine(s string) string { return echo.Terminal(strings.Join(strings.Fields(s), " ")) }

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
	if m.netID.Known() {
		line := "Node says    " + m.netID.Describe()
		if err := wallet.NetworkMismatch(p, m.netID); err != nil {
			b.WriteString(stBad.Render(ansi.Truncate(line, m.width, "…")) + "\n" + stBad.Render(ansi.Truncate(oneLine(err.Error()), 2*m.width, "…")) + "\n")
		} else {
			b.WriteString(stOK.Render(ansi.Truncate(line, m.width, "…")) + "\n")
		}
	} else if m.netIDErr != nil {
		b.WriteString(stDim.Render(ansi.Truncate("Node says    nothing: the configuration decides ("+oneLine(m.netIDErr.Error())+")", m.width, "…")) + "\n")
	}
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
		"  l            lock: the password is asked again   a  another account (locks first)",
		"  s            send CS from the selected account (optional user data: public text)",
		"  d            delegations: made and received; n delegate, w withdraw",
		"  t            tokens: every token held with its contract address; s send, a add a token,",
		"               x remove it from your list, y copy the address (or Shift+drag)",
		"  c            contracts: o open a .java, n new from a template, a open an address;",
		"               a source compiles on the node (e edits it in $EDITOR, d deploys);",
		"               a contract's methods: Enter reads a @Getter free or signs a call",
		"  h            history of the selected account (e: export CSV)",
		"  y            copy the selected account's address (or select it with Shift+drag)",
		"  n / i        new account / import a key, a 24-word phrase or a keys JSON",
		"  g            a node's wallet whose node has no key yet: make the node's key",
		"               (24 words; the account \"node\", in clear in the node's folder)",
		"  S            node status        r  refresh        q  quit",
		"  The wallet locks by itself after lock_after_minutes without a key (default 10).",
		"",
		stTitle.Render("Forms"),
		"  Tab, ↑/↓     next or previous field    Enter  next / confirm    Esc  cancel",
		"  paste        the terminal's paste (Ctrl+Shift+V, right click): an address is checked",
		"",
		stTitle.Render("Safety"),
		"  The network is shown at the top. MAINNET is assumed unless the configuration declares",
		"  network = \"testnet\" or \"private\" for this node; on mainnet a transfer asks you to type",
		"  'send'. Unlocked, signing asks no password: lock (l) before leaving the terminal.",
		"  The wallet refuses to send while the node is not synced.",
		"",
		"  Scriptable commands: credits-wallet help",
		"",
		stDim.Render("any key: back"),
	}, "\n")
}
