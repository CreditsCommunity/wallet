package wallet

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
)

// The account's local contract list: the contracts the wallet deployed, one file per deployer
// address in the data folder (contracts/<address>.json, mode 600). It holds public data only (the
// addresses, the class name, the source file's path on this machine, the network and node, the
// block and inner id), so that a contract stays at hand when the node no longer lists it (another
// network, a node without its deploy) or before it does.

// LocalContract is a contract in the local list.
type LocalContract struct {
	Address       string    `json:"address"`
	Name          string    `json:"name"`
	File          string    `json:"file,omitempty"`
	Network       string    `json:"network"`
	Node          string    `json:"node"`
	Deployed      time.Time `json:"deployed"`
	Block         int64     `json:"block,omitempty"`
	InnerID       int64     `json:"inner_id"`
	TokenStandard int32     `json:"token_standard,omitempty"`
}

type localContracts struct {
	Version   int             `json:"version"`
	Deployer  string          `json:"deployer"`
	Contracts []LocalContract `json:"contracts"`
}

// maxLocalList bounds the file the wallet reads.
const maxLocalList = 4 << 20

// ContractsDir is the folder of the local contract lists.
func ContractsDir(dataDir string) string { return filepath.Join(dataDir, "contracts") }

func (w *Wallet) localListPath(deployer ed25519.PublicKey) string {
	return filepath.Join(ContractsDir(w.DataDir), keys.Address(deployer)+".json")
}

// LocalContracts reads the local contract list of a deployer (none: an empty list).
func (w *Wallet) LocalContracts(deployer ed25519.PublicKey) ([]LocalContract, error) {
	f, err := os.Open(w.localListPath(deployer))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxLocalList+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxLocalList {
		return nil, fmt.Errorf("%s is larger than %d bytes: not read", f.Name(), maxLocalList)
	}
	var l localContracts
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("%s: %w", f.Name(), err)
	}
	if l.Deployer != keys.Address(deployer) {
		return nil, fmt.Errorf("%s is the list of another account", f.Name())
	}
	out := l.Contracts[:0]
	for _, c := range l.Contracts {
		if _, err := keys.ParseAddress(c.Address); err == nil {
			out = append(out, c)
		}
	}
	return out, nil
}

// AddLocalContract adds a contract to the deployer's local list (an address already there is
// replaced). The file is written whole to a temporary file, then renamed.
func (w *Wallet) AddLocalContract(deployer ed25519.PublicKey, c LocalContract) error {
	if w.DataDir == "" {
		return errors.New("no data folder")
	}
	list, err := w.LocalContracts(deployer)
	if err != nil {
		return err
	}
	l := localContracts{Version: 1, Deployer: keys.Address(deployer), Contracts: []LocalContract{c}}
	for _, x := range list {
		if x.Address != c.Address {
			l.Contracts = append(l.Contracts, x)
		}
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	dir := ContractsDir(w.DataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".list-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
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
	return os.Rename(tmp.Name(), w.localListPath(deployer))
}

// ContractRow is a contract of an account: listed by the node, in the local list, or both.
type ContractRow struct {
	Address       ed25519.PublicKey
	Name          string // as the contract calls itself
	Created       time.Time
	TokenStandard int32
	Transactions  int32
	OnNode        bool           // the node lists it (SmartContractsListGet)
	Local         *LocalContract // the local list's entry, if any
}

// MergeContracts merges the node's list of a deployer's contracts with the local list: one row per
// address, the node's data first, newest first.
func MergeContracts(onNode []node.ContractInfo, local []LocalContract) []ContractRow {
	var rows []ContractRow
	at := map[string]int{}
	for _, c := range onNode {
		k := string(c.Address)
		if _, dup := at[k]; dup {
			continue
		}
		at[k] = len(rows)
		rows = append(rows, ContractRow{Address: c.Address, Name: c.Name(), Created: c.Created, TokenStandard: c.TokenStandard,
			Transactions: c.Transactions, OnNode: true})
	}
	for i := range local {
		l := local[i]
		addr, err := keys.ParseAddress(l.Address)
		if err != nil {
			continue
		}
		if j, ok := at[string(addr)]; ok {
			if rows[j].Local == nil {
				rows[j].Local = &l
			}
			continue
		}
		at[string(addr)] = len(rows)
		rows = append(rows, ContractRow{Address: addr, Name: l.Name, Created: l.Deployed, TokenStandard: l.TokenStandard, Local: &l})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Created.After(rows[j].Created) })
	return rows
}

// Contracts lists a deployer's contracts: the node's list merged with the local one. A node error
// is returned next to the local rows (nodeErr), so that they are still shown; so are entries of the
// node's list it could not read (a known node bug: see node.ContractsBy).
func (w *Wallet) Contracts(ctx context.Context, deployer ed25519.PublicKey) (rows []ContractRow, nodeErr, localErr error) {
	onNode, skipped, nodeErr := w.Node.ContractsBy(ctx, deployer)
	if nodeErr == nil && skipped > 0 {
		nodeErr = fmt.Errorf("%d entries of the node's list could not be read (the node lists payments to contracts as deploys and fails on them)", skipped)
	}
	local, localErr := w.LocalContracts(deployer)
	return MergeContracts(onNode, local), nodeErr, localErr
}
