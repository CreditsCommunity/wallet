package tui

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/CreditsCommunity/wallet/internal/keys"
)

// Pasting an address. The terminal's paste (Ctrl+Shift+V, right click, Cmd+V) reaches the wallet
// as a bracketed paste: one key message with Paste set and the whole text. The text is untrusted
// (malware may have replaced what the user copied): it is trimmed, a leading "credits:" goes, and
// in an address field anything but a base58 32-byte address is refused, and only its length is
// shown (it may be a secret pasted by mistake). A pasted
// address is shown with its first and last 6 characters highlighted, to compare with the source.
// The wallet never reads the clipboard itself (that would need external programs).

// cleanPasted trims spaces and newlines and a leading "credits:".
func cleanPasted(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 8 && strings.EqualFold(s[:8], "credits:") {
		s = strings.TrimSpace(s[8:])
	}
	return s
}

// withoutControls drops control characters (escape sequences, newlines) from pasted text.
func withoutControls(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// highlightAddress shows an address with its first and last 6 characters highlighted.
func highlightAddress(addr string) string {
	if len(addr) <= 12 {
		return stHi.Render(addr)
	}
	return stHi.Render(addr[:6]) + addr[6:len(addr)-6] + stHi.Render(addr[len(addr)-6:])
}

// paste handles a bracketed paste into the focused field.
func (f *form) paste(k tea.KeyMsg) tea.Cmd {
	i := f.focus
	f.pasteErr, f.pasteErrAt = "", -1
	if f.inputs[i].EchoMode != textinput.EchoNormal {
		// a secret (a password, a key, a phrase): passed on as typed, never shown
		var cmd tea.Cmd
		f.inputs[i], cmd = f.inputs[i].Update(k)
		return cmd
	}
	text := cleanPasted(string(k.Runes))
	if pub, err := keys.ParseAddress(text); err == nil && text != "" {
		addr := keys.Address(pub)
		f.inputs[i].SetValue(addr)
		f.inputs[i].CursorEnd()
		f.pasted[i] = addr
		return nil
	}
	if f.addr[i] {
		// never the text itself: what was pasted may be a secret key or a phrase
		f.pasteErr = fmt.Sprintf("the pasted text (%d characters) is not a CREDITS address (base58 of 32 bytes): not shown, not taken", utf8.RuneCountInString(text))
		f.pasteErrAt = i
		return nil
	}
	k.Runes = []rune(withoutControls(text))
	var cmd tea.Cmd
	f.inputs[i], cmd = f.inputs[i].Update(k)
	return cmd
}

// pastedLine is the line under a field holding a pasted address (or the refusal of a paste).
func (f *form) pastedLine(i int) string {
	if f.pasteErrAt == i && f.pasteErr != "" {
		return stBad.Render(f.pasteErr) + "\n"
	}
	if a, ok := f.pasted[i]; ok && a == f.value(i) {
		return "           " + stDim.Render("pasted, compare: ") + highlightAddress(a) + "\n"
	}
	return ""
}

// isPasted tells whether field i holds the address pasted into it.
func (f *form) isPasted(i int) bool {
	a, ok := f.pasted[i]
	return ok && a == f.value(i)
}
