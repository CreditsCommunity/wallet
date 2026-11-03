// Package tx builds and signs transactions.
//
// SECURITY REVIEW: the signed bytes are the node's csdb::Transaction::to_byte_stream_for_sig(),
// byte-identical to the node's sample clients and ci/testnet/fund.py (tested against the
// samples' sign-expected.txt).
package tx

import (
	"crypto/ed25519"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/CreditsCommunity/wallet/internal/amount"
	"github.com/CreditsCommunity/wallet/thrift/api"
	"github.com/CreditsCommunity/wallet/thrift/general"
)

const currencyCS = 1

// MaxFee is the most a sender accepts to pay for a transfer, as in the node's samples and
// ci/testnet/fund.py: "1 CS", encoded 0x4c66 (0.99609375 CS). The node charges the actual fee
// (about 0.0087 CS on mainnet) and returns it.
var MaxFee = amount.EncodeCommission(1.0)

// MaxInnerID is the largest inner id the 6-byte field holds.
const MaxInnerID = 1<<48 - 1

// Transfer is a CS transfer before signing.
type Transfer struct {
	Source  ed25519.PublicKey
	Target  ed25519.PublicKey
	Amount  amount.Amount
	InnerID int64
	MaxFee  amount.Commission
}

// SignedBytes is the node's serialization for signing of a plain transfer (no user fields),
// little endian, 86 bytes: inner id (6), source (32), target (32), amount integral (i32) and
// fraction (u64), max fee (u16), currency (1), number of user fields (0).
func (t Transfer) SignedBytes() ([]byte, error) {
	if len(t.Source) != ed25519.PublicKeySize || len(t.Target) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("source and target must be 32-byte public keys")
	}
	if t.InnerID < 1 || t.InnerID > MaxInnerID {
		return nil, fmt.Errorf("inner id %d out of range", t.InnerID)
	}
	a, err := t.Amount.Thrift()
	if err != nil {
		return nil, err
	}
	b := make([]byte, 0, 86)
	b = binary.LittleEndian.AppendUint64(b, uint64(t.InnerID))[:6]
	b = append(b, t.Source...)
	b = append(b, t.Target...)
	b = binary.LittleEndian.AppendUint32(b, uint32(a.Integral))
	b = binary.LittleEndian.AppendUint64(b, uint64(a.Fraction))
	b = binary.LittleEndian.AppendUint16(b, uint16(t.MaxFee))
	b = append(b, currencyCS)
	b = append(b, 0) // no user fields
	return b, nil
}

// Sign signs the transfer with key and returns the transaction for TransactionFlow and the signed
// bytes. key must be the source's key.
func (t Transfer) Sign(key ed25519.PrivateKey) (*api.Transaction, []byte, error) {
	if !key.Public().(ed25519.PublicKey).Equal(t.Source) {
		return nil, nil, fmt.Errorf("the key is not the source's")
	}
	signed, err := t.SignedBytes()
	if err != nil {
		return nil, nil, err
	}
	a, _ := t.Amount.Thrift()
	tr := api.NewTransaction()
	tr.ID = api.TransactionInnerId(t.InnerID)
	tr.Source = append([]byte(nil), t.Source...)
	tr.Target = append([]byte(nil), t.Target...)
	tr.Amount = a
	tr.Balance = &general.Amount{}
	tr.Currency = currencyCS
	tr.Signature = ed25519.Sign(key, signed)
	tr.Fee = &api.AmountCommission{Commission: int16(t.MaxFee)}
	tr.TimeCreation = api.Time(time.Now().UnixMilli())
	tr.UserFields = []byte{}
	tr.Type = api.TransactionType_TT_Transfer
	return tr, signed, nil
}
