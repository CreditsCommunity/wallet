package node

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/apache/thrift/lib/go/thrift"

	"github.com/CreditsCommunity/wallet/internal/echo"
	"github.com/CreditsCommunity/wallet/thrift/api"
	"github.com/CreditsCommunity/wallet/thrift/general"
)

// Everything in this file that comes from a contract or the executor (class names, method names,
// compile errors, state variables) is text others chose: callers show it through echo.Printable.

// ---- compile -----------------------------------------------------------------------------------

// Compiled is the node's compilation of a contract source (its executor compiles it).
type Compiled struct {
	Classes       []*general.ByteCodeObject
	TokenStandard int32
}

// CompileError is one error of the Java compiler: the executor reports the line (no column) and
// the message; Column is 0 when the message has none.
type CompileError struct {
	Line   int
	Column int
	Text   string
}

// CompileFailure is a source the node refused to compile.
type CompileFailure struct {
	Errors []CompileError
	// Message is the node's whole message (the executor's stack trace), escaped, cut to a few lines
	// when Errors were found in it.
	Message string
}

func (c *CompileFailure) Error() string {
	if len(c.Errors) == 0 {
		return "the node did not compile the source: " + c.Message
	}
	return fmt.Sprintf("the source does not compile: %d errors, the first on line %d: %s", len(c.Errors), c.Errors[0].Line,
		echo.Printable(c.Errors[0].Text))
}

// ExecutorCompileBug is what the node answers when its contract executor fails on a source that
// does not compile: the executor builds its answer without the byte code list, then reads that
// list's size for its own log (ContractExecutorHandler.compileSourceCode), and Thrift turns the
// NullPointerException into this message. The compile errors are lost on the way: the executor
// releases so far (community.16, executor-community-2-rc1) have the bug.
const ExecutorCompileBug = "Internal error processing compileSourceCode"

// ExecutorCompileBugText is what the wallet says instead.
const ExecutorCompileBugText = "the source does not compile, but the node's contract executor failed instead of listing the errors " +
	"(an executor bug: it loses them); compile it with javac and the contract API (com.credits.scapi) to see them"

// "12:';' expected" (the executor's CompilationException: line, colon, message), and
// "12:5: message" when a column is there.
var compileErrorLine = regexp.MustCompile(`^\s*(\d+):(?:(\d+):)?\s?(.*)$`)

// ParseCompileErrors reads the executor's compile errors out of the node's message: the executor
// sends the root cause's stack trace, whose first line is "...CompilationException: compilation
// errors in class X :", then one "LINE:message" per error, then the "\tat ..." frames.
func ParseCompileErrors(message string) []CompileError {
	var out []CompileError
	inErrors := false
	for _, line := range strings.Split(message, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.Contains(line, "compilation errors in class") {
			inErrors = true
			continue
		}
		if !inErrors {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "at ") || strings.HasPrefix(line, "\t") {
			break
		}
		m := compileErrorLine.FindStringSubmatch(line)
		if m == nil {
			if len(out) > 0 && strings.TrimSpace(line) != "" {
				// a message on several lines (the compiler's caret lines and the like)
				out[len(out)-1].Text += "\n" + line
			}
			continue
		}
		n, _ := strconv.Atoi(m[1])
		col, _ := strconv.Atoi(m[2])
		out = append(out, CompileError{Line: n, Column: col, Text: m[3]})
		if len(out) == 200 {
			break
		}
	}
	return out
}

// Compile asks the node to compile a contract's Java source (SmartContractCompile). A source the
// executor refuses returns a *CompileFailure.
func (c *Client) Compile(ctx context.Context, source string) (Compiled, error) {
	var out Compiled
	err := c.do(func(a *api.APIClient) error {
		r, err := a.SmartContractCompile(ctx, source)
		if err != nil {
			return err
		}
		if r.Status == nil {
			return &APIError{Call: "SmartContractCompile", Code: -1, Message: "no status in the answer"}
		}
		if r.Status.Code != codeSuccess {
			if strings.Contains(r.Status.Message, ExecutorCompileBug) {
				return &CompileFailure{Message: ExecutorCompileBugText}
			}
			f := &CompileFailure{Errors: ParseCompileErrors(r.Status.Message)}
			msg := r.Status.Message
			if len(f.Errors) > 0 {
				if i := strings.Index(msg, "\n\tat "); i > 0 {
					msg = msg[:i]
				}
			}
			f.Message = echo.Printable(msg)
			return f
		}
		if len(r.ByteCodeObjects) == 0 {
			// the node answers nothing at all for a source naming a library it does not allow
			return &CompileFailure{Message: "no byte code in the answer: the source may use a Java library the node does not allow"}
		}
		out = Compiled{Classes: r.ByteCodeObjects, TokenStandard: r.TokenStandard}
		return nil
	})
	return out, err
}

// MethodsOf lists the public methods of compiled byte code (ContractAllMethodsGet).
func (c *Client) MethodsOf(ctx context.Context, classes []*general.ByteCodeObject) ([]*general.MethodDescription, error) {
	var methods []*general.MethodDescription
	err := c.do(func(a *api.APIClient) error {
		r, err := a.ContractAllMethodsGet(ctx, classes)
		if err != nil {
			return err
		}
		if r.Code != 0 {
			return fmt.Errorf("ContractAllMethodsGet: %s", echo.Printable(r.Message))
		}
		methods = r.Methods
		return nil
	})
	return methods, err
}

// ---- deployed contracts ------------------------------------------------------------------------

// ContractInfo is what the node knows of a deployed contract.
type ContractInfo struct {
	Address       ed25519.PublicKey
	Deployer      ed25519.PublicKey
	Classes       []string // class names, the first is the contract's
	TokenStandard int32
	Created       time.Time
	Transactions  int32
	SourceSize    int
	ByteCode      []*general.ByteCodeObject
}

// Name is the contract's class name ("" if unknown).
func (ci ContractInfo) Name() string {
	if len(ci.Classes) == 0 {
		return ""
	}
	return ci.Classes[0]
}

func contractInfo(sc *api.SmartContract) (ContractInfo, bool) {
	if sc == nil || len(sc.Address) != ed25519.PublicKeySize {
		return ContractInfo{}, false
	}
	ci := ContractInfo{Address: ed25519.PublicKey(sc.Address), Transactions: sc.TransactionsCount}
	if len(sc.Deployer) == ed25519.PublicKeySize {
		ci.Deployer = ed25519.PublicKey(sc.Deployer)
	}
	if sc.CreateTime > 0 {
		ci.Created = time.UnixMilli(int64(sc.CreateTime))
	}
	if d := sc.SmartContractDeploy; d != nil {
		ci.TokenStandard, ci.SourceSize, ci.ByteCode = d.TokenStandard, len(d.SourceCode), d.ByteCodeObjects
		for _, b := range d.ByteCodeObjects {
			if b != nil {
				ci.Classes = append(ci.Classes, b.Name)
			}
		}
	}
	return ci, true
}

// Contract reads a deployed contract (SmartContractGet); ErrNotAContract when there is none.
func (c *Client) Contract(ctx context.Context, address ed25519.PublicKey) (ContractInfo, error) {
	var ci ContractInfo
	err := c.do(func(a *api.APIClient) error {
		r, err := a.SmartContractGet(ctx, general.Address(address))
		if err != nil {
			return err
		}
		if r.Status == nil || r.Status.Code != codeSuccess || r.SmartContract == nil || r.SmartContract.SmartContractDeploy == nil {
			return ErrNotAContract
		}
		var ok bool
		if ci, ok = contractInfo(r.SmartContract); !ok {
			return ErrNotAContract
		}
		if !ci.Address.Equal(address) {
			return fmt.Errorf("SmartContractGet answered about another address")
		}
		return nil
	})
	return ci, err
}

// MaxListed is the most contracts of one deployer the wallet reads (SmartContractsListGet pages).
const MaxListed = 1000

// ContractsBy lists the contracts a wallet deployed, newest first (SmartContractsListGet), and how
// many entries of the node's list could not be read.
//
// The node records a payment to a contract (a CS transfer that runs payable(): no invocation, so
// an empty method) in its list of the payer's deploys (APIHandler: is_smart_deploy is
// method.empty()), and then fails a whole page that holds such an entry while reading its missing
// invocation ("No more data to read"). A page that fails is read again one entry at a time, and the
// entries that still fail are skipped (counted in skipped).
func (c *Client) ContractsBy(ctx context.Context, deployer ed25519.PublicKey) (list []ContractInfo, skipped int, err error) {
	seen := map[string]bool{}
	add := func(page []*api.SmartContract) {
		for _, sc := range page {
			if ci, ok := contractInfo(sc); ok && !seen[string(ci.Address)] {
				seen[string(ci.Address)] = true
				list = append(list, ci)
			}
		}
	}
	total := int64(-1) // the node's count, once a page answered
	for offset := int64(0); offset < MaxListed && (total < 0 || offset < total); offset += 100 {
		page, count, err := c.contractsPage(ctx, deployer, offset, 100)
		var app thrift.TApplicationException
		if errors.As(err, &app) {
			// one entry at a time: only the unreadable entries fail
			for i := offset; i < offset+100 && (total < 0 || i < total); i++ {
				one, count, err := c.contractsPage(ctx, deployer, i, 1)
				if errors.As(err, &app) {
					skipped++
					if total < 0 && skipped >= 100 {
						break
					}
					continue
				}
				if err != nil {
					return nil, 0, err
				}
				if count == 0 {
					total = i
					break
				}
				total = int64(count)
				add(one)
			}
			if total < 0 {
				break
			}
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		total = int64(count)
		add(page)
		if len(page) == 0 {
			break
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Created.After(list[j].Created) })
	return list, skipped, nil
}

// contractsPage is one SmartContractsListGet; a deployer without contracts is an empty page.
func (c *Client) contractsPage(ctx context.Context, deployer ed25519.PublicKey, offset, limit int64) ([]*api.SmartContract, int32, error) {
	var page []*api.SmartContract
	var count int32
	err := c.do(func(a *api.APIClient) error {
		r, err := a.SmartContractsListGet(ctx, general.Address(deployer), offset, limit)
		if err != nil {
			return err
		}
		if r.Status != nil && r.Status.Code == codeNotFound {
			count = r.Count
			return nil
		}
		if err := check("SmartContractsListGet", r.Status); err != nil {
			return err
		}
		page, count = r.SmartContractsList, r.Count
		return nil
	})
	return page, count, err
}

// ContractState reads a contract's state: its fields and their values (SmartContractDataGet).
func (c *Client) ContractState(ctx context.Context, address ed25519.PublicKey) (map[string]*general.Variant, error) {
	var vars map[string]*general.Variant
	err := c.do(func(a *api.APIClient) error {
		r, err := a.SmartContractDataGet(ctx, general.Address(address))
		if err != nil {
			return err
		}
		if err := check("SmartContractDataGet", r.Status); err != nil {
			return err
		}
		vars = r.Variables
		return nil
	})
	return vars, err
}

// WaitContract waits until the node knows the contract at address (a deploy is listed once it is in
// a block and its constructor ran), or the context ends.
func (c *Client) WaitContract(ctx context.Context, address ed25519.PublicKey, every time.Duration) error {
	for {
		_, err := c.Contract(ctx, address)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrNotAContract) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the node does not list the contract yet: %w", ctx.Err())
		case <-time.After(every):
		}
	}
}

// ---- methods -----------------------------------------------------------------------------------

// GetterAnnotation is the annotation that marks a method meant to be read without a transaction
// (com.credits.scapi.annotations.Getter, kept at run time; the executor lists it by its full name).
// It only documents the author's intent: running any method as a getter is free and changes
// nothing, and a method without it may still only read. The node's ContractAllMethodsGet drops the
// executor's annotations (it copies names and types only), so today no method is marked; the user
// chooses get or call.
const GetterAnnotation = "com.credits.scapi.annotations.Getter"

// IsGetter tells whether the method is annotated @Getter.
func IsGetter(m *general.MethodDescription) bool {
	if m == nil {
		return false
	}
	for _, a := range m.Annotations {
		if a != nil && (a.Name == GetterAnnotation || a.Name == "Getter") {
			return true
		}
	}
	return false
}

// SimpleType is a Java type name without its package ("java.math.BigDecimal" -> "BigDecimal").
func SimpleType(t string) string {
	if i := strings.LastIndex(t, "."); i >= 0 && !strings.Contains(t, "<") {
		return t[i+1:]
	}
	return strings.NewReplacer("java.lang.", "", "java.math.", "", "java.util.", "").Replace(t)
}

// Signature is "name(Type name, Type name) : Return", with simple type names, escaped
// (echo.Printable): the names are the contract author's.
func Signature(m *general.MethodDescription) string {
	args := make([]string, 0, len(m.Arguments))
	for _, a := range m.Arguments {
		if a == nil {
			continue
		}
		args = append(args, strings.TrimSpace(SimpleType(a.Type)+" "+a.Name))
	}
	return echo.Printable(fmt.Sprintf("%s(%s) : %s", m.Name, strings.Join(args, ", "), SimpleType(m.ReturnType)))
}
