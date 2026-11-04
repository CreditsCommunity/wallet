package main

import "golang.org/x/sys/unix"

// hardenProcess keeps the wallet's memory (keys, the phrase) out of core dumps and away from other
// processes of the same user (ptrace, /proc/PID/mem): not dumpable, no core file. Best effort.
func hardenProcess() {
	_ = unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)
	_ = unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{Cur: 0, Max: 0})
}
