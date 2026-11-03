package tx

import (
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/apache/thrift/lib/go/thrift"

	"github.com/CreditsCommunity/wallet/internal/amount"
	"github.com/CreditsCommunity/wallet/thrift/api"
	"github.com/CreditsCommunity/wallet/thrift/general"
)

// Contract calls, as the node's sample clients build them (samples/clients/go, "call"): the
// invocation travels as the transaction's one user field (cs::trx_uf::deploy::Code), its Thrift
// binary serialization, which the signature covers. Every field the IDL does not mark optional is
// set, so these are the bytes the node serializes again to check the signature.

// Invocation builds a SmartContractInvocation of a method (no deploy).
func Invocation(method string, params []*general.Variant, getter bool) *api.SmartContractInvocation {
	if params == nil {
		params = []*general.Variant{}
	}
	sci := api.NewSmartContractInvocation()
	sci.Method = method
	sci.Params = params
	sci.UsedContracts = []general.Address{}
	sci.ForgetNewState = getter
	sci.Version = 1
	return sci
}

// SerializeInvocation is the invocation's Thrift binary serialization (the user field's value).
func SerializeInvocation(sci *api.SmartContractInvocation) ([]byte, error) {
	return thrift.NewTSerializer().Write(context.Background(), sci)
}

// ContractCall is a signed call of a contract's method, without CS (amount 0).
type ContractCall struct {
	Source   ed25519.PublicKey
	Contract ed25519.PublicKey
	Method   string
	Params   []*general.Variant
	InnerID  int64
	MaxFee   amount.Commission
}

// SignedBytes is the node's serialization for signing of the call: the head with amount 0, then
// one user field, the serialized invocation as u32 size + bytes.
func (c ContractCall) SignedBytes() ([]byte, *api.SmartContractInvocation, error) {
	if c.Method == "" {
		return nil, nil, fmt.Errorf("no method")
	}
	sci := Invocation(c.Method, c.Params, false)
	code, err := SerializeInvocation(sci)
	if err != nil {
		return nil, nil, err
	}
	b, err := head(c.InnerID, c.Source, c.Contract, &general.Amount{}, c.MaxFee)
	if err != nil {
		return nil, nil, err
	}
	b = append(b, 1)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(code)))
	return append(b, code...), sci, nil
}

// Size is the size the node prices the call by, close enough for ActualFeeGet.
func (c ContractCall) Size() int {
	sci := Invocation(c.Method, c.Params, false)
	code, err := SerializeInvocation(sci)
	if err != nil {
		return 86
	}
	return 86 + 4 + 1 + 4 + len(code)
}

// Sign signs the call with key (the source's) and returns the transaction for TransactionFlow and
// the signed bytes.
func (c ContractCall) Sign(key ed25519.PrivateKey) (*api.Transaction, []byte, error) {
	if !key.Public().(ed25519.PublicKey).Equal(c.Source) {
		return nil, nil, fmt.Errorf("the key is not the source's")
	}
	signed, sci, err := c.SignedBytes()
	if err != nil {
		return nil, nil, err
	}
	t := api.NewTransaction()
	t.ID = api.TransactionInnerId(c.InnerID)
	t.Source = append([]byte(nil), c.Source...)
	t.Target = append([]byte(nil), c.Contract...)
	t.Amount = &general.Amount{}
	t.Balance = &general.Amount{}
	t.Currency = currencyCS
	t.Signature = ed25519.Sign(key, signed)
	t.SmartContract = sci
	t.Fee = &api.AmountCommission{Commission: int16(c.MaxFee)}
	t.TimeCreation = api.Time(time.Now().UnixMilli())
	t.Type = api.TransactionType_TT_ContractCall
	return t, signed, nil
}

// Getter is a method run as a getter: TransactionFlow with forgetNewState, which the node runs on
// its own executor against the current state and then forgets. Nothing is signed (a zero
// signature), sent or paid; the source is only who the contract sees as the caller.
func Getter(caller, contract ed25519.PublicKey, method string, params []*general.Variant) *api.Transaction {
	if len(caller) != ed25519.PublicKeySize {
		caller = make([]byte, ed25519.PublicKeySize)
	}
	t := api.NewTransaction()
	t.Source = append([]byte(nil), caller...)
	t.Target = append([]byte(nil), contract...)
	t.Amount = &general.Amount{}
	t.Balance = &general.Amount{}
	t.Currency = currencyCS
	t.Signature = make([]byte, ed25519.SignatureSize)
	t.SmartContract = Invocation(method, params, true)
	t.Fee = &api.AmountCommission{Commission: int16(MaxFee)}
	t.TimeCreation = api.Time(time.Now().UnixMilli())
	t.Type = api.TransactionType_TT_ContractCall
	return t
}
