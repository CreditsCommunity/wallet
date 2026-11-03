package tx

import (
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/apache/thrift/lib/go/thrift"
	"golang.org/x/crypto/blake2s"

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

// contractSigned is the node's serialization for signing of a contract transaction: the head with
// amount 0, then one user field, the serialized invocation as u32 size + bytes.
func contractSigned(innerID int64, source, target ed25519.PublicKey, maxFee amount.Commission, sci *api.SmartContractInvocation) ([]byte, error) {
	code, err := SerializeInvocation(sci)
	if err != nil {
		return nil, err
	}
	b, err := head(innerID, source, target, &general.Amount{}, maxFee)
	if err != nil {
		return nil, err
	}
	b = append(b, 1)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(code)))
	return append(b, code...), nil
}

// contractTransaction signs a contract transaction (deploy or call) with key.
func contractTransaction(key ed25519.PrivateKey, innerID int64, source, target ed25519.PublicKey, maxFee amount.Commission,
	sci *api.SmartContractInvocation, kind api.TransactionType) (*api.Transaction, []byte, error) {
	if !key.Public().(ed25519.PublicKey).Equal(source) {
		return nil, nil, fmt.Errorf("the key is not the source's")
	}
	signed, err := contractSigned(innerID, source, target, maxFee, sci)
	if err != nil {
		return nil, nil, err
	}
	t := api.NewTransaction()
	t.ID = api.TransactionInnerId(innerID)
	t.Source = append([]byte(nil), source...)
	t.Target = append([]byte(nil), target...)
	t.Amount = &general.Amount{}
	t.Balance = &general.Amount{}
	t.Currency = currencyCS
	t.Signature = ed25519.Sign(key, signed)
	t.SmartContract = sci
	t.Fee = &api.AmountCommission{Commission: int16(maxFee)}
	t.TimeCreation = api.Time(time.Now().UnixMilli())
	t.Type = kind
	return t, signed, nil
}

// SignedBytes is the node's serialization for signing of the call.
func (c ContractCall) SignedBytes() ([]byte, *api.SmartContractInvocation, error) {
	if c.Method == "" {
		return nil, nil, fmt.Errorf("no method")
	}
	sci := Invocation(c.Method, c.Params, false)
	b, err := contractSigned(c.InnerID, c.Source, c.Contract, c.MaxFee, sci)
	return b, sci, err
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
	if c.Method == "" {
		return nil, nil, fmt.Errorf("no method")
	}
	return contractTransaction(key, c.InnerID, c.Source, c.Contract, c.MaxFee, Invocation(c.Method, c.Params, false),
		api.TransactionType_TT_ContractCall)
}

// ---- deploy ------------------------------------------------------------------------------------

// ContractAddress is the address of a contract deployed by deployer with a transaction of inner id
// innerID, as the node derives it (SmartContracts::get_valid_smart_address, checked again by every
// node, IterValidator): blake2s-256 of the deployer's public key, the inner id as 6 bytes little
// endian, and the byte code of every class in order.
func ContractAddress(deployer ed25519.PublicKey, innerID int64, classes []*general.ByteCodeObject) ed25519.PublicKey {
	h, _ := blake2s.New256(nil)
	h.Write(deployer)
	h.Write(binary.LittleEndian.AppendUint64(nil, uint64(innerID))[:6])
	for _, c := range classes {
		if c != nil {
			h.Write(c.ByteCode)
		}
	}
	return ed25519.PublicKey(h.Sum(nil))
}

// ContractDeploy is the deployment of a compiled contract (TT_ContractDeploy), as the node's sample
// clients build it ("deploy"): an invocation without a method, carrying the source, the byte code
// the node compiled and the token standard it found; the target is the contract's address.
type ContractDeploy struct {
	Source        ed25519.PublicKey
	Code          string // the Java source
	Classes       []*general.ByteCodeObject
	TokenStandard int32
	InnerID       int64
	MaxFee        amount.Commission
}

// Address is the address the contract gets.
func (d ContractDeploy) Address() ed25519.PublicKey {
	return ContractAddress(d.Source, d.InnerID, d.Classes)
}

// Invocation is the deploy's SmartContractInvocation.
func (d ContractDeploy) Invocation() *api.SmartContractInvocation {
	classes := d.Classes
	if classes == nil {
		classes = []*general.ByteCodeObject{}
	}
	sci := Invocation("", nil, false)
	sci.SmartContractDeploy = &api.SmartContractDeploy{SourceCode: d.Code, ByteCodeObjects: classes, HashState: "",
		TokenStandard: d.TokenStandard, Lang: 0}
	return sci
}

// SignedBytes is the node's serialization for signing of the deploy.
func (d ContractDeploy) SignedBytes() ([]byte, error) {
	if len(d.Classes) == 0 || d.Code == "" {
		return nil, fmt.Errorf("a deploy needs the source and its compiled byte code")
	}
	return contractSigned(d.InnerID, d.Source, d.Address(), d.MaxFee, d.Invocation())
}

// Size is the size the node prices the deploy by (cs::fee::getFee: the transaction's serialization),
// estimated as for a call.
func (d ContractDeploy) Size() int {
	code, err := SerializeInvocation(d.Invocation())
	if err != nil {
		return 86
	}
	return 86 + 4 + 1 + 4 + len(code)
}

// Sign signs the deploy with key (the deployer's).
func (d ContractDeploy) Sign(key ed25519.PrivateKey) (*api.Transaction, []byte, error) {
	if len(d.Classes) == 0 || d.Code == "" {
		return nil, nil, fmt.Errorf("a deploy needs the source and its compiled byte code")
	}
	return contractTransaction(key, d.InnerID, d.Source, d.Address(), d.MaxFee, d.Invocation(), api.TransactionType_TT_ContractDeploy)
}

// MaxDeploySize is the largest deploy the wallet sends: below 50 KiB a deploy costs at most
// 0.4375 CS; from 50 KiB the node charges 17.87 CS (cs::fee feeLevels), from 100 KiB it refuses.
const MaxDeploySize = 50 * 1024

// deployLevels is the deploy column of the node's fee table (csnode/src/fee.cpp feeLevels): size
// below which a deploy pays the fee.
var deployLevels = []struct {
	below int
	fee   float64
}{{5 * 1024, 0.087402}, {20 * 1024, 0.174805}, {50 * 1024, 0.437500}, {100 * 1024, 17.87109}}

// stateFee is the cheapest new state (fee.cpp minContractStateFee), which a deploy and a call pay
// on top of their own fee.
const stateFee = 0.008740

// DeployFee is what the node charges for a deploy of size bytes before the execution of its
// constructor: the deploy fee of its size (ActualFeeGet knows only the column of the other
// transactions) plus the cheapest new state. The size is an estimate: a margin of 64 bytes picks the
// higher level near a boundary.
func DeployFee(size int) amount.Amount {
	for _, l := range deployLevels {
		if size+64 < l.below {
			return amount.EncodeCommission(l.fee).Amount().Add(amount.EncodeCommission(stateFee).Amount())
		}
	}
	return amount.EncodeCommission(deployLevels[len(deployLevels)-1].fee).Amount()
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
