//go:build !windows

package wallet

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// holdLock takes the node's lock on path (flock, as the community node) until unlock.
func holdLock(t *testing.T, path string) (unlock func()) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	return func() { f.Close() }
}
