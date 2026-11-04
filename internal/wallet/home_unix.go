//go:build unix

package wallet

import (
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// ownedByMe tells whether dir belongs to the effective user.
func ownedByMe(dir string) bool {
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(sys.Uid) == os.Geteuid()
}

// accountHome is the home folder of the effective user's account (the password database).
func accountHome() (string, error) {
	u, err := user.LookupId(strconv.Itoa(os.Geteuid()))
	if err != nil {
		return "", err
	}
	return u.HomeDir, nil
}
