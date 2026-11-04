package wallet

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/CreditsCommunity/wallet/internal/keys"
)

// The backup (spec 4.5): every account of the key store in one file, sealed under a backup
// password (keys.SealBackup). Inside, each account's key file exactly as stored (still encrypted
// under the account's own password: the backup never decrypts it), its name, its address, and
// its local token and contract lists (public data). Not inside: the node's key (it belongs to
// the node's folder), config.toml and anything else of this machine.

// BackupFormat names the content of a backup.
const BackupFormat = "credits-wallet backup"

// BackupAdvice is said after a backup is written (terminal and TUI).
const BackupAdvice = "This file contains your accounts, each still protected by its own password and the whole file by the backup password; keep it off this computer, e.g. on a USB key."

// BackupNotIncluded says what a backup leaves out.
const BackupNotIncluded = "Not in the backup: the node's key (NodePrivate.txt belongs to the node's folder: keep its 24 words, or a copy of it offline) and config.toml (settings of this machine)."

// BackupPasswordsNote is said on import.
const BackupPasswordsNote = "Restored accounts keep their own passwords: the ones they had when the backup was made."

const maxBackupAccounts = 10000

// BackupAccount is an account in a backup.
type BackupAccount struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	// KeyFile is the key file as stored (JSON, encrypted under the account's password).
	KeyFile []byte `json:"key_file"`
	// Tokens and Contracts are the local lists (tokens/<address>.json, contracts/<address>.json)
	// as stored; nil when the account has none.
	Tokens    []byte `json:"tokens,omitempty"`
	Contracts []byte `json:"contracts,omitempty"`
}

// Backup is the content of a backup file.
type Backup struct {
	Format   string          `json:"format"`
	Version  int             `json:"version"`
	Created  time.Time       `json:"created"`
	Accounts []BackupAccount `json:"accounts"`
}

// Wipe zeroes what the backup holds (the key files are encrypted, but nothing of them stays).
func (b *Backup) Wipe() {
	if b == nil {
		return
	}
	for i := range b.Accounts {
		keys.Zero(b.Accounts[i].KeyFile)
		keys.Zero(b.Accounts[i].Tokens)
		keys.Zero(b.Accounts[i].Contracts)
	}
	b.Accounts = nil
}

// DefaultBackupPath is ~/credits-wallet-backup-YYYY-MM-DD.dat.
func DefaultBackupPath(now time.Time) (string, error) {
	home, err := UserHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "credits-wallet-backup-"+now.Format("2006-01-02")+".dat"), nil
}

// CheckBackupPath checks where a backup may be written, with the recovery file's checks: a new
// file (no file or link at the path), in an existing folder, outside the data folder, the
// accounts folder and the node's folder (compared by identity), and not in or below a folder
// holding key files.
func (w *Wallet) CheckBackupPath(path string) (string, error) {
	return w.CheckRecoveryPath(path)
}

// PasswordHint is a hint on a password's strength, "" when nothing is to be said. It never
// refuses: the user decides.
func PasswordHint(pw []byte) string {
	if n := utf8.RuneCount(pw); n < 12 {
		return fmt.Sprintf("hint: a short password (%d characters); 12 or more, or a few unrelated words, are much harder to guess", n)
	}
	return ""
}

// CollectBackup reads every account of the key store with its local lists. problems are the
// key files that could not be read (not in the backup) and the lists left out.
func (w *Wallet) CollectBackup(now time.Time) (b *Backup, problems []error, err error) {
	files, problems, err := w.Store.List()
	if err != nil {
		return nil, nil, err
	}
	b = &Backup{Format: BackupFormat, Version: 1, Created: now.UTC().Truncate(time.Second)}
	for _, f := range files {
		raw, err := readBounded(w.Store.FilePath(f.Name), maxKeyFile)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w (not in the backup)", f.Name, err))
			continue
		}
		pub, err := f.PublicKey()
		if err != nil {
			keys.Zero(raw)
			problems = append(problems, fmt.Errorf("%s: %w (not in the backup)", f.Name, err))
			continue
		}
		a := BackupAccount{Name: f.Name, Address: f.Public, KeyFile: raw}
		if w.DataDir != "" {
			if a.Tokens, err = readListFile(w.tokenListPath(pub)); err != nil {
				problems = append(problems, fmt.Errorf("%s: the token list: %w (left out)", f.Name, err))
			}
			if a.Contracts, err = readListFile(w.localListPath(pub)); err != nil {
				problems = append(problems, fmt.Errorf("%s: the contract list: %w (left out)", f.Name, err))
			}
		}
		b.Accounts = append(b.Accounts, a)
	}
	if len(b.Accounts) == 0 {
		return nil, problems, errors.New("no account in the key store to back up (the node's key is never in a backup)")
	}
	return b, problems, nil
}

// WriteBackup seals the backup under the password and writes it to path with WriteSecretFile
// (mode 0600, never over a file, never through a link). path was checked by CheckBackupPath.
func WriteBackup(path string, b *Backup, password []byte) error {
	plain, err := json.Marshal(b)
	if err != nil {
		return err
	}
	defer keys.Zero(plain)
	sealed, err := keys.SealBackup(plain, password, keys.BackupKDF)
	if err != nil {
		return err
	}
	return WriteSecretFile(path, sealed)
}

// ReadBackupFile reads a backup file: a regular file of at most keys.MaxBackupSize bytes.
func ReadBackupFile(path string) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := readBounded(path, keys.MaxBackupSize)
	if err != nil {
		return nil, err
	}
	if !keys.IsBackup(data) {
		return nil, fmt.Errorf("%s: %w", path, keys.ErrNotBackup)
	}
	return data, nil
}

func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		keys.Zero(data)
		return nil, fmt.Errorf("%s is larger than %d bytes: not read", path, limit)
	}
	return data, nil
}

// OpenBackup opens a backup with its password and checks its content: a wrong password and a
// damaged file both say keys.ErrBackupOpen. The caller wipes the backup (Backup.Wipe).
func OpenBackup(data, password []byte) (*Backup, error) {
	plain, err := keys.OpenBackup(data, password)
	if err != nil {
		return nil, err
	}
	defer keys.Zero(plain)
	var b Backup
	if err := json.Unmarshal(plain, &b); err != nil {
		b.Wipe()
		return nil, fmt.Errorf("the backup's content is not valid: %w", err)
	}
	if b.Format != BackupFormat || b.Version != 1 {
		b.Wipe()
		return nil, fmt.Errorf("the backup's content is version %d of %q: not supported by this wallet", b.Version, b.Format)
	}
	if err := b.check(); err != nil {
		b.Wipe()
		return nil, fmt.Errorf("the backup's content is not valid: %w", err)
	}
	return &b, nil
}

// check validates every account: a valid name, a key file of the wallet whose address is the
// account's, lists of that account; no name or address twice.
func (b *Backup) check() error {
	if len(b.Accounts) == 0 || len(b.Accounts) > maxBackupAccounts {
		return fmt.Errorf("%d accounts", len(b.Accounts))
	}
	names, addresses := map[string]bool{}, map[string]bool{}
	for _, a := range b.Accounts {
		if err := keys.ValidName(a.Name); err != nil {
			return err
		}
		if _, err := keys.ParseAddress(a.Address); err != nil {
			return fmt.Errorf("%s: %w", a.Name, err)
		}
		if names[a.Name] || addresses[a.Address] {
			return fmt.Errorf("%s (%s) is there twice", a.Name, a.Address)
		}
		names[a.Name], addresses[a.Address] = true, true
		if len(a.KeyFile) > maxKeyFile {
			return fmt.Errorf("%s: the key file is too large", a.Name)
		}
		f, err := keys.ParseFile(a.KeyFile)
		if err != nil {
			return fmt.Errorf("%s: %w", a.Name, err)
		}
		if f.Public != a.Address {
			return fmt.Errorf("%s: the key file is of another address", a.Name)
		}
		if a.Tokens != nil {
			if _, err := parseTokenList(a.Tokens, a.Address); err != nil {
				return fmt.Errorf("%s: the token list: %w", a.Name, err)
			}
		}
		if a.Contracts != nil {
			if _, err := parseContractList(a.Contracts, a.Address); err != nil {
				return fmt.Errorf("%s: the contract list: %w", a.Name, err)
			}
		}
	}
	return nil
}

func parseTokenList(data []byte, address string) ([]LocalToken, error) {
	if len(data) > maxLocalList {
		return nil, errors.New("too large")
	}
	var l localTokens
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, err
	}
	if l.Account != address {
		return nil, errors.New("the list of another account")
	}
	return l.Tokens, nil
}

func parseContractList(data []byte, address string) ([]LocalContract, error) {
	if len(data) > maxLocalList {
		return nil, errors.New("too large")
	}
	var l localContracts
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, err
	}
	if l.Deployer != address {
		return nil, errors.New("the list of another account")
	}
	return l.Contracts, nil
}

// RestoreState is what the restore would do with an account of the backup.
type RestoreState int

const (
	// RestoreNew: the address is not in the wallet and its name is free.
	RestoreNew RestoreState = iota
	// RestoreExists: the address is already an account of the wallet (skipped).
	RestoreExists
	// RestoreNameTaken: the name is another address's here: a new name is needed.
	RestoreNameTaken
)

// RestorePlan is an account of the backup and what the restore would do with it.
type RestorePlan struct {
	Account BackupAccount
	State   RestoreState
	// Existing is the wallet's account with that address (RestoreExists) or that name
	// (RestoreNameTaken).
	Existing string
}

// PlanRestore compares the backup with the wallet's accounts (the node's key included).
func (w *Wallet) PlanRestore(b *Backup) ([]RestorePlan, error) {
	files, _, err := w.Accounts()
	if err != nil {
		return nil, err
	}
	byAddress := map[string]string{}
	for _, f := range files {
		byAddress[f.Public] = f.Name
	}
	var plans []RestorePlan
	for _, a := range b.Accounts {
		p := RestorePlan{Account: a}
		switch name, ok := byAddress[a.Address]; {
		case ok:
			p.State, p.Existing = RestoreExists, name
		case w.NameTaken(a.Name):
			p.State, p.Existing = RestoreNameTaken, a.Name
		}
		plans = append(plans, p)
	}
	return plans, nil
}

// NameTaken tells whether an account name is in use here (the node's key account included).
func (w *Wallet) NameTaken(name string) bool {
	return w.HasAccount(name) || (w.NodeKey != nil && name == w.NodeAccountName())
}

// RestoreAccount writes an account of the backup under name (its own, or a new one for a name
// clash) with the key store's writer (whole, never over an account), then merges its local
// lists. The key file is not decrypted: the account keeps its password.
func (w *Wallet) RestoreAccount(a BackupAccount, name string) error {
	if err := keys.ValidName(name); err != nil {
		return err
	}
	if w.NameTaken(name) {
		return fmt.Errorf("an account named %q already exists", name)
	}
	f, err := keys.ParseFile(a.KeyFile)
	if err != nil {
		return err
	}
	if f.Public != a.Address {
		return errors.New("the key file is of another address")
	}
	f.Name = name
	if err := w.Store.Save(f); err != nil {
		return err
	}
	return w.MergeBackupLists(a)
}

// MergeBackupLists adds the backup's token and contract list entries the wallet's lists of that
// address do not have (an entry already there is kept as it is).
func (w *Wallet) MergeBackupLists(a BackupAccount) error {
	if w.DataDir == "" || (a.Tokens == nil && a.Contracts == nil) {
		return nil
	}
	pub, err := keys.ParseAddress(a.Address)
	if err != nil {
		return err
	}
	var errs []error
	if a.Tokens != nil {
		if err := w.mergeTokens(pub, a); err != nil {
			errs = append(errs, fmt.Errorf("the token list: %w", err))
		}
	}
	if a.Contracts != nil {
		if err := w.mergeContracts(pub, a); err != nil {
			errs = append(errs, fmt.Errorf("the contract list: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (w *Wallet) mergeTokens(pub ed25519.PublicKey, a BackupAccount) error {
	from, err := parseTokenList(a.Tokens, a.Address)
	if err != nil {
		return err
	}
	return w.writeLocalTokens(pub, func(list []LocalToken) ([]LocalToken, bool) {
		changed := false
		for _, t := range from {
			if _, err := keys.ParseAddress(t.Address); err != nil {
				continue
			}
			found := false
			for _, x := range list {
				found = found || (x.Address == t.Address && x.Network == t.Network)
			}
			if !found {
				list, changed = append(list, t), true
			}
		}
		return list, changed
	})
}

func (w *Wallet) mergeContracts(pub ed25519.PublicKey, a BackupAccount) error {
	from, err := parseContractList(a.Contracts, a.Address)
	if err != nil {
		return err
	}
	list, err := w.LocalContracts(pub)
	if err != nil {
		return err
	}
	changed := false
	for _, c := range from {
		if _, err := keys.ParseAddress(c.Address); err != nil {
			continue
		}
		found := false
		for _, x := range list {
			found = found || x.Address == c.Address
		}
		if !found {
			list, changed = append(list, c), true
		}
	}
	if !changed {
		return nil
	}
	data, err := json.MarshalIndent(localContracts{Version: 1, Deployer: a.Address, Contracts: list}, "", "  ")
	if err != nil {
		return err
	}
	return writeListFile(ContractsDir(w.DataDir), w.localListPath(pub), data)
}

// BackupSummary is the metadata of a backup (never its content): for the output and --json.
type BackupSummary struct {
	Created  time.Time `json:"created"`
	Accounts []struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	} `json:"accounts"`
}

// Summary lists the backup's accounts by name and address.
func (b *Backup) Summary() BackupSummary {
	s := BackupSummary{Created: b.Created}
	for _, a := range b.Accounts {
		s.Accounts = append(s.Accounts, struct {
			Name    string `json:"name"`
			Address string `json:"address"`
		}{a.Name, a.Address})
	}
	return s
}

// RestoreResult is what the restore did with an account of the backup.
type RestoreResult struct {
	Name       string `json:"name"`
	Address    string `json:"address"`
	Result     string `json:"result"` // "restored", "skipped" or "failed"
	RestoredAs string `json:"restored_as,omitempty"`
	Reason     string `json:"reason,omitempty"`
	// Warning: the account is restored but a local list could not be merged.
	Warning string `json:"warning,omitempty"`
}

// Restore restores each plans[i] under names[i] ("" skips it; an account whose address is here
// is always skipped, its local lists merged). Accounts are written one by one with the key
// store's writer; a failure is reported and the others go on.
func (w *Wallet) Restore(plans []RestorePlan, names []string) (results []RestoreResult, restored, failed int) {
	for i, p := range plans {
		r := RestoreResult{Name: p.Account.Name, Address: p.Account.Address}
		name := ""
		if i < len(names) {
			name = names[i]
		}
		switch {
		case p.State == RestoreExists:
			r.Result, r.Reason = "skipped", "the address is already the account "+p.Existing
			if err := w.MergeBackupLists(p.Account); err != nil {
				r.Warning = err.Error()
			}
		case name == "":
			r.Result, r.Reason = "skipped", fmt.Sprintf("the name %q is another account's here", p.Account.Name)
		default:
			err := w.RestoreAccount(p.Account, name)
			switch {
			case err != nil && !w.Store.Exists(name):
				r.Result, r.Reason = "failed", err.Error()
				failed++
				results = append(results, r)
				continue
			case err != nil: // the key file is written; a list was not merged
				r.Warning = err.Error()
			}
			r.Result = "restored"
			if name != p.Account.Name {
				r.RestoredAs = name
			}
			restored++
		}
		results = append(results, r)
	}
	return results, restored, failed
}

// FreeRestoreName checks a new name for an account whose name is taken: valid, not an account
// here, not among taken (the names the restore already uses).
func (w *Wallet) FreeRestoreName(name string, taken map[string]bool) error {
	if err := keys.ValidName(name); err != nil {
		return err
	}
	if w.NameTaken(name) || taken[name] {
		return fmt.Errorf("%q is taken too", name)
	}
	return nil
}
