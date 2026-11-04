//go:build unix && !linux

package keys

func dontDump([]byte) {}
