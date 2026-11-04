// Package config reads the wallet's configuration file (TOML) and finds its data folder.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/CreditsCommunity/wallet/internal/echo"
)

// Network is what the wallet believes the node's network is.
type Network string

const (
	Mainnet Network = "mainnet"
	Testnet Network = "testnet"
	Private Network = "private"
)

// IsMainnet: anything not declared as a test or private network is treated as mainnet.
func (n Network) IsMainnet() bool { return n != Testnet && n != Private }

// Label is the network as shown on screen.
func (n Network) Label() string { return strings.ToUpper(string(n)) }

// DefaultAddress is the node API of a wallet next to its node.
const DefaultAddress = "127.0.0.1:9090"

// Profile is a node the wallet can talk to.
type Profile struct {
	Name    string  `toml:"-"`
	Address string  `toml:"address"`
	Network Network `toml:"network"`
	// HistoryNode is a remote full node (HOST:PORT) the history is read from, read-only: it gets
	// only public addresses and is never used to send. Empty: the history comes from Address.
	HistoryNode string `toml:"history_node"`
	// Diag is the node's diagnostic API (HOST:PORT), asked for the network identity of a community
	// node; "off" not to ask. Empty: the address's host with port 9088 (the release packages'
	// [api] diag_port).
	Diag string `toml:"diag"`
	// NetworkUUID pins the network: the uuid the node must report (decimal), checked before
	// sending when the node reports one. Optional.
	NetworkUUID string `toml:"network_uuid"`
	// RequireNetworkUUID refuses to send when the node does not report its network uuid (an
	// official node, an older community release, the diag port closed): the network must be
	// confirmed by the node, not only declared.
	RequireNetworkUUID bool `toml:"require_network_uuid"`
	// NodeKey makes the wallet the node's wallet: the node's own key file (NodePrivate.txt, a
	// path relative to the configuration file's folder), used in place as the account "node".
	// Unset: $CREDITS_WALLET_NODE_KEY if set; "off": never. Never guessed from the current folder.
	NodeKey string `toml:"node_key"`
}

// NodeKeyOff is the value of node_key that turns the node's key account off.
const NodeKeyOff = "off"

// DiagOff is the value of diag that turns the network-identity call off.
const DiagOff = "off"

// DefaultDiagPort is the [api] diag_port of the node release packages.
const DefaultDiagPort = "9088"

// DiagAddress is where the network identity is asked: the profile's diag, or the node address's
// host with the default diag port; "" when off.
func (p Profile) DiagAddress() string {
	switch p.Diag {
	case DiagOff:
		return ""
	case "":
		host, _, err := net.SplitHostPort(p.Address)
		if err != nil {
			return ""
		}
		return net.JoinHostPort(host, DefaultDiagPort)
	}
	return p.Diag
}

// PinnedUUID is the network uuid the profile pins (0: none).
func (p Profile) PinnedUUID() uint64 {
	n, _ := strconv.ParseUint(p.NetworkUUID, 10, 64)
	return n
}

// Config is the configuration file.
type Config struct {
	// Node is the profile used when --profile is not given.
	Node  string              `toml:"node"`
	Nodes map[string]*Profile `toml:"nodes"`
	// HistoryPage is the number of transactions per history page.
	HistoryPage int `toml:"history_page"`

	Path string `toml:"-"` // where it was read from ("" when the defaults are used)
}

// Example is written by "credits-wallet config init" and shown in the README.
const Example = `# credits-wallet configuration
# the node profile used by default (another one: --profile NAME)
node = "local"
# transactions per history page
history_page = 20

# a node profile: the API address (HOST:PORT) and its network.
# network = "mainnet" (the default: full confirmation before sending), "testnet" or "private"
[nodes.local]
address = "127.0.0.1:9090"
network = "mainnet"

# history from a remote full node (read-only, never used to send), for a pruned local node:
# history_node = "203.0.113.7:9090"
# the node's diagnostic API, asked for its network identity (a community node from release
# community.17 tells its network uuid; the wallet then refuses to send when it contradicts
# "network"); default: this address's host, port 9088; "off" not to ask
# diag = "127.0.0.1:9088"
# the wallet of the node in this folder: its key file, used in place as the account "node" (in
# clear on the server, as the node needs it; a path relative to this file's folder); "off" not
# to use it; never guessed from the current folder (--node-key FILE on the line also works)
# node_key = "/home/me/csnode/NodePrivate.txt"

# [nodes.lab]
# address = "10.0.0.5:9090"
# network = "testnet"
# network_uuid = "1234567890123456789"   # optional: the uuid this network must have (the node
#                                        # must then report it, or nothing is sent)
# require_network_uuid = true            # optional: send only when the node confirms its network
`

// Default is the configuration without a file: the local node, mainnet.
func Default() *Config {
	return &Config{Node: "local", Nodes: map[string]*Profile{"local": {Name: "local", Address: DefaultAddress, Network: Mainnet}}, HistoryPage: 20}
}

// DataDir is the wallet's folder: $CREDITS_WALLET_HOME, or ~/.credits-wallet (Linux, macOS; the
// home as UserHome finds it), %APPDATA%\credits-wallet (Windows).
func DataDir() (string, error) {
	if d := os.Getenv("CREDITS_WALLET_HOME"); d != "" {
		return d, nil
	}
	if runtime.GOOS == "windows" {
		if a := os.Getenv("APPDATA"); a != "" {
			return filepath.Join(a, "credits-wallet"), nil
		}
		return "", errors.New("APPDATA is not set")
	}
	home, err := UserHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".credits-wallet"), nil
}

// AccountsDir is where the key files are.
func AccountsDir(dataDir string) string { return filepath.Join(dataDir, "accounts") }

// DefaultPath is the configuration file in the data folder.
func DefaultPath(dataDir string) string { return filepath.Join(dataDir, "config.toml") }

// Load reads a configuration file. A missing file at the default path gives the defaults; a
// missing file named explicitly is an error.
func Load(path string, explicit bool) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && !explicit {
		return Default(), nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(string(data), path)
}

// Parse reads the TOML text of a configuration.
func Parse(text, path string) (*Config, error) {
	c := &Config{}
	md, err := toml.Decode(text, c)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("%s: unknown settings: %s", path, strings.Join(keys, ", "))
	}
	c.Path = path
	if c.HistoryPage <= 0 {
		c.HistoryPage = 20
	}
	if len(c.Nodes) == 0 {
		c.Nodes = Default().Nodes
	}
	for name, p := range c.Nodes {
		p.Name = name
		if p.Address == "" {
			p.Address = DefaultAddress
		}
		if _, _, err := net.SplitHostPort(p.Address); err != nil {
			return nil, fmt.Errorf("%s: nodes.%s.address %q: expected HOST:PORT", path, name, p.Address)
		}
		if p.HistoryNode != "" {
			if _, _, err := net.SplitHostPort(p.HistoryNode); err != nil {
				return nil, fmt.Errorf("%s: nodes.%s.history_node %q: expected HOST:PORT", path, name, p.HistoryNode)
			}
		}
		if p.Diag != "" && p.Diag != DiagOff {
			if _, _, err := net.SplitHostPort(p.Diag); err != nil {
				return nil, fmt.Errorf("%s: nodes.%s.diag %q: expected HOST:PORT or \"off\"", path, name, p.Diag)
			}
		}
		if p.NetworkUUID != "" {
			if n, err := strconv.ParseUint(p.NetworkUUID, 10, 64); err != nil || n == 0 {
				return nil, fmt.Errorf("%s: nodes.%s.network_uuid %q: expected the uuid as a decimal number", path, name, p.NetworkUUID)
			}
		}
		if strings.ContainsRune(p.NodeKey, 0) {
			return nil, fmt.Errorf("%s: nodes.%s.node_key: not a file name", path, name)
		}
		switch p.Network {
		case "":
			p.Network = Mainnet
		case Mainnet, Testnet, Private:
		default:
			return nil, fmt.Errorf("%s: nodes.%s.network %q: expected mainnet, testnet or private", path, name, p.Network)
		}
	}
	if c.Node == "" {
		if len(c.Nodes) == 1 {
			for name := range c.Nodes {
				c.Node = name
			}
		} else if _, ok := c.Nodes["local"]; ok {
			c.Node = "local"
		}
	}
	if _, ok := c.Nodes[c.Node]; !ok {
		return nil, fmt.Errorf("%s: node = %q, but there is no [nodes.%s]", path, c.Node, c.Node)
	}
	return c, nil
}

// ProfileNames lists the profiles, sorted.
func (c *Config) ProfileNames() []string {
	names := make([]string, 0, len(c.Nodes))
	for n := range c.Nodes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ResolveHistory sets the profile's history node from --history-node: HOST:PORT, or "local" for
// the profile's own node. Empty keeps the configuration's.
func ResolveHistory(p Profile, override string) (Profile, error) {
	switch override {
	case "":
		return p, nil
	case "local":
		p.HistoryNode = ""
		return p, nil
	}
	if _, _, err := net.SplitHostPort(override); err != nil {
		return p, fmt.Errorf("--history-node %s: expected HOST:PORT or local", echo.Quote(override))
	}
	p.HistoryNode = override
	return p, nil
}

// Resolve picks the node to talk to: the profile named (or the default one), its address
// replaced by addressOverride (--node) when given. An address that no profile declares is
// treated as mainnet, whatever the chosen profile says: declaring a test network is the only way
// to lighten the confirmation, and it must be about that node.
func (c *Config) Resolve(profile, addressOverride string) (Profile, error) {
	name := profile
	if name == "" {
		name = c.Node
	}
	p, ok := c.Nodes[name]
	if !ok {
		return Profile{}, fmt.Errorf("no node profile %s (known: %s)", echo.Quote(name), strings.Join(c.ProfileNames(), ", "))
	}
	out := *p
	if addressOverride == "" || addressOverride == p.Address {
		return out, nil
	}
	if _, _, err := net.SplitHostPort(addressOverride); err != nil {
		return Profile{}, fmt.Errorf("--node %s: expected HOST:PORT", echo.Quote(addressOverride))
	}
	out.Address = addressOverride
	out.Network = Mainnet
	out.Name = ""
	out.HistoryNode = ""
	out.Diag = ""
	out.NetworkUUID = ""
	out.RequireNetworkUUID = false
	for _, n := range c.ProfileNames() {
		if c.Nodes[n].Address == addressOverride {
			o := *c.Nodes[n]
			o.Name = n
			out = o
			break
		}
	}
	return out, nil
}

// PermissionWarnings checks that the data folder and its accounts folder are private. On Linux and
// macOS they must not be accessible by group or others. On Windows the wallet does not set ACLs
// itself: %APPDATA% is private to the user by default, so it warns when CREDITS_WALLET_HOME puts
// the data outside the user's profile, where the folder's ACL may let others read it.
func PermissionWarnings(dataDir string) []string {
	var warnings []string
	if runtime.GOOS == "windows" {
		profile := os.Getenv("USERPROFILE")
		if os.Getenv("CREDITS_WALLET_HOME") != "" && profile != "" && !isWithin(dataDir, profile) {
			warnings = append(warnings, fmt.Sprintf("%s is outside your user profile (%s): make sure only you can read it (folder Properties > Security)", dataDir, profile))
		}
		return warnings
	}
	for _, d := range []string{dataDir, AccountsDir(dataDir)} {
		st, err := os.Stat(d)
		if err != nil {
			continue
		}
		if st.Mode().Perm()&0o077 != 0 {
			warnings = append(warnings, fmt.Sprintf("%s is accessible by other users (mode %v): chmod 700 it", d, st.Mode().Perm()))
		}
	}
	return warnings
}

func isWithin(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
