package wallet

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/keys"
)

// A node's key in the current folder (spec 4.4, "Found in the current folder"): the wallet never
// uses it by itself (someone could have planted it there), but the terminal interface says so and
// offers it, and the commands print a one-line hint.

// UnverifiedAddressNote tags the address of a key the node encrypted until it is opened: it comes
// from NodePublic.txt.
const UnverifiedAddressNote = "(from NodePublic.txt, unverified until unlocked)"

// FoundNodeKey is a NodePrivate.txt in the folder the wallet was started in.
type FoundNodeKey struct {
	Path      string // absolute
	Address   string // "" when refused
	Encrypted bool   // encrypted by the node: its password opens it
	Warning   string // other users can read it
	Refused   error  // the ownership and mode checks failed: it cannot be used
}

// NodeKeyIgnoredFile lists, in the data folder, the node key files the user said not to be asked
// about again (one absolute path per line).
const NodeKeyIgnoredFile = "node-keys-ignored.txt"

// NodeKeyExplicitlyOff tells whether node_key is "off" for this run (--node-key off, the profile,
// or $CREDITS_WALLET_NODE_KEY): then nothing is offered either.
func NodeKeyExplicitlyOff(p config.Profile, flag string) bool {
	switch {
	case flag != "":
		return flag == config.NodeKeyOff
	case p.NodeKey != "":
		return p.NodeKey == config.NodeKeyOff
	}
	return os.Getenv(NodeKeyEnv) == config.NodeKeyOff
}

// FindLocalNodeKey looks for NodePrivate.txt in cwd. nil when there is none, when the wallet
// already has a node's key (configured) or node_key is off, or when the user said not to ask again
// for that file. A file that fails the checks of ReadNodeKeySafe is returned with Refused set.
func FindLocalNodeKey(cwd, dataDir string, configured *NodeKey, off bool) *FoundNodeKey {
	if configured != nil || off || cwd == "" {
		return nil
	}
	path, err := filepath.Abs(filepath.Join(cwd, keys.NodePrivateFile))
	if err != nil {
		return nil
	}
	if _, err := os.Lstat(path); err != nil {
		return nil
	}
	if NodeKeyIgnored(dataDir, path) {
		return nil
	}
	found := &FoundNodeKey{Path: path}
	found.Address, found.Encrypted, found.Warning, found.Refused = NodeKeyFileInfo(path)
	if found.Refused != nil {
		found.Address = ""
	}
	return found
}

// LocalNodeKeyHint is the commands' one line on stderr for a node's key in the current folder.
func LocalNodeKeyHint(f *FoundNodeKey) string {
	if f == nil {
		return ""
	}
	return "This folder holds a node key: to use it, --node-key " + keys.NodePrivateFile + " (not used without it)"
}

// NodeKeyIgnored tells whether path is on the data folder's list of keys not to offer again.
func NodeKeyIgnored(dataDir, path string) bool {
	data, err := readListFile(filepath.Join(dataDir, NodeKeyIgnoredFile))
	if err != nil {
		return false
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == path {
			return true
		}
	}
	return false
}

// IgnoreNodeKey adds path to the list of node key files not to offer again.
func IgnoreNodeKey(dataDir, path string) error {
	if strings.ContainsAny(path, "\n\r") || !filepath.IsAbs(path) {
		return errors.New("not an absolute file name")
	}
	if NodeKeyIgnored(dataDir, path) {
		return nil
	}
	list := filepath.Join(dataDir, NodeKeyIgnoredFile)
	data, err := readListFile(list)
	if err != nil {
		return err
	}
	text := strings.TrimRight(string(data), "\n")
	if text != "" {
		text += "\n"
	}
	return writeListFile(dataDir, list, []byte(text+path))
}

// UseFoundNodeKey makes the found key the wallet's node's key for this run.
func (w *Wallet) UseFoundNodeKey(f *FoundNodeKey, source string) {
	nk := NodeKeyIn(filepath.Dir(f.Path), source)
	nk.Private = f.Path
	w.NodeKey = nk
	w.noticed = false
}
