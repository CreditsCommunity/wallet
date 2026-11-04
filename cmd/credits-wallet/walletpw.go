package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/CreditsCommunity/wallet/internal/echo"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

// ---- the wallet password ---------------------------------------------------------------------------

// passwordPrompt is what to ask to open an account: the wallet password, the account's old
// password (not migrated yet), or the node's password for a key the node encrypted.
func passwordPrompt(f *keys.File) string {
	switch {
	case f.NodeKeyEncrypted:
		return "Password of the node's key (the one the node asks at its start): "
	case f.OldPassword():
		return "Old password of " + f.Name + " (not under the wallet password yet: 'wallet migrate'): "
	}
	return "Wallet password: "
}

// chooseWalletPassword asks a new wallet password twice (or reads --password-file) and shows the
// strength hint.
func (a *app) chooseWalletPassword(what string) ([]byte, error) {
	if a.passwordFile == "" && stdinIsTerminal() {
		fmt.Fprintln(os.Stderr, what)
	}
	pw, err := a.readPassword("Wallet password: ", true)
	if err != nil {
		return nil, err
	}
	if hint := wallet.PasswordHint(pw); hint != "" {
		fmt.Fprintln(os.Stderr, hint)
	}
	return pw, nil
}

// walletPasswordForNewAccount is the password that opens the wallet to add an account: the wallet
// password, or, for the first account, a new one chosen now.
func (a *app) walletPasswordForNewAccount() ([]byte, error) {
	if a.w.HasWalletPassword() {
		return a.readPassword("Wallet password: ", false)
	}
	if a.w.NeedsMigration() {
		return nil, wallet.ErrMigrateFirst
	}
	return a.chooseWalletPassword("Choose the wallet password: it encrypts every account of this wallet, and one password opens them all.")
}

func (a *app) cmdWallet(_ context.Context, args []string) error {
	fs := newFlagSet(a, "wallet")
	newPwFile := fs.String("new-password-file", "", "password: the new password from FILE")
	pos, err := a.commandArgs(fs, args, 1)
	if err != nil {
		return err
	}
	switch {
	case len(pos) == 0:
		return a.walletStatus()
	case len(pos) == 1 && pos[0] == "migrate":
		return a.walletMigrate()
	case len(pos) == 1 && pos[0] == "password":
		return a.walletChangePassword(*newPwFile)
	}
	return usagef("wallet, wallet migrate or wallet password")
}

func (a *app) walletStatus() error {
	old := a.w.OldPasswordAccounts()
	names := []string{}
	for _, f := range old {
		names = append(names, f.Name)
	}
	_, nextErr := os.Lstat(a.w.Store.WalletKeyNextPath())
	if a.json {
		return a.printJSON(map[string]any{"wallet_password": a.w.HasWalletPassword(), "old_password_accounts": names,
			"change_interrupted": nextErr == nil})
	}
	switch {
	case a.w.HasWalletPassword():
		fmt.Fprintf(a.out, "wallet password: yes (%s)\n", a.w.Store.WalletKeyPath())
	case len(old) > 0:
		fmt.Fprintln(a.out, "wallet password: not yet: 'wallet migrate' chooses it and moves the accounts under it")
	default:
		fmt.Fprintln(a.out, "wallet password: not yet: it is chosen with the first account")
	}
	if len(old) > 0 {
		fmt.Fprintf(a.out, "old password   : %s (%s)\n", plural(len(old), "account"), strings.Join(names, ", "))
	}
	if nextErr == nil {
		fmt.Fprintln(a.out, "A change of the wallet password was interrupted: any command that opens the wallet with the NEW password finishes it.")
	}
	return nil
}

// walletMigrate moves the accounts that still have a password of their own under the wallet
// password: the wallet password (chosen now when there is none: it may be one of the accounts'
// passwords) opens every account that shares it, then each other account's own password is asked
// once (Enter keeps it as it is, for later). Resumable: a migration interrupted anywhere is
// checked and finished when the wallet is opened.
func (a *app) walletMigrate() error {
	old := a.w.OldPasswordAccounts()
	if len(old) == 0 && a.w.HasWalletPassword() {
		fmt.Fprintln(os.Stderr, "Every account is already under the wallet password.")
		return nil
	}
	var pw []byte
	var s *wallet.Session
	var err error
	if a.w.HasWalletPassword() {
		if pw, err = a.readPassword("Wallet password: ", false); err != nil {
			return err
		}
		s, err = a.w.OpenWallet(pw)
	} else {
		if len(old) == 0 {
			return wallet.ErrNoWalletPassword
		}
		if pw, err = a.chooseWalletPassword(fmt.Sprintf("%s have a password each. Choose the wallet password: one password for every\naccount. It may be one of your accounts' passwords: the accounts that have it are moved at once.", plural(len(old), "account"))); err != nil {
			return err
		}
		s, err = a.w.CreateWalletPassword(pw)
	}
	if err != nil {
		keys.Zero(pw)
		return err
	}
	defer s.Lock()
	moved, left, err := a.w.MigrateWith(s, pw)
	keys.Zero(pw)
	if err != nil {
		return err
	}
	var kept []string
	for _, f := range left {
		done := false
		for tries := 0; tries < 3 && !done && stdinIsTerminal(); tries++ {
			own, err := readSecret(fmt.Sprintf("Password of %s (Enter: keep its old password for now): ", f.Name))
			if err != nil || own == "" {
				break
			}
			b := []byte(own)
			err = a.w.MigrateAccount(s, f, b)
			keys.Zero(b)
			switch {
			case err == nil:
				moved, done = append(moved, f.Name), true
			case errors.Is(err, keys.ErrWrongPassword):
				fmt.Fprintln(os.Stderr, "not that password")
			default:
				return fmt.Errorf("%s: %w", f.Name, err)
			}
		}
		if !done {
			kept = append(kept, f.Name)
		}
	}
	problems := a.w.FinishMigration(s)
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "warning:", echo.Terminal(p.Error()))
	}
	if a.json {
		return a.printJSON(map[string]any{"moved": nonNil(moved), "old_password": nonNil(kept)})
	}
	fmt.Fprintf(a.out, "under the wallet password: %d (%s)\n", len(moved), strings.Join(moved, ", "))
	if len(kept) > 0 {
		fmt.Fprintf(a.out, "old password kept: %d (%s): 'wallet migrate' again when you have it\n", len(kept), strings.Join(kept, ", "))
	}
	return nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// walletChangePassword changes the wallet password: every account is re-encrypted under a new
// key; an interruption is finished by opening the wallet with the new password.
func (a *app) walletChangePassword(newPwFile string) error {
	old, err := a.readPassword("Current wallet password: ", false)
	if err != nil {
		return err
	}
	defer keys.Zero(old)
	var newPw []byte
	if newPwFile != "" {
		newPw, err = readPasswordFile(newPwFile)
	} else {
		if a.passwordFile != "" && !stdinIsTerminal() {
			return errors.New("the new password: --new-password-file FILE (no terminal to ask it on)")
		}
		saved := a.passwordFile
		a.passwordFile = ""
		newPw, err = a.chooseWalletPassword("Choose the new wallet password.")
		a.passwordFile = saved
	}
	if err != nil {
		return err
	}
	defer keys.Zero(newPw)
	if hint := wallet.PasswordHint(newPw); hint != "" && newPwFile != "" {
		fmt.Fprintln(os.Stderr, hint)
	}
	if err := a.w.ChangeWalletPassword(old, newPw); err != nil {
		return err
	}
	if a.json {
		return a.printJSON(map[string]any{"changed": true})
	}
	fmt.Fprintln(a.out, "wallet password changed: every account is re-encrypted under a new wallet key")
	fmt.Fprintln(os.Stderr, "Backups made before keep opening with the old password: make a new one (backup export).")
	return nil
}
