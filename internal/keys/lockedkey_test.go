package keys

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"sync"
	"testing"
	"time"
)

func testLockedKey(t *testing.T) *LockedKey {
	t.Helper()
	key, _ := FromBase58(testSecret)
	lk, err := NewLockedKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return lk
}

// The session's key: usable until Destroy, the copy lent to Use zeroed after it, then refused;
// Destroy twice is fine; Destroyed says so.
func TestLockedKeyUseAndDestroy(t *testing.T) {
	lk := testLockedKey(t)
	if lk.Destroyed() || Address(lk.Public()) != testAddress {
		t.Fatal("new key")
	}
	var lent ed25519.PrivateKey
	err := lk.Use(func(k ed25519.PrivateKey) error {
		lent = k
		if Base58(k) != testSecret {
			t.Error("another key")
		}
		if !ed25519.Verify(lk.Public(), []byte("x"), ed25519.Sign(k, []byte("x"))) {
			t.Error("does not sign")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(lent, make([]byte, 64)) {
		t.Error("the copy lent to Use was not zeroed")
	}
	want := errors.New("fn's error")
	if err := lk.Use(func(ed25519.PrivateKey) error { return want }); err != want {
		t.Errorf("fn's error: %v", err)
	}
	lk.Destroy()
	lk.Destroy() // twice: fine
	var nilKey *LockedKey
	nilKey.Destroy() // nil: fine
	if !lk.Destroyed() {
		t.Error("not Destroyed")
	}
	called := false
	if err := lk.Use(func(ed25519.PrivateKey) error { called = true; return nil }); !errors.Is(err, ErrLocked) || called {
		t.Errorf("Use after Destroy: %v, called %v", err, called)
	}
}

// Destroy waits for a Use in progress: the key is not zeroed under a signature.
func TestLockedKeyDestroyWaitsForUse(t *testing.T) {
	lk := testLockedKey(t)
	inUse, release := make(chan struct{}), make(chan struct{})
	var seen string
	go lk.Use(func(k ed25519.PrivateKey) error {
		close(inUse)
		<-release
		seen = Base58(k)
		return nil
	})
	<-inUse
	destroyed := make(chan struct{})
	go func() { lk.Destroy(); close(destroyed) }()
	select {
	case <-destroyed:
		t.Fatal("Destroy did not wait for Use")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-destroyed
	if seen != testSecret || !lk.Destroyed() {
		t.Errorf("seen %q, destroyed %v", seen, lk.Destroyed())
	}
}

// The heap fallback (no page of its own, as when mmap is refused): the key works, Warning says it
// may reach swap, Destroy zeroes it.
func TestLockedKeyHeapFallback(t *testing.T) {
	heapOnly = true
	defer func() { heapOnly = false }()
	lk := testLockedKey(t)
	if lk.Warning() != NotLockedWarning || lk.Warning() != "the key could not be locked in memory: it may reach swap" {
		t.Errorf("warning %q", lk.Warning())
	}
	mem := lk.mem.b
	if err := lk.Use(func(k ed25519.PrivateKey) error {
		if Base58(k) != testSecret {
			t.Error("another key")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	lk.Destroy()
	if !bytes.Equal(mem, make([]byte, 64)) {
		t.Error("the heap copy was not zeroed by Destroy")
	}
}

// Concurrent Use and Destroy (run with -race): every Use sees the whole key or ErrLocked.
func TestLockedKeyConcurrent(t *testing.T) {
	for range 20 {
		lk := testLockedKey(t)
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 20 {
					err := lk.Use(func(k ed25519.PrivateKey) error {
						if Base58(k) != testSecret {
							t.Error("a partial key")
						}
						return nil
					})
					if err != nil && !errors.Is(err, ErrLocked) {
						t.Error(err)
					}
				}
			}()
		}
		wg.Add(1)
		go func() { defer wg.Done(); lk.Destroy() }()
		wg.Wait()
		if !lk.Destroyed() {
			t.Fatal("not destroyed")
		}
	}
}
