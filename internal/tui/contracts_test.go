package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/CreditsCommunity/wallet/internal/amount"
	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
	"github.com/CreditsCommunity/wallet/internal/templates"
	"github.com/CreditsCommunity/wallet/internal/tx"
	"github.com/CreditsCommunity/wallet/internal/wallet"
	"github.com/CreditsCommunity/wallet/thrift/general"
)

func fits(t *testing.T, name string, view string) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > 24 {
		t.Errorf("%s: %d lines", name, len(lines))
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w > 80 {
			t.Errorf("%s line %d: %d columns: %q", name, i, w, l)
		}
	}
	if !strings.Contains(lines[0], "MAINNET") {
		t.Errorf("%s: the network is not on the first line", name)
	}
	for _, esc := range []string{"\x1b[2J", "\x1b]0;"} {
		if strings.Contains(view, esc) {
			t.Errorf("%s: a terminal escape from a contract reached the screen", name)
		}
	}
	if testing.Verbose() {
		t.Logf("%s:\n%s", name, view)
	}
}

func contractMethods() []*general.MethodDescription {
	arg := func(t, n string) *general.MethodArgument { return &general.MethodArgument{Type: t, Name: n} }
	getter := []*general.Annotation{{Name: node.GetterAnnotation}}
	return []*general.MethodDescription{
		{Name: "transfer", ReturnType: "boolean", Arguments: []*general.MethodArgument{arg("java.lang.String", "to"), arg("java.math.BigDecimal", "amount")}},
		{Name: "balanceOf", ReturnType: "java.math.BigDecimal", Arguments: []*general.MethodArgument{arg("java.lang.String", "owner")}, Annotations: getter},
		{Name: "getName\x1b[2J", ReturnType: "java.lang.String", Annotations: getter},
		{Name: "payable", ReturnType: "java.lang.String", Arguments: []*general.MethodArgument{arg("java.math.BigDecimal", "amount"), arg("byte[]", "userData")}},
		{Name: "aVeryLongMethodNameThatGoesOnAndOn", ReturnType: "void", Arguments: []*general.MethodArgument{arg("int", "a"), arg("long", "b"), arg("double", "c")}},
	}
}

func TestContractScreensFit80x24(t *testing.T) {
	w := wallet.New(config.Default(), config.Profile{Name: "local", Address: "127.0.0.1:1", Network: config.Mainnet}, t.TempDir())
	m := newModel(w)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	f := &keys.File{Name: "main", Public: "u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w"}
	pub, _ := keys.ParseAddress(f.Public)
	m.Update(accountsMsg{files: []*keys.File{f}})

	list := newContractsScreen(f)
	rows := []wallet.ContractRow{{Address: pub, Name: "TestToken", Created: time.Now(), OnNode: true},
		{Address: pub, Name: "Evil\x1b]0;x\a", Created: time.Now(), Local: &wallet.LocalContract{Network: "private"}}}
	for range 30 {
		rows = append(rows, rows[0])
	}
	list.update(m, contractsMsg{rows: rows})

	dir := t.TempDir()
	file := filepath.Join(dir, "TestToken.java")
	source, _, _ := templates.Get("TestToken")
	os.WriteFile(file, []byte(source+"\n// \x1b[2J\n"), 0o644)
	src := &sourceScreen{account: f, file: file}
	src.reload(m)
	src.update(m, compiledMsg{source: src.source, err: &node.CompileFailure{Errors: []node.CompileError{
		{Line: 12, Text: "cannot find symbol\n  symbol: x \x1b[2J"}, {Line: 30, Text: "';' expected"}, {Line: 31, Text: "x"}}}})
	ok := &sourceScreen{account: f, file: file}
	ok.reload(m)
	ok.update(m, compiledMsg{source: ok.source, comp: &wallet.Compilation{Compiled: node.Compiled{Classes: []*general.ByteCodeObject{{Name: "TestToken"}}},
		Methods: contractMethods()}})
	ok.deployed = pub

	cs := &contractScreen{account: f, address: pub}
	cs.update(m, contractLoadedMsg{name: "TestToken\x1b[2J", methods: contractMethods(), state: map[string]*general.Variant{
		"owner": {VString: &f.Public}, "note\x1b[2J": {VString: &f.Public}, "a": {}, "b": {}, "c": {}, "d": {}, "e": {}}})
	cs.result = "balanceOf\x1b]0;x\a = 12\x1b[2J" // raw: escaped where it is rendered

	for name, s := range map[string]screen{"contracts": list, "source with errors": src, "source compiled": ok, "contract": cs} {
		m.screen = s
		fits(t, name, m.View())
	}
	// the method form of every method
	for i := range cs.methods {
		cs.sel = i
		cs.newMethodForm()
		cs.inForm = cs.form != nil
		m.screen = cs
		fits(t, "method "+cs.methods[i].Name, m.View())
	}

	// the reviews of a deploy, a call and a payment
	amt, _ := amount.Parse("3")
	fee, _ := amount.Parse("0.0175")
	base := wallet.SendPlan{Account: "main", From: pub, To: pub, Amount: amt, Fee: fee, Balance: amt, InnerID: 7, Network: config.Mainnet, MaxFee: tx.MaxFee}
	deploy := base
	deploy.Kind = wallet.KindDeploy
	deploy.Contract = &wallet.ContractPlan{Address: pub, Name: "TestToken\x1b[2J", File: file, Methods: 14,
		Deploy: &tx.ContractDeploy{Classes: []*general.ByteCodeObject{{Name: "TestToken"}}, TokenStandard: 4}}
	call := base
	call.Kind = wallet.KindCall
	call.Contract = &wallet.ContractPlan{Address: pub, Name: "TestToken", Method: contractMethods()[4], Args: []string{"1", "2\x1b[2J", strings.Repeat("9", 100)}}
	pay := base
	pay.Kind, pay.Data = wallet.KindPay, "tip \x1b]0;x\a"
	pay.Contract = &wallet.ContractPlan{Address: pub, Name: "TipJar"}
	for _, p := range []wallet.SendPlan{deploy, call, pay} {
		s := &sendScreen{kind: p.Kind, account: f, plan: &p, step: sendReview, confirm: newForm("Confirm", "Password").secret(1)}
		m.screen = s
		view := m.View()
		fits(t, string(p.Kind), view)
		if !strings.Contains(view, "This sends a real "+string(p.Kind)+" on MAINNET") || !strings.Contains(view, "Max fee") {
			t.Errorf("%s review:\n%s", p.Kind, view)
		}
		s.step, s.result = sendDone, wallet.SendResult{Returned: "ok\x1b[2J", Result: &general.Variant{}, Contract: pub}
		fits(t, string(p.Kind)+" done", m.View())
	}
}

// Keys of the contract screen: a getter runs on Enter; a call opens the review (no signing without
// the confirmation); Esc goes back.
func TestContractScreenKeys(t *testing.T) {
	w := wallet.New(config.Default(), config.Profile{Name: "local", Address: "127.0.0.1:1", Network: config.Mainnet}, t.TempDir())
	m := newModel(w)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	f := &keys.File{Name: "main", Public: "u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w"}
	pub, _ := keys.ParseAddress(f.Public)
	list := newContractsScreen(f)
	cs := &contractScreen{account: f, address: pub, parent: list}
	m.screen = cs
	cs.update(m, contractLoadedMsg{methods: contractMethods()})
	// sorted: aVeryLong…, balanceOf, getName, payable, transfer
	if cs.selected().Name != "aVeryLongMethodNameThatGoesOnAndOn" {
		t.Fatalf("first method %s", cs.selected().Name)
	}
	key := func(k string) tea.Cmd {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		_, cmd := m.Update(msg)
		return cmd
	}
	key("down") // balanceOf, a getter with one argument
	if cs.selected().Name != "balanceOf" || cs.form == nil || len(cs.form.inputs) != 1 {
		t.Fatalf("balanceOf form: %+v", cs.form)
	}
	key("enter") // into the form
	if !cs.inForm {
		t.Fatal("Enter did not open the form")
	}
	for _, r := range f.Public {
		key(string(r))
	}
	if cs.args()[0] != f.Public {
		t.Fatalf("typed %q", cs.args())
	}
	if cmd := key("enter"); cmd == nil || !cs.busy {
		t.Error("Enter on a getter's form does not read it")
	}
	if _, ok := m.screen.(*contractScreen); !ok {
		t.Error("a getter left the screen")
	}
	key("esc")
	key("down")
	key("down")
	key("down") // transfer: not a getter
	if cs.selected().Name != "transfer" {
		t.Fatalf("selected %s", cs.selected().Name)
	}
	key("enter")
	key("x")
	key("enter")
	cmd := key("enter") // last field: a call goes to the review, nothing is signed here
	if ps, ok := m.screen.(*sendScreen); !ok || ps.kind != wallet.KindCall || ps.step != sendPreparing || cmd == nil {
		t.Fatalf("a call did not open the review: %T", m.screen)
	}
	ps := m.screen.(*sendScreen)
	ps.update(m, preparedMsg{err: os.ErrNotExist}) // the check failed: Enter checks again, Esc goes back
	if ps.step != sendFields || !strings.Contains(m.View(), "check again") {
		t.Errorf("failed check:\n%s", m.View())
	}
	key("esc")
	if m.screen != cs {
		t.Errorf("Esc from the review goes back to the contract: %T", m.screen)
	}
	key("esc")
	key("esc")
	if m.screen != list {
		t.Errorf("Esc goes back to the list: %T", m.screen)
	}
}

func TestEditorCommand(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "code -w")
	c := editorCommand("/tmp/A.java")
	if len(c.Args) != 3 || c.Args[1] != "-w" || c.Args[2] != "/tmp/A.java" {
		t.Errorf("EDITOR with arguments: %v", c.Args)
	}
	t.Setenv("VISUAL", "myedit")
	if c := editorCommand("x; rm -rf ~"); len(c.Args) != 2 || c.Args[1] != "x; rm -rf ~" {
		t.Errorf("no shell: %v", c.Args)
	}
}

// Results carry the contract's method names and values: escaped where the screen renders them,
// whichever path set them (a getter, a call's outcome).
func TestContractResultsEscaped(t *testing.T) {
	w := wallet.New(config.Default(), config.Profile{Name: "local", Address: "127.0.0.1:1", Network: config.Mainnet}, t.TempDir())
	m := newModel(w)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	f := &keys.File{Name: "main", Public: "u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w"}
	pub, _ := keys.ParseAddress(f.Public)
	cs := &contractScreen{account: f, address: pub}
	m.screen = cs
	evil := "v\x1b[2J"
	cs.update(m, gotMsg{method: "get\x1b]0;x\a", value: &general.Variant{VString: &evil}})
	fits(t, "getter result", m.View())
	methods := contractMethods()
	cs.update(m, contractLoadedMsg{methods: methods})
	cs.sel = len(cs.methods) - 1
	cs.newMethodForm()
	cs.methods[cs.sel].Name = "x\x1b[2J"
	cs.call(m) // the review screen; its onDone sets the result as the call's outcome would
	ps := m.screen.(*sendScreen)
	ps.onDone(wallet.SendResult{Returned: "r\x1b]0;y\a", Result: &general.Variant{}}, nil)
	m.screen = cs
	fits(t, "call result", m.View())
	ps.onDone(wallet.SendResult{}, nil)
	fits(t, "call in a block", m.View())
}
