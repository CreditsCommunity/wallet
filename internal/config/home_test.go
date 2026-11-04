package config

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// L3: with a HOME that is not the effective user's (sudo without -H), the data folder and the
// recovery file use the same home, and a warning says so.
func TestHomeNotMine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("USERPROFILE on Windows")
	}
	t.Setenv("CREDITS_WALLET_HOME", "")
	t.Setenv("HOME", "/") // root's
	home, err := UserHome()
	if err != nil {
		t.Skip(err)
	}
	dir, err := DataDir()
	if err != nil || dir != filepath.Join(home, ".credits-wallet") {
		t.Errorf("DataDir %q %v, want it in %q", dir, err, home)
	}
	if home == "/" {
		t.Skip("running as root") // then / is the effective user's
	}
	if w := HomeWarning(); !strings.Contains(w, "sudo without -H") {
		t.Errorf("no warning: %q", w)
	}
	t.Setenv("HOME", t.TempDir())
	if w := HomeWarning(); w != "" {
		t.Errorf("a warning with the user's own HOME: %q", w)
	}
}
