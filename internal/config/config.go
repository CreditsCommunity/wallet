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
	"strings"

	"github.com/BurntSushi/toml"
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

# [nodes.lab]
# address = "10.0.0.5:9090"
# network = "testnet"
`

// Default is the configuration without a file: the local node, mainnet.
func Default() *Config {
	return &Config{Node: "local", Nodes: map[string]*Profile{"local": {Name: "local", Address: DefaultAddress, Network: Mainnet}}, HistoryPage: 20}
}

// DataDir is the wallet's folder: $CREDITS_WALLET_HOME, or ~/.credits-wallet (Linux, macOS),
// %APPDATA%\credits-wallet (Windows).
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
	home, err := os.UserHomeDir()
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
		return Profile{}, fmt.Errorf("no node profile %q (known: %s)", name, strings.Join(c.ProfileNames(), ", "))
	}
	out := *p
	if addressOverride == "" || addressOverride == p.Address {
		return out, nil
	}
	if _, _, err := net.SplitHostPort(addressOverride); err != nil {
		return Profile{}, fmt.Errorf("--node %q: expected HOST:PORT", addressOverride)
	}
	out.Address = addressOverride
	out.Network = Mainnet
	out.Name = ""
	for _, n := range c.ProfileNames() {
		if c.Nodes[n].Address == addressOverride {
			out.Network = c.Nodes[n].Network
			out.Name = n
			break
		}
	}
	return out, nil
}
