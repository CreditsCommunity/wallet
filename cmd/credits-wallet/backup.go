package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/CreditsCommunity/wallet/internal/echo"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

// ---- backup ------------------------------------------------------------------------------------

func (a *app) cmdBackup(_ context.Context, args []string) error {
	fs := newFlagSet(a, "backup")
	suffix := fs.String("rename-suffix", "", "import: a name already taken gets this suffix")
	yes := fs.Bool("yes", false, "import: no confirmation")
	pos, err := a.commandArgs(fs, args, 1)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return usagef("backup export FILE.dat | backup import FILE.dat")
	}
	switch pos[0] {
	case "export":
		if *suffix != "" || *yes {
			return usagef("--rename-suffix and --yes go with backup import")
		}
		return a.backupExport(pos[1])
	case "import":
		return a.backupImport(pos[1], *suffix, *yes)
	}
	return usagef("backup: export or import")
}

type backupRow struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

func errorTexts(errs []error) []string {
	out := []string{}
	for _, e := range errs {
		out = append(out, e.Error())
	}
	return out
}

// backupExport writes every account of the key store, with its local lists, into one file sealed
// under a backup password typed twice. The key files go in as they are (encrypted under their own
// passwords); the node's key and the configuration stay out.
func (a *app) backupExport(path string) error {
	abs, err := a.w.CheckBackupPath(path)
	if err != nil {
		return err
	}
	b, problems, err := a.w.CollectBackup(time.Now())
	if err != nil {
		reportBackupProblems(problems)
		return err
	}
	defer b.Wipe()
	reportBackupProblems(problems)
	if !a.json {
		names := make([]string, 0, len(b.Accounts))
		for _, acc := range b.Accounts {
			names = append(names, acc.Name)
		}
		fmt.Fprintf(os.Stderr, "The backup will hold %s: %s.\n", plural(len(b.Accounts), "account"), strings.Join(names, ", "))
		if a.passwordFile == "" {
			fmt.Fprintln(os.Stderr, "Choose a backup password: it protects the whole file (each account keeps its own password inside).")
		}
	}
	pw, err := a.readPassword("Backup password: ", true)
	if err != nil {
		return err
	}
	defer keys.Zero(pw)
	if hint := wallet.PasswordHint(pw); hint != "" {
		fmt.Fprintln(os.Stderr, hint)
	}
	err = wallet.WriteBackup(abs, b, pw)
	debug.FreeOSMemory() // Argon2id's 256 MiB go back now
	if err != nil {
		return err
	}
	if a.json {
		rows := []backupRow{}
		for _, acc := range b.Accounts {
			rows = append(rows, backupRow{acc.Name, acc.Address})
		}
		return a.printJSON(map[string]any{"file": abs, "count": len(rows), "accounts": rows,
			"not_included": []string{"the node's key (NodePrivate.txt)", "config.toml"}, "problems": errorTexts(problems)})
	}
	fmt.Fprintf(a.out, "backup written: %s (mode 600)\naccounts: %d\n", echo.Terminal(abs), len(b.Accounts))
	fmt.Fprintln(os.Stderr, wallet.BackupAdvice)
	fmt.Fprintln(os.Stderr, wallet.BackupNotIncluded)
	return nil
}

func reportBackupProblems(problems []error) {
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "warning:", echo.Terminal(p.Error()))
	}
}

// backupImport opens a backup with its password, lists its accounts and, once confirmed, writes
// those whose address the wallet does not have. A name taken by another address gets
// --rename-suffix, or a name asked on the terminal, or is skipped.
func (a *app) backupImport(path, suffix string, yes bool) error {
	data, err := wallet.ReadBackupFile(path)
	if err != nil {
		return err
	}
	pw, err := a.readPassword("Backup password: ", false)
	if err != nil {
		return err
	}
	b, err := wallet.OpenBackup(data, pw)
	keys.Zero(pw)
	debug.FreeOSMemory()
	if err != nil {
		return err
	}
	defer b.Wipe()
	plans, err := a.w.PlanRestore(b)
	if err != nil {
		return err
	}
	terminal := stdinIsTerminal()
	fmt.Fprintf(os.Stderr, "The backup of %s holds %s:\n", b.Created.Format("2006-01-02 15:04 UTC"), plural(len(plans), "account"))
	for _, p := range plans {
		note := ""
		switch p.State {
		case wallet.RestoreExists:
			note = "  already here as " + p.Existing + ": skipped"
		case wallet.RestoreNameTaken:
			note = "  the name is another account's here"
		}
		fmt.Fprintf(os.Stderr, "  %-20s %s%s\n", p.Account.Name, p.Account.Address, note)
	}
	// the names, chosen before the confirmation
	names := make([]string, len(plans))
	taken := map[string]bool{} // the names restored: a new name may not be one of them
	for i, p := range plans {
		if p.State == wallet.RestoreNew {
			names[i] = p.Account.Name
			taken[names[i]] = true
		}
	}
	for i, p := range plans {
		if p.State == wallet.RestoreNameTaken {
			if names[i] = a.newRestoreName(p, suffix, terminal, taken); names[i] != "" {
				taken[names[i]] = true
				fmt.Fprintf(os.Stderr, "  %s will be restored as %s\n", p.Account.Name, names[i])
			}
		}
	}
	count := 0
	for _, n := range names {
		if n != "" {
			count++
		}
	}
	if !yes {
		if !terminal {
			return errors.New("refused without a terminal to confirm on: add --yes (nothing was restored)")
		}
		question := fmt.Sprintf("Restore %s? [y/N] ", plural(count, "account"))
		if count == 0 {
			question = "No account to add; merge the token and contract lists of those already here? [y/N] "
		}
		if ans, _ := ask(question); strings.ToLower(ans) != "y" && strings.ToLower(ans) != "yes" {
			return errors.New("cancelled: nothing was restored")
		}
	}
	rows, restored, failed := a.w.Restore(plans, names)
	var result error
	if failed > 0 {
		result = fmt.Errorf("%s could not be restored", plural(failed, "account"))
	}
	if a.json {
		out := map[string]any{"file": path, "created": b.Created, "accounts": rows, "restored": restored,
			"skipped": len(rows) - restored - failed, "failed": failed}
		if err := a.printJSON(out); err != nil {
			return err
		}
		if result != nil {
			return shownError{result}
		}
		return nil
	}
	for _, r := range rows {
		line := fmt.Sprintf("%-9s %-20s %s", r.Result, r.Name, r.Address)
		if r.RestoredAs != "" {
			line += " as " + r.RestoredAs
		}
		if r.Reason != "" {
			line += ": " + r.Reason
		}
		fmt.Fprintln(a.out, echo.Terminal(line))
		if r.Warning != "" {
			fmt.Fprintln(os.Stderr, "warning:", r.Name+":", echo.Terminal(r.Warning))
		}
	}
	fmt.Fprintf(a.out, "restored: %d, skipped: %d\n", restored, len(rows)-restored-failed)
	if restored > 0 {
		fmt.Fprintln(os.Stderr, wallet.BackupPasswordsNote)
	}
	return result
}

// newRestoreName is the name for an account whose name is taken here: its name with the suffix,
// or a name asked on the terminal (Enter: skip it). "" skips the account.
func (a *app) newRestoreName(p wallet.RestorePlan, suffix string, terminal bool, taken map[string]bool) string {
	if suffix != "" {
		name := p.Account.Name + suffix
		if err := a.w.FreeRestoreName(name, taken); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s: skipped\n", p.Account.Name, echo.Terminal(err.Error()))
			return ""
		}
		return name
	}
	if !terminal {
		return ""
	}
	for tries := 0; tries < 3; tries++ {
		name, err := ask(fmt.Sprintf("New name for %s (%s) [Enter: skip it]: ", p.Account.Name, keys.Short(p.Account.Address)))
		if err != nil || name == "" {
			return ""
		}
		if err := a.w.FreeRestoreName(name, taken); err != nil {
			fmt.Fprintln(os.Stderr, echo.Terminal(err.Error()))
			continue
		}
		return name
	}
	return ""
}
