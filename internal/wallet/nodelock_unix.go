//go:build !windows

package wallet

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// lockBusy tells whether another process holds the exclusive lock (flock) on path, as the
// community node does on db/.lock. The file is opened read-only, never created; a lock taken here
// is released at once.
func lockBusy(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		return false
	}
	return errors.Is(err, unix.EWOULDBLOCK)
}

// folderSafe refuses a node folder that is not the wallet user's, or that others can write to.
func folderSafe(real string, st os.FileInfo) error {
	sys, ok := st.Sys().(*syscall.Stat_t)
	if ok && int(sys.Uid) != os.Geteuid() {
		return fmt.Errorf("%s belongs to another user (uid %d): refused%s", real, sys.Uid, folderOwnerNote(real))
	}
	if st.Mode().Perm()&0o002 != 0 {
		return fmt.Errorf("%s can be written by every user (mode %v): refused, chmod o-w it first", real, st.Mode().Perm())
	}
	return nil
}
