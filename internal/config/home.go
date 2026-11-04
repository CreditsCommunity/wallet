package config

import (
	"errors"
	"fmt"
	"os"
	"runtime"
)

// UserHome is the home folder of the user the wallet runs as: %USERPROFILE% on Windows; on Linux
// and macOS $HOME, unless it is not a folder of the effective user (sudo without -H keeps the
// invoking user's $HOME), in which case the home of the effective user's account. The data folder
// and the recovery file both use it, so they never disagree.
func UserHome() (string, error) {
	if runtime.GOOS == "windows" {
		if p := os.Getenv("USERPROFILE"); p != "" {
			return p, nil
		}
		return os.UserHomeDir()
	}
	home := os.Getenv("HOME")
	if home != "" && ownedByMe(home) {
		return home, nil
	}
	if h, err := accountHome(); err == nil && h != "" {
		return h, nil
	}
	if home != "" {
		return home, nil
	}
	return "", errors.New("no home folder: HOME is not set")
}

// HomeWarning is a warning when the home folder may not be the one the user expects: the wallet
// runs as root, or $HOME is not a folder of the effective user (sudo without -H). "" otherwise.
func HomeWarning() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	home, err := UserHome()
	if err != nil {
		return ""
	}
	if env := os.Getenv("HOME"); env != "" && env != home {
		return fmt.Sprintf("HOME (%s) is not a folder of the user the wallet runs as (sudo without -H?): it uses %s", env, home)
	}
	if os.Geteuid() == 0 {
		return fmt.Sprintf("the wallet runs as root: its files go to root's home (%s); run it as the user it is for", home)
	}
	return ""
}
