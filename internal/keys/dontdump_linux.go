package keys

import "golang.org/x/sys/unix"

// dontDump leaves the page out of core dumps (best effort).
func dontDump(page []byte) { _ = unix.Madvise(page, unix.MADV_DONTDUMP) }
