// Package wallet is what the commands and the terminal interface share: the accounts, the node
// and the steps of a transfer (checks, signing, sending, following).
package wallet

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"strings"

	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/echo"
	"github.com/CreditsCommunity/wallet/internal/history"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
)

// Wallet ties the configuration, the key store and the node together.
type Wallet struct {
	Config  *config.Config
	Profile config.Profile
	Store   keys.Store
	Node    *node.Client
	// KDF is the key derivation of new key files (keys.DefaultKDF; lighter only in tests).
	KDF keys.KDFParams
	// DataDir is the data folder (the local contract lists are in it).
	DataDir string

	historyNode *node.Client // the remote history node, when the profile names one
}

// New opens a wallet on the data folder and the chosen node profile.
func New(cfg *config.Config, profile config.Profile, dataDir string) *Wallet {
	w := &Wallet{Config: cfg, Profile: profile, Store: keys.Store{Dir: config.AccountsDir(dataDir)},
		Node: node.New(profile.Address), KDF: keys.DefaultKDF, DataDir: dataDir}
	w.Node.DiagAddress = profile.DiagAddress()
	if profile.HistoryNode != "" {
		w.historyNode = node.New(profile.HistoryNode)
	}
	return w
}

// History is the history source: the profile's remote full node when it names one (read-only:
// its client is only ever wrapped in a history.Source), otherwise the local node.
func (w *Wallet) History() history.Source {
	if w.historyNode == nil {
		return history.Local(w.Node)
	}
	return history.RemoteNode(w.historyNode)
}

// NetworkCheck is what the node said of its network before a send.
type NetworkCheck struct {
	ID node.NetworkID
	// Confirmed: the node reported a network uuid and it agrees with the profile.
	Confirmed bool
	// Why the network is not confirmed (the node did not report it), "" when confirmed.
	Why string
}

// Describe is the check for a review: "confirmed by the node (uuid …)" or "not confirmed by the
// node: …".
func (c NetworkCheck) Describe() string {
	if c.Confirmed {
		return fmt.Sprintf("confirmed by the node (uuid %d)", c.ID.UUID)
	}
	return "not confirmed by the node (" + c.Why + ")"
}

// CheckNetwork compares the declared network with what the node says of itself (spec 5.1): a
// community node's diagnostic API reports its network uuid, which must agree with the profile.
// When the node reports none (an official node, an older community release, the diag port closed
// or off) the configuration decides, unless the profile pins network_uuid or sets
// require_network_uuid: then nothing is sent.
func (w *Wallet) CheckNetwork(ctx context.Context) (NetworkCheck, error) {
	id, err := w.Node.NetworkIdentity(ctx)
	return JudgeNetwork(w.Profile, id, err)
}

// JudgeNetwork is CheckNetwork on an answer of node.NetworkIdentity: the check, or why nothing may
// be sent.
func JudgeNetwork(p config.Profile, id node.NetworkID, err error) (NetworkCheck, error) {
	if err != nil || !id.Known() {
		// NetworkIdentity reports every failure as node.ErrNoNetworkID: no identity, whatever the reason
		why := "the node does not tell its network"
		if err != nil {
			why = strings.TrimPrefix(err.Error(), node.ErrNoNetworkID.Error()+": ")
		}
		switch {
		case p.PinnedUUID() != 0:
			return NetworkCheck{}, fmt.Errorf("the profile pins network_uuid = %d, but the node did not report one (%s): refused", p.PinnedUUID(), why)
		case p.RequireNetworkUUID:
			return NetworkCheck{}, fmt.Errorf("the profile sets require_network_uuid, but the node did not report its network (%s): refused", why)
		}
		return NetworkCheck{ID: id, Why: why}, nil
	}
	if err := NetworkMismatch(p, id); err != nil {
		return NetworkCheck{}, err
	}
	return NetworkCheck{ID: id, Confirmed: true}, nil
}

// NetworkMismatch refuses a node whose network identity contradicts the profile: a profile that
// says mainnet (declared, or assumed without a declaration) needs the mainnet uuid; one that says
// testnet or private needs another uuid; a profile that pins network_uuid needs that one.
func NetworkMismatch(p config.Profile, id node.NetworkID) error {
	if !id.Known() {
		return nil // nothing to compare with: the configuration decides
	}
	if pin := p.PinnedUUID(); pin != 0 && id.UUID != pin {
		return fmt.Errorf("the node reports network uuid %d, but the profile pins network_uuid = %d: refused, check the node or the configuration", id.UUID, pin)
	}
	switch {
	case id.IsMainnet() && !p.Network.IsMainnet():
		return fmt.Errorf("the configuration says %s, but the node reports MAINNET (uuid %d): refused; fix the configuration (network = \"mainnet\")", p.Network, id.UUID)
	case !id.IsMainnet() && p.Network.IsMainnet():
		return fmt.Errorf("the configuration says mainnet (declared or assumed), but the node reports another network (uuid %d): refused; declare it with network = \"testnet\" or \"private\" in its node profile", id.UUID)
	}
	return nil
}

// Account resolves an account name, or failing that a base58 address (read-only use).
func (w *Wallet) Account(nameOrAddress string) (name string, pub ed25519.PublicKey, err error) {
	if keys.ValidName(nameOrAddress) == nil && w.Store.Exists(nameOrAddress) {
		f, err := w.Store.Load(nameOrAddress)
		if err != nil {
			return "", nil, err
		}
		pub, err := f.PublicKey()
		return f.Name, pub, err
	}
	if pub, err := keys.ParseAddress(nameOrAddress); err == nil {
		return "", pub, nil
	}
	return "", nil, fmt.Errorf("%s is neither an account (see 'account list') nor an address", echo.Quote(nameOrAddress))
}

// CreateAccount encrypts and saves a key under name.
func (w *Wallet) CreateAccount(name string, key ed25519.PrivateKey, password []byte) (*keys.File, error) {
	if w.Store.Exists(name) {
		return nil, fmt.Errorf("an account named %q already exists", name)
	}
	f, err := keys.Encrypt(name, key, password, w.KDF)
	if err != nil {
		return nil, err
	}
	return f, w.Store.Save(f)
}
