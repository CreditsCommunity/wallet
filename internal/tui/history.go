package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CreditsCommunity/wallet/internal/history"
	"github.com/CreditsCommunity/wallet/internal/keys"
)

type historyScreen struct {
	account  *keys.File
	pageSize int
	offset   int
	page     history.Page
	loading  bool
	note     string
	err      error
}

type historyMsg struct {
	page history.Page
	err  error
}
type exportedMsg struct {
	file string
	n    int
	err  error
}

func newHistoryScreen(f *keys.File, pageSize int) *historyScreen {
	return &historyScreen{account: f, pageSize: pageSize}
}

func (s *historyScreen) title() string { return "History  " + s.account.Name }

func (s *historyScreen) load(m *model) tea.Cmd {
	s.loading = true
	offset, size := s.offset, s.pageSize
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		pub, err := s.account.PublicKey()
		if err != nil {
			return historyMsg{err: err}
		}
		p, err := m.w.History().Page(ctx, pub, offset, size)
		return historyMsg{p, err}
	}
}

func (s *historyScreen) export(m *model) tea.Cmd {
	s.note = "exporting…"
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		pub, err := s.account.PublicKey()
		if err != nil {
			return exportedMsg{err: err}
		}
		all, err := history.All(ctx, m.w.History(), pub, 100)
		if err != nil {
			return exportedMsg{err: err}
		}
		name := fmt.Sprintf("history-%s-%s.csv", s.account.Name, time.Now().Format("20060102-150405"))
		out, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return exportedMsg{err: err}
		}
		if err := history.WriteCSV(out, s.account.Public, all); err != nil {
			out.Close()
			return exportedMsg{err: err}
		}
		return exportedMsg{file: name, n: len(all), err: out.Close()}
	}
}

func (s *historyScreen) update(m *model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case historyMsg:
		s.loading = false
		s.page, s.err = msg.page, msg.err
	case exportedMsg:
		if msg.err != nil {
			s.note, s.err = "", msg.err
		} else {
			wd, _ := os.Getwd()
			s.note = fmt.Sprintf("%d transactions written to %s in %s", msg.n, msg.file, wd)
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q":
			return m.back()
		case "right", "pgdown", "n", "l":
			if !s.loading && s.offset+s.pageSize < s.page.Total && len(s.page.Entries) == s.pageSize {
				s.offset += s.pageSize
				return s.load(m)
			}
		case "left", "pgup", "p", "h":
			if !s.loading && s.offset > 0 {
				s.offset = max(0, s.offset-s.pageSize)
				return s.load(m)
			}
		case "e":
			return s.export(m)
		case "r":
			return s.load(m)
		}
	}
	return nil
}

func (s *historyScreen) view(m *model) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s on %s, %d transactions in all", s.account.Public, m.w.History().Describe(), s.page.Total)
	if m.oldest > 1 {
		fmt.Fprintf(&b, "; history from block %s on this node (pruned)", groupInt(m.oldest))
	}
	b.WriteString("\n\n")
	switch {
	case s.loading && len(s.page.Entries) == 0:
		b.WriteString(stDim.Render("loading…") + "\n")
	case len(s.page.Entries) == 0 && s.page.Total > 0:
		b.WriteString(stDim.Render("None of them is on this node: it may be pruned; a full node has the older history.") + "\n")
	case len(s.page.Entries) == 0:
		b.WriteString(stDim.Render("no transactions") + "\n")
	}
	width := m.width - 2
	rows := max(m.height-7, 3)
	for i, e := range s.page.Entries {
		if i >= rows {
			break
		}
		line := entryLine(e, width-16)
		b.WriteString(line + stDim.Render(fmt.Sprintf("  fee %s", e.Fee.Fixed(6))) + "\n")
	}
	if n := len(s.page.Entries); n > 0 {
		fmt.Fprintf(&b, "\n%s\n", stDim.Render(fmt.Sprintf("%d–%d of %d", s.offset+1, s.offset+n, s.page.Total)))
	}
	if s.note != "" {
		b.WriteString(stOK.Render(s.note) + "\n")
	}
	if s.err != nil {
		b.WriteString(stBad.Render(oneLine(s.err.Error())) + "\n")
	}
	b.WriteString(keyHelp("←/→", "page", "e", "export CSV", "r", "refresh", "Esc", "back"))
	return b.String()
}
