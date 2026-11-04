package wallet

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// holdLock takes the node's lock on path (LockFileEx over the whole file, as the community node)
// until unlock.
func holdLock(t *testing.T, path string) (unlock func()) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, ^uint32(0), ^uint32(0), ol); err != nil {
		t.Fatal(err)
	}
	return func() { f.Close() }
}
