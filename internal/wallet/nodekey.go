package wallet

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/keys"
)

// The wallet of a node: next to its node (started with the package's wallet.toml, whose profile
// sets node_key, or in the node's folder) the wallet uses the node's own key pair, in place, as
// the account "node": the node's NodePrivate.txt is read where the node reads it, never copied
// into the key store and never encrypted (the node needs the plain file). When the node has no
// key yet, the wallet makes it from a new 24-word phrase and writes it in the node's folder in the
// node's format, so that the node uses it at its first start and its identity has a paper backup.

// NodeKeyWarning is said once where the node's key becomes an account (the terminal interface,
// account list, node-key, the export to the node's format).
const NodeKeyWarning = "The node's key is kept in clear on this server, because the node needs it: CS on it are as exposed as the server. " +
	"Operators usually keep their stake there already: keep on it only what the node needs, and savings in a separate account."

// NodeIdentityNote is said before the node's key is written or replaced.
const NodeIdentityNote = "The node's identity follows its key: its address, the delegations it received, its history as a trusted node and its rewards all belong to the key. " +
	"A node started with another key is another node."

// NodeAccount is the name of the node's key account (NodeAccountName: another one if the key store
// already has an account of that name).
const NodeAccount = "node"

// NodeKey is the key of the node whose wallet this is.
type NodeKey struct {
	Dir     string // the node's folder (absolute)
	Private string // its NodePrivate.txt
	Public  string // its NodePublic.txt, next to it
	Source  string // what made the wallet the node's wallet
}

// NodeKeyIn is the node key of the node in dir.
func NodeKeyIn(dir, source string) *NodeKey {
	return &NodeKey{Dir: dir, Private: filepath.Join(dir, keys.NodePrivateFile), Public: filepath.Join(dir, keys.NodePublicFile), Source: source}
}

// NodeKeyEnv names the node's key file when neither --node-key nor the profile does (the node's
// Docker image sets it: its key is not in the node program's folder).
const NodeKeyEnv = "CREDITS_WALLET_NODE_KEY"

// ResolveNodeKey finds the node whose wallet this is. It is always an explicit choice, never
// guessed from the current folder (a folder someone else controls could hold a planted key pair):
// flag (--node-key: a file or a node's folder; relative to the current folder), else the profile's
// node_key (relative to the configuration file's folder), else $CREDITS_WALLET_NODE_KEY (an
// absolute path). nil when the wallet is not a node's wallet (none of them, or "off").
func ResolveNodeKey(p config.Profile, configPath, flag, cwd string) (*NodeKey, error) {
	value, base, source := flag, cwd, "--node-key"
	switch {
	case flag != "":
	case p.NodeKey != "":
		value, source = p.NodeKey, "node_key in "+configPath
		if configPath != "" {
			base = filepath.Dir(configPath)
		}
	case os.Getenv(NodeKeyEnv) != "":
		value, source = os.Getenv(NodeKeyEnv), "$"+NodeKeyEnv
		if !filepath.IsAbs(ExpandHome(value)) {
			return nil, fmt.Errorf("%s=%s: give an absolute path", NodeKeyEnv, value)
		}
	default:
		return nil, nil
	}
	if value == config.NodeKeyOff {
		return nil, nil
	}
	path := ExpandHome(value)
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		return NodeKeyIn(path, source), nil
	}
	nk := NodeKeyIn(filepath.Dir(path), source)
	nk.Private = path
	return nk, nil
}

// CheckNodeKeySafe checks the node's key file and its folder before the key is used: each owned by
// the user the wallet runs as (or root) and writable by nobody else (no group or world write);
// the key file a regular file, or a link whose target passes the same checks (as does the
// target's folder). On Windows the ACLs are not checked (the wallet sets none itself).
func CheckNodeKeySafe(private string) error {
	paths := []string{filepath.Dir(private)}
	st, err := os.Lstat(private)
	if err != nil {
		return err
	}
	target := private
	if st.Mode()&os.ModeSymlink != 0 {
		if target, err = filepath.EvalSymlinks(private); err != nil {
			return fmt.Errorf("%s is a link that leads nowhere: %w", private, err)
		}
		paths = append(paths, filepath.Dir(target))
	}
	tst, err := os.Stat(target)
	if err != nil {
		return err
	}
	if !tst.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file: refused", target)
	}
	if err := ownedSafe(target, tst, "the node's key file", "chmod 600 "); err != nil {
		return err
	}
	for _, d := range paths {
		dst, err := os.Stat(d)
		if err != nil {
			return err
		}
		if err := ownedSafe(d, dst, "the node's folder", "chmod go-w "); err != nil {
			return err
		}
	}
	return nil
}

// NodeAccountName is the name of the node's key account: "node", or "node-key" when the key store
// has an account named "node" (an earlier import of the node's key, say), and so on.
func (w *Wallet) NodeAccountName() string {
	for _, name := range []string{NodeAccount, "node-key", "node-key-2", "node-key-3"} {
		if !w.Store.Exists(name) {
			return name
		}
	}
	return "node-key-x"
}

// NodeAccount is the node's key account, nil when the wallet is not a node's wallet or the node
// has no key yet. Its key is read only to find its address, then wiped.
func (w *Wallet) NodeAccount() (*keys.File, error) {
	if w.NodeKey == nil {
		return nil, nil
	}
	if _, err := os.Lstat(w.NodeKey.Private); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err := CheckNodeKeySafe(w.NodeKey.Private); err != nil {
		return nil, fmt.Errorf("%w; the node's key is not used", err)
	}
	key, err := keys.ReadNodeKey(w.NodeKey.Private)
	if err != nil {
		return nil, err
	}
	defer keys.Zero(key)
	f := &keys.File{Version: keys.FileVersion, Name: w.NodeAccountName(), Public: keys.Address(key.Public().(ed25519.PublicKey)),
		NodeKey: w.NodeKey.Private}
	if w.NodeKeyNotice != nil && !w.noticed {
		w.noticed = true
		w.NodeKeyNotice(NodeKeySource(f, w.NodeKey))
	}
	return f, nil
}

// NodeKeySource says which key the node's key account is and where it comes from, shown the first
// time it is used in a session so that a substituted key is visible.
func NodeKeySource(f *keys.File, nk *NodeKey) string {
	return fmt.Sprintf("node key account %s: %s, read from %s (%s)", f.Name, f.Public, f.NodeKey, nk.Source)
}

// NodeKeyMissing tells whether the wallet is a node's wallet whose node has no key yet.
func (w *Wallet) NodeKeyMissing() bool {
	if w.NodeKey == nil {
		return false
	}
	_, err := os.Lstat(w.NodeKey.Private)
	return errors.Is(err, os.ErrNotExist)
}

// Accounts lists the accounts: the node's key first (the default account of a node's wallet), then
// the key store's, sorted by name; and the key files it could not read.
func (w *Wallet) Accounts() (files []*keys.File, problems []error, err error) {
	files, problems, err = w.Store.List()
	if err != nil {
		return nil, nil, err
	}
	nf, nerr := w.NodeAccount()
	if nerr != nil {
		problems = append([]error{fmt.Errorf("the node's key: %w", nerr)}, problems...)
	}
	if nf != nil {
		files = append([]*keys.File{nf}, files...)
	}
	return files, problems, nil
}

// LoadAccount loads an account by name: the node's key, or a key file of the store.
func (w *Wallet) LoadAccount(name string) (*keys.File, error) {
	if w.NodeKey != nil && name == w.NodeAccountName() {
		f, err := w.NodeAccount()
		if err != nil {
			return nil, err
		}
		if f == nil {
			return nil, fmt.Errorf("%w: %s (the node in %s has no key yet: 'account node-key new' makes it)", keys.ErrNotFound, name, w.NodeKey.Dir)
		}
		return f, nil
	}
	return w.Store.Load(name)
}

// HasAccount tells whether an account of that name exists (the node's key included).
func (w *Wallet) HasAccount(name string) bool {
	if w.NodeKey != nil && name == w.NodeAccountName() && !w.NodeKeyMissing() {
		return true
	}
	return w.Store.Exists(name)
}

// ---- is the node running? ----------------------------------------------------------------------

// NodeRunning tells whether a node runs in dir, and how the wallet saw it ("" when it saw none):
//   - the community node holds an exclusive lock on db/.lock for its whole run (taken before it
//     reads its key);
//   - a node API answers on 127.0.0.1 at the port of the folder's config.ini ([api] port);
//   - on Linux, a process named node runs in that folder (the official node takes no lock).
//
// When dir has no config.ini but the folder above has one (the Docker image keeps the keys in
// /data/keys, the node runs in /data), that folder is looked at too. The node writes no PID file.
// A node of this folder started with another --db-path and another API port, in another user's
// processes, is not seen: stop the node first.
func NodeRunning(dir string) string {
	folders := []string{dir}
	if !isFile(filepath.Join(dir, "config.ini")) && isFile(filepath.Join(filepath.Dir(dir), "config.ini")) {
		folders = append(folders, filepath.Dir(dir))
	}
	for _, d := range folders {
		if lockBusy(filepath.Join(d, "db", ".lock")) {
			return "the node holds " + filepath.Join(d, "db", ".lock")
		}
		if port := apiPort(filepath.Join(d, "config.ini")); port != "" {
			if apiAnswers(net.JoinHostPort("127.0.0.1", port)) {
				return "a node API answers on 127.0.0.1:" + port + " (the [api] port of " + filepath.Join(d, "config.ini") + ")"
			}
		}
		if pid := nodeProcessIn(d); pid != "" {
			return "the process " + pid + " (node) runs in " + d
		}
	}
	return ""
}

func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// apiAnswers connects to a node API (replaced in tests).
var apiAnswers = func(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// apiPort is the [api] port of a node's config.ini, "" when none (or 0, the API off).
func apiPort(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	section := ""
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.Trim(line, "[] \t"))
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || section != "api" || strings.TrimSpace(k) != "port" {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n <= 0 || n > 65535 {
			return ""
		}
		return strconv.Itoa(n)
	}
	return ""
}

// ---- writing the node's key --------------------------------------------------------------------

// nodeFolder checks the folder the node's key goes to and returns its real path: an existing
// folder (a link to one is followed), and on Linux and macOS owned by the user the wallet runs as
// and not writable by others (a folder another user can write to could swap the files).
func nodeFolder(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("no node folder")
	}
	abs, err := filepath.Abs(ExpandHome(dir))
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("%s: %w", abs, err)
	}
	st, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("%s is not a folder: give the node's folder (csnode)", abs)
	}
	if err := folderSafe(real, st); err != nil {
		return "", err
	}
	return real, nil
}

// CheckNodeKeyTarget checks that a new key can be written in the node folder dir: the folder (see
// nodeFolder), the node stopped, and (unless replace) no key file there, nor a link of that name.
// It returns the folder's real path.
func CheckNodeKeyTarget(dir string, replace bool) (string, error) {
	real, err := nodeFolder(dir)
	if err != nil {
		return "", err
	}
	if why := NodeRunning(real); why != "" {
		return "", fmt.Errorf("the node is running (%s): stop it first; nothing was written", why)
	}
	for _, name := range []string{keys.NodePrivateFile, keys.NodePublicFile} {
		path := filepath.Join(real, name)
		st, err := os.Lstat(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			continue
		case err != nil:
			return "", err
		case st.Mode()&os.ModeSymlink != 0:
			return "", fmt.Errorf("%s is a symbolic link: refused, nothing was written", path)
		case !replace:
			return "", fmt.Errorf("%s already exists: the node has a key; the wallet never overwrites it (to replace the node's identity: account export NAME --format node --out %s --replace)", path, real)
		case !st.Mode().IsRegular():
			return "", fmt.Errorf("%s is not a regular file: refused", path)
		}
	}
	for _, name := range []string{keys.NodePrivateFile, keys.NodePublicFile} {
		if _, err := os.Lstat(filepath.Join(real, name) + ".partial"); err == nil {
			return "", fmt.Errorf("%s.partial exists, left by an interrupted write: it may hold a secret; check it, delete it, then try again", filepath.Join(real, name))
		}
	}
	return real, nil
}

// WriteNodeKey writes the node's key files in the node folder dir, as the node writes them (see
// keys.NodeKeyFiles): mode 0600, created new (never overwriting, never through a link), whole or
// not at all (WriteSecretFile), NodePrivate.txt first. Refused while the node runs or when a key
// file exists (CheckNodeKeyTarget). Both files are read back as the node reads them.
func WriteNodeKey(dir string, key ed25519.PrivateKey) error {
	real, err := CheckNodeKeyTarget(dir, false)
	if err != nil {
		return err
	}
	return writeNodeFiles(real, key)
}

func writeNodeFiles(real string, key ed25519.PrivateKey) error {
	priv, pub := keys.NodeKeyFiles(key)
	defer keys.Zero(priv)
	privPath, pubPath := filepath.Join(real, keys.NodePrivateFile), filepath.Join(real, keys.NodePublicFile)
	if err := WriteSecretFile(privPath, priv); err != nil {
		return err
	}
	if err := WriteSecretFile(pubPath, pub); err != nil {
		os.Remove(privPath)
		return err
	}
	if err := checkNodeFiles(real, key); err != nil {
		os.Remove(privPath)
		os.Remove(pubPath)
		return err
	}
	return nil
}

// checkNodeFiles reads the node's key files back as the node does.
func checkNodeFiles(real string, key ed25519.PrivateKey) error {
	got, err := keys.ReadNodeKey(filepath.Join(real, keys.NodePrivateFile))
	if err != nil {
		return fmt.Errorf("reading the node's key back: %w", err)
	}
	same := bytes.Equal(got, key)
	keys.Zero(got)
	if !same {
		return errors.New("the node's key read back differs from the key written")
	}
	return keys.CheckNodePublic(filepath.Join(real, keys.NodePublicFile), key.Public().(ed25519.PublicKey))
}

// ReplaceNodeKey replaces the node's key in the node folder dir with key: the node stopped, the old
// files kept first as NAME.bak-YYYYMMDD-HHMMSS (mode 0600, new files), then removed, then the new
// ones written as WriteNodeKey does. If the new files cannot be written, the old ones are put back.
// It returns the backups made (none when the folder had no key).
func ReplaceNodeKey(dir string, key ed25519.PrivateKey, now time.Time) (backups []string, err error) {
	real, err := CheckNodeKeyTarget(dir, true)
	if err != nil {
		return nil, err
	}
	stamp := now.Format("20060102-150405")
	type old struct {
		path, backup string
		data         []byte
	}
	var olds []old
	defer func() {
		for _, o := range olds {
			keys.Zero(o.data)
		}
	}()
	for _, name := range []string{keys.NodePrivateFile, keys.NodePublicFile} {
		path := filepath.Join(real, name)
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		olds = append(olds, old{path, path + ".bak-" + stamp, data})
	}
	if len(olds) > 0 && olds[0].path == filepath.Join(real, keys.NodePrivateFile) {
		if cur, err := keys.ParseNodePrivate(olds[0].data); err == nil {
			same := bytes.Equal(cur, key)
			keys.Zero(cur)
			if same {
				return nil, errors.New("the node already has this key: nothing to replace")
			}
		}
	}
	for _, o := range olds {
		if err := WriteSecretFile(o.backup, o.data); err != nil {
			for _, b := range backups {
				os.Remove(b)
			}
			return nil, fmt.Errorf("the backup of %s: %w; nothing was replaced", o.path, err)
		}
		backups = append(backups, o.backup)
	}
	for _, o := range olds {
		if err := os.Remove(o.path); err != nil {
			restore := func() {
				for _, o := range olds {
					if _, err := os.Lstat(o.path); errors.Is(err, os.ErrNotExist) {
						_ = WriteSecretFile(o.path, o.data)
					}
				}
			}
			restore()
			return backups, fmt.Errorf("removing %s: %w; the old key is in place", o.path, err)
		}
	}
	if err := writeNodeFiles(real, key); err != nil {
		for _, o := range olds {
			if _, statErr := os.Lstat(o.path); errors.Is(statErr, os.ErrNotExist) {
				_ = WriteSecretFile(o.path, o.data)
			}
		}
		return backups, fmt.Errorf("%w; the old key was put back", err)
	}
	return backups, nil
}

// folderOwnerNote is the advice for a folder refused by folderSafe.
func folderOwnerNote(real string) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	return fmt.Sprintf(" (run the wallet as the user who owns %s and runs the node)", real)
}
