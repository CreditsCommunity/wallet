package wallet

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/thrift/api"
)

// Unlocked is an account opened for a session of the terminal interface (spec 6, "Unlock"): its
// password was typed once, and its key stays in locked memory (keys.LockedKey) until Lock, which
// the interface calls on a manual lock, a switch of account, the inactivity timer and its exit.
// Signing in the session asks no password again. A plain node's key holds nothing in memory: it is
// read from the node's file at each signature, as outside a session.
type Unlocked struct {
	Account *keys.File
	key     *keys.LockedKey // nil: a plain node's key
	nodeSum [32]byte        // a key the node encrypted: its file's SHA-256 when it was opened
}

// ErrUnlock is what a wrong password says: nothing more (spec 6).
var ErrUnlock = errors.New("could not unlock: wrong password, or a damaged key file")

// IsWrongPassword tells whether err is a password that did not open the account.
func IsWrongPassword(err error) bool {
	return errors.Is(err, keys.ErrWrongPassword) || errors.Is(err, keys.ErrNodeKeyPassword) || errors.Is(err, ErrUnlock)
}

// NeedsPassword tells whether an account asks a password to unlock: all but a plain node's key.
func NeedsPassword(f *keys.File) bool { return !f.IsNodeKey() || f.NodeKeyEncrypted }

// Unlock opens an account for a session: the key file decrypted with the password, a node's key
// the node encrypted opened with the node's password (its address must be its NodePublic.txt's),
// or a plain node's key checked (no password). A wrong password is ErrUnlock.
func (w *Wallet) Unlock(f *keys.File, password []byte) (*Unlocked, error) {
	// the derivation's working memory (Argon2id: 256 MiB, 1 GiB for a node's key) goes back now;
	// x/crypto/argon2 does not zero it
	defer debug.FreeOSMemory()
	switch {
	case !f.IsNodeKey():
		key, err := f.Decrypt(password)
		if err != nil {
			if errors.Is(err, keys.ErrWrongPassword) {
				return nil, ErrUnlock
			}
			return nil, err
		}
		defer keys.Zero(key)
		return lockedSession(f, key)
	case !f.NodeKeyEncrypted:
		key, err := w.OpenKey(f, nil)
		if err != nil {
			return nil, err
		}
		keys.Zero(key)
		return &Unlocked{Account: f}, nil
	}
	if len(password) == 0 {
		return nil, ErrUnlock
	}
	key, sum, err := OpenNodeKeyFile(f.NodeKey, password)
	if err != nil {
		if errors.Is(err, keys.ErrNodeKeyPassword) || errors.Is(err, keys.ErrNodeKeyEncrypted) {
			return nil, ErrUnlock
		}
		return nil, fmt.Errorf("%w; the node's key is not used", err)
	}
	defer keys.Zero(key)
	if keys.Address(key.Public().(ed25519.PublicKey)) != f.Public {
		return nil, fmt.Errorf("the key in %s is not the one of %s next to it: refused (the node would not start with them either)", f.NodeKey, keys.NodePublicFile)
	}
	u, err := lockedSession(f, key)
	if u != nil {
		u.nodeSum = sum
	}
	return u, err
}

func lockedSession(f *keys.File, key ed25519.PrivateKey) (*Unlocked, error) {
	lk, err := keys.NewLockedKey(key)
	if err != nil {
		return nil, err
	}
	if keys.Address(lk.Public()) != f.Public {
		lk.Destroy()
		return nil, fmt.Errorf("the key of %s is not its address: refused", f.Name)
	}
	return &Unlocked{Account: f, key: lk}, nil
}

// Lock zeroes the session's key. Safe on nil and twice. A collection follows, so that the heap
// copies made to sign (zeroed) and the expanded keys of crypto/ed25519 (not zeroed by it) are freed
// and their memory reused sooner.
func (u *Unlocked) Lock() {
	if u != nil && u.key != nil && !u.key.Destroyed() {
		u.key.Destroy()
		runtime.GC()
	}
}

// Warning is keys.NotLockedWarning when the key could not be kept in locked memory ("" otherwise,
// and for a plain node's key, which is not kept).
func (u *Unlocked) Warning() string {
	if u == nil || u.key == nil {
		return ""
	}
	return u.key.Warning()
}

// Locked tells whether Lock was called (a plain node's key, which holds nothing, never is).
func (u *Unlocked) Locked() bool { return u.key != nil && u.key.Destroyed() }

// sign signs a plan of the session's account with the session's key.
func (u *Unlocked) sign(w *Wallet, p *SendPlan) (*api.Transaction, error) {
	if p.Account != u.Account.Name {
		return nil, fmt.Errorf("the transaction is from %s, the unlocked account is %s: refused", p.Account, u.Account.Name)
	}
	if u.key == nil {
		return w.sign(p, nil) // a plain node's key: read from its file, as without a session
	}
	if u.Account.NodeKeyEncrypted {
		sum, err := NodeKeyFileSum(u.Account.NodeKey)
		if err != nil {
			return nil, fmt.Errorf("%w; the node's key is not used", err)
		}
		if sum != u.nodeSum {
			return nil, fmt.Errorf("%s changed since the account was unlocked: refused, lock and unlock it again", u.Account.NodeKey)
		}
	}
	var t *api.Transaction
	err := u.key.Use(func(key ed25519.PrivateKey) error {
		var err error
		t, err = signWith(p, key)
		return err
	})
	return t, err
}
