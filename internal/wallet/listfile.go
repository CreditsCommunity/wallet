package wallet

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// The local lists (contracts/, tokens/ in the data folder) are small JSON files of public data.

// maxLocalList bounds the file the wallet reads.
const maxLocalList = 4 << 20

// readListFile reads a local list: nil and no error when there is none. Only a regular file is
// read: a link, a FIFO or a device in its place is refused.
func readListFile(path string) ([]byte, error) {
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file (%v): refused, remove it", path, st.Mode().Type())
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxLocalList+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxLocalList {
		return nil, fmt.Errorf("%s is larger than %d bytes: not read", path, maxLocalList)
	}
	return data, nil
}

// writeListFile writes a local list whole to a temporary file (mode 600) in dir, then renames it
// over path (a rename replaces a link itself, never what it points to). Two wallets writing at the
// same moment: the last writer wins.
func writeListFile(dir, path string, data []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if filepath.Dir(path) != filepath.Clean(dir) {
		return fmt.Errorf("%s is not in %s", path, dir)
	}
	tmp, err := os.CreateTemp(dir, ".list-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
