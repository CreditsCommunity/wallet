package wallet

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockBusy tells whether another process holds the exclusive lock (LockFileEx) on path, as the
// community node does on db\.lock. The file is opened for reading, never created; a lock taken
// here is released at once.
func lockBusy(path string) bool {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return errors.Is(err, windows.ERROR_SHARING_VIOLATION)
	}
	defer windows.CloseHandle(h)
	ol := new(windows.Overlapped)
	err = windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if err == nil {
		_ = windows.UnlockFileEx(h, 0, 1, 0, ol)
		return false
	}
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

// folderSafe: on Windows the folder's ACL is not checked (the wallet sets no ACLs itself).
func folderSafe(real string, st os.FileInfo) error { return nil }

// ownedSafe: on Windows the ACLs are not checked.
func ownedSafe(path string, st os.FileInfo, what, fix string) error { return nil }

// fileOwner: not used on Windows (the tests that replace it skip there).
var fileOwner = func(st os.FileInfo) int { return -1 }

// readableWarning: not on Windows (the ACLs are not checked).
func readableWarning(path string, st os.FileInfo) string { return "" }

// permissionsChecked: on Windows the owner and ACL of the node's key and folder are not checked.
const permissionsChecked = false

// PermissionsChecked tells whether the owner and permission checks of the node's key are done here.
func PermissionsChecked() bool { return permissionsChecked }
