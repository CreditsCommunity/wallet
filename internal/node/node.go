// Package node is the wallet's client of the node's Thrift API (binary protocol, buffered
// transport), the calls it makes and how it reads their answers.
package node

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/apache/thrift/lib/go/thrift"

	"github.com/CreditsCommunity/wallet/internal/amount"
	"github.com/CreditsCommunity/wallet/thrift/api"
	"github.com/CreditsCommunity/wallet/thrift/general"
)

// Status codes of general.APIResponse (the node's APIRequestStatusType).
const (
	codeSuccess    = 0
	codeNotFound   = 3
	codeInProgress = 4
)

// Client talks to one node. It connects on first use and again after a connection error. Calls
// are serialized: one connection carries one call at a time.
type Client struct {
	Address string
	// SocketTimeout bounds one call; TransactionFlow waits for the block, so it is long.
	SocketTimeout time.Duration

	mu        sync.Mutex
	transport thrift.TTransport
	api       *api.APIClient
}

// New returns a client of the node at address (HOST:PORT). It does not connect yet.
func New(address string) *Client {
	return &Client{Address: address, SocketTimeout: 120 * time.Second}
}

// Close closes the connection.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeLocked()
}

func (c *Client) closeLocked() {
	if c.transport != nil {
		c.transport.Close()
	}
	c.transport, c.api = nil, nil
}

// do runs one or more API calls on the connection, connecting first if needed. A transport error
// drops the connection, so that the next call reconnects.
func (c *Client) do(f func(a *api.APIClient) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.api == nil {
		socket := thrift.NewTSocketConf(c.Address, &thrift.TConfiguration{ConnectTimeout: 10 * time.Second, SocketTimeout: c.SocketTimeout})
		transport := thrift.NewTBufferedTransport(socket, 8192)
		if err := transport.Open(); err != nil {
			return fmt.Errorf("cannot reach the node at %s: %w", c.Address, err)
		}
		c.transport = transport
		c.api = api.NewAPIClientFactory(transport, thrift.NewTBinaryProtocolFactoryConf(nil))
	}
	err := f(c.api)
	var te thrift.TTransportException
	var pe thrift.TProtocolException
	if errors.As(err, &te) || errors.As(err, &pe) {
		c.closeLocked()
		return fmt.Errorf("node %s: %w", c.Address, err)
	}
	return err
}

// APIError is a call the node answered with a non-zero status.
type APIError struct {
	Call    string
	Code    int8
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("%s: %s (code %d)", e.Call, e.Message, e.Code) }

// check turns a non-success status into an error. A missing status is an error too: every call
// that has one always sets it.
func check(call string, s *general.APIResponse) error {
	if s == nil {
		return &APIError{Call: call, Code: -1, Message: "no status in the answer"}
	}
	if s.Code != codeSuccess {
		return &APIError{Call: call, Code: s.Code, Message: s.Message}
	}
	return nil
}

// ---- status --------------------------------------------------------------------------------------

// MaxLag is how many rounds the last block may be behind the current round on a synced node
// (normally 1: the block of the previous round).
const MaxLag = 10

// MaxHeadAge is how old the last block may be on a synced node (the network makes blocks every
// few seconds; a node whose head is older is stuck or cut off).
const MaxHeadAge = 10 * time.Minute

// Status is the node's view of the chain.
type Status struct {
	LastBlock int64
	Round     int64
	HeadTime  time.Time // time of the last block
	Synced    bool
	Why       string // why it is not synced
}

// Judge decides whether a node is synced: the last block within MaxLag rounds of the current
// round, and not older than MaxHeadAge.
func Judge(lastBlock, round int64, head, now time.Time) (bool, string) {
	switch {
	case round <= 0 || lastBlock <= 0:
		return false, "the node has no chain yet (starting)"
	case round-lastBlock > MaxLag:
		return false, fmt.Sprintf("the node is %d rounds behind (catching up)", round-lastBlock)
	case !head.IsZero() && now.Sub(head) > MaxHeadAge:
		return false, fmt.Sprintf("the last block is %s old", now.Sub(head).Round(time.Second))
	}
	return true, ""
}

// Status reads the last block, the round and the head's time (SyncStateGet, PoolListGet).
func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	err := c.do(func(a *api.APIClient) error {
		r, err := a.SyncStateGet(ctx)
		if err != nil {
			return err
		}
		if err := check("SyncStateGet", r.Status); err != nil {
			return err
		}
		s.LastBlock, s.Round = r.LastBlock, r.CurrRound
		pools, err := a.PoolListGet(ctx, 0, 1)
		if err != nil {
			return err
		}
		if err := check("PoolListGet", pools.Status); err != nil {
			return err
		}
		if len(pools.Pools) > 0 {
			s.HeadTime = time.UnixMilli(int64(pools.Pools[0].Time))
		}
		return nil
	})
	if err != nil {
		return s, err
	}
	s.Synced, s.Why = Judge(s.LastBlock, s.Round, s.HeadTime, time.Now())
	return s, nil
}

// OldestBlock finds the oldest block the node holds (a pruned node keeps only recent ones), by a
// binary search with PoolInfoGet between 1 and last: about 28 calls on mainnet.
func (c *Client) OldestBlock(ctx context.Context, last int64) (int64, error) {
	has := func(seq int64) (bool, error) {
		var found bool
		err := c.do(func(a *api.APIClient) error {
			r, err := a.PoolInfoGet(ctx, seq, 0)
			if err != nil {
				return err
			}
			if err := check("PoolInfoGet", r.Status); err != nil {
				return err
			}
			found = r.IsFound
			return nil
		})
		return found, err
	}
	if ok, err := has(1); err != nil || ok {
		return 1, err
	}
	if ok, err := has(last); err != nil || !ok {
		return last, err
	}
	lo, hi := int64(1), last // !has(lo), has(hi)
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		ok, err := has(mid)
		if err != nil {
			return 0, err
		}
		if ok {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi, nil
}

// NetworkID is what a community node will answer about its network: the uuid it derives from
// block 1 and its version (spec 5.1).
type NetworkID struct {
	UUID    string
	Version string
}

// ErrNoNetworkID: the node's API has no call that names its network (official nodes, and the
// community node until it adds one).
var ErrNoNetworkID = errors.New("the node does not tell its network")

// MainnetUUID is the uuid of mainnet, to fill in once the node's API returns it.
const MainnetUUID = ""

// NetworkIdentity is the hook for the network-identifier call of the community node (spec 5.1).
// The API has no such call yet: it always returns ErrNoNetworkID, and the configuration decides.
func (c *Client) NetworkIdentity(ctx context.Context) (NetworkID, error) {
	return NetworkID{}, ErrNoNetworkID
}

// ---- wallets -----------------------------------------------------------------------------------

// Balance is what the node knows of a wallet's CS.
type Balance struct {
	CS           amount.Amount
	DelegatedOut amount.Amount // delegated by this wallet to nodes
	DelegatedIn  amount.Amount // delegated to this wallet
	Delegations  int           // number of outgoing delegations listed
}

// Balance reads a wallet's balance and delegated amounts (WalletBalanceGet).
func (c *Client) Balance(ctx context.Context, address ed25519.PublicKey) (Balance, error) {
	var b Balance
	err := c.do(func(a *api.APIClient) error {
		r, err := a.WalletBalanceGet(ctx, general.Address(address))
		if err != nil {
			return err
		}
		if err := check("WalletBalanceGet", r.Status); err != nil {
			return err
		}
		b.CS = amount.FromThrift(r.Balance)
		if d := r.Delegated; d != nil {
			b.DelegatedOut = amount.FromThrift(d.Outgoing)
			b.DelegatedIn = amount.FromThrift(d.Incoming)
			b.Delegations = len(d.Recipients)
		}
		return nil
	})
	return b, err
}

// NextInnerID is the sender's last inner id + 1 (replay protection). A wallet the node has never
// seen answers "not found" (code 3): its first transaction has inner id 1. Any other failure is an
// error: guessing an inner id would get the transfer rejected, or worse, collide with a pending one.
func (c *Client) NextInnerID(ctx context.Context, address ed25519.PublicKey) (int64, error) {
	var next int64
	err := c.do(func(a *api.APIClient) error {
		r, err := a.WalletTransactionsCountGet(ctx, general.Address(address))
		if err != nil {
			return err
		}
		next, err = nextInnerID(r)
		return err
	})
	return next, err
}

func nextInnerID(r *api.WalletTransactionsCountGetResult_) (int64, error) {
	if r == nil || r.Status == nil {
		return 0, &APIError{Call: "WalletTransactionsCountGet", Code: -1, Message: "no status in the answer"}
	}
	switch r.Status.Code {
	case codeSuccess:
		last := int64(r.LastTransactionInnerId)
		if last < 0 || last >= MaxInnerID {
			return 0, fmt.Errorf("WalletTransactionsCountGet: last inner id %d out of range", last)
		}
		return last + 1, nil
	case codeNotFound:
		return 1, nil
	}
	return 0, check("WalletTransactionsCountGet", r.Status)
}

// MaxInnerID is the largest inner id: the node keeps 46 bits of the API's id (bits 46 and 47 of
// the 6-byte field are flags of the wallet-id encoding).
const MaxInnerID = 1<<46 - 1

// LastInnerID is the inner id of the wallet's last transaction in a block (0: none).
func (c *Client) LastInnerID(ctx context.Context, address ed25519.PublicKey) (int64, error) {
	n, err := c.NextInnerID(ctx, address)
	return n - 1, err
}

// TransferSize is the size the node uses to price a plain transfer.
const TransferSize = 86

// TransferFee is the fee the node charges for a transfer now (ActualFeeGet).
func (c *Client) TransferFee(ctx context.Context) (amount.Commission, error) {
	var fee amount.Commission
	err := c.do(func(a *api.APIClient) error {
		r, err := a.ActualFeeGet(ctx, TransferSize)
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

// ---- sending -----------------------------------------------------------------------------------

// Sent is the node's answer to a transaction.
type Sent struct {
	Round      int32
	Block      int64 // the block it is in (0 while in progress)
	Index      int32
	Fee        amount.Amount
	InProgress bool // the node stopped waiting before the block: follow it with WaitInBlock
}

// Send hands a signed transaction to the node (TransactionFlow). The node answers once the
// transaction is in a block, or with "in progress" when it stops waiting.
func (c *Client) Send(ctx context.Context, t *api.Transaction) (Sent, error) {
	var s Sent
	err := c.do(func(a *api.APIClient) error {
		r, err := a.TransactionFlow(ctx, t)
		if err != nil {
			return err
		}
		s.Round = r.RoundNum
		if r.Status != nil && r.Status.Code == codeInProgress {
			s.InProgress = true
			return nil
		}
		if err := check("TransactionFlow", r.Status); err != nil {
			return err
		}
		if r.ID != nil {
			s.Block, s.Index = r.ID.PoolSeq, r.ID.Index
		}
		s.Fee = amount.FromThrift(r.Fee)
		return nil
	})
	return s, err
}

// WaitInBlock follows a transaction the node stopped waiting for, until it is in a block or the
// context ends. The node's TransactionsStateGet is not implemented, so it polls the sender's last
// inner id: equal to ours means ours is recorded; beyond ours means another transaction of the
// account was recorded after, so ours is confirmed only if the account's latest transactions
// (TransactionsGet) hold it.
func (c *Client) WaitInBlock(ctx context.Context, source ed25519.PublicKey, innerID int64, every time.Duration) error {
	for {
		last, err := c.LastInnerID(ctx, source)
		if err != nil {
			return err
		}
		if last == innerID {
			return nil
		}
		if last > innerID {
			list, _, err := c.Transactions(ctx, source, 0, 100)
			if err != nil {
				return err
			}
			if HasTransaction(list, source, innerID) {
				return nil
			}
			return fmt.Errorf("the account's last recorded inner id is %d, past this transfer's %d, and the transfer is not among its last 100 transactions: its outcome is unknown, check the history", last, innerID)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("not in a block yet (inner id %d, last recorded %d): %w", innerID, last, ctx.Err())
		case <-time.After(every):
		}
	}
}

// HasTransaction tells whether list holds the transaction of source with that inner id.
func HasTransaction(list []*api.SealedTransaction, source ed25519.PublicKey, innerID int64) bool {
	for _, s := range list {
		if s != nil && s.Trxn != nil && int64(s.Trxn.ID) == innerID && ed25519.PublicKey(s.Trxn.Source).Equal(source) {
			return true
		}
	}
	return false
}

// ---- history -----------------------------------------------------------------------------------

// Transactions returns a page of a wallet's transactions, newest first, and the wallet's total
// transaction count (TransactionsGet).
func (c *Client) Transactions(ctx context.Context, address ed25519.PublicKey, offset, limit int64) ([]*api.SealedTransaction, int, error) {
	var list []*api.SealedTransaction
	var total int
	err := c.do(func(a *api.APIClient) error {
		r, err := a.TransactionsGet(ctx, general.Address(address), offset, limit)
		if err != nil {
			return err
		}
		if err := check("TransactionsGet", r.Status); err != nil {
			return err
		}
		list, total = r.Transactions, int(r.TotalTrxnsCount)
		return nil
	})
	return list, total, err
}
