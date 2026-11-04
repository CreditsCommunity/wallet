package wallet

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mr-tron/base58"

	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/keys"
)

// The recovery file: on request, after the phrase of a new account was shown and checked, its 24
// words also go to a file. The file is never next to the keys: not in the data folder (where the
// encrypted key files are), not in a folder that holds key files of any kind. A copy of the words
// beside the key files would let whoever takes that folder skip the password altogether. It is
// created new (never overwriting, never through a symbolic link), owner-only (0600), written
// whole (see WriteSecretFile).

// RecoveryAdvice is what to do with the file once written (terminal and TUI).
const RecoveryAdvice = "This file holds your account in clear: copy it to paper or an offline USB key, then delete it from this computer."

// RecoveryQuestion is the question asked after the check of the phrase; the default is no.
const RecoveryQuestion = "Save the 24 words to a file as well? [y/N] "

// UserHome is config.UserHome: the data folder and the recovery file use the same home.
func UserHome() (string, error) { return config.UserHome() }

// DefaultRecoveryPath is ~/credits-recovery-<account>.txt (%USERPROFILE%\... on Windows).
func DefaultRecoveryPath(account string) (string, error) {
	home, err := UserHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "credits-recovery-"+account+".txt"), nil
}

// ExpandHome turns a leading "~" (alone, or followed by a separator) into the home folder.
func ExpandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") && !(runtime.GOOS == "windows" && strings.HasPrefix(path, `~\`)) {
		return path
	}
	home, err := UserHome()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

// CheckRecoveryPath checks where the recovery file may go and returns its absolute path. Refused:
// an existing file or link at that path, a folder that does not exist or cannot be listed, a path
// in the data folder or the accounts folder (compared by identity: links, junctions, bind mounts,
// a case-insensitive file system), a folder (or a folder above it) holding key files.
func (w *Wallet) CheckRecoveryPath(path string) (string, error) {
	return CheckRecoveryPath(path, w.DataDir, w.Store.Dir)
}

// CheckRecoveryPath is Wallet.CheckRecoveryPath with the folders given.
func CheckRecoveryPath(path, dataDir, accountsDir string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("no file name")
	}
	abs, err := filepath.Abs(ExpandHome(path))
	if err != nil {
		return "", err
	}
	if st, err := os.Lstat(abs); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%s is a symbolic link: refused", abs)
		}
		return "", fmt.Errorf("%s already exists: refused, the wallet never overwrites a file", abs)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	dir := filepath.Dir(abs)
	realDir := dir
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		realDir = r
	}
	// the data and accounts folders: by name (they may not exist yet), then by identity
	type kept struct {
		path string
		st   os.FileInfo
	}
	var keep []kept
	for _, k := range []string{dataDir, accountsDir} {
		if k == "" {
			continue
		}
		kAbs, err := filepath.Abs(k)
		if err != nil {
			continue
		}
		realK := kAbs
		if r, err := filepath.EvalSymlinks(kAbs); err == nil {
			realK = r
		}
		if within(dir, kAbs) || within(realDir, realK) || within(realDir, kAbs) {
			return "", dataFolderErr(abs, kAbs)
		}
		if st, err := os.Stat(kAbs); err == nil {
			keep = append(keep, kept{kAbs, st})
		}
	}
	if st, err := os.Stat(realDir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("%s is not an existing folder", dir)
	}
	for _, start := range []string{realDir, dir} {
		for d := start; ; d = filepath.Dir(d) {
			if st, err := os.Stat(d); err == nil {
				for _, k := range keep {
					if os.SameFile(st, k.st) {
						return "", dataFolderErr(abs, k.path)
					}
				}
			}
			if filepath.Dir(d) == d {
				break
			}
		}
	}
	for d := realDir; ; d = filepath.Dir(d) {
		name, err := keyFileIn(d)
		if err != nil && d == realDir {
			return "", fmt.Errorf("%s cannot be listed (%v): refused, the wallet must see that no key files are there", d, err)
		}
		if name != "" {
			return "", fmt.Errorf("%s holds key files (%s): refused, the words never go next to keys", d, name)
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	return abs, nil
}

func dataFolderErr(abs, folder string) error {
	return fmt.Errorf("%s is in the wallet's data folder %s, next to the keys: refused, choose a folder outside it", abs, folder)
}

// within tells whether path is dir or inside it (case-insensitive on Windows, as filepath.Rel).
func within(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// maxKeyScan bounds how many files of a folder are read to look for key files (names are always
// checked); maxKeyFile is the largest file read.
const (
	maxKeyScan = 4096
	maxKeyFile = 16 << 10
)

// keyFileIn returns the name of a key file in dir, or "": a node's NodePrivate*/NodePublic* file,
// a wallet key file (whatever its name: an encrypted export too), the official wallets' keys JSON,
// a base58 secret key (export --format base58); a link to any of them counts. The error is that
// of listing the folder.
func keyFileIn(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	read := 0
	for _, e := range entries {
		name := e.Name()
		lower := strings.ToLower(name)
		if !e.IsDir() && (strings.HasPrefix(lower, "nodeprivate") || strings.HasPrefix(lower, "nodepublic")) {
			return name, nil
		}
		if read >= maxKeyScan {
			continue
		}
		st, err := os.Stat(filepath.Join(dir, name)) // follows a link
		if err != nil || !st.Mode().IsRegular() || st.Size() == 0 || st.Size() > maxKeyFile {
			continue
		}
		read++
		if isKeyFile(filepath.Join(dir, name)) {
			return name, nil
		}
	}
	return "", nil
}

// isKeyFile tells whether a small file holds a key: a wallet key file, the official keys JSON, or
// a base58 secret key of 64 bytes.
func isKeyFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxKeyFile+1))
	if err != nil || len(data) > maxKeyFile {
		return false
	}
	defer keys.Zero(data)
	if trimmed := strings.TrimSpace(string(data)); len(trimmed) >= 80 && len(trimmed) <= 100 && !strings.ContainsAny(trimmed, " \t\r\n") {
		if b, err := base58.Decode(trimmed); err == nil && len(b) == 64 {
			keys.Zero(b)
			return true
		}
	}
	var k struct {
		Ciphertext string `json:"ciphertext"`
		KDF        struct {
			Name string `json:"name"`
		} `json:"kdf"`
		Key struct {
			Private string `json:"private"`
		} `json:"key"`
	}
	if json.Unmarshal(data, &k) != nil {
		return false
	}
	return (k.Ciphertext != "" && k.KDF.Name != "") || k.Key.Private != ""
}

// RecoveryText is the content of the recovery file. The caller zeroes it once written.
func RecoveryText(account, address string, when time.Time, words []string) []byte {
	b := make([]byte, 0, 1024)
	b = append(b, "CREDITS wallet: recovery phrase\n"...)
	b = append(b, "WARNING: anyone with these words controls the funds of this account.\n\n"...)
	b = append(b, "account: "...)
	b = append(b, account...)
	b = append(b, "\naddress: "...)
	b = append(b, address...)
	b = append(b, "\ndate:    "...)
	b = append(b, when.UTC().Format("2006-01-02 15:04 UTC")...)
	b = append(b, "\n\n"...)
	for i, w := range words {
		b = fmt.Appendf(b, "%2d. ", i+1)
		b = append(b, w...)
		b = append(b, '\n')
	}
	return b
}

// WriteSecretFile creates a new file readable by its owner only (0600) holding data. It never
// overwrites and never writes through a link, and the file appears whole or not at all:
//   - on Linux, an unnamed file in the folder (O_TMPFILE), written and synced, then given its name
//     (linkat): nothing is left behind if the wallet is killed or the power fails;
//   - elsewhere (or where O_TMPFILE is not supported) PATH.partial, created exclusively, kept
//     open, written and synced, then hard-linked to path; a crash may leave PATH.partial, which
//     holds the secret: the docs say to delete it; the next write refuses while it is there;
//   - without hard links, path itself with O_CREATE|O_EXCL.
//
// After the name is given, the file at path must be the one written (os.SameFile with the open
// descriptor): if another user swapped it in a shared folder, path is removed and the write fails.
// Windows ignores the mode: the file takes its folder's ACL (the user profile is private).
func WriteSecretFile(path string, data []byte) error {
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symbolic link: refused", path)
		}
		return existsErr(path)
	}
	if done, err := unnamedWrite(path, data); done {
		if err == nil {
			syncDir(filepath.Dir(path))
		}
		return err
	}
	partial := path + ".partial"
	tmp, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s exists, left by an interrupted write: it may hold a secret; check it, delete it, then try again", partial)
	}
	if err != nil {
		return err
	}
	defer os.Remove(partial) // after Close (deferred later, run first): Windows does not remove an open file
	defer tmp.Close()
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := os.Link(partial, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return existsErr(path)
		}
		return writeExclusive(path, data)
	}
	if err := sameFile(tmp, path); err != nil {
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

// unnamedWrite is writeUnnamed (replaced in tests, to exercise the named fallback on Linux).
var unnamedWrite = writeUnnamed

func existsErr(path string) error {
	return fmt.Errorf("%s already exists: refused, the wallet never overwrites a file", path)
}

// sameFile checks that path is the file open as f; if not, path is removed.
func sameFile(f *os.File, path string) error {
	mine, err1 := f.Stat()
	there, err2 := os.Lstat(path)
	if err1 == nil && err2 == nil && os.SameFile(mine, there) {
		return nil
	}
	if err2 == nil {
		os.Remove(path)
	}
	return fmt.Errorf("%s was replaced while it was written (another user in this folder?): removed, nothing written", path)
}

// writeExclusive is the fallback without hard links: O_CREATE|O_EXCL (which also refuses a link).
func writeExclusive(path string, data []byte) error {
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return existsErr(path)
		}
		return err
	}
	fail := func(err error) error {
		out.Close()
		os.Remove(path)
		return err
	}
	if _, err := out.Write(data); err != nil {
		return fail(err)
	}
	if err := out.Sync(); err != nil {
		return fail(err)
	}
	if err := sameFile(out, path); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}

// RecoveryProfileWarning is, on Windows, a warning for a file outside the user's profile (whose
// folder's ACL may let others read it); "" otherwise.
func RecoveryProfileWarning(path string) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	profile := os.Getenv("USERPROFILE")
	if profile == "" || within(path, profile) {
		return ""
	}
	return fmt.Sprintf("%s is outside your user profile (%s): make sure only you can read it (Properties > Security)", path, profile)
}
