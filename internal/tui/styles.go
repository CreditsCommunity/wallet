package tui

import (
	"strings"

	"github.com/CreditsCommunity/wallet/internal/echo"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Colours adapt to light and dark terminals (lipgloss detects the background).
var (
	colAccent  = lipgloss.AdaptiveColor{Light: "#005f87", Dark: "#5fd7ff"}
	colDim     = lipgloss.AdaptiveColor{Light: "#6c6c6c", Dark: "#8a8a8a"}
	colOK      = lipgloss.AdaptiveColor{Light: "#007700", Dark: "#5fd75f"}
	colWarn    = lipgloss.AdaptiveColor{Light: "#af5f00", Dark: "#ffaf00"}
	colBad     = lipgloss.AdaptiveColor{Light: "#af0000", Dark: "#ff5f5f"}
	colMainnet = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#000000"}

	stTitle   = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	stDim     = lipgloss.NewStyle().Foreground(colDim)
	stOK      = lipgloss.NewStyle().Foreground(colOK)
	stWarn    = lipgloss.NewStyle().Foreground(colWarn)
	stBad     = lipgloss.NewStyle().Foreground(colBad).Bold(true)
	stSel     = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	stKey     = lipgloss.NewStyle().Bold(true)
	stHi      = lipgloss.NewStyle().Bold(true).Underline(true).Foreground(colAccent) // the ends of an address
	stMainnet = lipgloss.NewStyle().Bold(true).Foreground(colMainnet).Background(lipgloss.AdaptiveColor{Light: "#af0000", Dark: "#ff5f5f"}).Padding(0, 1)
	stTestnet = lipgloss.NewStyle().Bold(true).Foreground(colMainnet).Background(lipgloss.AdaptiveColor{Light: "#007700", Dark: "#5fd75f"}).Padding(0, 1)
	stBox     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colDim).Padding(0, 1)
)

// keyHelp renders "[s] send  [h] history".
func keyHelp(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, stKey.Render("["+pairs[i]+"]")+" "+pairs[i+1])
	}
	return strings.Join(parts, "  ")
}

// form is a column of labelled text inputs; Tab, Shift+Tab, Up and Down move between them.
type form struct {
	labels []string
	inputs []textinput.Model
	focus  int

	addr       map[int]bool   // fields that take only an address (a paste of anything else is refused)
	pasted     map[int]string // the address pasted into a field (highlighted while it is unchanged)
	pasteErr   string         // the refusal of the last paste
	pasteErrAt int
}

func newForm(labels ...string) *form {
	f := &form{labels: labels, addr: map[int]bool{}, pasted: map[int]string{}, pasteErrAt: -1}
	for range labels {
		in := textinput.New()
		in.Prompt = ""
		in.CharLimit = 4096
		in.Width = 50
		// Ctrl+V would read the clipboard through external programs (xclip, pbpaste): off. The
		// terminal's own paste arrives as a bracketed paste (see paste.go).
		in.KeyMap.Paste = key.NewBinding(key.WithDisabled())
		f.inputs = append(f.inputs, in)
	}
	f.inputs[0].Focus()
	return f
}

func (f *form) secret(i int) *form {
	f.inputs[i].EchoMode = textinput.EchoPassword
	f.inputs[i].EchoCharacter = '•'
	return f
}

// address marks fields that take only an address.
func (f *form) address(fields ...int) *form {
	for _, i := range fields {
		f.addr[i] = true
	}
	return f
}

func (f *form) value(i int) string { return strings.TrimSpace(f.inputs[i].Value()) }

func (f *form) setFocus(i int) {
	f.pasteErr, f.pasteErrAt = "", -1
	f.inputs[f.focus].Blur()
	f.focus = (i + len(f.inputs)) % len(f.inputs)
	f.inputs[f.focus].Focus()
}

// update handles a key; it returns true when Enter was pressed on the last field.
func (f *form) update(msg tea.Msg) (tea.Cmd, bool) {
	if k, ok := msg.(tea.KeyMsg); ok {
		if k.Paste {
			return f.paste(k), false
		}
		f.pasteErr, f.pasteErrAt = "", -1
		switch k.String() {
		case "tab", "down":
			f.setFocus(f.focus + 1)
			return nil, false
		case "shift+tab", "up":
			f.setFocus(f.focus - 1)
			return nil, false
		case "enter":
			if f.focus == len(f.inputs)-1 {
				return nil, true
			}
			f.setFocus(f.focus + 1)
			return nil, false
		}
	}
	var cmd tea.Cmd
	f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
	return cmd, false
}

func (f *form) view() string {
	var b strings.Builder
	for i, in := range f.inputs {
		label := lipgloss.NewStyle().Width(10).Render(f.labels[i])
		if i == f.focus {
			label = stSel.Width(10).Render(f.labels[i])
		}
		b.WriteString(label + " " + in.View() + "\n")
		b.WriteString(f.pastedLine(i))
	}
	return b.String()
}

// wipe clears the secret inputs of a form.
func (f *form) wipe() {
	for i := range f.inputs {
		f.inputs[i].SetValue("")
	}
}

// printable: see echo.Printable.
func printable(s string) string { return echo.Printable(s) }

// truncate cuts a line to the screen's width (less the margin).
func truncate(s string, width int) string { return ansi.Truncate(s, max(width-2, 10), "…") }
