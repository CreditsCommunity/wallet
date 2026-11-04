package wallet

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"

	"github.com/CreditsCommunity/wallet/internal/keys"
)

// One wallet password (spec 4.2): it opens accounts/wallet.key, whose random master key encrypts
// every account file of version 2. A Session is the wallet opened: the master key in locked memory
// (keys.LockedKey), until Lock. The node's key is apart: a plain NodePrivate.txt needs no password,
// one the node encrypted keeps the node's own password.

var (
	// ErrMigrateFirst: the wallet's accounts still have passwords of their own and no wallet
	// password exists yet.
	ErrMigrateFirst = errors.New("this wallet's accounts still have a password each: choose the wallet password first ('credits-wallet wallet migrate', or start the terminal interface)")
	// ErrNoWalletPassword: no account yet, no wallet password yet.
	ErrNoWalletPassword = errors.New("the wallet has no password yet: it is chosen with the first account")
	// ErrChangeInterrupted: a change of the wallet password was interrupted, and the old password
	// was typed.
	ErrChangeInterrupted = errors.New("a change of the wallet password was interrupted: type the NEW password, which finishes it")
)

// Session is the wallet unlocked.
type Session struct {
	master *keys.LockedKey
	ID     string // the wallet key's id
}

// Lock zeroes the master key. Safe on nil and twice.
func (s *Session) Lock() {
	if s != nil && s.master != nil && !s.master.Destroyed() {
		s.master.Destroy()
		runtime.GC()
	}
}

// Locked tells whether Lock was called.
func (s *Session) Locked() bool { return s == nil || s.master == nil || s.master.Destroyed() }

// Warning is keys.NotLockedWarning when the master key is not in locked memory.
func (s *Session) Warning() string {
	if s == nil || s.master == nil {
		return ""
	}
	return s.master.Warning()
}

func newSession(id string, master []byte) *Session {
	return &Session{master: keys.NewLockedSecret(master), ID: id}
}

// Open decrypts an account under the wallet password. The caller zeroes the key.
func (s *Session) Open(f *keys.File) (key ed25519.PrivateKey, err error) {
	if s.Locked() {
		return nil, keys.ErrLocked
	}
	err = s.master.UseSecret(func(m []byte) error {
		key, err = f.DecryptWithMaster(m, s.ID)
		return err
	})
	return key, err
}

// Encrypt makes the account file of a key under the wallet password.
func (s *Session) Encrypt(name string, key ed25519.PrivateKey) (f *keys.File, err error) {
	if s.Locked() {
		return nil, keys.ErrLocked
	}
	err = s.master.UseSecret(func(m []byte) error {
		f, err = keys.EncryptWithMaster(name, key, m, s.ID)
		return err
	})
	return f, err
}

// HasWalletPassword tells whether the wallet has its password (wallet.key).
func (w *Wallet) HasWalletPassword() bool { return w.Store.HasWalletKey() }

// OldPasswordAccounts are the accounts of the store that still have a password of their own.
func (w *Wallet) OldPasswordAccounts() []*keys.File {
	files, _, _ := w.Store.List()
	var out []*keys.File
	for _, f := range files {
		if f.OldPassword() {
			out = append(out, f)
		}
	}
	return out
}

// NeedsMigration: accounts with passwords of their own and no wallet password yet.
func (w *Wallet) NeedsMigration() bool {
	return !w.HasWalletPassword() && len(w.OldPasswordAccounts()) > 0
}

// CreateWalletPassword makes the wallet key under a new wallet password (never over one).
func (w *Wallet) CreateWalletPassword(password []byte) (*Session, error) {
	if w.HasWalletPassword() {
		return nil, errors.New("the wallet already has a password")
	}
	master, id, err := keys.NewMasterKey()
	if err != nil {
		return nil, err
	}
	defer keys.Zero(master)
	defer debug.FreeOSMemory()
	k, err := keys.SealWalletKey(id, master, "", nil, password, w.KDF)
	if err != nil {
		return nil, err
	}
	if err := w.Store.SaveWalletKey(k, false, false); err != nil {
		return nil, err
	}
	return newSession(id, master), nil
}

// OpenWallet opens the wallet with its password. An interrupted change of password is finished
// here with the new password; the old one says ErrChangeInterrupted. An interrupted migration is
// checked and its copies removed (FinishMigration).
func (w *Wallet) OpenWallet(password []byte) (*Session, error) {
	defer debug.FreeOSMemory() // Argon2id's working memory
	if next, err := w.Store.LoadWalletKey(true); err == nil {
		master, previous, err := next.Open(password)
		if err == nil {
			defer keys.Zero(master)
			defer keys.Zero(previous)
			if err := w.finishChange(next.ID, master, next.PreviousID, previous, password); err != nil {
				return nil, fmt.Errorf("finishing the change of the wallet password: %w", err)
			}
			s := newSession(next.ID, master)
			w.FinishMigration(s)
			return s, nil
		}
		if cur, err := w.Store.LoadWalletKey(false); err == nil {
			if m, _, err := cur.Open(password); err == nil {
				keys.Zero(m)
				return nil, ErrChangeInterrupted
			}
		}
		return nil, keys.ErrWalletPassword
	}
	k, err := w.Store.LoadWalletKey(false)
	if errors.Is(err, os.ErrNotExist) {
		if w.NeedsMigration() {
			return nil, ErrMigrateFirst
		}
		return nil, ErrNoWalletPassword
	}
	if err != nil {
		return nil, err
	}
	master, _, err := k.Open(password)
	if err != nil {
		return nil, err
	}
	defer keys.Zero(master)
	s := newSession(k.ID, master)
	w.FinishMigration(s)
	return s, nil
}

// crashAt, in tests, fails a step of a change of password or a migration (a crash there).
var crashAt = func(step string) error { return nil }

// ChangeWalletPassword changes the wallet password: a new master key, every account re-encrypted
// under it, then the new wallet key. Each file is replaced atomically; wallet.key.next (written
// first, exclusive) holds the new key and the old one under the new password, so that opening the
// wallet with the new password finishes a change interrupted anywhere.
func (w *Wallet) ChangeWalletPassword(old, new []byte) error {
	if len(new) == 0 {
		return errors.New("the new password is empty")
	}
	if _, err := os.Lstat(w.Store.WalletKeyNextPath()); err == nil {
		return ErrChangeInterrupted
	}
	cur, err := w.Store.LoadWalletKey(false)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNoWalletPassword
	}
	if err != nil {
		return err
	}
	defer debug.FreeOSMemory()
	m1, _, err := cur.Open(old)
	if err != nil {
		return err
	}
	defer keys.Zero(m1)
	m2, id2, err := keys.NewMasterKey()
	if err != nil {
		return err
	}
	defer keys.Zero(m2)
	next, err := keys.SealWalletKey(id2, m2, cur.ID, m1, new, w.KDF)
	if err != nil {
		return err
	}
	if err := w.Store.SaveWalletKey(next, true, false); err != nil {
		return err
	}
	return w.finishChange(id2, m2, cur.ID, m1, new)
}

// finishChange re-encrypts the accounts still under the previous key, writes the new wallet key
// and removes wallet.key.next. Safe to run again after a crash at any step.
func (w *Wallet) finishChange(id2 string, m2 []byte, id1 string, m1 []byte, password []byte) error {
	files, _, err := w.Store.List()
	if err != nil {
		return err
	}
	for _, f := range files {
		if !f.UnderWalletKey() || f.KeyID != id1 {
			continue
		}
		key, err := f.DecryptWithMaster(m1, id1)
		if err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
		nf, err := keys.EncryptWithMaster(f.Name, key, m2, id2)
		keys.Zero(key)
		if err != nil {
			return err
		}
		nf.Created = f.Created
		if err := crashAt("change:" + f.Name); err != nil {
			return err
		}
		if err := w.Store.Replace(nf); err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	if err := crashAt("change:wallet.key"); err != nil {
		return err
	}
	final, err := keys.SealWalletKey(id2, m2, "", nil, password, w.KDF)
	if err != nil {
		return err
	}
	if err := w.Store.SaveWalletKey(final, false, true); err != nil {
		return err
	}
	if err := crashAt("change:next"); err != nil {
		return err
	}
	if err := os.Remove(w.Store.WalletKeyNextPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ---- migration -----------------------------------------------------------------------------------

// MigrationDir holds, during a migration, a copy (mode 0600) of each account file being moved
// under the wallet password; FinishMigration removes it.
func (w *Wallet) MigrationDir() string { return filepath.Join(w.Store.Dir, ".migration") }

// MigrateAccount moves an account with a password of its own under the wallet password: its old
// password opens it, a copy of the old file is kept in MigrationDir, the new file replaces the old
// one atomically and is read back with the wallet key.
func (w *Wallet) MigrateAccount(s *Session, f *keys.File, oldPassword []byte) error {
	if !f.OldPassword() {
		return nil
	}
	key, err := f.Decrypt(oldPassword)
	if err != nil {
		return err
	}
	defer keys.Zero(key)
	if err := os.MkdirAll(w.MigrationDir(), 0o700); err != nil {
		return err
	}
	orig, err := os.ReadFile(w.Store.FilePath(f.Name))
	if err != nil {
		return err
	}
	copyPath := filepath.Join(w.MigrationDir(), f.Name+".json")
	if err := WriteSecretFile(copyPath, orig); err != nil {
		if _, statErr := os.Lstat(copyPath); statErr != nil {
			return err
		} // a copy from an interrupted run: the older one is kept
	}
	nf, err := s.Encrypt(f.Name, key)
	if err != nil {
		return err
	}
	nf.Created = f.Created
	if err := crashAt("migrate:" + f.Name); err != nil {
		return err
	}
	if err := w.Store.Replace(nf); err != nil {
		return err
	}
	back, err := w.Store.Load(f.Name)
	if err != nil {
		return err
	}
	k2, err := s.Open(back)
	if err != nil {
		return fmt.Errorf("%s: the new file does not open (%v): the old one is kept in %s", f.Name, err, copyPath)
	}
	keys.Zero(k2)
	return nil
}

// MigrateWith moves under the wallet password every account the password opens (an account
// whose own password is the wallet password). It returns the accounts it moved and those left.
func (w *Wallet) MigrateWith(s *Session, password []byte) (moved []string, left []*keys.File, err error) {
	defer debug.FreeOSMemory()
	for _, f := range w.OldPasswordAccounts() {
		switch err := w.MigrateAccount(s, f, password); {
		case err == nil:
			moved = append(moved, f.Name)
		case errors.Is(err, keys.ErrWrongPassword):
			left = append(left, f)
		default:
			return moved, left, fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	return moved, left, nil
}

// FinishMigration checks the copies of an interrupted or finished migration and removes them: an
// account file missing is put back from its copy; one now under the wallet key must open with it
// (otherwise the copy is put back); one still with its old password is unchanged. Problems are
// returned (the copies concerned are kept).
func (w *Wallet) FinishMigration(s *Session) []error {
	entries, err := os.ReadDir(w.MigrationDir())
	if err != nil {
		return nil
	}
	var problems []error
	for _, e := range entries {
		name, ok := cutJSON(e.Name())
		copyPath := filepath.Join(w.MigrationDir(), e.Name())
		if !ok || keys.ValidName(name) != nil {
			continue
		}
		cur, err := w.Store.Load(name)
		switch {
		case errors.Is(err, keys.ErrNotFound):
			if err := os.Rename(copyPath, w.Store.FilePath(name)); err != nil {
				problems = append(problems, err)
			}
			continue
		case err != nil:
			problems = append(problems, fmt.Errorf("%s: %w (its old file is kept in %s)", name, err, copyPath))
			continue
		case cur.UnderWalletKey():
			k, err := s.Open(cur)
			if err != nil {
				data, rerr := os.ReadFile(copyPath)
				if rerr == nil {
					if f, perr := keys.ParseFile(data); perr == nil {
						f.Name = name
						rerr = w.Store.Replace(f)
					} else {
						rerr = perr
					}
				}
				if rerr != nil {
					problems = append(problems, fmt.Errorf("%s: %v; its old file is kept in %s", name, rerr, copyPath))
					continue
				}
			} else {
				keys.Zero(k)
			}
		}
		os.Remove(copyPath)
	}
	os.Remove(w.MigrationDir()) // only when empty
	return problems
}

func cutJSON(name string) (string, bool) {
	if len(name) > 5 && name[len(name)-5:] == ".json" {
		return name[:len(name)-5], true
	}
	return "", false
}

// CreateAccountIn encrypts and saves a key under name with the open wallet.
func (w *Wallet) CreateAccountIn(s *Session, name string, key ed25519.PrivateKey) (*keys.File, error) {
	if w.NameTaken(name) {
		return nil, fmt.Errorf("an account named %q already exists", name)
	}
	f, err := s.Encrypt(name, key)
	if err != nil {
		return nil, err
	}
	return f, w.Store.Save(f)
}

// OpenForNewAccount opens the wallet to add an account: with its password, or, for the first
// account, a new wallet password (the caller asked it twice). A wallet whose accounts still have
// their own passwords is migrated first.
func (w *Wallet) OpenForNewAccount(password []byte) (*Session, error) {
	if w.HasWalletPassword() {
		return w.OpenWallet(password)
	}
	if w.NeedsMigration() {
		return nil, ErrMigrateFirst
	}
	return w.CreateWalletPassword(password)
}

// ExportEncrypted is an account's key as a key file of its own (version 1: Argon2id under
// filePassword, importable by any wallet): password opens the account (the wallet password, its
// old password, or the node's).
func (w *Wallet) ExportEncrypted(f *keys.File, password, filePassword []byte) ([]byte, error) {
	if f.NodeKeyEncrypted {
		return nil, fmt.Errorf("%s is the node's key, encrypted by the node in %s: the wallet does not export it", f.Name, f.NodeKey)
	}
	key, err := w.OpenKey(f, password)
	if err != nil {
		return nil, err
	}
	defer keys.Zero(key)
	defer debug.FreeOSMemory()
	kf, err := keys.Encrypt(f.Name, key, filePassword, w.KDF)
	if err != nil {
		return nil, err
	}
	return marshalKeyFile(kf)
}

func marshalKeyFile(f *keys.File) ([]byte, error) {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// UnlockWith opens an account of the open wallet for the session of the terminal interface: no
// password for an account under the wallet password or a plain node's key. An account with an
// old password, or the node's key the node encrypted, needs Unlock with its own password.
func (w *Wallet) UnlockWith(s *Session, f *keys.File) (*Unlocked, error) {
	switch {
	case f.UnderWalletKey():
		// read again: the list shown may be older than a change of the wallet password
		cur, err := w.Store.Load(f.Name)
		if err != nil {
			return nil, err
		}
		if cur.Public != f.Public {
			return nil, fmt.Errorf("the key file of %s changed: its address is not the one shown", f.Name)
		}
		key, err := s.Open(cur)
		if err != nil {
			return nil, err
		}
		defer keys.Zero(key)
		return lockedSession(cur, key)
	case f.IsNodeKey() && !f.NodeKeyEncrypted:
		return w.Unlock(f, nil)
	}
	return nil, errors.New("this account has a password of its own")
}
