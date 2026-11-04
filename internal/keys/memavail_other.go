//go:build !linux && !windows && !darwin

package keys

// availableMemory: unknown on this system (the opening is tried).
func availableMemory() (uint64, bool) { return 0, false }
