//go:build !linux

package wallet

// nodeProcessIn: processes are looked at on Linux only (db/.lock and the API port elsewhere).
func nodeProcessIn(dir string) string { return "" }
