package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/CreditsCommunity/wallet/internal/echo"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
	"github.com/CreditsCommunity/wallet/internal/templates"
	"github.com/CreditsCommunity/wallet/internal/tx"
	"github.com/CreditsCommunity/wallet/internal/wallet"
	"github.com/CreditsCommunity/wallet/thrift/general"
)

// ---- smart contracts ---------------------------------------------------------------------------

func (a *app) cmdContract(ctx context.Context, args []string) error {
	fs := newFlagSet(a, "contract")
	yes := fs.Bool("yes", false, "send without asking (scripts)")
	wait := fs.Int("wait", 180, "seconds to follow the transaction until it is in a block")
	force := fs.Bool("force", false, "new: replace an existing file")
	from := fs.String("from", "", "get: the caller the contract sees (account or address)")
	data := fs.String("data", "", "pay: user data (text), the userData of payable()")
	pos, err := a.commandArgs(fs, args, 1)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return usagef("contract: templates, new, compile, deploy, methods, info, get, call, pay or list")
	}
	sub, rest := pos[0], pos[1:]
	switch {
	case sub == "templates" && len(rest) == 0:
		return a.contractTemplates()
	case sub == "new" && len(rest) == 2:
		return a.contractNew(rest[0], rest[1], *force)
	case sub == "compile" && len(rest) == 1:
		return a.contractCompile(ctx, rest[0])
	case sub == "deploy" && len(rest) == 2:
		source, err := readSource(rest[1])
		if err != nil {
			return err
		}
		plan, err := a.w.PrepareDeploy(ctx, rest[0], rest[1], source)
		var failure *node.CompileFailure
		if errors.As(err, &failure) && !a.json {
			printCompileErrors(rest[1], failure)
		}
		if err != nil {
			return err
		}
		return a.confirmAndSend(ctx, plan, *yes, *wait)
	case sub == "methods" && len(rest) == 1:
		return a.contractMethods(ctx, rest[0])
	case sub == "info" && len(rest) == 1:
		return a.contractInfo(ctx, rest[0])
	case sub == "get" && len(rest) >= 2:
		return a.contractGet(ctx, rest[0], rest[1], rest[2:], *from)
	case sub == "call" && len(rest) >= 3:
		plan, err := a.w.PrepareCall(ctx, rest[0], rest[1], rest[2], rest[3:])
		if err != nil {
			return err
		}
		return a.confirmAndSend(ctx, plan, *yes, *wait)
	case sub == "pay" && len(rest) == 3:
		plan, err := a.w.PreparePay(ctx, rest[0], rest[1], rest[2], *data)
		if err != nil {
			return err
		}
		return a.confirmAndSend(ctx, plan, *yes, *wait)
	case sub == "list" && len(rest) == 1:
		return a.contractList(ctx, rest[0])
	}
	return usagef("contract %s: wrong arguments", echo.Quote(sub))
}

// readSource reads a contract's Java source file (at most wallet.MaxSource bytes).
func readSource(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a file", path)
	}
	if st.Size() > wallet.MaxSource {
		return "", fmt.Errorf("%s has %d bytes, at most %d", path, st.Size(), wallet.MaxSource)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	source := string(b)
	return source, wallet.CheckSource(source)
}

func (a *app) contractTemplates() error {
	list := templates.List()
	if a.json {
		type row struct {
			Name  string `json:"name"`
			About string `json:"about"`
		}
		rows := []row{}
		for _, t := range list {
			rows = append(rows, row{t.Name, t.About})
		}
		return a.printJSON(map[string]any{"source": templates.Source, "templates": rows})
	}
	for _, t := range list {
		fmt.Fprintf(a.out, "%-10s %s\n", t.Name, t.About)
	}
	fmt.Fprintf(a.out, "\nfrom %s; 'contract new NAME FILE' writes one\n", templates.Source)
	return nil
}

func (a *app) contractNew(name, file string, force bool) error {
	source, t, err := templates.Get(name)
	if err != nil {
		return err
	}
	mode := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		mode = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(file, mode, 0o644)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s exists: choose another name, or add --force to replace it", file)
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString(source); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if a.json {
		return a.printJSON(map[string]any{"template": t.Name, "file": file, "source": templates.Source})
	}
	fmt.Fprintf(a.out, "%s written to %s (%s)\n", t.Name, file, templates.Source)
	if t.Name == "TestToken" {
		fmt.Fprintln(os.Stderr, "TestToken is for tests only: deploy it on a private or test network, never on mainnet.")
	}
	return nil
}

type compileErrorRow struct {
	Line    int    `json:"line"`
	Column  int    `json:"column,omitempty"`
	Message string `json:"message"`
}

func (a *app) contractCompile(ctx context.Context, file string) error {
	source, err := readSource(file)
	if err != nil {
		return err
	}
	c, err := a.w.Compile(ctx, source)
	var failure *node.CompileFailure
	if errors.As(err, &failure) {
		if a.json {
			rows := []compileErrorRow{}
			for _, e := range failure.Errors {
				rows = append(rows, compileErrorRow{e.Line, e.Column, e.Text})
			}
			_ = a.printJSON(map[string]any{"file": file, "compiled": false, "errors": rows, "message": failure.Message})
			return shownError{failure}
		}
		printCompileErrors(file, failure)
		return failure
	}
	if err != nil {
		return err
	}
	if a.json {
		classes := []string{}
		for _, b := range c.Classes {
			classes = append(classes, b.Name)
		}
		out := map[string]any{"file": file, "compiled": true, "classes": classes, "token_standard": c.TokenStandard}
		if c.MethodsErr == nil {
			out["methods"] = methodRows(c.Methods)
		}
		return a.printJSON(out)
	}
	fmt.Fprintf(a.out, "compiled: %s, %s", plural(len(c.Classes), "class"), printable(c.Name()))
	if c.TokenStandard != 0 {
		fmt.Fprintf(a.out, ", token standard %d", c.TokenStandard)
	}
	fmt.Fprintln(a.out)
	if c.MethodsErr != nil {
		fmt.Fprintf(a.out, "methods: %s\n", echo.Terminal(c.MethodsErr.Error()))
		return nil
	}
	fmt.Fprintf(a.out, "%s:\n", plural(len(c.Methods), "method"))
	printMethods(a, c.Methods)
	return nil
}

// printCompileErrors writes the errors as FILE:LINE: message (the compiler's own form), escaped.
func printCompileErrors(file string, f *node.CompileFailure) {
	if len(f.Errors) == 0 {
		fmt.Fprintln(os.Stderr, echo.Terminal(f.Message)) // escaped by the node package too; again at the output
		return
	}
	for _, e := range f.Errors {
		where := fmt.Sprintf("%s:%d", file, e.Line)
		if e.Column > 0 {
			where += fmt.Sprintf(":%d", e.Column)
		}
		fmt.Fprintf(os.Stderr, "%s: %s\n", where, strings.ReplaceAll(printable(e.Text), `\n`, "\n    "))
	}
}

type methodRow struct {
	Name      string   `json:"name"`
	Arguments []string `json:"arguments"`
	Names     []string `json:"argument_names"`
	Returns   string   `json:"returns"`
	Getter    bool     `json:"getter"`
}

func sortedMethods(list []*general.MethodDescription) []*general.MethodDescription {
	out := make([]*general.MethodDescription, 0, len(list))
	for _, m := range list {
		if m != nil {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func methodRows(list []*general.MethodDescription) []methodRow {
	rows := []methodRow{}
	for _, m := range sortedMethods(list) {
		r := methodRow{Name: m.Name, Returns: m.ReturnType, Getter: node.IsGetter(m), Arguments: []string{}, Names: []string{}}
		for _, x := range m.Arguments {
			if x != nil {
				r.Arguments, r.Names = append(r.Arguments, x.Type), append(r.Names, x.Name)
			}
		}
		rows = append(rows, r)
	}
	return rows
}

func printMethods(a *app, list []*general.MethodDescription) {
	for _, m := range sortedMethods(list) {
		mark := ""
		switch {
		case m.Name == "payable":
			mark = "  (runs on a CS payment: 'contract pay')"
		case node.IsGetter(m):
			mark = "  getter"
		}
		fmt.Fprintf(a.out, "  %s%s\n", node.Signature(m), mark)
	}
}

func (a *app) contractMethods(ctx context.Context, text string) error {
	contract, err := keys.ParseAddress(text)
	if err != nil {
		return fmt.Errorf("contract: %w", err)
	}
	methods, err := a.w.Node.ContractMethods(ctx, contract)
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(map[string]any{"contract": text, "methods": methodRows(methods)})
	}
	fmt.Fprintf(a.out, "%s: %s ('contract get' reads any of them for free; 'contract call' signs a call; getter: annotated @Getter, when the node tells)\n",
		text, plural(len(methods), "method"))
	printMethods(a, methods)
	return nil
}

// stateRows renders a contract's state variables, escaped, sorted by name.
func stateRows(vars map[string]*general.Variant) map[string]string {
	out := map[string]string{}
	for k, v := range vars {
		out[k] = tx.Show(v)
	}
	return out
}

func printState(a *app, vars map[string]*general.Variant, err error) {
	if err != nil {
		fmt.Fprintf(a.out, "state: not available (%s)\n", echo.Terminal(err.Error()))
		return
	}
	names := make([]string, 0, len(vars))
	for k := range vars {
		names = append(names, k)
	}
	sort.Strings(names)
	fmt.Fprintf(a.out, "state: %s (the node shows a contract's public fields only; read the others with getters)\n", plural(len(names), "public field"))
	for _, k := range names {
		fmt.Fprintf(a.out, "  %-20s %s\n", printable(k), printable(tx.Show(vars[k])))
	}
}

func (a *app) contractInfo(ctx context.Context, text string) error {
	contract, err := keys.ParseAddress(text)
	if err != nil {
		return fmt.Errorf("contract: %w", err)
	}
	info, err := a.w.Node.Contract(ctx, contract)
	if err != nil {
		return err
	}
	vars, stateErr := a.w.Node.ContractState(ctx, contract)
	if a.json {
		out := map[string]any{"contract": text, "name": info.Name(), "classes": info.Classes, "token_standard": info.TokenStandard,
			"transactions": info.Transactions, "source_bytes": info.SourceSize}
		if info.Deployer != nil {
			out["deployer"] = keys.Address(info.Deployer)
		}
		if !info.Created.IsZero() {
			out["created"] = info.Created.UTC()
		}
		if stateErr == nil {
			out["state"] = stateRows(vars)
		}
		return a.printJSON(out)
	}
	fmt.Fprintf(a.out, "contract     : %s\nname         : %s (as the contract calls itself)\n", text, printable(info.Name()))
	if info.Deployer != nil {
		fmt.Fprintf(a.out, "deployer     : %s\n", keys.Address(info.Deployer))
	}
	if !info.Created.IsZero() {
		fmt.Fprintf(a.out, "created      : %s\n", info.Created.UTC().Format("2006-01-02 15:04:05 UTC"))
	}
	fmt.Fprintf(a.out, "classes      : %d\ntransactions : %d\n", len(info.Classes), info.Transactions)
	if info.TokenStandard != 0 {
		fmt.Fprintf(a.out, "token standard %d\n", info.TokenStandard)
	}
	printState(a, vars, stateErr)
	return nil
}

func (a *app) contractGet(ctx context.Context, contract, method string, args []string, from string) error {
	var caller ed25519.PublicKey
	if from != "" {
		_, pub, err := a.w.Account(from)
		if err != nil {
			return err
		}
		caller = pub
	}
	r, err := a.w.Get(ctx, caller, contract, method, args)
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(map[string]any{"contract": contract, "method": r.Method.Name, "args": args, "returns": r.Method.ReturnType,
			"result": tx.Show(r.Value), "getter": node.IsGetter(r.Method)})
	}
	fmt.Fprintln(a.out, printable(tx.Show(r.Value)))
	return nil
}

func (a *app) contractList(ctx context.Context, who string) error {
	name, pub, err := a.w.Account(who)
	if err != nil {
		return err
	}
	rows, nodeErr, localErr := a.w.Contracts(ctx, pub)
	if localErr != nil {
		fmt.Fprintln(os.Stderr, "warning: local contract list:", echo.Terminal(localErr.Error()))
	}
	if nodeErr != nil {
		fmt.Fprintln(os.Stderr, "warning: the node's list (SmartContractsListGet):", echo.Terminal(nodeErr.Error()))
	}
	type row struct {
		Address  string     `json:"address"`
		Name     string     `json:"name"`
		Created  *time.Time `json:"created,omitempty"`
		OnNode   bool       `json:"on_node"`
		Local    bool       `json:"local"`
		File     string     `json:"file,omitempty"`
		Network  string     `json:"network,omitempty"`
		Standard int32      `json:"token_standard,omitempty"`
	}
	out := []row{}
	for _, r := range rows {
		x := row{Address: keys.Address(r.Address), Name: r.Name, OnNode: r.OnNode, Local: r.Local != nil, Standard: r.TokenStandard}
		if !r.Created.IsZero() {
			c := r.Created.UTC()
			x.Created = &c
		}
		if r.Local != nil {
			x.File, x.Network = r.Local.File, r.Local.Network
		}
		out = append(out, x)
	}
	if a.json {
		res := map[string]any{"deployer": keys.Address(pub), "contracts": out}
		if nodeErr != nil {
			res["node_error"] = nodeErr.Error()
		}
		return a.printJSON(res)
	}
	label := keys.Address(pub)
	if name != "" {
		label = name + " " + label
	}
	fmt.Fprintf(a.out, "%s: %s\n", label, plural(len(out), "contract"))
	for _, r := range out {
		when := "                "
		if r.Created != nil {
			when = r.Created.Local().Format("2006-01-02 15:04")
		}
		where := "node and local list"
		switch {
		case !r.Local:
			where = "node"
		case !r.OnNode:
			where = "local list only (" + printable(r.Network) + "): not on this node"
		}
		fmt.Fprintf(a.out, "%s  %s  %-14s %s", when, r.Address, printable(r.Name), where)
		if r.File != "" {
			fmt.Fprintf(a.out, "  %s", printable(r.File))
		}
		fmt.Fprintln(a.out)
	}
	return nil
}
