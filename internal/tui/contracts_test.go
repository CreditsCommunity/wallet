package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	base := wallet.SendPlan{Account: "main", From: pub, To: pub, Amount: amt, Fee: fee, Balance: amt, InnerID: 7, Network: config.Mainnet, MaxFee: tx.MaxFee,
		NetworkCheck: wallet.NetworkCheck{Why: "its diagnostic API 127.0.0.1:9088 does not answer"}}
	deploy := base
	deploy.Kind = wallet.KindDeploy
	deploy.Contract = &wallet.ContractPlan{Address: pub, Name: "TestToken\x1b[2J", File: file, Methods: 14,
		Deploy: &tx.ContractDeploy{Classes: []*general.ByteCodeObject{{Name: "TestToken"}}, TokenStandard: 4}}
	call := base
	call.Kind = wallet.KindCall
	long := strings.Repeat("9", 150) + "END"
	call.Contract = &wallet.ContractPlan{Address: pub, Name: "TestToken", Method: contractMethods()[4], Args: []string{"1", "2\x1b[2J", long}}
	pay := base
	pay.Kind, pay.Data = wallet.KindPay, "tip \x1b]0;x\a"
	pay.Contract = &wallet.ContractPlan{Address: pub, Name: "TipJar"}
	for _, p := range []wallet.SendPlan{deploy, call, pay} {
		s := &sendScreen{kind: p.Kind, account: f, plan: &p, step: sendReview, confirm: newForm("Confirm", "Password").secret(1)}
		m.screen = s
		view := m.View()
		fits(t, string(p.Kind), view)
		// page through a review taller than the screen: everything is shown, nothing is cut
		all := view
		for range 6 {
			s.update(m, tea.KeyMsg{Type: tea.KeyPgDown})
			v := m.View()
			fits(t, string(p.Kind)+" scrolled", v)
			all += v
		}
		if !strings.Contains(all, "This sends a real "+string(p.Kind)+" on MAINNET") || !strings.Contains(all, "Max fee") ||
			!strings.Contains(all, "not confirmed by the node (its diagnostic API") || !s.seenEnd {
			t.Errorf("%s review:\n%s", p.Kind, all)
		}
		if p.Kind == wallet.KindCall {
			flat := strings.NewReplacer("│", "", " ", "", "\n", "").Replace(all)
			if !strings.Contains(flat, "c(double)="+strings.Repeat("9", 60)) || !strings.Contains(flat, "END") || strings.Contains(all, "more arguments") {
				t.Errorf("an argument is cut:\n%s", all)
			}
		}
		s.step, s.result = sendDone, wallet.SendResult{Returned: "ok\x1b[2J", Result: &general.Variant{}, Contract: pub}
		fits(t, string(p.Kind)+" done", m.View())
	}
}

// A review that does not fit is not signed before it was read to the end (security review L1).
func TestReviewMustBeReadToTheEnd(t *testing.T) {
	w := wallet.New(config.Default(), config.Profile{Name: "local", Address: "127.0.0.1:1", Network: config.Testnet}, t.TempDir())
	m := newModel(w)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	f := &keys.File{Name: "main", Public: "u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w"}
	m.unlocked = &wallet.Unlocked{Account: f}
	pub, _ := keys.ParseAddress(f.Public)
	args := make([]string, 12)
	md := &general.MethodDescription{Name: "many", ReturnType: "void"}
	for i := range args {
		args[i] = strings.Repeat("x", 100)
		md.Arguments = append(md.Arguments, &general.MethodArgument{Type: "java.lang.String", Name: fmt.Sprint("a", i)})
	}
	p := wallet.SendPlan{Kind: wallet.KindCall, Account: "main", From: pub, To: pub, InnerID: 7, Network: config.Testnet, MaxFee: tx.MaxFee,
		Contract: &wallet.ContractPlan{Address: pub, Method: md, Args: args}, Prepared: time.Now(),
		NetworkCheck: wallet.NetworkCheck{Confirmed: true, ID: node.NetworkID{UUID: 42}}}
	s := &sendScreen{kind: p.Kind, account: f, plan: &p, step: sendReview, confirm: newForm("Password").secret(0)}
	m.screen = s
	if v := m.View(); !strings.Contains(v, "confirmed by the node (uuid 42)") || s.seenEnd {
		t.Fatalf("first page:\n%s", v)
	}
	s.confirm.inputs[0].SetValue("pw")
	if cmd := s.update(m, tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil || s.step != sendReview || s.err == nil {
		t.Fatal("signed a review that was not read to the end")
	}
	for !s.seenEnd {
		s.update(m, tea.KeyMsg{Type: tea.KeyPgDown})
		m.View()
	}
	s.confirm.inputs[0].SetValue("pw")
	if cmd := s.update(m, tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil || s.step != sendSending {
		t.Errorf("not signed after reading it all: %v", s.err)
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
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts as editors")
	}
	bin := t.TempDir()
	for _, name := range []string{"code", "myedit"} {
		os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755)
	}
	t.Setenv("PATH", bin)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "code -w")
	c, err := editorCommand("/tmp/A.java")
	if err != nil || len(c.Args) != 3 || c.Args[1] != "-w" || c.Args[2] != "/tmp/A.java" || c.Path != filepath.Join(bin, "code") {
		t.Errorf("EDITOR with arguments: %v %v", c, err)
	}
	t.Setenv("VISUAL", "myedit")
	if c, err := editorCommand("x; rm -rf ~"); err != nil || len(c.Args) != 2 || c.Args[1] != "x; rm -rf ~" {
		t.Errorf("no shell: %v %v", c, err)
	}
	// a file name the editor could take for an option or a command
	for in, want := range map[string]string{"-rf.java": "./-rf.java", "+qa.java": "./+qa.java", "/x/-a.java": "/x/-a.java", "a-b.java": "a-b.java"} {
		if c, err := editorCommand(in); err != nil || c.Args[len(c.Args)-1] != want {
			t.Errorf("%s: %v %v", in, c, err)
		}
	}
	// a bad $EDITOR: a clear error, nothing run
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "nosuch-editor --flag")
	if _, err := editorCommand("A.java"); err == nil || !strings.Contains(err.Error(), `$EDITOR, "nosuch-editor", is not found`) {
		t.Errorf("bad $EDITOR: %v", err)
	}
	t.Setenv("EDITOR", "")
	if _, err := editorCommand("A.java"); err == nil || !strings.Contains(err.Error(), "no editor found") {
		t.Errorf("no editor at all: %v", err)
	}
}
