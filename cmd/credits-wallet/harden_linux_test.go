package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/CreditsCommunity/wallet/internal/wallet"
)

// L5: the process is not dumpable and has no core limit, and the recovery file is still written
// (an unnamed file is named through /proc/self/fd, which a non-dumpable process may still use).
func TestHardenProcess(t *testing.T) {
	hardenProcess()
	if d, err := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0); err != nil || d != 0 {
		t.Errorf("dumpable %d %v", d, err)
	}
	var rl unix.Rlimit
	if unix.Getrlimit(unix.RLIMIT_CORE, &rl) != nil || rl.Cur != 0 {
		t.Errorf("core limit %+v", rl)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := wallet.WriteSecretFile(path, []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("left behind: %v", entries)
	}
}
