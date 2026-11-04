//go:build windows

package keys

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// lockedMem: pages of their own (VirtualAlloc), VirtualLocked, PAGE_NOACCESS when idle.
type lockedMem struct {
	b    []byte
	addr uintptr // 0: Go's heap, when VirtualAlloc failed
	size uintptr
}

// newLockedMem: locked is false when the key could not be kept out of swap.
func newLockedMem(n int) (mem *lockedMem, locked bool) {
	if heapOnly {
		return &lockedMem{b: make([]byte, n)}, false
	}
	size := uintptr(4096)
	addr, err := windows.VirtualAlloc(0, size, windows.MEM_COMMIT|windows.MEM_RESERVE, windows.PAGE_READWRITE)
	if err != nil || addr == 0 {
		return &lockedMem{b: make([]byte, n)}, false
	}
	locked = windows.VirtualLock(addr, size) == nil
	p := *(*unsafe.Pointer)(unsafe.Pointer(&addr)) // memory outside Go's heap: vet's uintptr rule does not apply
	b := unsafe.Slice((*byte)(p), n)
	return &lockedMem{b: b, addr: addr, size: size}, locked
}

func (m *lockedMem) protect() {
	if m.addr != 0 {
		var old uint32
		_ = windows.VirtualProtect(m.addr, m.size, windows.PAGE_NOACCESS, &old)
	}
}

func (m *lockedMem) unprotect() {
	if m.addr != 0 {
		var old uint32
		_ = windows.VirtualProtect(m.addr, m.size, windows.PAGE_READWRITE, &old)
	}
}

func (m *lockedMem) free() {
	if m.addr != 0 {
		_ = windows.VirtualUnlock(m.addr, m.size)
		_ = windows.VirtualFree(m.addr, 0, windows.MEM_RELEASE)
	}
	m.b, m.addr = nil, 0
}
