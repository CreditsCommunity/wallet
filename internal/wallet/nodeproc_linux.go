package wallet

import (
	"os"
	"path/filepath"
	"strings"
)

// nodeProcessIn returns the PID of a process named node whose working folder is dir ("" when
// none): the node runs in its folder. Only the processes /proc lets this user see are looked at.
func nodeProcessIn(dir string) string {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return ""
	}
	for _, e := range entries {
		pid := e.Name()
		if pid == "" || strings.Trim(pid, "0123456789") != "" {
			continue
		}
		cwd, err := os.Readlink(filepath.Join("/proc", pid, "cwd"))
		if err != nil || filepath.Clean(cwd) != filepath.Clean(dir) {
			continue
		}
		exe, err := os.Readlink(filepath.Join("/proc", pid, "exe"))
		if err != nil {
			continue
		}
		if filepath.Base(strings.TrimSuffix(exe, " (deleted)")) == "node" {
			return pid
		}
	}
	return ""
}
