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

// ContractsBy lists the contracts a wallet deployed, newest first (SmartContractsListGet).
func (c *Client) ContractsBy(ctx context.Context, deployer ed25519.PublicKey) ([]ContractInfo, error) {
	var out []ContractInfo
	seen := map[string]bool{}
	for offset := int64(0); offset < MaxListed; offset += 100 {
		var page []*api.SmartContract
		var count int32
		err := c.do(func(a *api.APIClient) error {
			r, err := a.SmartContractsListGet(ctx, general.Address(deployer), offset, 100)
			if err != nil {
				return err
			}
			if r.Status != nil && r.Status.Code == codeNotFound {
				return nil
			}
			if err := check("SmartContractsListGet", r.Status); err != nil {
				return err
			}
			page, count = r.SmartContractsList, r.Count
			return nil
		})
		if err != nil {
			return nil, err
		}
		for _, sc := range page {
			if ci, ok := contractInfo(sc); ok && !seen[string(ci.Address)] {
				seen[string(ci.Address)] = true
				out = append(out, ci)
			}
		}
		if len(page) < 100 || int64(count) <= offset+100 {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
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
// nothing, and a method without it may still only read.
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
