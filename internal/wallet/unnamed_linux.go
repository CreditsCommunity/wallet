package wallet

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

// writeUnnamed writes data to an unnamed file in path's folder (O_TMPFILE), then names it path
// (linkat through /proc/self/fd, which fails if anything is at path). done is false when the
// kernel, the file system or /proc does not allow it and nothing was written: the caller then
// uses the named fallback.
func writeUnnamed(path string, data []byte) (done bool, err error) {
	fd, err := unix.Open(filepath.Dir(path), unix.O_TMPFILE|unix.O_WRONLY|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return false, nil // EOPNOTSUPP, EISDIR (old kernels), EINVAL...
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return true, err
	}
	if _, err := f.Write(data); err != nil {
		return true, err
	}
	if err := f.Sync(); err != nil {
		return true, err
	}
	err = unix.Linkat(unix.AT_FDCWD, "/proc/self/fd/"+strconv.Itoa(fd), unix.AT_FDCWD, path, unix.AT_SYMLINK_FOLLOW)
	switch {
	case errors.Is(err, unix.EEXIST):
		return true, existsErr(path)
	case err != nil:
		return false, nil // no /proc, or linkat refused: the unnamed file goes away on Close
	}
	return true, sameFile(f, path)
}
