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

// fileOwner is the owner of a file (replaced in tests).
var fileOwner = func(st os.FileInfo) int {
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return int(sys.Uid)
	}
	return -1
}

// ownedSafe refuses a file or folder that is not the wallet user's (or root's) or that the group
// or other users can write to.
func ownedSafe(path string, st os.FileInfo, what, fix string) error {
	if uid := fileOwner(st); uid != os.Geteuid() && uid != 0 {
		return fmt.Errorf("%s %s belongs to another user (uid %d): refused%s", what, path, uid, folderOwnerNote(path))
	}
	if st.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s %s can be written by other users (mode %v): refused, %s%s first", what, path, st.Mode().Perm(), fix, path)
	}
	return nil
}

// readableWarning is a warning for a node's key file that other users can read, with its fix.
func readableWarning(path string, st os.FileInfo) string {
	if st.Mode().Perm()&0o044 == 0 {
		return ""
	}
	return fmt.Sprintf("%s can be read by other users of this machine (mode %v): chmod 600 %s", path, st.Mode().Perm(), path)
}

// permissionsChecked: the owner and mode checks are done on this system.
const permissionsChecked = true

// PermissionsChecked tells whether the owner and permission checks of the node's key are done here.
func PermissionsChecked() bool { return permissionsChecked }
