package wallet

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/CreditsCommunity/wallet/internal/amount"
	"github.com/CreditsCommunity/wallet/internal/echo"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
	"github.com/CreditsCommunity/wallet/internal/tx"
	"github.com/CreditsCommunity/wallet/thrift/general"
)

// Smart contracts (spec 5.5): compile through the node, deploy, call and run getters. A deploy and
// a call are signed transactions with the same confirmation as a transfer; a getter runs on the
// node's executor against the current state and is forgotten: nothing signed, sent or paid.
//
// Names of classes and methods, results, compile errors and state variables come from the contract
// or the executor: they are shown through echo.Printable (node.Signature already escapes).

// ContractPlan is the contract side of a deploy, a call or a payment.
type ContractPlan struct {
	// Address is the contract; for a deploy, the address it gets (derived from the inner id).
	Address ed25519.PublicKey
	// Name is the contract's class name, as the contract calls itself.
	Name string
	// File is the source file of a deploy ("" when unknown).
	File string
	// Deploy is the deployment: the source and the byte code the node compiled.
	Deploy *tx.ContractDeploy
	// Methods is the number of public methods of a deploy (-1: unknown).
	Methods int
	// Method is the method a call runs, Params its arguments converted to the declared types.
	Method *general.MethodDescription
	Params []*general.Variant
	Args   []string
	// Getter: the method is annotated @Getter (it is still called as a transaction when asked).
	Getter bool
}

// ResolveMethod finds the method a call means: by name and number of arguments, as the node's
// sample clients do with overloads. "payable" is never called: a CS transfer to the contract runs it.
func ResolveMethod(methods []*general.MethodDescription, name string, nargs int) (*general.MethodDescription, error) {
	var same []*general.MethodDescription
	for _, m := range methods {
		if m != nil && m.Name == name {
			same = append(same, m)
		}
	}
	for _, m := range same {
		if len(m.Arguments) == nargs {
			return m, nil
		}
	}
	if len(same) == 0 {
		return nil, fmt.Errorf("the contract has no method %s ('contract methods' lists them)", echo.Quote(name))
	}
	known := make([]string, 0, len(same))
	for _, m := range same {
		known = append(known, node.Signature(m))
	}
	return nil, fmt.Errorf("no method %s with %d arguments; there is %s", echo.Quote(name), nargs, strings.Join(known, ", "))
}

// TypedParams converts the arguments, as typed, to the Variants of the types the method declares
// (the executor refuses others).
func TypedParams(m *general.MethodDescription, args []string) ([]*general.Variant, error) {
	if len(args) != len(m.Arguments) {
		return nil, fmt.Errorf("%s takes %d arguments, got %d", node.Signature(m), len(m.Arguments), len(args))
	}
	params := make([]*general.Variant, 0, len(args))
	for i, a := range m.Arguments {
		if a == nil {
			return nil, errors.New("the node described an argument without its type")
		}
		v, err := tx.Variant(a.Type, args[i])
		if err != nil {
			return nil, fmt.Errorf("argument %d (%s): %w", i+1, echo.Printable(node.SimpleType(a.Type)+" "+a.Name), err)
		}
		params = append(params, v)
	}
	return params, nil
}

// HasPayable tells whether the methods include payable(BigDecimal, byte[]) (contract API v1 and
// later) or payable(String, String) (the older one): what a CS transfer to the contract runs.
func HasPayable(methods []*general.MethodDescription) bool {
	for _, m := range methods {
		if m != nil && m.Name == "payable" && len(m.Arguments) == 2 {
			return true
		}
	}
	return false
}

func parseContract(text string) (ed25519.PublicKey, error) {
	c, err := keys.ParseAddress(text)
	if err != nil {
		return nil, fmt.Errorf("contract: %w", err)
	}
	return c, nil
}

// ---- compile and deploy ------------------------------------------------------------------------

// MaxSource is the largest source the wallet sends to compile (a deploy must stay below
// tx.MaxDeploySize with its byte code).
const MaxSource = 48 * 1024

// CheckSource refuses a source the wallet does not send: empty, too large, not UTF-8.
func CheckSource(source string) error {
	switch {
	case strings.TrimSpace(source) == "":
		return errors.New("the source is empty")
	case len(source) > MaxSource:
		return fmt.Errorf("the source has %d bytes, at most %d (a deploy of 50 KiB or more costs 17.87 CS)", len(source), MaxSource)
	case !utf8.ValidString(source):
		return errors.New("the source is not UTF-8 text")
	}
	return nil
}

// Compilation is a source compiled by the node, with its methods.
type Compilation struct {
	node.Compiled
	Methods    []*general.MethodDescription
	MethodsErr error
}

// Name is the contract's class name.
func (c *Compilation) Name() string {
	if len(c.Classes) == 0 || c.Classes[0] == nil {
		return ""
	}
	return c.Classes[0].Name
}

// Compile compiles a source through the node and lists its methods. A source with errors returns a
// *node.CompileFailure.
func (w *Wallet) Compile(ctx context.Context, source string) (*Compilation, error) {
	if err := CheckSource(source); err != nil {
		return nil, err
	}
	compiled, err := w.Node.Compile(ctx, source)
	if err != nil {
		return nil, err
	}
	c := &Compilation{Compiled: compiled}
	c.Methods, c.MethodsErr = w.Node.MethodsOf(ctx, compiled.Classes)
	return c, nil
}

// PrepareDeploy checks a deploy: the source compiled by the node, its size and fee, the balance
// with the signed max fee (held while the constructor runs); then the inner id and, from it, the
// contract's address.
func (w *Wallet) PrepareDeploy(ctx context.Context, account, file, source string) (*SendPlan, error) {
	if err := CheckSource(source); err != nil {
		return nil, err
	}
	p, err := w.start(ctx, account, KindDeploy)
	if err != nil {
		return nil, err
	}
	c, err := w.Compile(ctx, source)
	if err != nil {
		return nil, err
	}
	d := tx.ContractDeploy{Source: p.From, Code: source, Classes: c.Classes, TokenStandard: c.TokenStandard, InnerID: 1, MaxFee: p.MaxFee}
	size := d.Size()
	if size+64 >= tx.MaxDeploySize {
		return nil, fmt.Errorf("the deploy has about %d bytes (source and byte code): from 50 KiB the node charges 17.87 CS; the wallet does not send it", size)
	}
	p.Fee = tx.DeployFee(size)
	if err := p.need(p.MaxFee.Amount(), "a deploy (the signed max fee, held while the constructor runs)"); err != nil {
		return nil, err
	}
	methods := -1
	if c.MethodsErr == nil {
		methods = len(c.Methods)
	}
	if p, err = w.finish(ctx, p); err != nil {
		return nil, err
	}
	d.InnerID = p.InnerID
	p.To = d.Address()
	p.Contract = &ContractPlan{Address: p.To, Name: c.Name(), File: file, Deploy: &d, Methods: methods}
	return p, nil
}

// ---- calls -------------------------------------------------------------------------------------

// PrepareCall checks a signed call of a contract's method: the method by name and number of
// arguments, the arguments converted to the declared types, the fee (twice ActualFeeGet of the
// call's size, plus the execution charged later), the balance with the signed max fee.
func (w *Wallet) PrepareCall(ctx context.Context, account, contractText, method string, args []string) (*SendPlan, error) {
	contract, err := parseContract(contractText)
	if err != nil {
		return nil, err
	}
	if method == "payable" {
		return nil, errors.New("payable() runs when CS are sent to the contract: use 'contract pay ACCOUNT CONTRACT AMOUNT'")
	}
	p, err := w.start(ctx, account, KindCall)
	if err != nil {
		return nil, err
	}
	if contract.Equal(p.From) {
		return nil, errors.New("the contract is the sending account itself")
	}
	p.To = contract
	methods, err := w.Node.ContractMethods(ctx, contract)
	if err != nil {
		return nil, err
	}
	m, err := ResolveMethod(methods, method, len(args))
	if err != nil {
		return nil, err
	}
	params, err := TypedParams(m, args)
	if err != nil {
		return nil, err
	}
	cp := &ContractPlan{Address: contract, Method: m, Params: params, Args: append([]string(nil), args...), Getter: node.IsGetter(m), Methods: len(methods)}
	if info, err := w.Node.Contract(ctx, contract); err == nil {
		cp.Name = info.Name()
	}
	p.Contract = cp
	call := tx.ContractCall{Source: p.From, Contract: contract, Method: m.Name, Params: params, InnerID: 1, MaxFee: p.MaxFee}
	if err := w.fee(ctx, p, call.Size()); err != nil {
		return nil, err
	}
	p.Fee = p.Fee.Add(p.Fee)
	if err := p.need(p.MaxFee.Amount(), "a contract call (the signed max fee, held while the call runs)"); err != nil {
		return nil, err
	}
	return w.finish(ctx, p)
}

// PreparePay checks a payment to a contract: a CS transfer to the contract's address, which makes
// the node run the contract's payable(amount, userData) (the user data, a text, is userData) and
// answer once it ran; a payable() that throws refuses the payment and the node gives the CS back
// (the fee is paid). This is how the node's samples pay a contract (samples/contracts README).
func (w *Wallet) PreparePay(ctx context.Context, account, contractText, amountText, data string) (*SendPlan, error) {
	contract, err := parseContract(contractText)
	if err != nil {
		return nil, err
	}
	if data != "" {
		if err := tx.CheckData(data); err != nil {
			return nil, err
		}
	}
	amt, err := amount.ParsePositive(amountText)
	if err != nil {
		return nil, err
	}
	p, err := w.start(ctx, account, KindPay)
	if err != nil {
		return nil, err
	}
	if contract.Equal(p.From) {
		return nil, errors.New("the contract is the sending account itself")
	}
	p.To, p.Amount, p.Data = contract, amt, data
	methods, err := w.Node.ContractMethods(ctx, contract)
	if errors.Is(err, node.ErrNotAContract) {
		return nil, errors.New("no contract is deployed at this address: use 'send' for a plain transfer")
	}
	if err != nil {
		return nil, err
	}
	if !HasPayable(methods) {
		return nil, errors.New("the contract has no payable(): it cannot take CS")
	}
	cp := &ContractPlan{Address: contract, Methods: len(methods)}
	if info, err := w.Node.Contract(ctx, contract); err == nil {
		cp.Name = info.Name()
	}
	p.Contract = cp
	// the transfer and the new state of payable() each pay the fee of their size, then the execution
	if err := w.fee(ctx, p, p.transfer().Size()); err != nil {
		return nil, err
	}
	p.Fee = p.Fee.Add(p.Fee)
	if err := p.need(amt.Add(p.MaxFee.Amount()), "the payment with the signed max fee (held while payable() runs)"); err != nil {
		return nil, err
	}
	return w.finish(ctx, p)
}

// ---- getters -----------------------------------------------------------------------------------

// GetResult is a method run as a getter.
type GetResult struct {
	Contract ed25519.PublicKey
	Method   *general.MethodDescription
	Value    *general.Variant
}

// Get runs a method as a getter (TransactionFlow with forgetNewState): free, signed by nobody, and
// nothing changes. caller (may be nil: a zero address) is who the contract sees as the initiator.
func (w *Wallet) Get(ctx context.Context, caller ed25519.PublicKey, contractText, method string, args []string) (*GetResult, error) {
	contract, err := parseContract(contractText)
	if err != nil {
		return nil, err
	}
	if method == "payable" {
		return nil, errors.New("payable() runs only when CS are sent to the contract ('contract pay')")
	}
	methods, err := w.Node.ContractMethods(ctx, contract)
	if err != nil {
		return nil, err
	}
	m, err := ResolveMethod(methods, method, len(args))
	if err != nil {
		return nil, err
	}
	params, err := TypedParams(m, args)
	if err != nil {
		return nil, err
	}
	v, err := w.Node.Get(ctx, tx.Getter(caller, contract, m.Name, params))
	if err != nil {
		return nil, err
	}
	return &GetResult{Contract: contract, Method: m, Value: v}, nil
}
