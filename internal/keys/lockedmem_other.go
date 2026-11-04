//go:build !unix && !windows

package keys

// lockedMem on other systems: Go's heap, zeroed on Destroy.
type lockedMem struct{ b []byte }

func newLockedMem(n int) (*lockedMem, bool) { return &lockedMem{b: make([]byte, n)}, false }
func (m *lockedMem) protect()               {}
func (m *lockedMem) unprotect()             {}
func (m *lockedMem) free()                  { m.b = nil }
