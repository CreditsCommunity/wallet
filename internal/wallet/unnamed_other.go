//go:build !linux

package wallet

// writeUnnamed: unnamed files (O_TMPFILE) are Linux only; the caller uses the named fallback.
func writeUnnamed(string, []byte) (bool, error) { return false, nil }
