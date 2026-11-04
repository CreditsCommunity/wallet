package keys

import (
	"crypto/ed25519"
	"errors"
	"sync"
)

// LockedKey holds a decrypted key for the length of a session of the terminal interface (spec
// 4.2): in memory of its own outside Go's heap (mmap / VirtualAlloc, so the garbage collector never
// copies it), locked against swapping (mlock / VirtualLock), left out of core dumps (Linux
// MADV_DONTDUMP), and inaccessible (PROT_NONE / PAGE_NOACCESS) except to copy it out for a
// signature (Use). Destroy zeroes it and gives it back; it is what locking the session does.
//
// What it does not cover: each signature works on a copy on Go's heap (zeroed after it), and
// crypto/ed25519 derives from it an expanded key on the heap that the standard library never
// zeroes (it stays until the garbage collector reuses that memory). The password typed to unlock
// passes through the terminal interface's text input as Go strings, which cannot be zeroed. Each
// step is best effort: where the system refuses the locked page or the lock (an mlock limit, an
// unknown system) the key is kept on the heap or unlocked, Warning says so, and Destroy still
// zeroes it.
type LockedKey struct {
	mu     sync.Mutex
	mem    *lockedMem
	public ed25519.PublicKey
	warn   string
}

// ErrLocked is returned by a LockedKey after Destroy.
var ErrLocked = errors.New("the account is locked: unlock it again")

// NotLockedWarning is LockedKey.Warning when the key could not be kept out of swap.
const NotLockedWarning = "the key could not be locked in memory: it may reach swap"

// NewLockedKey copies key into locked memory. The caller zeroes its own copy.
func NewLockedKey(key ed25519.PrivateKey) (*LockedKey, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("not a secret key")
	}
	mem, locked := newLockedMem(ed25519.PrivateKeySize)
	copy(mem.b, key)
	mem.protect()
	pub := make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(pub, key[32:])
	lk := &LockedKey{mem: mem, public: pub}
	if !locked {
		lk.warn = NotLockedWarning
	}
	return lk, nil
}

// Public is the key's public half.
func (k *LockedKey) Public() ed25519.PublicKey { return k.public }

// Warning is NotLockedWarning when the key is not in locked memory (mmap or mlock refused), "" when
// it is.
func (k *LockedKey) Warning() string { return k.warn }

// Use lends a copy of the key to fn and zeroes the copy afterwards; fn must not keep it. The copy
// is on Go's heap: crypto/ed25519 keeps a weak reference to the key it signs with (its cache of the
// expanded key), which the runtime allows only for heap memory; that expanded key is not zeroed
// (see LockedKey).
func (k *LockedKey) Use(fn func(ed25519.PrivateKey) error) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.mem == nil {
		return ErrLocked
	}
	key := make(ed25519.PrivateKey, ed25519.PrivateKeySize)
	defer Zero(key)
	k.mem.unprotect()
	copy(key, k.mem.b)
	k.mem.protect()
	return fn(key)
}

// Destroy zeroes the key and frees its memory; it waits for a Use in progress. Safe to call twice.
func (k *LockedKey) Destroy() {
	if k == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.mem == nil {
		return
	}
	k.mem.unprotect()
	Zero(k.mem.b)
	k.mem.free()
	k.mem = nil
}

// Destroyed tells whether Destroy was called.
func (k *LockedKey) Destroyed() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.mem == nil
}

// heapOnly makes newLockedMem use Go's heap, as when the system refuses a page of its own (tests).
var heapOnly = false
