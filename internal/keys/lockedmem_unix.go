//go:build unix

package keys

import "golang.org/x/sys/unix"

// lockedMem: a page of its own (mmap), mlocked, out of core dumps on Linux, PROT_NONE when idle.
type lockedMem struct {
	b    []byte // the secret's bytes
	page []byte // the mapping (nil: Go's heap, when mmap failed)
}

// newLockedMem: locked is false when the key could not be kept out of swap (mmap or mlock failed).
func newLockedMem(n int) (mem *lockedMem, locked bool) {
	if heapOnly {
		return &lockedMem{b: make([]byte, n)}, false
	}
	page, err := unix.Mmap(-1, 0, unix.Getpagesize(), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil || len(page) < n {
		return &lockedMem{b: make([]byte, n)}, false
	}
	locked = unix.Mlock(page) == nil // RLIMIT_MEMLOCK may refuse it
	dontDump(page)
	return &lockedMem{b: page[:n:n], page: page}, locked
}

func (m *lockedMem) protect() {
	if m.page != nil {
		_ = unix.Mprotect(m.page, unix.PROT_NONE)
	}
}

func (m *lockedMem) unprotect() {
	if m.page != nil {
		_ = unix.Mprotect(m.page, unix.PROT_READ|unix.PROT_WRITE)
	}
}

func (m *lockedMem) free() {
	if m.page != nil {
		_ = unix.Munlock(m.page)
		_ = unix.Munmap(m.page)
	}
	m.b, m.page = nil, nil
}
