//go:build unix && !linux

package main

import "golang.org/x/sys/unix"

// hardenProcess: no core file with the wallet's memory in it. Best effort.
func hardenProcess() {
	_ = unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{Cur: 0, Max: 0})
}
