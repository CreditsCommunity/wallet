package keys

import (
	"bufio"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// availableMemory: MemAvailable of /proc/meminfo (what can be allocated without swapping), or
// sysinfo's free and buffer RAM on kernels without it.
func availableMemory() (uint64, bool) {
	if f, err := os.Open("/proc/meminfo"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if rest, ok := strings.CutPrefix(sc.Text(), "MemAvailable:"); ok {
				fields := strings.Fields(rest) // "1234 kB"
				if len(fields) > 0 {
					if kb, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
						return kb * 1024, true
					}
				}
			}
		}
	}
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err != nil {
		return 0, false
	}
	return (uint64(si.Freeram) + uint64(si.Bufferram)) * uint64(si.Unit), true
}
