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
)

// The recovery file: on request, after the phrase of a new account was shown and checked, its 24
// words also go to a file. The file is never next to the keys: not in the data folder (where the
// encrypted key files are), not in a folder that holds key files of any kind. A copy of the words
// beside the key files would let whoever takes that folder skip the password altogether. It is
// created new (never overwriting, never through a symbolic link), owner-only (0600), written
// whole: a temporary file in the same folder, linked to its name only when complete.

// RecoveryAdvice is what to do with the file once written (terminal and TUI).
const RecoveryAdvice = "This file holds your account in clear: copy it to paper or an offline USB key, then delete it from this computer."

// RecoveryQuestion is the question asked after the check of the phrase; the default is no.
const RecoveryQuestion = "Save the 24 words to a file as well? [y/N] "

// UserHome is the home folder of the user the wallet runs as: %USERPROFILE% on Windows; on Linux
// and macOS $HOME, unless it is not a folder of the effective user (sudo without -H keeps the
// invoking user's $HOME), in which case the home of the effective user's account.
func UserHome() (string, error) {
	if runtime.GOOS == "windows" {
		if p := os.Getenv("USERPROFILE"); p != "" {
			return p, nil
		}
		return os.UserHomeDir()
	}
	home := os.Getenv("HOME")
	if home != "" && ownedByMe(home) {
		return home, nil
	}
	if h, err := accountHome(); err == nil && h != "" {
		return h, nil
	}
	if home != "" {
		return home, nil
	}
	return "", errors.New("no home folder: HOME is not set")
}

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
// an existing file or link at that path, a folder that does not exist, a path in the data folder
// or the accounts folder (also through links), a folder (or a folder above it) holding key files.
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
	for _, keep := range []string{dataDir, accountsDir} {
		if keep == "" {
			continue
		}
		keepAbs, err := filepath.Abs(keep)
		if err != nil {
			continue
		}
		realKeep := keepAbs
		if r, err := filepath.EvalSymlinks(keepAbs); err == nil {
			realKeep = r
		}
		if within(dir, keepAbs) || within(realDir, realKeep) || within(realDir, keepAbs) {
			return "", fmt.Errorf("%s is in the wallet's data folder %s, next to the keys: refused, choose a folder outside it", abs, keepAbs)
		}
	}
	if st, err := os.Stat(realDir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("%s is not an existing folder", dir)
	}
	for d := realDir; ; {
		if name := keyFileIn(d); name != "" {
			return "", fmt.Errorf("%s holds key files (%s): refused, the words never go next to keys", d, name)
		}
		up := filepath.Dir(d)
		if up == d {
			break
		}
		d = up
	}
	return abs, nil
}

// within tells whether path is dir or inside it (case-insensitive on Windows, as filepath.Rel).
func within(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// keyFileIn returns the name of a key file in dir, or "": a node's NodePrivate.txt or
// NodePublic.txt, a wallet key file, or the official wallets' keys JSON.
func keyFileIn(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		name := e.Name()
		if strings.EqualFold(name, "NodePrivate.txt") || strings.EqualFold(name, "NodePublic.txt") {
			return name
		}
		if !e.Type().IsRegular() || !strings.EqualFold(filepath.Ext(name), ".json") {
			continue
		}
		if isKeyJSON(filepath.Join(dir, name)) {
			return name
		}
	}
	return ""
}

func isKeyJSON(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 64<<10+1))
	if err != nil || len(data) > 64<<10 {
		return false
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
// overwrites and never writes through a link: data goes to a temporary file in the same folder
// (created exclusively), synced, then hard-linked to path, which fails if anything (a file, a
// link) is there; on a file system without hard links the file is created with O_EXCL directly.
// Windows ignores the mode: the file takes its folder's ACL (the user profile is private).
func WriteSecretFile(path string, data []byte) error {
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symbolic link: refused", path)
		}
		return fmt.Errorf("%s already exists: refused, the wallet never overwrites a file", path)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".credits-secret-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
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
	if err := os.Link(tmpName, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists: refused, the wallet never overwrites a file", path)
		}
		return writeExclusive(path, data)
	}
	syncDir(dir)
	return nil
}

// writeExclusive is the fallback without hard links: O_CREATE|O_EXCL (which also refuses a link).
func writeExclusive(path string, data []byte) error {
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := out.Write(data); err != nil {
		out.Close()
		os.Remove(path)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(path)
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
