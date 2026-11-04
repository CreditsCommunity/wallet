package keys

import "golang.org/x/sys/unix"

// availableMemory on macOS: the physical memory (hw.memsize). macOS reports no "available" figure
// as Linux does (free pages leave out what it reclaims at once), and it compresses and swaps
// rather than killing the process: only a machine with less memory in all than the node's KDF
// needs is refused.
func availableMemory() (uint64, bool) {
	n, err := unix.SysctlUint64("hw.memsize")
	return n, err == nil
}
