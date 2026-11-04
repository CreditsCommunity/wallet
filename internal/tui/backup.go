package tui

import (
	"bytes"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CreditsCommunity/wallet/internal/echo"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

// The backup screen (b, on the unlock screen and the main screen): export every account of the
// key store into one file sealed under a backup password, or import such a file (spec 4.5).

type backupStep int

const (
	bkChoose     backupStep = iota
	bkExportForm            // File, Password, Repeat
	bkImportForm            // File, Password
	bkWorking               // Argon2id, writing or opening
	bkRename                // a name of the backup is another account's here: a new name
	bkConfirm               // the accounts of the backup, and what the restore does
	bkDone
)

type backupScreen struct {
	step backupStep
	form *form
	err  error

	// export
	written  string
	count    int
	problems []error

	// import
	backup  *wallet.Backup
	plans   []wallet.RestorePlan
	names   []string
	taken   map[string]bool
	renameI int // the plan being renamed
	results []wallet.RestoreResult
	note    string
}

type backupWrittenMsg struct {
	path     string
	count    int
	problems []error
	err      error
}

type backupOpenedMsg struct {
	b     *wallet.Backup
	plans []wallet.RestorePlan
	err   error
}

type backupRestoredMsg struct {
	results []wallet.RestoreResult
}

func newBackupScreen() *backupScreen { return &backupScreen{} }

func (s *backupScreen) title() string { return "Backup" }

func (s *backupScreen) forget() {
	if s.form != nil {
		s.form.wipe()
	}
	s.backup.Wipe()
	s.backup = nil
}

func (s *backupScreen) update(m *model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case backupWrittenMsg:
		s.step, s.err = bkDone, msg.err
		s.written, s.count, s.problems = msg.path, msg.count, msg.problems
		if msg.err != nil {
			s.step = bkExportForm
		}
		return nil
	case backupOpenedMsg:
		if msg.err != nil {
			s.step, s.err = bkImportForm, msg.err
			return nil
		}
		s.backup, s.plans, s.err = msg.b, msg.plans, nil
		s.names = make([]string, len(s.plans))
		s.taken = map[string]bool{}
		for i, p := range s.plans {
			if p.State == wallet.RestoreNew {
				s.names[i] = p.Account.Name
				s.taken[p.Account.Name] = true
			}
		}
		s.renameI = -1
		s.nextRename()
		return nil
	case backupRestoredMsg:
		s.results = msg.results
		s.backup.Wipe()
		s.backup = nil
		s.step = bkDone
		return m.loadAccounts()
	case tea.KeyMsg:
		if msg.String() == "esc" && s.step != bkWorking {
			s.forget()
			return m.back()
		}
	}
	switch s.step {
	case bkChoose:
		k, ok := msg.(tea.KeyMsg)
		if !ok {
			return nil
		}
		switch k.String() {
		case "e":
			s.form = newForm("File", "Password", "Repeat").secret(1).secret(2)
			if def, err := wallet.DefaultBackupPath(time.Now()); err == nil {
				s.form.inputs[0].SetValue(def)
				s.form.inputs[0].CursorEnd()
			}
			s.step = bkExportForm
		case "i":
			s.form = newForm("File", "Password").secret(1)
			s.step = bkImportForm
		}
		return nil
	case bkExportForm:
		cmd, done := s.form.update(msg)
		if !done {
			return cmd
		}
		path, err := m.w.CheckBackupPath(s.form.value(0))
		if err != nil {
			s.err = err
			return nil
		}
		pw, again := []byte(s.form.inputs[1].Value()), []byte(s.form.inputs[2].Value())
		defer keys.Zero(again)
		if len(pw) == 0 || !bytes.Equal(pw, again) {
			keys.Zero(pw)
			s.err = fmt.Errorf("the passwords are empty or differ")
			return nil
		}
		s.form.wipe()
		s.err, s.step = nil, bkWorking
		w := m.w
		return func() tea.Msg {
			defer keys.Zero(pw)
			defer debug.FreeOSMemory()
			b, problems, err := w.CollectBackup(time.Now())
			if err != nil {
				return backupWrittenMsg{problems: problems, err: err}
			}
			defer b.Wipe()
			if err := wallet.WriteBackup(path, b, pw); err != nil {
				return backupWrittenMsg{problems: problems, err: err}
			}
			return backupWrittenMsg{path: path, count: len(b.Accounts), problems: problems}
		}
	case bkImportForm:
		cmd, done := s.form.update(msg)
		if !done {
			return cmd
		}
		path := wallet.ExpandHome(s.form.value(0))
		pw := []byte(s.form.inputs[1].Value())
		s.form.inputs[1].SetValue("")
		if path == "" || len(pw) == 0 {
			keys.Zero(pw)
			s.err = fmt.Errorf("the file and the backup password, please")
			return nil
		}
		s.err, s.step = nil, bkWorking
		w := m.w
		return func() tea.Msg {
			defer keys.Zero(pw)
			defer debug.FreeOSMemory()
			data, err := wallet.ReadBackupFile(path)
			if err != nil {
				return backupOpenedMsg{err: err}
			}
			b, err := wallet.OpenBackup(data, pw)
			if err != nil {
				return backupOpenedMsg{err: err}
			}
			plans, err := w.PlanRestore(b)
			if err != nil {
				b.Wipe()
				return backupOpenedMsg{err: err}
			}
			return backupOpenedMsg{b: b, plans: plans}
		}
	case bkRename:
		cmd, done := s.form.update(msg)
		if !done {
			return cmd
		}
		name := s.form.value(0)
		if name != "" {
			if err := m.w.FreeRestoreName(name, s.taken); err != nil {
				s.err = err
				return nil
			}
			s.names[s.renameI], s.taken[name] = name, true
		}
		s.err = nil
		s.nextRename()
		return nil
	case bkConfirm:
		k, ok := msg.(tea.KeyMsg)
		if !ok || k.String() != "enter" {
			return nil
		}
		s.step = bkWorking
		w, plans, names := m.w, s.plans, s.names
		return func() tea.Msg {
			results, _, _ := w.Restore(plans, names)
			return backupRestoredMsg{results}
		}
	case bkDone:
		if _, ok := msg.(tea.KeyMsg); ok {
			return m.back()
		}
	}
	return nil
}

// nextRename asks a new name for the next account whose name is taken, or goes on to the
// confirmation.
func (s *backupScreen) nextRename() {
	for i := s.renameI + 1; i < len(s.plans); i++ {
		if s.plans[i].State == wallet.RestoreNameTaken {
			s.renameI = i
			s.form = newForm("New name")
			s.step = bkRename
			return
		}
	}
	s.step = bkConfirm
}

func (s *backupScreen) view(m *model) string {
	var b strings.Builder
	switch s.step {
	case bkChoose:
		b.WriteString("Every account of the key store in one file, sealed under a backup password.\n\n")
		b.WriteString(keyHelp("e", "export a backup", "i", "import a backup", "Esc", "back") + "\n")
		b.WriteString("\n" + stDim.Render("The key files go in as they are, each encrypted under its own password, with the") + "\n")
		b.WriteString(stDim.Render("token and contract lists. Not the node's key, not config.toml.") + "\n")
	case bkExportForm:
		b.WriteString("Export a backup: a new file, outside the wallet's folder.\n\n")
		b.WriteString(s.form.view())
		if pw := s.form.inputs[1].Value(); pw != "" {
			if hint := wallet.PasswordHint([]byte(pw)); hint != "" {
				b.WriteString(stWarn.Render(hint) + "\n")
			}
		}
		b.WriteString("\n" + stDim.Render("The backup password protects the whole file; each account keeps its own inside.") + "\n")
		b.WriteString(stDim.Render(wallet.BackupNotIncluded) + "\n")
		b.WriteString("\n" + keyHelp("Enter", "next / write", "Esc", "cancel") + "\n")
	case bkImportForm:
		b.WriteString("Import a backup (.dat).\n\n")
		b.WriteString(s.form.view())
		b.WriteString("\n" + keyHelp("Enter", "next / open", "Esc", "cancel") + "\n")
	case bkWorking:
		b.WriteString(stDim.Render("Argon2id and the backup file…") + "\n")
	case bkRename:
		p := s.plans[s.renameI]
		fmt.Fprintf(&b, "%s (%s) is in the backup, but the name %q\n", printable(p.Account.Name), p.Account.Address, p.Account.Name)
		b.WriteString("is another account's here. A new name for it, or Enter to skip it.\n\n")
		b.WriteString(s.form.view())
		b.WriteString("\n" + keyHelp("Enter", "this name (empty: skip)", "Esc", "cancel") + "\n")
	case bkConfirm:
		fmt.Fprintf(&b, "The backup of %s holds %d accounts:\n\n", s.backup.Created.Format("2006-01-02 15:04 UTC"), len(s.plans))
		count := 0
		for i, p := range s.plans {
			what := "restore"
			switch {
			case p.State == wallet.RestoreExists:
				what = stDim.Render("here as " + printable(p.Existing) + ": skipped")
			case s.names[i] == "":
				what = stWarn.Render("name taken: skipped")
			case s.names[i] != p.Account.Name:
				what = "restore as " + printable(s.names[i])
				count++
			default:
				count++
			}
			fmt.Fprintf(&b, "%-18s %s  %s\n", printable(truncName(p.Account.Name)), keys.Short(p.Account.Address), what)
		}
		b.WriteString("\n" + stDim.Render(wallet.BackupPasswordsNote) + "\n")
		b.WriteString("\n" + keyHelp("Enter", fmt.Sprintf("restore %d", count), "Esc", "cancel") + "\n")
	case bkDone:
		if s.results == nil {
			b.WriteString(stOK.Render("✓ backup written") + "\n")
			b.WriteString(echo.Terminal(s.written) + " (mode 600)\n")
			fmt.Fprintf(&b, "%d accounts\n\n", s.count)
			b.WriteString(stWarn.Width(74).Render(wallet.BackupAdvice) + "\n")
			b.WriteString(stDim.Width(74).Render(wallet.BackupNotIncluded) + "\n")
			for _, p := range s.problems {
				b.WriteString(stBad.Render(truncate(oneLine(p.Error()), 76)) + "\n")
			}
		} else {
			restored := 0
			for _, r := range s.results {
				line := fmt.Sprintf("%-8s %-18s %s", r.Result, truncName(r.Name), keys.Short(r.Address))
				if r.RestoredAs != "" {
					line += " as " + r.RestoredAs
				}
				if r.Result == "restored" {
					restored++
					b.WriteString(stOK.Render(printable(line)) + "\n")
				} else {
					b.WriteString(printable(line) + "\n")
				}
				if r.Result == "failed" || r.Warning != "" {
					b.WriteString(stBad.Render(truncate(oneLine(r.Reason+" "+r.Warning), 76)) + "\n")
				}
			}
			if restored > 0 {
				b.WriteString("\n" + stWarn.Render(wallet.BackupPasswordsNote) + "\n")
			}
		}
		b.WriteString("\n" + stDim.Render("any key: back") + "\n")
	}
	if s.err != nil {
		b.WriteString("\n" + stBad.Render(oneLine(s.err.Error())) + "\n")
	}
	return stBox.Width(min(m.width-2, 78)).Render(strings.TrimRight(b.String(), "\n"))
}

func truncName(name string) string {
	if len(name) > 18 {
		return name[:17] + "…"
	}
	return name
}
