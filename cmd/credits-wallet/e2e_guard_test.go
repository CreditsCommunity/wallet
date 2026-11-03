package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ci/privnet/e2e.sh empties its work folder: only one it made (its marker), a new or an empty one,
// never / or $HOME.
func TestE2EWorkFolderGuard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash script")
	}
	home := t.TempDir()
	run := func(work string) (string, error) {
		cmd := exec.Command("bash", "../../ci/privnet/e2e.sh")
		cmd.Env = append(os.Environ(), "NODE_BIN=/bin/false", "WALLET=/bin/false", "IDL=/", "CONTRACTS=0", "GUARD_ONLY=1",
			"HOME="+home, "WORK="+work)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	// a folder with someone's files: refused, nothing removed
	other := t.TempDir()
	keep := filepath.Join(other, "precious.txt")
	os.WriteFile(keep, []byte("x"), 0o600)
	if out, err := run(other); err == nil || !strings.Contains(out, "refused") {
		t.Errorf("a foreign folder was accepted: %v %s", err, out)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("a file of a foreign folder was removed")
	}
	for _, bad := range []string{"/", home, home + "/"} {
		if out, err := run(bad); err == nil || !strings.Contains(out, "refused") {
			t.Errorf("WORK=%s accepted: %s", bad, out)
		}
	}
	// a new folder gets the marker; then its leftovers are removed on the next run
	work := filepath.Join(t.TempDir(), "run")
	if out, err := run(work); err != nil {
		t.Fatalf("new folder: %v %s", err, out)
	}
	os.WriteFile(filepath.Join(work, "old.log"), []byte("x"), 0o600)
	if out, err := run(work); err != nil {
		t.Fatalf("own folder: %v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(work, "old.log")); err == nil {
		t.Error("the leftovers of the script's own folder were kept")
	}
	if _, err := os.Stat(filepath.Join(work, ".credits-wallet-e2e")); err != nil {
		t.Error("no marker")
	}
}
