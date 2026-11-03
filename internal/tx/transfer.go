// Package tx builds and signs transactions.
//
// SECURITY REVIEW: the signed bytes are the node's csdb::Transaction::to_byte_stream_for_sig(),
// byte-identical to the node's sample clients and ci/testnet/fund.py (tested against the
// samples' sign-expected.txt and against vectors made by fund.py's code, testdata/sign-vectors.py).
package tx

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/CreditsCommunity/wallet/internal/amount"
	"github.com/CreditsCommunity/wallet/thrift/api"
	"github.com/CreditsCommunity/wallet/thrift/general"
)

const currencyCS = 1

// MaxFee is the most a sender accepts to pay for a transfer, as in the node's samples and
// ci/testnet/fund.py: "1 CS", encoded 0x4c66 (0.99609375 CS). The node charges the actual fee
// (about 0.0087 CS on mainnet) and returns it.
var MaxFee = amount.EncodeCommission(1.0)

// MaxInnerID is the largest inner id: the signed field has 6 bytes, but the node keeps 46 bits
// of the API's id (bits 46 and 47 are flags of the wallet-id encoding in the signed bytes).
const MaxInnerID = 1<<46 - 1

// User fields the wallet sends (the node's cs::trx_uf). Only one per transaction.
const (
	ufText      = 1 // cs::trx_uf::ordinary::Text, a string
	ufDelegated = 5 // cs::trx_uf::sp::delegated, an integer

	ufTypeInteger = 1 // csdb::UserField::Integer
)

// Values of the delegated user field (cs::trx_uf::sp::de).
const (
	// Delegate delegates the amount to the target without a time limit.
	Delegate uint64 = 1
	// DelegateWithdraw takes back (part of) a delegation without a time limit.
	DelegateWithdraw uint64 = 2
	// DelegateMinUTC is the smallest value read as the UTC expiry time (unix seconds) of a
	// delegation with a time limit; the nodes release it once a block is past that time.
	DelegateMinUTC uint64 = 4
)

// MaxData is the longest user data (text) the wallet puts on a transfer, in bytes. Every
// transaction below 5 KiB pays the smallest fee (cs::fee); the node refuses 100 KiB and more.
const MaxData = 4096

// Transfer is a CS transfer before signing. It carries at most one user field: Data (a text) or
// Delegation (the delegated field).
type Transfer struct {
	Source  ed25519.PublicKey
	Target  ed25519.PublicKey
	Amount  amount.Amount
	InnerID int64
	MaxFee  amount.Commission
	// Data is the text of the transfer's user data (user field 1), empty for none.
	Data string
	// Delegation is the value of the delegated user field (5): Delegate, DelegateWithdraw or an
	// expiry time >= DelegateMinUTC; 0 for none.
	Delegation uint64
}

// CheckData refuses user data the wallet does not send: empty, more than MaxData bytes, not UTF-8,
// or with control characters other than newline and tab. The node reads the API's userFields as a
// text when its first byte is not 0 (APIHandler::checkTransaction), which a valid text guarantees.
func CheckData(text string) error {
	switch {
	case text == "":
		return errors.New("the user data is empty")
	case len(text) > MaxData:
		return fmt.Errorf("the user data has %d bytes, at most %d", len(text), MaxData)
	case !utf8.ValidString(text):
		return errors.New("the user data is not valid UTF-8 text")
	}
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return fmt.Errorf("the user data has a control character (U+%04X)", r)
		}
	}
	return nil
}

// userFields returns the user fields as signed (count, then each value: an integer as u64, a
// string as u32 size + bytes; csdb's obstream::put_smart) and as the API carries them
// (Transaction.userFields: a text as it is; other fields as flag 0, count, then id u32, type u8,
// value, read by APIHandler::checkTransaction).
func (t Transfer) userFields() (signed, wire []byte, err error) {
	switch {
	case t.Data != "" && t.Delegation != 0:
		return nil, nil, errors.New("a transfer carries user data or a delegation, not both")
	case t.Data != "":
		if err := CheckData(t.Data); err != nil {
			return nil, nil, err
		}
		signed = []byte{1}
		signed = binary.LittleEndian.AppendUint32(signed, uint32(len(t.Data)))
		signed = append(signed, t.Data...)
		return signed, []byte(t.Data), nil
	case t.Delegation != 0:
		if t.Delegation == 3 {
			return nil, nil, errors.New("delegated value 3 (a release) is the network's, not a sender's")
		}
		signed = binary.LittleEndian.AppendUint64([]byte{1}, t.Delegation)
		wire = []byte{0, 1}
		wire = binary.LittleEndian.AppendUint32(wire, ufDelegated)
		wire = append(wire, ufTypeInteger)
		wire = binary.LittleEndian.AppendUint64(wire, t.Delegation)
		return signed, wire, nil
	}
	return []byte{0}, []byte{}, nil
}

// head is the part of the signed bytes every transaction of a public-key source and target
// shares: inner id (6), source (32), target (32), amount integral (i32) and fraction (u64), max
// fee (u16), currency (1), little endian.
func head(innerID int64, source, target ed25519.PublicKey, a *general.Amount, maxFee amount.Commission) ([]byte, error) {
	if len(source) != ed25519.PublicKeySize || len(target) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("source and target must be 32-byte public keys")
	}
	if innerID < 1 || innerID > MaxInnerID {
		return nil, fmt.Errorf("inner id %d out of range", innerID)
	}
	b := make([]byte, 0, 128)
	b = binary.LittleEndian.AppendUint64(b, uint64(innerID))[:6]
	b = append(b, source...)
	b = append(b, target...)
	b = binary.LittleEndian.AppendUint32(b, uint32(a.Integral))
	b = binary.LittleEndian.AppendUint64(b, uint64(a.Fraction))
	b = binary.LittleEndian.AppendUint16(b, uint16(maxFee))
	b = append(b, currencyCS)
	return b, nil
}

// SignedBytes is the node's serialization for signing of the transfer: 86 bytes for a plain
// transfer (the head and 0 user fields), more with a user field.
func (t Transfer) SignedBytes() ([]byte, error) {
	b, _, err := t.signedAndWire()
	return b, err
}

func (t Transfer) signedAndWire() ([]byte, []byte, error) {
	a, err := t.Amount.Thrift()
	if err != nil {
		return nil, nil, err
	}
	if t.Amount.Sign() < 0 {
		return nil, nil, errors.New("negative amount")
	}
	b, err := head(t.InnerID, t.Source, t.Target, a, t.MaxFee)
	if err != nil {
		return nil, nil, err
	}
	fields, wire, err := t.userFields()
	if err != nil {
		return nil, nil, err
	}
	return append(b, fields...), wire, nil
}

// Size is the size the node prices the transaction by (cs::fee::getFee: its serialization with the
// user fields' ids and types), close enough for ActualFeeGet: every transfer the wallet makes is
// far below the 5 KiB of the smallest fee.
func (t Transfer) Size() int {
	switch {
	case t.Data != "":
		return 86 + 4 + 1 + 4 + len(t.Data)
	case t.Delegation != 0:
		return 86 + 4 + 1 + 8
	}
	return 86
}

// Sign signs the transfer with key and returns the transaction for TransactionFlow and the signed
// bytes. key must be the source's key.
func (t Transfer) Sign(key ed25519.PrivateKey) (*api.Transaction, []byte, error) {
	if !key.Public().(ed25519.PublicKey).Equal(t.Source) {
		return nil, nil, fmt.Errorf("the key is not the source's")
	}
	signed, wire, err := t.signedAndWire()
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
	tr.UserFields = wire
	tr.Type = api.TransactionType_TT_Transfer
	return tr, signed, nil
}

// DelegationValue is the delegated field's value for a delegation until (UTC unix seconds), or
// without a time limit when until is zero. A time limit must be in the future.
func DelegationValue(until time.Time, now time.Time) (uint64, error) {
	if until.IsZero() {
		return Delegate, nil
	}
	if !until.After(now) {
		return 0, fmt.Errorf("the expiry %s is not in the future", until.UTC().Format("2006-01-02 15:04:05 UTC"))
	}
	v := until.Unix()
	if v < int64(DelegateMinUTC) {
		return 0, errors.New("expiry out of range")
	}
	return uint64(v), nil
}
