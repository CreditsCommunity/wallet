package wallet

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/CreditsCommunity/wallet/internal/amount"
	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/echo"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
	"github.com/CreditsCommunity/wallet/internal/tx"
	"github.com/CreditsCommunity/wallet/thrift/api"
	"github.com/CreditsCommunity/wallet/thrift/general"
)

// Kind is what a plan sends.
type Kind string

const (
	KindTransfer   Kind = "transfer"
	KindDelegate   Kind = "delegation"
	KindUndelegate Kind = "delegation withdrawal"
	KindToken      Kind = "token transfer"
	KindDeploy     Kind = "contract deploy"
	KindCall       Kind = "contract call"
	KindPay        Kind = "payment to a contract"
)

// MinDelegation is the smallest amount a delegation or a withdrawal may move
// (Consensus::MinStakeDelegated, 5 CS).
var MinDelegation, _ = amount.Parse("5")

// TokenPlan is the token side of a token transfer.
type TokenPlan struct {
	Contract ed25519.PublicKey
	Code     string // symbol, "" if unknown
	Name     string
	Decimals int
	Amount   string // the amount sent, as the contract receives it
	Balance  string // the sender's token balance before (balanceOf)
	Method   string
	Params   []*general.Variant
	// Warnings: the code or name could pass for another asset (CS, or a token held at another
	// address). Code and name are what the contract says of itself: anyone can deploy a token
	// with any code, only the contract address identifies it.
	Warnings []string
}

// confusables maps letters that look like Latin capitals (Cyrillic, Greek, and the like) to them,
// for the letters of "CS" and "CREDITS" and the usual look-alikes; a skeleton in the spirit of
// Unicode TR 39, small and explicit.
var confusables = map[rune]rune{
	'С': 'C', 'с': 'C', 'Ϲ': 'C', 'ϲ': 'C', 'Ⅽ': 'C', 'ⅽ': 'C', 'Ꮯ': 'C',
	'Ѕ': 'S', 'ѕ': 'S', 'Ꮪ': 'S',
	'Е': 'E', 'е': 'E', 'Ε': 'E', 'ε': 'E', 'Ꭼ': 'E',
	'Т': 'T', 'т': 'T', 'Τ': 'T', 'τ': 'T', 'Ꭲ': 'T',
	'І': 'I', 'і': 'I', 'Ι': 'I', 'ι': 'I', 'Ӏ': 'I', 'Ⅰ': 'I', 'ⅰ': 'I', 'ı': 'I', 'l': 'I', '|': 'I', '1': 'I',
	'Р': 'P', 'р': 'P', 'Ρ': 'P', 'ρ': 'P',
	'О': 'O', 'о': 'O', 'Ο': 'O', 'ο': 'O', '0': 'O',
	'А': 'A', 'а': 'A', 'Α': 'A', 'α': 'A',
	'В': 'B', 'в': 'B', 'Β': 'B', 'β': 'B',
	'Н': 'H', 'н': 'H', 'Η': 'H',
	'К': 'K', 'к': 'K', 'Κ': 'K', 'κ': 'K',
	'М': 'M', 'м': 'M', 'Μ': 'M',
	'Х': 'X', 'х': 'X', 'Χ': 'X', 'χ': 'X',
	'Ү': 'Y', 'у': 'Y', 'Υ': 'Y',
	'Ζ': 'Z', 'Ν': 'N', 'ԁ': 'D', 'Ꭰ': 'D', 'Ꭱ': 'R', 'ʀ': 'R', 'Ꮢ': 'R',
}

// Skeleton is what a token code or name looks like, for comparing look-alikes: confusable letters
// mapped to Latin ones, compatibility forms folded (NFKD: fullwidth "ＣＳ" is "CS"), combining marks,
// format and space characters dropped (accents, zero-width joiners, U+202E), upper case.
func Skeleton(s string) string {
	// confusables first (NFKD would turn some of them, such as the lunate sigma, into other
	// letters), then compatibility decomposition, which also separates the combining marks
	s = strings.Map(func(r rune) rune {
		if c, ok := confusables[r]; ok {
			return c
		}
		return r
	}, s)
	var b strings.Builder
	for _, r := range norm.NFKD.String(s) {
		if unicode.Is(unicode.Cf, r) || unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Mn, r) {
			continue
		}
		if c, ok := confusables[r]; ok {
			r = c
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

// plainASCII: only printable ASCII.
func plainASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// TokenWarnings tells when a token's self-declared code or name could be mistaken for another
// asset: CS itself, or another token the account holds under a code that looks the same; and when
// the code or name has characters outside printable ASCII, which can imitate others.
func TokenWarnings(contract ed25519.PublicKey, code, name string, held []node.TokenBalance) []string {
	var out []string
	for _, x := range []string{code, name} {
		if sk := Skeleton(x); sk == "CS" || sk == "CREDITS" || sk == "CREDITSCS" {
			looks := ""
			if strings.ToUpper(strings.TrimSpace(x)) != sk {
				looks = ", which looks like " + sk
			}
			out = append(out, fmt.Sprintf("this token calls itself %s%s: it is NOT CS, only a token of contract %s",
				echo.Printable(x), looks, keys.Address(contract)))
			break
		}
	}
	for _, x := range []string{code, name} {
		if !plainASCII(x) {
			out = append(out, fmt.Sprintf("the token's code or name %s has characters outside plain ASCII, which can imitate others: check the contract address",
				echo.Printable(x)))
			break
		}
	}
	mine := Skeleton(code)
	for _, h := range held {
		if !h.Token.Equal(contract) && mine != "" && Skeleton(h.Code) == mine {
			out = append(out, fmt.Sprintf("another token you hold, %s, has a code that looks the same (%s): check the contract address",
				keys.Address(h.Token), echo.Printable(h.Code)))
		}
	}
	return out
}

// SendPlan is a transaction checked and ready to sign: what the confirmation shows.
type SendPlan struct {
	Kind    Kind
	Account string
	From    ed25519.PublicKey
	// To is the recipient of a transfer or of the tokens, or the node of a delegation.
	To ed25519.PublicKey
	// Amount is the CS moved (0 for a token transfer).
	Amount amount.Amount
	// Data is the transfer's user data ("" for none).
	Data string
	// Until is the expiry of a delegation with a time limit (zero: none).
	Until time.Time
	// Delegated is, for a withdrawal, the delegation without a time limit to that node.
	Delegated amount.Amount
	Token     *TokenPlan
	// Contract is the contract side of a deploy, a call or a payment.
	Contract *ContractPlan

	Fee     amount.Amount // the fee the node charges now (estimate for a token transfer)
	MaxFee  amount.Commission
	Balance amount.Amount
	InnerID int64
	Network config.Network
	// NetworkCheck is what the node said of its network (confirmed or not).
	NetworkCheck NetworkCheck
	Node         string
	Status       node.Status
	Prepared     time.Time
}

// ErrNotSynced is returned when the node is not at the head of the chain.
var ErrNotSynced = errors.New("the node is not synced")

// start loads the sender and checks the node: synced and on the declared network.
func (w *Wallet) start(ctx context.Context, account string, kind Kind) (*SendPlan, error) {
	f, err := w.Store.Load(account)
	if err != nil {
		return nil, err
	}
	from, err := f.PublicKey()
	if err != nil {
		return nil, err
	}
	st, err := w.Node.Status(ctx)
	if err != nil {
		return nil, err
	}
	if !st.Synced {
		return nil, fmt.Errorf("%w: %s; a transaction sent now would be refused or delayed. Wait until 'status' says synced", ErrNotSynced, st.Why)
	}
	check, err := w.CheckNetwork(ctx)
	if err != nil {
		return nil, err
	}
	bal, err := w.Node.Balance(ctx, from)
	if err != nil {
		return nil, err
	}
	return &SendPlan{Kind: kind, Account: f.Name, From: from, MaxFee: tx.MaxFee, Balance: bal.CS, Network: w.Profile.Network,
		NetworkCheck: check, Node: w.Profile.Address, Status: st}, nil
}

// finish reads the inner id: the last step, so that the plan is as fresh as possible.
func (w *Wallet) finish(ctx context.Context, p *SendPlan) (*SendPlan, error) {
	inner, err := w.Node.NextInnerID(ctx, p.From)
	if err != nil {
		return nil, err
	}
	p.InnerID, p.Prepared = inner, time.Now()
	return p, nil
}

func (w *Wallet) target(p *SendPlan, text, what string) error {
	target, err := keys.ParseAddress(text)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if target.Equal(p.From) {
		return fmt.Errorf("the %s is the sending account itself", what)
	}
	p.To = target
	return nil
}

func (w *Wallet) fee(ctx context.Context, p *SendPlan, size int) error {
	fee, err := w.Node.Fee(ctx, size)
	if err != nil {
		return err
	}
	p.Fee = fee.Amount()
	return nil
}

func (p *SendPlan) need(total amount.Amount, what string) error {
	if p.Balance.Cmp(total) < 0 {
		return fmt.Errorf("not enough CS: the balance is %s CS, %s needs %s CS", p.Balance.Grouped(), what, total.Grouped())
	}
	return nil
}

// SendRequest is a CS transfer to check.
type SendRequest struct {
	Account, To, Amount string
	Data                string // user data, optional
}

// PrepareSend checks a transfer before it is confirmed: the node synced and on the declared
// network, the recipient, the amount, the user data, the balance with the fee; and reads the
// inner id.
func (w *Wallet) PrepareSend(ctx context.Context, r SendRequest) (*SendPlan, error) {
	if r.Data != "" {
		if err := tx.CheckData(r.Data); err != nil {
			return nil, err
		}
	}
	amt, err := amount.ParsePositive(r.Amount)
	if err != nil {
		return nil, err
	}
	p, err := w.start(ctx, r.Account, KindTransfer)
	if err != nil {
		return nil, err
	}
	if err := w.target(p, r.To, "recipient"); err != nil {
		return nil, err
	}
	p.Amount, p.Data = amt, r.Data
	if err := w.fee(ctx, p, p.transfer().Size()); err != nil {
		return nil, err
	}
	if err := p.need(amt.Add(p.Fee), "the transfer with the fee"); err != nil {
		return nil, err
	}
	return w.finish(ctx, p)
}

// PrepareDelegate checks a delegation of amount CS to a node, without a time limit (until zero)
// or until a UTC time.
func (w *Wallet) PrepareDelegate(ctx context.Context, account, nodeAddr, amountText string, until time.Time) (*SendPlan, error) {
	amt, err := amount.ParsePositive(amountText)
	if err != nil {
		return nil, err
	}
	if amt.Cmp(MinDelegation) < 0 {
		return nil, fmt.Errorf("a delegation moves at least %s CS", MinDelegation.String())
	}
	if _, err := tx.DelegationValue(until, time.Now().Add(time.Minute)); err != nil {
		return nil, fmt.Errorf("%w (at least a minute ahead)", err)
	}
	p, err := w.start(ctx, account, KindDelegate)
	if err != nil {
		return nil, err
	}
	if err := w.target(p, nodeAddr, "node"); err != nil {
		return nil, err
	}
	p.Amount, p.Until = amt, until
	if err := w.fee(ctx, p, p.transfer().Size()); err != nil {
		return nil, err
	}
	if err := p.need(amt.Add(p.Fee), "the delegation with the fee"); err != nil {
		return nil, err
	}
	return w.finish(ctx, p)
}

// PrepareUndelegate checks the withdrawal of amount CS from the delegation without a time limit to
// a node (a delegation with a time limit ends by itself).
func (w *Wallet) PrepareUndelegate(ctx context.Context, account, nodeAddr, amountText string) (*SendPlan, error) {
	amt, err := amount.ParsePositive(amountText)
	if err != nil {
		return nil, err
	}
	if amt.Cmp(MinDelegation) < 0 {
		return nil, fmt.Errorf("a withdrawal moves at least %s CS", MinDelegation.String())
	}
	p, err := w.start(ctx, account, KindUndelegate)
	if err != nil {
		return nil, err
	}
	if err := w.target(p, nodeAddr, "node"); err != nil {
		return nil, err
	}
	d, err := w.Node.Delegations(ctx, p.From)
	if err != nil {
		return nil, err
	}
	for _, x := range d.Out {
		if x.Wallet.Equal(p.To) && x.Until.IsZero() {
			p.Delegated = p.Delegated.Add(x.Amount)
		}
	}
	if p.Delegated.Sign() == 0 {
		return nil, fmt.Errorf("this account has no delegation without a time limit to %s (one with a time limit ends by itself)", keys.Address(p.To))
	}
	if p.Delegated.Cmp(amt) < 0 {
		return nil, fmt.Errorf("the delegation without a time limit to that node is %s CS, less than %s", p.Delegated.Grouped(), amt.Grouped())
	}
	p.Amount = amt
	if err := w.fee(ctx, p, p.transfer().Size()); err != nil {
		return nil, err
	}
	// the node refuses a withdrawal when the balance is below the signed max fee
	if err := p.need(p.MaxFee.Amount(), "a withdrawal (the signed max fee)"); err != nil {
		return nil, err
	}
	return w.finish(ctx, p)
}

// transfer is the plan as a transfer (all kinds but the token transfer).
func (p *SendPlan) transfer() tx.Transfer {
	t := tx.Transfer{Source: p.From, Target: p.To, Amount: p.Amount, InnerID: p.InnerID, MaxFee: p.MaxFee, Data: p.Data}
	switch p.Kind {
	case KindDelegate:
		t.Delegation = tx.Delegate
		if !p.Until.IsZero() {
			t.Delegation = uint64(p.Until.Unix())
		}
	case KindUndelegate:
		t.Delegation = tx.DelegateWithdraw
	}
	return t
}

// ---- tokens ------------------------------------------------------------------------------------

// TokenMethods finds a token's transfer method and reads its decimals (getDecimal), symbol and
// name, with getters run as caller.
func (w *Wallet) TokenMethods(ctx context.Context, caller, token ed25519.PublicKey) (*TokenPlan, []*general.MethodDescription, error) {
	methods, err := w.Node.ContractMethods(ctx, token)
	if err != nil {
		return nil, nil, err
	}
	tp := &TokenPlan{Contract: token, Decimals: -1}
	has := map[string]bool{}
	for _, m := range methods {
		if len(m.Arguments) == 0 {
			has[m.Name] = true
		}
		if m.Name == "transfer" && len(m.Arguments) == 2 && m.Arguments[0].Type == "java.lang.String" &&
			(m.Arguments[1].Type == "java.math.BigDecimal" || m.Arguments[1].Type == "java.lang.String") {
			tp.Method = m.Name + "(" + m.Arguments[0].Type + "," + m.Arguments[1].Type + ")"
		}
	}
	if tp.Method == "" || !has["getDecimal"] {
		return nil, nil, errors.New("not a token: the contract has no transfer(String, BigDecimal) and getDecimal()")
	}
	dec, err := w.Node.Get(ctx, tx.Getter(caller, token, "getDecimal", nil))
	if err != nil {
		return nil, nil, fmt.Errorf("getDecimal: %w", err)
	}
	if tp.Decimals, err = strconv.Atoi(tx.Show(dec)); err != nil || tp.Decimals < 0 || tp.Decimals > amount.MaxTokenDecimals {
		return nil, nil, fmt.Errorf("getDecimal returned %s: not a number of decimals", echo.Printable(tx.Show(dec)))
	}
	if info, err := w.Node.TokenInfo(ctx, token); err == nil {
		tp.Code, tp.Name = info.Code, info.Name
	} else {
		if has["getSymbol"] {
			if v, err := w.Node.Get(ctx, tx.Getter(caller, token, "getSymbol", nil)); err == nil {
				tp.Code = tx.Show(v)
			}
		}
		if has["getName"] {
			if v, err := w.Node.Get(ctx, tx.Getter(caller, token, "getName", nil)); err == nil {
				tp.Name = tx.Show(v)
			}
		}
	}
	return tp, methods, nil
}

// TokenBalanceOf runs the token's balanceOf(owner) as a getter.
func (w *Wallet) TokenBalanceOf(ctx context.Context, token, owner ed25519.PublicKey) (string, error) {
	param, _ := tx.Variant("java.lang.String", keys.Address(owner))
	v, err := w.Node.Get(ctx, tx.Getter(owner, token, "balanceOf", []*general.Variant{param}))
	if err != nil {
		return "", fmt.Errorf("balanceOf: %w", err)
	}
	return tx.Show(v), nil
}

// PrepareTokenSend checks a token transfer: a signed call of the token's transfer(to, amount),
// the amount with at most the token's decimals and within the token balance; the CS balance must
// cover the signed max fee, which the node holds back for a contract call.
func (w *Wallet) PrepareTokenSend(ctx context.Context, account, tokenText, to, amountText string) (*SendPlan, error) {
	token, err := keys.ParseAddress(tokenText)
	if err != nil {
		return nil, fmt.Errorf("token: %w", err)
	}
	p, err := w.start(ctx, account, KindToken)
	if err != nil {
		return nil, err
	}
	if err := w.target(p, to, "recipient"); err != nil {
		return nil, err
	}
	if p.To.Equal(token) {
		return nil, errors.New("the recipient is the token contract itself")
	}
	tp, _, err := w.TokenMethods(ctx, p.From, token)
	if err != nil {
		return nil, err
	}
	if tp.Amount, err = amount.ParseToken(amountText, tp.Decimals); err != nil {
		return nil, err
	}
	held, _ := w.Node.TokenBalances(ctx, p.From) // without the token cache: no list to compare with
	tp.Warnings = TokenWarnings(token, tp.Code, tp.Name, held)
	if tp.Balance, err = w.TokenBalanceOf(ctx, token, p.From); err != nil {
		return nil, err
	}
	if c, ok := amount.CompareDecimal(tp.Balance, tp.Amount); !ok || c < 0 {
		return nil, fmt.Errorf("not enough tokens: the balance is %s %s, less than %s", echo.Printable(tp.Balance), echo.Printable(tp.Code), tp.Amount)
	}
	argTypes := strings.Split(strings.TrimSuffix(strings.TrimPrefix(tp.Method, "transfer("), ")"), ",")
	pTo, err := tx.Variant(argTypes[0], keys.Address(p.To))
	if err != nil {
		return nil, err
	}
	pAmt, err := tx.Variant(argTypes[1], tp.Amount)
	if err != nil {
		return nil, err
	}
	tp.Method, tp.Params = "transfer", []*general.Variant{pTo, pAmt}
	p.Token = tp
	// the call and the new state it makes each pay the fee of their size, then the execution
	call := tx.ContractCall{Source: p.From, Contract: token, Method: tp.Method, Params: tp.Params, InnerID: 1, MaxFee: p.MaxFee}
	if err := w.fee(ctx, p, call.Size()); err != nil {
		return nil, err
	}
	p.Fee = p.Fee.Add(p.Fee)
	if err := p.need(p.MaxFee.Amount(), "a token transfer (the signed max fee, held while the call runs)"); err != nil {
		return nil, err
	}
	return w.finish(ctx, p)
}

// ---- sending -----------------------------------------------------------------------------------

// SendResult is the outcome of a transaction.
type SendResult struct {
	InnerID int64
	Block   int64
	Fee     amount.Amount
	Round   int32
	// InBlock: the transaction is in a block (the node said so, or the inner id was seen)
	InBlock bool
	// Returned is what a token's transfer method or a contract method returned ("" when unknown):
	// text the contract chose, shown through echo.Printable
	Returned string
	// Result is what a contract method returned (nil when unknown)
	Result *general.Variant
	// Contract is the address of a deployed contract
	Contract ed25519.PublicKey
	// Listed: the node lists the deployed contract (its constructor ran)
	Listed bool
	// SaveErr: the deployed contract could not be added to the account's local contract list
	SaveErr error
}

// sign decrypts the key with the password and signs the plan. The key is zeroed at once.
func (w *Wallet) sign(p *SendPlan, password []byte) (*api.Transaction, error) {
	f, err := w.Store.Load(p.Account)
	if err != nil {
		return nil, err
	}
	key, err := f.Decrypt(password)
	if err != nil {
		return nil, err
	}
	defer keys.Zero(key)
	switch p.Kind {
	case KindDeploy:
		if p.Contract == nil || p.Contract.Deploy == nil {
			return nil, errors.New("no deploy in the plan")
		}
		d := *p.Contract.Deploy
		d.Source, d.InnerID, d.MaxFee = p.From, p.InnerID, p.MaxFee
		if !d.Address().Equal(p.To) {
			return nil, errors.New("the deploy's address does not match the plan")
		}
		t, _, err := d.Sign(key)
		return t, err
	case KindCall:
		if p.Contract == nil || p.Contract.Method == nil {
			return nil, errors.New("no call in the plan")
		}
		t, _, err := tx.ContractCall{Source: p.From, Contract: p.To, Method: p.Contract.Method.Name, Params: p.Contract.Params,
			InnerID: p.InnerID, MaxFee: p.MaxFee}.Sign(key)
		return t, err
	}
	if p.Kind == KindToken {
		if p.Token == nil {
			return nil, errors.New("no token in the plan")
		}
		t, _, err := tx.ContractCall{Source: p.From, Contract: p.Token.Contract, Method: p.Token.Method, Params: p.Token.Params,
			InnerID: p.InnerID, MaxFee: p.MaxFee}.Sign(key)
		return t, err
	}
	t, _, err := p.transfer().Sign(key)
	return t, err
}

// Send decrypts the key with the password, signs the planned transaction, sends it and follows it
// until it is in a block (at most follow).
func (w *Wallet) Send(ctx context.Context, plan *SendPlan, password []byte, follow time.Duration) (SendResult, error) {
	res := SendResult{InnerID: plan.InnerID}
	t, err := w.sign(plan, password)
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
	if sent.Result != nil {
		res.Result, res.Returned = sent.Result, tx.Show(sent.Result)
	}
	wait, cancel := context.WithTimeout(ctx, follow)
	defer cancel()
	if !sent.InProgress {
		res.InBlock = true
		if plan.Kind == KindToken && sent.Result != nil && sent.Result.VBoolean != nil && !*sent.Result.VBoolean {
			return res, errors.New("the token contract's transfer returned false: no tokens were moved (the fee is paid)")
		}
	} else {
		if err := sender.WaitInBlock(wait, plan.From, plan.InnerID, 2*time.Second); err != nil {
			return res, fmt.Errorf("sent, but %w; check 'history' later, and do not send it again before", err)
		}
		res.InBlock = true
	}
	if plan.Kind == KindDeploy {
		w.afterDeploy(wait, sender, plan, &res)
	}
	return res, nil
}

// afterDeploy waits until the node lists the new contract and adds it to the account's local
// contract list (also when the node does not list it yet: the deploy is in a block).
func (w *Wallet) afterDeploy(ctx context.Context, sender *node.Client, plan *SendPlan, res *SendResult) {
	res.Contract = plan.To
	res.Listed = sender.WaitContract(ctx, plan.To, 2*time.Second) == nil
	c := plan.Contract
	res.SaveErr = w.AddLocalContract(plan.From, LocalContract{Address: keys.Address(plan.To), Name: c.Name, File: c.File,
		Network: string(plan.Network), Node: plan.Node, Deployed: time.Now().UTC(), Block: res.Block, InnerID: plan.InnerID,
		TokenStandard: c.Deploy.TokenStandard})
}

// ParseUntil reads the expiry of a delegation: a UTC date ("2027-01-31", midnight UTC), a UTC date
// and time ("2027-01-31 18:00", "2027-01-31T18:00:00") or RFC 3339 with its offset.
func ParseUntil(text string) (time.Time, error) {
	s := strings.TrimSpace(text)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	for _, layout := range []string{"2006-01-02", "2006-01-02 15:04", "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			if t.Year() > 2200 {
				break
			}
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%s: not a date; write 2027-01-31, \"2027-01-31 18:00\" (UTC) or 2027-01-31T18:00:00+01:00", echo.Quote(text))
}
