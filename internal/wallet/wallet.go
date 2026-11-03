// Package wallet is what the commands and the terminal interface share: the accounts, the node
// and the steps of a transfer (checks, signing, sending, following).
package wallet

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/CreditsCommunity/wallet/internal/amount"
	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/echo"
	"github.com/CreditsCommunity/wallet/internal/history"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
	"github.com/CreditsCommunity/wallet/internal/tx"
)

// Wallet ties the configuration, the key store and the node together.
type Wallet struct {
	Config  *config.Config
	Profile config.Profile
	Store   keys.Store
	Node    *node.Client
	// KDF is the key derivation of new key files (keys.DefaultKDF; lighter only in tests).
	KDF keys.KDFParams
}

// New opens a wallet on the data folder and the chosen node profile.
func New(cfg *config.Config, profile config.Profile, dataDir string) *Wallet {
	return &Wallet{Config: cfg, Profile: profile, Store: keys.Store{Dir: config.AccountsDir(dataDir)},
		Node: node.New(profile.Address), KDF: keys.DefaultKDF}
}

// History is the history source (milestone 1: the local node).
func (w *Wallet) History() history.Source { return history.LocalNode{Node: w.Node} }

// CheckNetwork compares the declared network with what the node says of itself, when it can say
// it (spec 5.1). Today's nodes cannot: the configuration decides.
func (w *Wallet) CheckNetwork(ctx context.Context) error {
	id, err := w.Node.NetworkIdentity(ctx)
	if errors.Is(err, node.ErrNoNetworkID) {
		return nil
	}
	if err != nil {
		return err
	}
	return NetworkMismatch(w.Profile.Network, id)
}

// NetworkMismatch refuses a node whose network identifier contradicts the configuration.
func NetworkMismatch(declared config.Network, id node.NetworkID) error {
	if node.MainnetUUID == "" || id.UUID == "" {
		return nil // nothing to compare with
	}
	isMainnet := id.UUID == node.MainnetUUID
	if isMainnet && !declared.IsMainnet() {
		return fmt.Errorf("the configuration says %s, but the node is on MAINNET: fix the configuration", declared)
	}
	if !isMainnet && declared.IsMainnet() {
		return fmt.Errorf("the configuration says mainnet, but the node is on another network (%s)", id.UUID)
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

// SendPlan is a transfer checked and ready to sign: what the confirmation shows.
type SendPlan struct {
	Account  string
	From     ed25519.PublicKey
	To       ed25519.PublicKey
	Amount   amount.Amount
	Fee      amount.Amount // the fee the node charges now (ActualFeeGet)
	MaxFee   amount.Commission
	Balance  amount.Amount
	InnerID  int64
	Network  config.Network
	Node     string
	Status   node.Status
	Prepared time.Time
}

// ErrNotSynced is returned when the node is not at the head of the chain.
var ErrNotSynced = errors.New("the node is not synced")

// PrepareSend checks a transfer before it is confirmed: the node synced and on the declared
// network, the recipient, the amount, the balance with the fee; and reads the inner id.
func (w *Wallet) PrepareSend(ctx context.Context, account, to, amountText string) (*SendPlan, error) {
	f, err := w.Store.Load(account)
	if err != nil {
		return nil, err
	}
	from, err := f.PublicKey()
	if err != nil {
		return nil, err
	}
	target, err := keys.ParseAddress(to)
	if err != nil {
		return nil, fmt.Errorf("recipient: %w", err)
	}
	if target.Equal(from) {
		return nil, errors.New("the recipient is the sending account itself")
	}
	amt, err := amount.ParsePositive(amountText)
	if err != nil {
		return nil, err
	}
	st, err := w.Node.Status(ctx)
	if err != nil {
		return nil, err
	}
	if !st.Synced {
		return nil, fmt.Errorf("%w: %s; a transfer sent now would be refused or delayed. Wait until 'status' says synced", ErrNotSynced, st.Why)
	}
	if err := w.CheckNetwork(ctx); err != nil {
		return nil, err
	}
	fee, err := w.Node.TransferFee(ctx)
	if err != nil {
		return nil, err
	}
	bal, err := w.Node.Balance(ctx, from)
	if err != nil {
		return nil, err
	}
	if need := amt.Add(fee.Amount()); bal.CS.Cmp(need) < 0 {
		return nil, fmt.Errorf("not enough CS: the balance is %s CS, the transfer needs %s CS with the fee", bal.CS.Grouped(), need.Grouped())
	}
	inner, err := w.Node.NextInnerID(ctx, from)
	if err != nil {
		return nil, err
	}
	return &SendPlan{Account: f.Name, From: from, To: target, Amount: amt, Fee: fee.Amount(), MaxFee: tx.MaxFee,
		Balance: bal.CS, InnerID: inner, Network: w.Profile.Network, Node: w.Profile.Address, Status: st, Prepared: time.Now()}, nil
}

// SendResult is the outcome of a transfer.
type SendResult struct {
	InnerID int64
	Block   int64
	Fee     amount.Amount
	Round   int32
	// InBlock: the transaction is in a block (the node said so, or the inner id was seen)
	InBlock bool
}

// Send decrypts the key with the password, signs the planned transfer, sends it and follows it
// until it is in a block (at most follow). The key is zeroed as soon as the transaction is signed.
func (w *Wallet) Send(ctx context.Context, plan *SendPlan, password []byte, follow time.Duration) (SendResult, error) {
	res := SendResult{InnerID: plan.InnerID}
	f, err := w.Store.Load(plan.Account)
	if err != nil {
		return res, err
	}
	key, err := f.Decrypt(password)
	if err != nil {
		return res, err
	}
	t, _, err := tx.Transfer{Source: plan.From, Target: plan.To, Amount: plan.Amount, InnerID: plan.InnerID, MaxFee: plan.MaxFee}.Sign(key)
	keys.Zero(key)
	if err != nil {
		return res, err
	}
	// a connection of its own: TransactionFlow blocks until the block, the screens keep refreshing
	sender := node.New(w.Node.Address)
	defer sender.Close()
	sent, err := sender.Send(ctx, t)
	if err != nil {
		return res, err
	}
	res.Round, res.Fee, res.Block = sent.Round, sent.Fee, sent.Block
	if !sent.InProgress {
		res.InBlock = true
		return res, nil
	}
	wait, cancel := context.WithTimeout(ctx, follow)
	defer cancel()
	if err := sender.WaitInBlock(wait, plan.From, plan.InnerID, 2*time.Second); err != nil {
		return res, fmt.Errorf("sent, but %w; check 'history' later, and do not send it again before", err)
	}
	res.InBlock = true
	return res, nil
}
