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
	walletPwFile := fs.String("wallet-password-file", "", "import: this wallet's password, when it is not the backup's")
	pos, err := a.commandArgs(fs, args, 1)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return usagef("backup export FILE.dat | backup import FILE.dat")
	}
	switch pos[0] {
	case "export":
		if *suffix != "" || *yes || *walletPwFile != "" {
			return usagef("--rename-suffix, --yes and --wallet-password-file go with backup import")
		}
		return a.backupExport(pos[1])
	case "import":
		return a.backupImport(pos[1], *suffix, *yes, *walletPwFile)
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

// backupExport writes every account of the key store, with the wallet key and the local lists,
// into one file sealed under the wallet password. The node's key and the configuration stay out.
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
		fmt.Fprintf(os.Stderr, "The backup will hold %s: %s. It is encrypted with the wallet password.\n", plural(len(b.Accounts), "account"), strings.Join(names, ", "))
	}
	pw, err := a.readPassword("Wallet password: ", false)
	if err != nil {
		return err
	}
	defer keys.Zero(pw)
	if err := a.w.CheckWalletPassword(pw); err != nil {
		return err
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
	fmt.Fprintln(os.Stderr, wallet.BackupOldPasswordNote)
	return nil
}

func reportBackupProblems(problems []error) {
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "warning:", echo.Terminal(p.Error()))
	}
}

// backupImport opens a backup with the password of the wallet it comes from, lists its accounts
// and, once confirmed, writes those whose address the wallet does not have, re-encrypted under
// this wallet's password (the same password: nothing more is asked; another one: this wallet's
// password is asked once; no wallet password yet: the backup's becomes it). A name taken by
// another address gets --rename-suffix, a name asked on the terminal, or is skipped. Accounts of
// the backup with an old password of their own are opened with the backup's password, or their
// own, asked once each.
func (a *app) backupImport(path, suffix string, yes bool, walletPwFile string) error {
	data, err := wallet.ReadBackupFile(path)
	if err != nil {
		return err
	}
	if a.w.NeedsMigration() {
		return wallet.ErrMigrateFirst
	}
	pw, err := a.readPassword("Password of the wallet the backup comes from: ", false)
	if err != nil {
		return err
	}
	defer keys.Zero(pw)
	b, err := wallet.OpenBackup(data, pw)
	debug.FreeOSMemory()
	if err != nil {
		return err
	}
	defer b.Wipe()
	src, err := b.SourceKey(pw)
	if err != nil {
		return err
	}
	defer src.Lock()
	plans, err := a.w.PlanRestore(b)
	if err != nil {
		return err
	}
	defer wallet.WipePlans(plans)
	for i := range plans {
		if plans[i].Old && plans[i].State != wallet.RestoreExists {
			plans[i].TryOldPassword(pw)
		}
	}
	terminal := stdinIsTerminal()
	fmt.Fprintf(os.Stderr, "The backup of %s holds %s:\n", b.Created.Format("2006-01-02 15:04 UTC"), plural(len(plans), "account"))
	for _, p := range plans {
		note := ""
		switch {
		case p.State == wallet.RestoreExists:
			note = "  already here as " + p.Existing + ": skipped"
		case p.State == wallet.RestoreReplace:
			note = "  replaces the orphaned " + p.Existing + " (its wallet key is lost; the old file goes to accounts/.orphaned/)"
		case p.State == wallet.RestoreNameTaken:
			note = "  the name is another account's here"
		case p.Old && !p.OldPasswordFound():
			note = "  a password of its own"
		}
		fmt.Fprintf(os.Stderr, "  %-20s %s%s\n", p.Account.Name, p.Account.Address, note)
	}
	// the names, chosen before the confirmation
	names := make([]string, len(plans))
	taken := map[string]bool{} // the names restored: a new name may not be one of them
	for i, p := range plans {
		switch p.State {
		case wallet.RestoreNew:
			names[i] = p.Account.Name
		case wallet.RestoreReplace:
			names[i] = p.Existing
		}
		if names[i] != "" {
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
	// an old wallet's accounts with passwords of their own: each asked once
	for i := range plans {
		if names[i] == "" || !plans[i].Old || plans[i].OldPasswordFound() || !terminal {
			continue
		}
		for tries := 0; tries < 3; tries++ {
			own, err := readSecret(fmt.Sprintf("Password of %s in the backup (Enter: keep its old password): ", plans[i].Account.Name))
			if err != nil || own == "" {
				break
			}
			ok := plans[i].TryOldPassword([]byte(own))
			if ok {
				break
			}
			fmt.Fprintln(os.Stderr, "not that password")
		}
	}
	count := 0
	for _, n := range names {
		if n != "" {
			count++
		}
	}
	// this wallet: the backup's password, or its own
	var dst *wallet.Session
	if a.w.HasWalletPassword() {
		dst, err = a.w.OpenWallet(pw)
		if wallet.IsWrongPassword(err) {
			var own []byte
			if walletPwFile != "" {
				own, err = readPasswordFile(walletPwFile)
			} else if terminal && a.passwordFile == "" {
				fmt.Fprintln(os.Stderr, "This wallet has another password: the accounts are re-encrypted under it.")
				own, err = a.readPassword("Password of this wallet: ", false)
			} else {
				err = errors.New("this wallet's password is not the backup's: give it with --wallet-password-file FILE")
			}
			if err != nil {
				return err
			}
			dst, err = a.w.OpenWallet(own)
			keys.Zero(own)
		}
		if err != nil {
			return err
		}
		defer dst.Lock()
	} else if count > 0 {
		if u := a.w.UncoveredOrphans(plans, names); len(u) > 0 {
			return a.w.OrphanedErr(u)
		}
		fmt.Fprintln(os.Stderr, "This wallet has no password yet: the backup's password becomes its wallet password.")
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
	if dst == nil && count > 0 {
		if err := a.w.PrepareReplace(plans, names); err != nil {
			return err
		}
		if dst, err = a.w.CreateWalletPassword(pw); err != nil {
			return err
		}
		defer dst.Lock()
	}
	rows, restored, failed := a.w.Restore(plans, names, src, dst)
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
