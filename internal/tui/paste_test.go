package tui

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

const pasteAddr = "u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w"

func pasteMsg(text string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text), Paste: true}
}

func testModel(t *testing.T) *model {
	t.Helper()
	m := newModel(wallet.New(config.Default(), config.Profile{Name: "local", Address: "127.0.0.1:1", Network: config.Mainnet}, t.TempDir()))
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

// A bracketed paste into the recipient: trimmed, "credits:" removed, the ends highlighted.
func TestPasteRecipient(t *testing.T) {
	m := testModel(t)
	f := &keys.File{Name: "main", Public: "FAe4sisG95oZ42w7buUn5qEE4TAnfTTFPiguZUHmhiF"}
	for _, kind := range []string{"send", "token", "delegate"} {
		var s *sendScreen
		field := 0
		switch kind {
		case "send":
			s = newSendScreen(f)
		case "token":
			s, field = newTokenSendScreen(f, pasteAddr), 1
		case "delegate":
			s = newDelegateScreen(f, false, "")
		}
		m.screen = s
		m.Update(pasteMsg("  \n credits:" + pasteAddr + "\r\n"))
		if got := s.fields.value(field); got != pasteAddr {
			t.Fatalf("%s: the field holds %q", kind, got)
		}
		view := m.View()
		if !strings.Contains(view, "pasted, compare: "+pasteAddr) {
			t.Errorf("%s: the pasted address is not shown:\n%s", kind, view)
		}
		if got := highlightAddress(pasteAddr); got != stHi.Render(pasteAddr[:6])+pasteAddr[6:len(pasteAddr)-6]+stHi.Render(pasteAddr[len(pasteAddr)-6:]) {
			t.Errorf("highlight: %q", got)
		}
		// typing changes it: no longer shown as pasted
		m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		if strings.Contains(m.View(), "pasted, compare") {
			t.Errorf("%s: an edited address is still shown as pasted", kind)
		}
	}
}

// Anything else pasted into an address field is refused, shown escaped, and the field kept.
func TestPasteRejected(t *testing.T) {
	m := testModel(t)
	f := &keys.File{Name: "main", Public: "FAe4sisG95oZ42w7buUn5qEE4TAnfTTFPiguZUHmhiF"}
	s := newSendScreen(f)
	m.screen = s
	for _, bad := range []string{"hello\x1b[2J", pasteAddr + "x", "0OIl" + pasteAddr[4:], "credits:", "9vX2wH7ytrbbo1KtNa13dUz3nvtJqaCH4oGrr2NEsBqd 1"} {
		m.Update(pasteMsg(bad))
		if s.fields.value(0) != "" {
			t.Errorf("%q: the field took %q", bad, s.fields.value(0))
		}
		view := m.View()
		if !strings.Contains(view, "not a CREDITS address") {
			t.Errorf("%q: no refusal:\n%s", bad, view)
		}
		if strings.Contains(view, "\x1b[2J") {
			t.Errorf("%q: a control sequence reached the screen", bad)
		}
	}
	// the next key clears the refusal
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	if strings.Contains(m.View(), "not a CREDITS address") {
		t.Error("the refusal stays after typing")
	}
}

// A paste into a secret field is never shown, even when it looks like an address.
func TestPasteIntoSecret(t *testing.T) {
	m := testModel(t)
	s := newAccountScreen(true)
	m.screen = s
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m.Update(pasteMsg(pasteAddr))
	if s.form.inputs[1].Value() != pasteAddr {
		t.Fatalf("secret field: %q", s.form.inputs[1].Value())
	}
	if strings.Contains(m.View(), pasteAddr) {
		t.Error("a pasted secret is on the screen")
	}
}

// A free-text field (a contract argument) takes a pasted text without control characters.
func TestPasteFreeText(t *testing.T) {
	f := newForm("Arg")
	f.update(pasteMsg(" a\x1b[2Jb\n"))
	if got := f.value(0); got != "a[2Jb" {
		t.Errorf("free text: %q", got)
	}
	g := newForm("Arg")
	g.update(pasteMsg("credits:" + pasteAddr))
	if g.value(0) != pasteAddr || !g.isPasted(0) {
		t.Errorf("an address in a free field: %q", g.value(0))
	}
}

// y on the main screen copies the selected account's full address (OSC 52); the address is whole
// on a line of its own.
func TestCopyOwnAddress(t *testing.T) {
	m := testModel(t)
	f := &keys.File{Name: "main", Public: pasteAddr}
	m.Update(accountsMsg{files: []*keys.File{f}})
	var buf strings.Builder
	copyTo = &buf
	defer func() { copyTo = os.Stderr }()
	t.Setenv("TMUX", "")
	t.Setenv("TERM", "xterm-256color")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(pasteAddr)) + "\a"
	if buf.String() != want {
		t.Errorf("copy: %q, want %q", buf.String(), want)
	}
	view := m.View()
	if !strings.Contains(view, "address copied") {
		t.Errorf("no note:\n%s", view)
	}
	found := false
	for _, l := range strings.Split(view, "\n") {
		if strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "│ ")) == pasteAddr || strings.HasSuffix(strings.TrimRight(l, " "), " "+pasteAddr) {
			found = true
		}
	}
	if !found {
		t.Errorf("the address is not alone on a line:\n%s", view)
	}

	// after an account is saved, y copies its address too
	buf.Reset()
	s := newAccountScreen(true)
	s.step, s.address = accSaved, pasteAddr
	m.screen = s
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if buf.String() != want || !strings.Contains(m.View(), "address copied") || m.screen != s {
		t.Errorf("copy after saving: %q\n%s", buf.String(), m.View())
	}
}
