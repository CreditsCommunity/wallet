package node

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/CreditsCommunity/wallet/internal/amount"
	"github.com/CreditsCommunity/wallet/thrift/api"
	"github.com/CreditsCommunity/wallet/thrift/general"
)

// ---- delegations -------------------------------------------------------------------------------

// Delegation is one delegation made or received by a wallet (DelegatedItem).
type Delegation struct {
	Wallet ed25519.PublicKey // the other side: the node delegated to, or the delegating wallet
	Amount amount.Amount
	Until  time.Time // zero: without a time limit
	From   time.Time // when a delegation with a time limit started (zero if unknown)
	Coeff  int8
}

// Delegations is what a wallet delegated and received.
type Delegations struct {
	Out      []Delegation // made by the wallet (recipients)
	In       []Delegation // received (donors)
	OutTotal amount.Amount
	InTotal  amount.Amount
}

// Delegations reads the delegations made and received by a wallet (WalletDataGet). A wallet the
// node has never seen has none.
func (c *Client) Delegations(ctx context.Context, address ed25519.PublicKey) (Delegations, error) {
	var d Delegations
	err := c.do(func(a *api.APIClient) error {
		r, err := a.WalletDataGet(ctx, general.Address(address))
		if err != nil {
			return err
		}
		if r.Status != nil && r.Status.Code == codeNotFound {
			return nil
		}
		if err := check("WalletDataGet", r.Status); err != nil {
			return err
		}
		if r.WalletData == nil || r.WalletData.Delegated == nil {
			return nil
		}
		x := r.WalletData.Delegated
		d.OutTotal, d.InTotal = amount.FromThrift(x.Outgoing), amount.FromThrift(x.Incoming)
		d.Out, d.In = delegationItems(x.Recipients), delegationItems(x.Donors)
		return nil
	})
	return d, err
}

func delegationItems(items []*api.DelegatedItem) []Delegation {
	out := make([]Delegation, 0, len(items))
	for _, it := range items {
		if it == nil || len(it.Wallet) != ed25519.PublicKeySize {
			continue
		}
		d := Delegation{Wallet: ed25519.PublicKey(it.Wallet), Amount: amount.FromThrift(it.Sum)}
		if it.ValidUntil != nil && *it.ValidUntil > 0 {
			d.Until = time.Unix(*it.ValidUntil, 0).UTC()
		}
		if it.FromTime != nil && *it.FromTime > 0 {
			d.From = time.Unix(*it.FromTime, 0).UTC()
		}
		if it.Coeff != nil {
			d.Coeff = *it.Coeff
		}
		out = append(out, d)
	}
	return out
}

// Fee is the fee the node charges now for a transaction of size bytes (ActualFeeGet).
func (c *Client) Fee(ctx context.Context, size int) (amount.Commission, error) {
	var fee amount.Commission
	err := c.do(func(a *api.APIClient) error {
		r, err := a.ActualFeeGet(ctx, int32(size))
		if err != nil {
			return err
		}
		if r.Fee == nil {
			return errors.New("ActualFeeGet: no fee in the answer")
		}
		fee = amount.Commission(uint16(r.Fee.Commission))
		return nil
	})
	return fee, err
}

// ---- tokens ------------------------------------------------------------------------------------

// ErrNoTokenCache: the node does not keep the token cache behind the Token* calls. A community
// node keeps it with "[api] tokens_cache=true" in its config.ini; an official node only in its
// monitor and web-wallet builds.
var ErrNoTokenCache = errors.New("the node does not serve token data: set tokens_cache=true in the [api] section of its config.ini and restart it")

func tokenCheck(call string, s *general.APIResponse) error {
	if s != nil && s.Code != codeSuccess && strings.Contains(s.Message, "doesn't provide such info") {
		return ErrNoTokenCache
	}
	return check(call, s)
}

// TokenBalance is a token a wallet holds (TokenBalancesGet).
type TokenBalance struct {
	Token   ed25519.PublicKey
	Code    string
	Name    string
	Balance string // decimal text, as the contract returns it
}

// TokenBalances lists the tokens a wallet holds with a balance above 0.
func (c *Client) TokenBalances(ctx context.Context, address ed25519.PublicKey) ([]TokenBalance, error) {
	var out []TokenBalance
	err := c.do(func(a *api.APIClient) error {
		r, err := a.TokenBalancesGet(ctx, general.Address(address))
		if err != nil {
			return err
		}
		if err := tokenCheck("TokenBalancesGet", r.Status); err != nil {
			return err
		}
		for _, b := range r.Balances {
			if b == nil || len(b.Token) != ed25519.PublicKeySize {
				continue
			}
			out = append(out, TokenBalance{Token: ed25519.PublicKey(b.Token), Code: string(b.Code), Name: b.Name, Balance: string(b.Balance)})
		}
		return nil
	})
	return out, err
}

// TokenInfo is what the node's token cache knows of a token.
type TokenInfo struct {
	Address     ed25519.PublicKey
	Code        string
	Name        string
	TotalSupply string
	Owner       ed25519.PublicKey
	Holders     int32
	Transfers   int32
	Standard    int32
}

// ErrNotAToken: the node's token cache does not list the address.
var ErrNotAToken = errors.New("the node does not know a token at this address")

// TokenInfo reads a token's description (TokenInfoGet).
func (c *Client) TokenInfo(ctx context.Context, token ed25519.PublicKey) (TokenInfo, error) {
	var t TokenInfo
	err := c.do(func(a *api.APIClient) error {
		r, err := a.TokenInfoGet(ctx, general.Address(token))
		if err != nil {
			return err
		}
		if err := tokenCheck("TokenInfoGet", r.Status); err != nil {
			if errors.Is(err, ErrNoTokenCache) {
				return err
			}
			return ErrNotAToken
		}
		if r.Token == nil {
			return ErrNotAToken
		}
		x := r.Token
		t = TokenInfo{Address: token, Code: string(x.Code), Name: x.Name, TotalSupply: string(x.TotalSupply),
			Owner: ed25519.PublicKey(x.Owner), Holders: x.HoldersCount, Transfers: x.TransfersCount, Standard: x.TokenStandard}
		return nil
	})
	return t, err
}

// TokenTransfers returns a page of a wallet's transfers of one token, newest first, and their count
// (TokenWalletTransfersGet).
func (c *Client) TokenTransfers(ctx context.Context, token, address ed25519.PublicKey, offset, limit int64) ([]*api.TokenTransfer, int, error) {
	var list []*api.TokenTransfer
	var count int
	err := c.do(func(a *api.APIClient) error {
		r, err := a.TokenWalletTransfersGet(ctx, general.Address(token), general.Address(address), offset, limit)
		if err != nil {
			return err
		}
		if err := tokenCheck("TokenWalletTransfersGet", r.Status); err != nil {
			if errors.Is(err, ErrNoTokenCache) {
				return err
			}
			return fmt.Errorf("%w (TokenWalletTransfersGet)", ErrNotAToken)
		}
		list, count = r.Transfers, int(r.Count)
		return nil
	})
	return list, count, err
}

// ---- contracts ---------------------------------------------------------------------------------

// ErrNotAContract: no contract is deployed at the address.
var ErrNotAContract = errors.New("no contract is deployed at this address")

// ContractMethods lists the public methods of a deployed contract with their declared types, as
// the node's executor reads them from its byte code (SmartContractGet, ContractAllMethodsGet).
func (c *Client) ContractMethods(ctx context.Context, contract ed25519.PublicKey) ([]*general.MethodDescription, error) {
	var methods []*general.MethodDescription
	err := c.do(func(a *api.APIClient) error {
		d, err := a.SmartContractGet(ctx, general.Address(contract))
		if err != nil {
			return err
		}
		if d.Status == nil || d.Status.Code != codeSuccess || d.SmartContract == nil || d.SmartContract.SmartContractDeploy == nil {
			return ErrNotAContract
		}
		r, err := a.ContractAllMethodsGet(ctx, d.SmartContract.SmartContractDeploy.ByteCodeObjects)
		if err != nil {
			return err
		}
		if r.Code != 0 {
			return fmt.Errorf("ContractAllMethodsGet: %s", r.Message)
		}
		methods = r.Methods
		return nil
	})
	return methods, err
}

// Get runs a getter (a transaction built by tx.Getter): free, nothing changes. It returns the
// method's result.
func (c *Client) Get(ctx context.Context, t *api.Transaction) (*general.Variant, error) {
	var v *general.Variant
	err := c.do(func(a *api.APIClient) error {
		r, err := a.TransactionFlow(ctx, t)
		if err != nil {
			return err
		}
		if err := check("TransactionFlow (getter)", r.Status); err != nil {
			return err
		}
		v = r.SmartContractResult_
		return nil
	})
	return v, err
}
