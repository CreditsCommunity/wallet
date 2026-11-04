package tui

import (
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
	bkExportForm            // File, Wallet pw
	bkImportForm            // File, the backup's password
	bkWorking               // Argon2id, writing or opening
	bkRename                // a name of the backup is another account's here: a new name
	bkOldPw                 // an account of the backup with a password of its own
	bkWalletPw              // this wallet's password, when it is not the backup's
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
	backup   *wallet.Backup
	src      *wallet.Session // the backup's wallet key
	dst      *wallet.Session // this wallet's
	dstOwned bool            // opened here (locked at the end); otherwise the model's
	pw       []byte          // the backup's password, kept when it becomes this wallet's
	plans    []wallet.RestorePlan
	names    []string
	taken    map[string]bool
	renameI  int // the plan being renamed
	oldI     int // the plan whose own password is asked
	needDst  bool
	results  []wallet.RestoreResult
}

type backupWrittenMsg struct {
	tried    bool // the wallet password was checked
	path     string
	count    int
	problems []error
	err      error
}

type backupOpenedMsg struct {
	dstTried bool  // the backup's password was tried as this wallet's
	dstErr   error // and failed
	b        *wallet.Backup
	src, dst *wallet.Session
	needDst  bool   // this wallet has another password
	pw       []byte // no wallet password here: the backup's becomes it
	plans    []wallet.RestorePlan
	err      error
}

type backupDstMsg struct {
	dst *wallet.Session
	err error
}

type backupRestoredMsg struct {
	results []wallet.RestoreResult
}

func newBackupScreen() *backupScreen { return &backupScreen{} }

func (s *backupScreen) title() string { return "Backup" }

func (s *backupScreen) forget(m *model) {
	if s.form != nil {
		s.form.wipe()
	}
	s.backup.Wipe()
	s.backup = nil
	wallet.WipePlans(s.plans)
	s.src.Lock()
	if s.dstOwned {
		s.dst.Lock()
	}
	s.src, s.dst = nil, nil
	keys.Zero(s.pw)
	s.pw = nil
}

func (s *backupScreen) update(m *model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case backupWrittenMsg:
		if msg.tried {
			m.pwTried(msg.err)
		}
		s.step, s.err = bkDone, wrongPassword(msg.err)
		s.written, s.count, s.problems = msg.path, msg.count, msg.problems
		if msg.err != nil {
			s.step = bkExportForm
		}
		return nil
	case backupOpenedMsg:
		if msg.dstTried {
			m.pwTried(msg.dstErr)
		}
		if msg.err != nil {
			s.step, s.err = bkImportForm, msg.err
			return nil
		}
		s.backup, s.src, s.plans, s.err = msg.b, msg.src, msg.plans, nil
		s.needDst, s.pw = msg.needDst, msg.pw
		if msg.dst != nil {
			s.dst, s.dstOwned = msg.dst, msg.dst != m.session
		}
		s.names = make([]string, len(s.plans))
		s.taken = map[string]bool{}
		for i, p := range s.plans {
			if p.State == wallet.RestoreNew {
				s.names[i] = p.Account.Name
				s.taken[p.Account.Name] = true
			}
		}
		s.renameI, s.oldI = -1, -1
		s.next()
		return nil
	case backupDstMsg:
		m.pwTried(msg.err)
		if msg.err != nil {
			s.step, s.err = bkWalletPw, wrongPassword(msg.err)
			return nil
		}
		s.dst, s.dstOwned, s.needDst, s.err = msg.dst, true, false, nil
		s.next()
		return nil
	case backupRestoredMsg:
		s.results = msg.results
		s.forget(m)
		s.step = bkDone
		return m.loadAccounts()
	case tea.KeyMsg:
		if msg.String() == "esc" && s.step != bkWorking {
			s.forget(m)
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
			s.form = newForm("File", "Wallet pw").secret(1)
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
		if err := m.pwThrottle(); err != nil {
			s.err = err
			return nil
		}
		pw := []byte(s.form.inputs[1].Value())
		s.form.inputs[1].SetValue("")
		if len(pw) == 0 {
			s.err = fmt.Errorf("type the wallet password")
			return nil
		}
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
			if err := w.CheckWalletPassword(pw); err != nil {
				return backupWrittenMsg{tried: true, problems: problems, err: err}
			}
			if err := wallet.WriteBackup(path, b, pw); err != nil {
				return backupWrittenMsg{tried: true, problems: problems, err: err}
			}
			return backupWrittenMsg{tried: true, path: path, count: len(b.Accounts), problems: problems}
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
			s.err = fmt.Errorf("the file and the password of the wallet it comes from, please")
			return nil
		}
		if m.w.NeedsMigration() {
			keys.Zero(pw)
			s.err = wallet.ErrMigrateFirst
			return nil
		}
		// the backup's password is tried as this wallet's (when it is closed): a try of the wallet
		// password like any other, so not while the delay runs
		if m.session == nil && m.w.HasWalletPassword() {
			if err := m.pwThrottle(); err != nil {
				keys.Zero(pw)
				s.err = err
				return nil
			}
		}
		s.err, s.step = nil, bkWorking
		w, session := m.w, m.session
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
			src, err := b.SourceKey(pw)
			if err != nil {
				b.Wipe()
				return backupOpenedMsg{err: err}
			}
			plans, err := w.PlanRestore(b)
			if err != nil {
				b.Wipe()
				src.Lock()
				return backupOpenedMsg{err: err}
			}
			for i := range plans {
				if plans[i].Old && plans[i].State != wallet.RestoreExists {
					plans[i].TryOldPassword(pw)
				}
			}
			msg := backupOpenedMsg{b: b, src: src, plans: plans, dst: session}
			switch {
			case session != nil:
			case w.HasWalletPassword():
				dst, err := w.OpenWallet(pw)
				msg.dstTried, msg.dstErr = true, err
				if err == nil {
					msg.dst = dst
				} else {
					msg.needDst = true
				}
			default:
				msg.pw = append([]byte(nil), pw...)
			}
			return msg
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
		s.next()
		return nil
	case bkOldPw:
		cmd, done := s.form.update(msg)
		if !done {
			return cmd
		}
		pw := []byte(s.form.inputs[0].Value())
		s.form.wipe()
		if len(pw) > 0 {
			ok := s.plans[s.oldI].TryOldPassword(pw)
			keys.Zero(pw)
			if !ok {
				s.err = fmt.Errorf("not that password: again, or Enter with nothing to keep it")
				return nil
			}
		}
		s.err = nil
		s.next()
		return nil
	case bkWalletPw:
		cmd, done := s.form.update(msg)
		if !done {
			return cmd
		}
		if err := m.pwThrottle(); err != nil {
			s.err = err
			return nil
		}
		pw := []byte(s.form.inputs[0].Value())
		s.form.wipe()
		s.step = bkWorking
		w := m.w
		return func() tea.Msg {
			defer keys.Zero(pw)
			dst, err := w.OpenWallet(pw)
			return backupDstMsg{dst, err}
		}
	case bkConfirm:
		k, ok := msg.(tea.KeyMsg)
		if !ok || k.String() != "enter" {
			return nil
		}
		s.step = bkWorking
		w, plans, names, src, dst, pw := m.w, s.plans, s.names, s.src, s.dst, s.pw
		created := false
		return func() tea.Msg {
			if dst == nil && pw != nil {
				var err error
				if dst, err = w.CreateWalletPassword(pw); err != nil {
					return backupRestoredMsg{[]wallet.RestoreResult{{Result: "failed", Reason: err.Error()}}}
				}
				created = true
			}
			results, _, _ := w.Restore(plans, names, src, dst)
			if created {
				dst.Lock()
			}
			return backupRestoredMsg{results}
		}
	case bkDone:
		if _, ok := msg.(tea.KeyMsg); ok {
			return m.back()
		}
	}
	return nil
}

// next asks what is still needed: a new name for each account whose name is taken, the password
// of each account with one of its own, this wallet's password; then the confirmation.
func (s *backupScreen) next() {
	for i := s.renameI + 1; i < len(s.plans); i++ {
		if s.plans[i].State == wallet.RestoreNameTaken {
			s.renameI = i
			s.form = newForm("New name")
			s.step = bkRename
			return
		}
	}
	s.renameI = len(s.plans)
	for i := s.oldI + 1; i < len(s.plans); i++ {
		if s.names[i] != "" && s.plans[i].Old && !s.plans[i].OldPasswordFound() {
			s.oldI = i
			s.form = newForm("Password").secret(0)
			s.step = bkOldPw
			return
		}
	}
	s.oldI = len(s.plans)
	if s.needDst {
		s.form = newForm("Wallet pw").secret(0)
		s.step = bkWalletPw
		return
	}
	s.step = bkConfirm
}

func (s *backupScreen) view(m *model) string {
	var b strings.Builder
	switch s.step {
	case bkChoose:
		b.WriteString("Every account of the wallet in one file, encrypted with the wallet password.\n\n")
		b.WriteString(keyHelp("e", "export a backup", "i", "import a backup", "Esc", "back") + "\n")
		b.WriteString("\n" + stDim.Render("The accounts go in encrypted, with the token and contract lists. Not the node's") + "\n")
		b.WriteString(stDim.Render("key, not config.toml. The backup is only as strong as the wallet password.") + "\n")
	case bkExportForm:
		b.WriteString("Export a backup: a new file, outside the wallet's folder.\n\n")
		b.WriteString(s.form.view())
		b.WriteString("\n" + stDim.Render("The wallet password encrypts the file: it opens it on import.") + "\n")
		b.WriteString(stDim.Render(wallet.BackupNotIncluded) + "\n")
		b.WriteString("\n" + keyHelp("Enter", "next / write", "Esc", "cancel") + "\n")
	case bkImportForm:
		b.WriteString("Import a backup (.dat): the password of the wallet it comes from.\n\n")
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
	case bkOldPw:
		p := s.plans[s.oldI]
		fmt.Fprintf(&b, "%s (%s) has a password of its own in the backup (an older wallet).\n", printable(p.Account.Name), keys.Short(p.Account.Address))
		b.WriteString("Type it once to put it under this wallet's password, or Enter to keep it.\n\n")
		b.WriteString(s.form.view())
		b.WriteString("\n" + keyHelp("Enter", "next", "Esc", "cancel") + "\n")
	case bkWalletPw:
		b.WriteString("This wallet has another password: the accounts are re-encrypted under it.\n\n")
		b.WriteString(s.form.view())
		b.WriteString("\n" + keyHelp("Enter", "next", "Esc", "cancel") + "\n")
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
			case p.Old && !p.OldPasswordFound():
				what = "restore (old password kept)"
				count++
			case s.names[i] != p.Account.Name:
				what = "restore as " + printable(s.names[i])
				count++
			default:
				count++
			}
			fmt.Fprintf(&b, "%-18s %s  %s\n", printable(truncName(p.Account.Name)), keys.Short(p.Account.Address), what)
		}
		if s.dst == nil && s.pw != nil {
			b.WriteString("\n" + stWarn.Render("This wallet has no password yet: the backup's becomes its wallet password.") + "\n")
		} else {
			b.WriteString("\n" + stDim.Render(wallet.BackupPasswordsNote) + "\n")
		}
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
