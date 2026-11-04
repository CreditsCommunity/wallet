//go:build !windows

package config

import (
	"fmt"
	"os"
	"syscall"
)

// fileOwner is the owner of a file (-1: unknown; replaced in tests).
var fileOwner = func(st os.FileInfo) int {
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return int(sys.Uid)
	}
	return -1
}

// ownedSafe refuses a file or folder that is not the user's (or root's), or that others can write
// to: the checks of the node's key (wallet.ownedSafe).
func ownedSafe(path string, st os.FileInfo, what string) error {
	if uid := fileOwner(st); uid != -1 && uid != os.Geteuid() && uid != 0 {
		return fmt.Errorf("%s %s belongs to another user (uid %d): refused", what, path, uid)
	}
	if st.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s %s can be written by other users (mode %v): refused, chmod go-w %s first", what, path, st.Mode().Perm(), path)
	}
	return nil
}
