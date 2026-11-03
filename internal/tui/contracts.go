package tui

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/CreditsCommunity/wallet/internal/echo"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
	"github.com/CreditsCommunity/wallet/internal/templates"
	"github.com/CreditsCommunity/wallet/internal/tx"
	"github.com/CreditsCommunity/wallet/internal/wallet"
	"github.com/CreditsCommunity/wallet/thrift/general"
)

// The contracts screens (spec 5.5, 6): the account's contracts, a source file (compile, errors,
// edit in $EDITOR, deploy) and a deployed contract (methods, a form per method, get, call, pay,
// results and state). Names, results, errors and state come from contracts or the executor: they
// go through printable (echo.Printable) or node.Signature.

// ---- the account's contracts -------------------------------------------------------------------

type contractsMode int

const (
	contractsList contractsMode = iota
	contractsAddress
	contractsOpen
	contractsNew
)

type contractsScreen struct {
	account  *keys.File
	rows     []wallet.ContractRow
	loaded   bool
	nodeErr  error
	localErr error
	sel      int
	mode     contractsMode
	input    *form
	err      error
}

type contractsMsg struct {
	rows              []wallet.ContractRow
	nodeErr, localErr error
}

func newContractsScreen(f *keys.File) *contractsScreen { return &contractsScreen{account: f} }

func (s *contractsScreen) title() string { return "Contracts  " + s.account.Name }

func (s *contractsScreen) load(m *model) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		pub, err := s.account.PublicKey()
		if err != nil {
			return contractsMsg{nodeErr: err}
		}
		rows, nodeErr, localErr := m.w.Contracts(ctx, pub)
		return contractsMsg{rows, nodeErr, localErr}
	}
}

func (s *contractsScreen) update(m *model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case contractsMsg:
		s.rows, s.nodeErr, s.localErr, s.loaded = msg.rows, msg.nodeErr, msg.localErr, true
		s.sel = min(s.sel, max(len(s.rows)-1, 0))
		return nil
	case tea.KeyMsg:
		if s.mode != contractsList {
			if msg.String() == "esc" {
				s.mode, s.err = contractsList, nil
				return nil
			}
			cmd, done := s.input.update(msg)
			if done {
				return s.submit(m)
			}
			return cmd
		}
		s.err = nil
		switch msg.String() {
		case "esc", "q":
			return m.back()
		case "up", "k":
			s.sel = max(s.sel-1, 0)
		case "down", "j":
			s.sel = min(s.sel+1, max(len(s.rows)-1, 0))
		case "enter":
			if s.sel < len(s.rows) {
				return openContract(m, s.account, s, s.rows[s.sel].Address)
			}
		case "a":
			s.mode, s.input = contractsAddress, newForm("Contract")
			s.input.inputs[0].Placeholder = "contract address (base58)"
		case "o":
			s.mode, s.input = contractsOpen, newForm("File")
			s.input.inputs[0].Placeholder = "path of a .java file"
		case "n":
			s.mode, s.input = contractsNew, newForm("Template", "File")
			s.input.inputs[0].Placeholder = strings.Join(templates.Names(), ", ")
			s.input.inputs[1].Placeholder = "new .java file to write"
		case "r":
			return s.load(m)
		}
	}
	return nil
}

// submit acts on the input of a mode.
func (s *contractsScreen) submit(m *model) tea.Cmd {
	switch s.mode {
	case contractsAddress:
		addr, err := keys.ParseAddress(s.input.value(0))
		if err != nil {
			s.err = err
			return nil
		}
		s.mode = contractsList
		return openContract(m, s.account, s, addr)
	case contractsOpen:
		s.mode = contractsList
		return openSource(m, s.account, s, s.input.value(0))
	case contractsNew:
		source, t, err := templates.Get(s.input.value(0))
		if err != nil {
			s.err = err
			return nil
		}
		file := s.input.value(1)
		if file == "" {
			file = t.Name + ".java"
		}
		if err := writeNew(file, source); err != nil {
			s.err = err
			return nil
		}
		s.mode = contractsList
		return openSource(m, s.account, s, file)
	}
	return nil
}

// writeNew writes a new file (never replacing one).
func writeNew(path, text string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s exists: choose another name, or open it with [o]", path)
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (s *contractsScreen) view(m *model) string {
	var b strings.Builder
	b.WriteString(s.account.Public + "\n\n")
	switch {
	case !s.loaded:
		b.WriteString(stDim.Render("loading…") + "\n")
	case len(s.rows) == 0:
		b.WriteString(stDim.Render("no contracts deployed by this account") + "\n")
	}
	if s.nodeErr != nil {
		b.WriteString(stWarn.Render(ansi.Truncate("node: "+oneLine(s.nodeErr.Error()), m.width-2, "…")) + "\n")
	}
	if s.localErr != nil {
		b.WriteString(stWarn.Render(ansi.Truncate("local list: "+oneLine(s.localErr.Error()), m.width-2, "…")) + "\n")
	}
	limit := max(m.height-12, 3)
	first := max(0, min(s.sel-limit+1, len(s.rows)-limit))
	for i := first; i < len(s.rows) && i < first+limit; i++ {
		r := s.rows[i]
		when := "                "
		if !r.Created.IsZero() {
			when = r.Created.Local().Format("2006-01-02 15:04")
		}
		where := "node+local"
		switch {
		case r.Local == nil:
			where = "node"
		case !r.OnNode:
			where = "local only (" + r.Local.Network + ")"
		}
		line := fmt.Sprintf("%s  %s  %s  %s", when, keys.Short(keys.Address(r.Address)), ansi.Truncate(printable(r.Name), 14, "…"), stDim.Render(where))
		line = ansi.Truncate(line, m.width-4, "…")
		if i == s.sel {
			b.WriteString(stSel.Render("> ") + line + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	b.WriteString("\n")
	if s.mode != contractsList {
		b.WriteString(s.input.view())
		b.WriteString(keyHelp("Enter", "open", "Esc", "cancel") + "\n")
	} else {
		b.WriteString(keyHelp("Enter", "open selected", "a", "by address", "o", "open .java", "n", "new from template", "r", "refresh", "Esc", "back") + "\n")
	}
	if s.err != nil {
		b.WriteString(stBad.Render(ansi.Truncate(oneLine(s.err.Error()), m.width-2, "…")) + "\n")
	}
	return b.String()
}

// ---- a source file -----------------------------------------------------------------------------

type sourceScreen struct {
	account *keys.File
	parent  screen
	file    string
	source  string
	lines   []string
	readErr error

	compiling bool
	comp      *wallet.Compilation
	failure   *node.CompileFailure
	err       error
	errLines  map[int]bool
	top       int
	deployed  ed25519.PublicKey
	note      string
}

type compiledMsg struct {
	source string
	comp   *wallet.Compilation
	err    error
}

type editorDoneMsg struct{ err error }

// openSource opens a .java file: it reads it and compiles it through the node.
func openSource(m *model, f *keys.File, parent screen, file string) tea.Cmd {
	s := &sourceScreen{account: f, parent: parent, file: file}
	m.screen = s
	return s.reload(m)
}

func (s *sourceScreen) title() string { return "Contract  " + filepath.Base(s.file) }

// reload reads the file and compiles it.
func (s *sourceScreen) reload(m *model) tea.Cmd {
	s.comp, s.failure, s.err, s.errLines = nil, nil, nil, nil
	data, err := os.ReadFile(s.file)
	if err == nil && len(data) > wallet.MaxSource {
		err = fmt.Errorf("%s has %d bytes, at most %d", s.file, len(data), wallet.MaxSource)
	}
	s.readErr = err
	if err != nil {
		s.lines = nil
		return nil
	}
	s.source = string(data)
	s.lines = strings.Split(strings.ReplaceAll(s.source, "\t", "    "), "\n")
	s.compiling = true
	source := s.source
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*callTimeout)
		defer cancel()
		c, err := m.w.Compile(ctx, source)
		return compiledMsg{source, c, err}
	}
}

// editorCommand is the user's editor: $VISUAL or $EDITOR (a command and its arguments, no shell),
// Notepad on Windows, otherwise nano or vi.
func editorCommand(file string) *exec.Cmd {
	for _, v := range []string{"VISUAL", "EDITOR"} {
		if f := strings.Fields(os.Getenv(v)); len(f) > 0 {
			return exec.Command(f[0], append(f[1:], file)...)
		}
	}
	if runtime.GOOS == "windows" {
		return exec.Command("notepad", file)
	}
	if p, err := exec.LookPath("nano"); err == nil {
		return exec.Command(p, file)
	}
	return exec.Command("vi", file)
}

func (s *sourceScreen) update(m *model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case compiledMsg:
		if msg.source != s.source {
			return nil // an older compilation
		}
		s.compiling = false
		s.comp, s.err = msg.comp, msg.err
		if errors.As(msg.err, &s.failure) {
			s.err = nil
			s.errLines = map[int]bool{}
			for _, e := range s.failure.Errors {
				s.errLines[e.Line] = true
			}
			if len(s.failure.Errors) > 0 {
				s.top = max(0, s.failure.Errors[0].Line-3)
			}
		}
		return nil
	case editorDoneMsg:
		if msg.err != nil {
			s.note = "editor: " + oneLine(msg.err.Error())
		}
		return s.reload(m)
	case tea.KeyMsg:
		s.note = ""
		page := max(m.height-9, 3)
		switch msg.String() {
		case "esc", "q":
			if s.parent != nil {
				m.screen = s.parent
				if c, ok := s.parent.(*contractsScreen); ok {
					return c.load(m)
				}
				return nil
			}
			return m.back()
		case "up", "k":
			s.top = max(s.top-1, 0)
		case "down", "j":
			s.top = min(s.top+1, max(len(s.lines)-page, 0))
		case "pgup":
			s.top = max(s.top-page, 0)
		case "pgdown", " ":
			s.top = min(s.top+page, max(len(s.lines)-page, 0))
		case "e":
			return tea.ExecProcess(editorCommand(s.file), func(err error) tea.Msg { return editorDoneMsg{err} })
		case "c":
			return s.reload(m)
		case "m":
			if s.deployed != nil {
				return openContract(m, s.account, s, s.deployed)
			}
		case "d":
			if s.comp == nil || s.compiling {
				s.note = "compile it first: the source must compile before it is deployed"
				return nil
			}
			account, file, source := s.account.Name, s.file, s.source
			ps, cmd := newPlannedScreen(m, wallet.KindDeploy, s.account, s, func(ctx context.Context) (*wallet.SendPlan, error) {
				return m.w.PrepareDeploy(ctx, account, file, source)
			}, func(res wallet.SendResult, err error) {
				if err == nil && res.Contract != nil {
					s.deployed = res.Contract
				}
			})
			m.screen = ps
			return cmd
		}
	}
	return nil
}

func (s *sourceScreen) view(m *model) string {
	var b strings.Builder
	page := max(m.height-9, 3)
	if s.readErr != nil {
		b.WriteString(stBad.Render(oneLine(s.readErr.Error())) + "\n")
	}
	for i := s.top; i < len(s.lines) && i < s.top+page; i++ {
		n := i + 1
		text := ansi.Truncate(echo.Terminal(s.lines[i]), m.width-7, "…")
		if s.errLines[n] {
			b.WriteString(stBad.Render(fmt.Sprintf("%4d!", n)) + " " + text + "\n")
		} else {
			b.WriteString(stDim.Render(fmt.Sprintf("%4d ", n)) + " " + text + "\n")
		}
	}
	b.WriteString(stDim.Render(strings.Repeat("─", max(m.width-2, 10))) + "\n")
	switch {
	case s.compiling:
		b.WriteString(stDim.Render("compiling on the node…") + "\n")
	case s.failure != nil:
		n := len(s.failure.Errors)
		if n == 0 {
			b.WriteString(stBad.Render(ansi.Truncate("✗ "+oneLine(s.failure.Message), m.width-2, "…")) + "\n")
		} else {
			b.WriteString(stBad.Render(fmt.Sprintf("✗ %d compile errors", n)) + "\n")
		}
		for i, e := range s.failure.Errors {
			if i == 2 {
				break
			}
			first, _, _ := strings.Cut(e.Text, "\n")
			b.WriteString(ansi.Truncate(fmt.Sprintf("  line %d: %s", e.Line, printable(first)), m.width-2, "…") + "\n")
		}
	case s.err != nil:
		b.WriteString(stBad.Render(ansi.Truncate(oneLine(s.err.Error()), m.width-2, "…")) + "\n")
	case s.comp != nil:
		methods := "methods unknown"
		if s.comp.MethodsErr == nil {
			methods = fmt.Sprintf("%d methods", len(s.comp.Methods))
		}
		b.WriteString(stOK.Render(fmt.Sprintf("✓ compiled: %d class, %s", len(s.comp.Classes), methods)) + "  " + stDim.Render(printable(s.comp.Name())) + "\n")
	}
	if s.deployed != nil {
		b.WriteString(stOK.Render("deployed at "+keys.Address(s.deployed)) + "  " + keyHelp("m", "methods") + "\n")
	}
	if s.note != "" {
		b.WriteString(stWarn.Render(ansi.Truncate(s.note, m.width-2, "…")) + "\n")
	}
	b.WriteString(keyHelp("d", "deploy", "e", "edit in $EDITOR", "c", "compile", "↑↓", "scroll", "Esc", "back"))
	return b.String()
}

// ---- a deployed contract -----------------------------------------------------------------------

type contractScreen struct {
	account *keys.File
	parent  screen
	address ed25519.PublicKey
	name    string
	methods []*general.MethodDescription
	loaded  bool
	err     error

	sel    int
	inForm bool
	form   *form
	busy   bool

	result    string
	resultErr error
	state     map[string]*general.Variant
	stateErr  error
}

type contractLoadedMsg struct {
	name     string
	methods  []*general.MethodDescription
	err      error
	state    map[string]*general.Variant
	stateErr error
}

type gotMsg struct {
	method string
	value  *general.Variant
	err    error
}

// openContract opens a deployed contract's methods.
func openContract(m *model, f *keys.File, parent screen, address ed25519.PublicKey) tea.Cmd {
	s := &contractScreen{account: f, parent: parent, address: address}
	m.screen = s
	return s.load(m)
}

func (s *contractScreen) title() string {
	t := "Contract  " + keys.Short(keys.Address(s.address))
	if s.name != "" {
		t += "  (" + ansi.Truncate(printable(s.name), 20, "…") + ")"
	}
	return t
}

func (s *contractScreen) load(m *model) tea.Cmd {
	addr := s.address
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		var msg contractLoadedMsg
		if info, err := m.w.Node.Contract(ctx, addr); err == nil {
			msg.name = info.Name()
		}
		msg.methods, msg.err = m.w.Node.ContractMethods(ctx, addr)
		msg.state, msg.stateErr = m.w.Node.ContractState(ctx, addr)
		return msg
	}
}

func (s *contractScreen) selected() *general.MethodDescription {
	if s.sel >= 0 && s.sel < len(s.methods) {
		return s.methods[s.sel]
	}
	return nil
}

// newMethodForm is the form of the selected method: one input per argument, typed as declared;
// payable() takes the amount of CS and the user data of a payment.
func (s *contractScreen) newMethodForm() {
	md := s.selected()
	s.form = nil
	if md == nil {
		return
	}
	if md.Name == "payable" {
		s.form = newForm("Amount CS", "Data")
		s.form.inputs[1].Placeholder = "optional text (public): payable()'s userData"
		return
	}
	if len(md.Arguments) == 0 {
		return
	}
	labels := make([]string, len(md.Arguments))
	for i, a := range md.Arguments {
		labels[i] = ansi.Truncate(printable(a.Name), 10, "…")
	}
	s.form = newForm(labels...)
	for i, a := range md.Arguments {
		s.form.inputs[i].Placeholder = printable(node.SimpleType(a.Type))
		s.form.inputs[i].Width = 36
	}
}

func (s *contractScreen) args() []string {
	if s.form == nil {
		return nil
	}
	out := make([]string, len(s.form.inputs))
	for i := range s.form.inputs {
		out[i] = s.form.inputs[i].Value() // as typed
	}
	return out
}

// get runs the selected method as a getter: free, nothing is kept.
func (s *contractScreen) get(m *model) tea.Cmd {
	md := s.selected()
	if md == nil || md.Name == "payable" {
		return nil
	}
	s.busy, s.result, s.resultErr = true, "", nil
	addr, name, args := keys.Address(s.address), md.Name, s.args()
	caller, _ := s.account.PublicKey()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		r, err := m.w.Get(ctx, caller, addr, name, args)
		if err != nil {
			return gotMsg{method: name, err: err}
		}
		return gotMsg{method: name, value: r.Value}
	}
}

// call signs and sends the selected method (or a payment for payable), through the review.
func (s *contractScreen) call(m *model) tea.Cmd {
	md := s.selected()
	if md == nil {
		return nil
	}
	account, addr, name, args := s.account.Name, keys.Address(s.address), md.Name, s.args()
	done := func(res wallet.SendResult, err error) {
		if err != nil {
			s.result, s.resultErr = "", err
			return
		}
		s.result, s.resultErr = name+" returned "+printable(res.Returned), nil
		if res.Result == nil {
			s.result = name + ": in a block"
		}
	}
	var ps *sendScreen
	var cmd tea.Cmd
	if name == "payable" {
		ps, cmd = newPlannedScreen(m, wallet.KindPay, s.account, s, func(ctx context.Context) (*wallet.SendPlan, error) {
			return m.w.PreparePay(ctx, account, addr, strings.TrimSpace(args[0]), args[1])
		}, done)
	} else {
		ps, cmd = newPlannedScreen(m, wallet.KindCall, s.account, s, func(ctx context.Context) (*wallet.SendPlan, error) {
			return m.w.PrepareCall(ctx, account, addr, name, args)
		}, done)
	}
	m.screen = ps
	return cmd
}

func (s *contractScreen) update(m *model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case contractLoadedMsg:
		s.loaded, s.err, s.state, s.stateErr = true, msg.err, msg.state, msg.stateErr
		if msg.name != "" {
			s.name = msg.name
		}
		if msg.err == nil {
			list := append([]*general.MethodDescription(nil), msg.methods...)
			sort.SliceStable(list, func(i, j int) bool { return list[i].Name < list[j].Name })
			s.methods = list[:0]
			for _, x := range list {
				if x != nil {
					s.methods = append(s.methods, x)
				}
			}
			s.sel = min(s.sel, max(len(s.methods)-1, 0))
			s.newMethodForm()
		}
		return nil
	case gotMsg:
		s.busy = false
		if msg.err != nil {
			s.resultErr = msg.err
		} else {
			s.result = msg.method + " = " + printable(tx.Show(msg.value))
		}
		return nil
	case tea.KeyMsg:
		key := msg.String()
		if s.inForm {
			switch key {
			case "esc":
				s.inForm = false
				return nil
			case "ctrl+g":
				return s.get(m)
			case "ctrl+t":
				return s.call(m)
			}
			cmd, done := s.form.update(msg)
			if done {
				return s.defaultAction(m)
			}
			return cmd
		}
		switch key {
		case "esc", "q":
			if s.parent != nil {
				m.screen = s.parent
				if c, ok := s.parent.(*contractsScreen); ok {
					return c.load(m)
				}
				return nil
			}
			return m.back()
		case "up", "k":
			if s.sel > 0 {
				s.sel--
				s.newMethodForm()
			}
		case "down", "j":
			if s.sel < len(s.methods)-1 {
				s.sel++
				s.newMethodForm()
			}
		case "enter", "tab", "right":
			if s.form != nil {
				s.inForm = true
				return nil
			}
			if key == "enter" {
				return s.defaultAction(m)
			}
		case "g":
			return s.get(m)
		case "c":
			return s.call(m)
		case "r":
			return s.load(m)
		}
	}
	return nil
}

// defaultAction: a getter (@Getter) is read for free, payable() is a payment, any other method is
// a signed call (through the review and its confirmation).
func (s *contractScreen) defaultAction(m *model) tea.Cmd {
	md := s.selected()
	if md == nil {
		return nil
	}
	if node.IsGetter(md) {
		return s.get(m)
	}
	return s.call(m)
}

func (s *contractScreen) view(m *model) string {
	leftW := 34
	rightW := max(m.width-leftW-3, 30)
	h := max(m.height-4, 6)
	var left strings.Builder
	left.WriteString(stTitle.Render("Methods") + "\n")
	switch {
	case !s.loaded:
		left.WriteString(stDim.Render("loading…") + "\n")
	case s.err != nil:
		left.WriteString(stBad.Render(ansi.Truncate(oneLine(s.err.Error()), leftW, "…")) + "\n")
	}
	limit := h - 3
	first := max(0, min(s.sel-limit+1, len(s.methods)-limit))
	for i := first; i < len(s.methods) && i < first+limit; i++ {
		md := s.methods[i]
		tag := ""
		switch {
		case md.Name == "payable":
			tag = " pay"
		case node.IsGetter(md):
			tag = " getter"
		}
		types := make([]string, 0, len(md.Arguments))
		for _, a := range md.Arguments {
			types = append(types, node.SimpleType(a.Type))
		}
		line := ansi.Truncate(printable(md.Name+"("+strings.Join(types, ",")+")"), leftW-3-len(tag), "…")
		line = lipgloss.NewStyle().Width(leftW-2-len(tag)).Render(line) + stDim.Render(tag)
		if i == s.sel {
			left.WriteString(stSel.Render("> ") + line + "\n")
		} else {
			left.WriteString("  " + line + "\n")
		}
	}

	var right strings.Builder
	right.WriteString(keys.Address(s.address) + "\n")
	if md := s.selected(); md != nil {
		right.WriteString(stTitle.Render(ansi.Truncate(node.Signature(md), rightW, "…")) + "\n")
		if md.Name == "payable" {
			right.WriteString(stDim.Render("a payment: CS sent to the contract run payable()") + "\n")
		} else if node.IsGetter(md) {
			right.WriteString(stDim.Render("@Getter: Enter reads it, free") + "\n")
		} else {
			right.WriteString(stDim.Render("Enter signs a call; ctrl+g reads it free") + "\n")
		}
		if s.form != nil {
			fv := s.form.view()
			if !s.inForm {
				fv = stDim.Render(fv)
			}
			right.WriteString(fv)
		}
	}
	if s.busy {
		right.WriteString(stDim.Render("running…") + "\n")
	}
	if s.resultErr != nil {
		right.WriteString(stBad.Render(ansi.Truncate(oneLine(s.resultErr.Error()), rightW*2, "…")) + "\n")
	} else if s.result != "" {
		right.WriteString(stOK.Render(ansi.Truncate(s.result, rightW*2, "…")) + "\n")
	}
	right.WriteString(stTitle.Render("State") + "\n")
	switch {
	case s.stateErr != nil:
		right.WriteString(stDim.Render(ansi.Truncate("not available: "+oneLine(s.stateErr.Error()), rightW, "…")) + "\n")
	case len(s.state) == 0 && s.loaded:
		right.WriteString(stDim.Render("(empty)") + "\n")
	}
	names := make([]string, 0, len(s.state))
	for k := range s.state {
		names = append(names, k)
	}
	sort.Strings(names)
	for i, k := range names {
		if i == 5 {
			right.WriteString(stDim.Render(fmt.Sprintf("… %d more fields", len(names)-5)) + "\n")
			break
		}
		right.WriteString(ansi.Truncate(fmt.Sprintf("%-12s %s", printable(k), printable(tx.Show(s.state[k]))), rightW, "…") + "\n")
	}
	leftCol := lipgloss.NewStyle().Width(leftW).Height(h - 1).MaxHeight(h - 1).Render(left.String())
	rightCol := lipgloss.NewStyle().Width(rightW).Height(h - 1).MaxHeight(h - 1).Render(right.String())
	sep := lipgloss.NewStyle().Foreground(colDim).Render(strings.Repeat("│\n", h-2) + "│")
	help := keyHelp("Enter", "form/run", "g", "get", "c", "call", "r", "refresh", "Esc", "back")
	if s.inForm {
		help = keyHelp("Enter", "run", "ctrl+g", "get (free)", "ctrl+t", "call (signed)", "Esc", "methods")
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, leftCol, sep, " ", rightCol) + "\n" + help
}
