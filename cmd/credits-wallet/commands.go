package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/CreditsCommunity/wallet/internal/echo"
	"github.com/CreditsCommunity/wallet/internal/history"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

// ---- status ------------------------------------------------------------------------------------

func (a *app) cmdStatus(ctx context.Context, args []string) error {
	fs := newFlagSet(a, "status")
	if _, err := a.commandArgs(fs, args, 1); err != nil {
		return err
	}
	w := a.w
	st, err := w.Node.Status(ctx)
	if err != nil {
		return err
	}
	oldest, oldestErr := w.Node.OldestBlock(ctx, st.LastBlock)
	id, idErr := w.Node.NetworkIdentity(ctx)
	check, mismatch := wallet.JudgeNetwork(w.Profile, id, idErr)
	if a.json {
		out := map[string]any{"node": w.Profile.Address, "profile": w.Profile.Name, "network": w.Profile.Network,
			"last_block": st.LastBlock, "round": st.Round, "synced": st.Synced, "head_time": st.HeadTime.UTC()}
		if id.Known() {
			out["network_uuid"] = fmt.Sprint(id.UUID)
			out["node_says_mainnet"] = id.IsMainnet()
		}
		if id.Version != "" {
			out["community_version"] = id.Version
		}
		if mismatch != nil {
			out["network_mismatch"] = mismatch.Error()
		}
		if !st.Synced {
			out["why"] = st.Why
		}
		if oldestErr == nil {
			out["oldest_block"] = oldest
		}
		return a.printJSON(out)
	}
	fmt.Fprintf(a.out, "network      : %s%s\n", w.Profile.Network.Label(), networkNote(w, check, mismatch))
	if idErr == nil {
		fmt.Fprintf(a.out, "node says    : %s\n", id.Describe())
	} else {
		fmt.Fprintf(a.out, "node says    : nothing (%s)\n", strings.TrimPrefix(echo.Terminal(idErr.Error()), node.ErrNoNetworkID.Error()+": "))
	}
	if mismatch != nil {
		fmt.Fprintf(a.out, "MISMATCH     : %s\n", mismatch)
	}
	fmt.Fprintf(a.out, "node         : %s\n", w.Profile.Address)
	fmt.Fprintf(a.out, "last block   : %d\n", st.LastBlock)
	fmt.Fprintf(a.out, "round        : %d\n", st.Round)
	if !st.HeadTime.IsZero() {
		fmt.Fprintf(a.out, "head time    : %s (%s ago)\n", st.HeadTime.UTC().Format("2006-01-02 15:04:05 UTC"), time.Since(st.HeadTime).Round(time.Second))
	}
	if st.Synced {
		fmt.Fprintln(a.out, "synced       : yes")
	} else {
		fmt.Fprintf(a.out, "synced       : NO, %s (sending is refused)\n", st.Why)
	}
	if oldestErr == nil {
		note := "a full node"
		if oldest > 1 {
			note = "pruned: older history is not on this node"
		}
		fmt.Fprintf(a.out, "history from : block %d (%s)\n", oldest, note)
	}
	return nil
}

func reportProblems(problems []error) {
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "warning: key file not readable:", p)
	}
}

// networkNote says where the network shown comes from: the configuration, and whether the node
// confirmed it.
func networkNote(w *wallet.Wallet, check wallet.NetworkCheck, mismatch error) string {
	from := "declared in the configuration"
	if w.Profile.Network.IsMainnet() && (w.Profile.Name == "" || w.Config.Path == "") {
		from = "assumed: no node profile declares it a test network"
	}
	switch {
	case mismatch != nil:
		return " (" + from + "; REFUSED: see below)"
	case check.Confirmed:
		return " (" + from + "; confirmed by the node)"
	}
	return " (" + from + "; not confirmed by the node)"
}

// ---- accounts ----------------------------------------------------------------------------------

func (a *app) cmdAccount(ctx context.Context, args []string) error {
	fs := newFlagSet(a, "account")
	format := fs.String("format", "encrypted", "export format")
	outFile := fs.String("out", "", "export to FILE")
	yes := fs.Bool("yes", false, "no questions")
	wordsFile := fs.String("words-file", "", "new: write the seed phrase to FILE")
	showWords := fs.Bool("show-words", false, "new: print the seed phrase in the output")
	pos, err := a.commandArgs(fs, args, 1)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return usagef("account: new, import, export, list or address")
	}
	sub, rest := pos[0], pos[1:]
	switch {
	case sub == "list" && len(rest) == 0:
		return a.accountList()
	case sub == "address" && len(rest) == 1:
		return a.accountAddress(rest[0])
	case sub == "new" && len(rest) == 1:
		return a.accountNew(rest[0], *wordsFile, *showWords)
	case sub == "import" && (len(rest) == 1 || len(rest) == 2):
		file := ""
		if len(rest) == 2 {
			file = rest[1]
		}
		return a.accountImport(rest[0], file)
	case sub == "export" && len(rest) == 1:
		return a.accountExport(rest[0], *format, *outFile, *yes)
	}
	return usagef("account %s: wrong arguments", sub)
}

// accountAddress prints an account's address alone, for a pipe into xclip, pbcopy or clip.
func (a *app) accountAddress(name string) error {
	if err := keys.ValidName(name); err != nil {
		return err
	}
	if !a.w.Store.Exists(name) {
		return fmt.Errorf("no account named %q (see 'account list')", name)
	}
	f, err := a.w.Store.Load(name)
	if err != nil {
		return err
	}
	if _, err := keys.ParseAddress(f.Public); err != nil {
		return fmt.Errorf("the key file of %q has no valid address: %w", name, err)
	}
	if a.json {
		return a.printJSON(map[string]any{"name": f.Name, "address": f.Public})
	}
	fmt.Fprintln(a.out, f.Public)
	return nil
}

func (a *app) accountList() error {
	files, problems, err := a.w.Store.List()
	if err != nil {
		return err
	}
	reportProblems(problems)
	if a.json {
		type row struct {
			Name    string `json:"name"`
			Address string `json:"address"`
		}
		rows := []row{}
		for _, f := range files {
			rows = append(rows, row{f.Name, f.Public})
		}
		return a.printJSON(rows)
	}
	if len(files) == 0 {
		fmt.Fprintf(a.out, "no accounts in %s: 'account new NAME' or 'account import NAME'\n", a.w.Store.Dir)
	}
	for _, f := range files {
		fmt.Fprintf(a.out, "%-20s %s\n", f.Name, f.Public)
	}
	return nil
}

// accountNew creates an account. On a terminal the phrase is shown there (never in the output,
// --json included) and three words are typed back; then, unless the phrase already goes somewhere
// (--words-file, --show-words) or the output is JSON, the wallet offers to save it to a file as
// well (default no), outside the data folder. Without a terminal the phrase must go somewhere
// explicit: --words-file FILE (created, mode 600) or --show-words (in the output).
func (a *app) accountNew(name, wordsFile string, showWords bool) error {
	if err := keys.ValidName(name); err != nil {
		return err
	}
	if a.w.Store.Exists(name) {
		return fmt.Errorf("an account named %q already exists", name)
	}
	interactive := stdinIsTerminal()
	if !interactive && wordsFile == "" && !showWords {
		return errors.New("no terminal to show the seed phrase on: add --words-file FILE (or --show-words to print it); nothing was created")
	}
	master, err := newMasterSeed()
	if err != nil {
		return err
	}
	defer keys.Zero(master[:])
	words := keys.Words(&master)
	key := keys.DeriveKey(&master)
	defer keys.Zero(key)
	recoveryFile := ""
	if interactive {
		fmt.Fprintln(os.Stderr, "Your seed phrase. Write it down on paper and keep it safe: it is shown only this once,")
		fmt.Fprintln(os.Stderr, "and anyone who has it owns the account.")
		fmt.Fprintln(os.Stderr)
		for i, w := range words {
			fmt.Fprintf(os.Stderr, "%3d. %-10s", i+1, w)
			if i%6 == 5 {
				fmt.Fprintln(os.Stderr)
			}
		}
		fmt.Fprintln(os.Stderr)
		if err := checkWords(words); err != nil {
			return err
		}
		if !a.json && wordsFile == "" && !showWords {
			if recoveryFile, err = a.askRecoveryFile(name); err != nil {
				return err
			}
		}
	}
	pw, err := a.readPassword("Password for the key file: ", true)
	if err != nil {
		return err
	}
	defer keys.Zero(pw)
	if wordsFile != "" {
		data := []byte(strings.Join(words, " ") + "\n")
		err := wallet.WriteSecretFile(wordsFile, data)
		keys.Zero(data)
		if err != nil {
			return fmt.Errorf("--words-file: %w; nothing was created", err)
		}
	}
	if recoveryFile != "" {
		data := wallet.RecoveryText(name, keys.Address(key.Public().(ed25519.PublicKey)), time.Now(), words)
		err := wallet.WriteSecretFile(recoveryFile, data)
		keys.Zero(data)
		if err != nil {
			return fmt.Errorf("the recovery file: %w; nothing was created", err)
		}
	}
	f, err := a.w.CreateAccount(name, key, pw)
	if err != nil {
		if wordsFile != "" {
			os.Remove(wordsFile)
		}
		if recoveryFile != "" {
			os.Remove(recoveryFile)
		}
		return err
	}
	if a.json {
		out := map[string]any{"name": f.Name, "address": f.Public}
		if showWords {
			out["words"] = strings.Join(words, " ")
		}
		if wordsFile != "" {
			out["words_file"] = wordsFile
		}
		return a.printJSON(out)
	}
	if showWords {
		fmt.Fprintf(a.out, "words  : %s\n", strings.Join(words, " "))
	}
	if wordsFile != "" {
		fmt.Fprintf(os.Stderr, "the seed phrase is in %s (mode 600): copy it to paper, then delete the file\n", wordsFile)
	}
	if recoveryFile != "" {
		fmt.Fprintf(os.Stderr, "The 24 words are in %s (mode 600).\n%s\n", recoveryFile, wallet.RecoveryAdvice)
	}
	fmt.Fprintf(a.out, "account: %s\naddress: %s\n", f.Name, f.Public)
	return nil
}

// newMasterSeed draws the master seed of a new account (replaced in tests).
var newMasterSeed = keys.NewMasterSeed

// askRecoveryFile asks whether to save the phrase to a file as well (default no) and where (default
// ~/credits-recovery-NAME.txt), refusing a place next to the keys. "" is no file.
func (a *app) askRecoveryFile(name string) (string, error) {
	answer, err := ask(wallet.RecoveryQuestion)
	if err != nil {
		return "", nil // no answer (end of input): no
	}
	if answer = strings.ToLower(answer); answer != "y" && answer != "yes" {
		return "", nil
	}
	def, err := wallet.DefaultRecoveryPath(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "no default place for the file:", err)
	}
	for tries := 0; tries < 3; tries++ {
		prompt := "File: "
		if def != "" {
			prompt = fmt.Sprintf("File [%s]: ", def)
		}
		path, err := ask(prompt)
		if err != nil {
			break
		}
		if path == "" {
			path = def
		}
		abs, err := a.w.CheckRecoveryPath(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, echo.Terminal(err.Error()))
			continue
		}
		if warning := wallet.RecoveryProfileWarning(abs); warning != "" {
			fmt.Fprintln(os.Stderr, "warning:", warning)
		}
		return abs, nil
	}
	fmt.Fprintln(os.Stderr, "No file: the words are only on your paper.")
	return "", nil
}

// checkPositions picks the three words to type back (replaced in tests).
var checkPositions = func(n int) []int {
	p := rand.Perm(n)[:3]
	sort.Ints(p)
	return p
}

// checkWords asks the user to type back three words of the phrase.
func checkWords(words []string) error {
	positions := checkPositions(len(words))
	fmt.Fprintln(os.Stderr, "Check: type back three of the words.")
	for _, p := range positions {
		for tries := 0; ; tries++ {
			got, err := ask(fmt.Sprintf("word %d: ", p+1))
			if err != nil {
				return err
			}
			if strings.EqualFold(got, words[p]) {
				break
			}
			if tries == 2 {
				return errors.New("the words do not match: nothing was saved; run 'account new' again")
			}
			fmt.Fprintln(os.Stderr, "not that one, look again")
		}
	}
	// clear the phrase from the screen and the scrollback where the terminal allows it
	fmt.Fprint(os.Stderr, "\033[2J\033[3J\033[H")
	fmt.Fprintln(os.Stderr, "The phrase was cleared from the screen. Your terminal's scrollback, a terminal multiplexer")
	fmt.Fprintln(os.Stderr, "(tmux, screen) or a session log may still hold it: clear or close them.")
	return nil
}

func (a *app) accountImport(name, file string) error {
	if err := keys.ValidName(name); err != nil {
		return err
	}
	if a.w.Store.Exists(name) {
		return fmt.Errorf("an account named %q already exists", name)
	}
	var text string
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		text = string(data)
		keys.Zero(data)
	} else {
		var err error
		if text, err = readSecretLine("Secret key (base58), 24-word phrase or keys JSON: "); err != nil {
			return err
		}
	}
	key, kind, err := keys.ParseSecret(text)
	if err != nil {
		return err
	}
	defer keys.Zero(key)
	pw, err := a.readPassword("Password for the key file: ", true)
	if err != nil {
		return err
	}
	defer keys.Zero(pw)
	f, err := a.w.CreateAccount(name, key, pw)
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(map[string]any{"name": f.Name, "address": f.Public, "from": kind})
	}
	fmt.Fprintf(a.out, "imported %s: account %s, address %s\n", kind, f.Name, f.Public)
	if file != "" {
		fmt.Fprintln(os.Stderr, wallet.PlainKeyAdvice(file))
	}
	return nil
}

func (a *app) accountExport(name, format, outFile string, yes bool) error {
	f, err := a.w.Store.Load(name)
	if err != nil {
		return err
	}
	var data []byte
	switch format {
	case "encrypted":
		data, err = os.ReadFile(a.w.Store.FilePath(f.Name))
		if err != nil {
			return err
		}
	case "base58", "official":
		if format == "official" {
			fmt.Fprintln(os.Stderr, "WARNING: the keys JSON of the official wallets holds the secret key NOT ENCRYPTED.")
			fmt.Fprintln(os.Stderr, "Anyone who reads the file owns the account. Use it only to move the key into an official wallet.")
			if !yes {
				if !stdinIsTerminal() {
					return errors.New("refused without a terminal to confirm on: add --yes")
				}
				if ans, _ := ask("Type 'export' to continue: "); ans != "export" {
					return errors.New("cancelled")
				}
			}
		}
		pw, err := a.readPassword("Password: ", false)
		if err != nil {
			return err
		}
		key, err := f.Decrypt(pw)
		keys.Zero(pw)
		if err != nil {
			return err
		}
		if format == "base58" {
			data = []byte(keys.Base58(key) + "\n")
		} else {
			data = append(keys.OfficialJSON(key), '\n')
		}
		keys.Zero(key)
	default:
		return usagef("--format must be encrypted, base58 or official")
	}
	defer keys.Zero(data)
	if outFile == "" {
		_, err = a.out.Write(data)
		return err
	}
	if err := wallet.WriteSecretFile(outFile, data); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "written to %s (mode 600)\n", outFile)
	return nil
}

// ---- balance -----------------------------------------------------------------------------------

func (a *app) cmdBalance(ctx context.Context, args []string) error {
	fs := newFlagSet(a, "balance")
	pos, err := a.commandArgs(fs, args, 1)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		files, problems, err := a.w.Store.List()
		if err != nil {
			return err
		}
		reportProblems(problems)
		if len(files) == 0 {
			return errors.New("no accounts: give an account or an address")
		}
		for _, f := range files {
			pos = append(pos, f.Name)
		}
	}
	type row struct {
		Account      string `json:"account,omitempty"`
		Address      string `json:"address"`
		Balance      string `json:"balance_cs"`
		DelegatedOut string `json:"delegated_out_cs"`
		DelegatedIn  string `json:"delegated_in_cs"`
	}
	var rows []row
	for _, p := range pos {
		name, pub, err := a.w.Account(p)
		if err != nil {
			return err
		}
		b, err := a.w.Node.Balance(ctx, pub)
		if err != nil {
			return err
		}
		rows = append(rows, row{name, keys.Address(pub), b.CS.String(), b.DelegatedOut.String(), b.DelegatedIn.String()})
		if !a.json {
			label := name
			if label == "" {
				label = "-"
			}
			fmt.Fprintf(a.out, "%-16s %s  %s CS", label, keys.Address(pub), b.CS.Grouped())
			if b.DelegatedOut.Sign() != 0 {
				fmt.Fprintf(a.out, "  (delegated %s CS)", b.DelegatedOut.Grouped())
			}
			if b.DelegatedIn.Sign() != 0 {
				fmt.Fprintf(a.out, "  (delegated to it %s CS)", b.DelegatedIn.Grouped())
			}
			fmt.Fprintln(a.out)
		}
	}
	if a.json {
		return a.printJSON(rows)
	}
	return nil
}

// ---- send ------------------------------------------------------------------------------------

func (a *app) cmdSend(ctx context.Context, args []string) error {
	fs := newFlagSet(a, "send")
	yes := fs.Bool("yes", false, "send without asking (scripts)")
	wait := fs.Int("wait", 120, "seconds to follow the transfer until it is in a block")
	data := fs.String("data", "", "user data (text)")
	pos, err := a.commandArgs(fs, args, 1)
	if err != nil {
		return err
	}
	if len(pos) != 3 {
		return usagef("send <account> <to> <amount>")
	}
	plan, err := a.w.PrepareSend(ctx, wallet.SendRequest{Account: pos[0], To: pos[1], Amount: pos[2], Data: *data})
	if err != nil {
		return err
	}
	return a.confirmAndSend(ctx, plan, *yes, *wait)
}

// confirmAndSend shows the plan, asks for the confirmation (on MAINNET: type 'send'), reads the
// password and sends.
func (a *app) confirmAndSend(ctx context.Context, plan *wallet.SendPlan, yes bool, wait int) error {
	if !yes || !a.json {
		printPlan(plan)
	}
	if !yes {
		if !stdinIsTerminal() {
			return errors.New("no terminal to confirm on: add --yes to send from a script")
		}
		if plan.Network.IsMainnet() {
			ans, err := ask(fmt.Sprintf("This sends a real %s on MAINNET. Type 'send' to sign and send: ", plan.Kind))
			if err != nil || ans != "send" {
				return errors.New("cancelled: nothing was sent")
			}
		} else {
			ans, err := ask(fmt.Sprintf("Send on %s? [y/N] ", plan.Network.Label()))
			if err != nil || (ans != "y" && ans != "Y" && ans != "yes") {
				return errors.New("cancelled: nothing was sent")
			}
		}
	}
	pw, err := a.readPassword("Password of "+plan.Account+": ", false)
	if err != nil {
		return err
	}
	res, err := a.w.Send(ctx, plan, pw, time.Duration(wait)*time.Second)
	keys.Zero(pw)
	if err != nil {
		return err
	}
	if a.json {
		out := map[string]any{"kind": plan.Kind, "account": plan.Account, "from": keys.Address(plan.From), "to": keys.Address(plan.To),
			"amount_cs": plan.Amount.String(), "fee_cs": res.Fee.String(), "inner_id": res.InnerID, "block": res.Block,
			"round": res.Round, "in_block": res.InBlock, "network": plan.Network}
		if plan.Data != "" {
			out["data"] = plan.Data
		}
		if !plan.Until.IsZero() {
			out["until"] = plan.Until.UTC()
		}
		if t := plan.Token; t != nil {
			out["token"], out["token_code"], out["token_amount"], out["returned"] = keys.Address(t.Contract), t.Code, t.Amount, res.Returned
		}
		if c := plan.Contract; c != nil {
			out["contract"], out["contract_name"] = keys.Address(c.Address), c.Name
			if res.Result != nil {
				out["returned"] = res.Returned
			}
			switch plan.Kind {
			case wallet.KindDeploy:
				out["listed"], out["file"] = res.Listed, c.File
				if res.SaveErr != nil {
					out["local_list_error"] = res.SaveErr.Error()
				}
			case wallet.KindCall:
				out["method"], out["args"] = c.Method.Name, c.Args
				if vars, err := a.w.Node.ContractState(ctx, c.Address); err == nil {
					out["state"] = stateRows(vars)
				}
			}
		}
		return a.printJSON(out)
	}
	where := "in a block"
	if res.Block > 0 {
		where = fmt.Sprintf("in block %d", res.Block)
	}
	what := plan.Amount.Grouped() + " CS"
	switch plan.Kind {
	case wallet.KindTransfer:
		fmt.Fprintf(a.out, "sent %s to %s", what, keys.Address(plan.To))
	case wallet.KindDelegate:
		fmt.Fprintf(a.out, "delegated %s to %s%s", what, keys.Address(plan.To), untilText(plan.Until))
	case wallet.KindUndelegate:
		fmt.Fprintf(a.out, "withdrew %s from the delegation to %s", what, keys.Address(plan.To))
	case wallet.KindToken:
		fmt.Fprintf(a.out, "sent %s %s to %s (transfer returned %s)", plan.Token.Amount, tokenLabel(plan.Token), keys.Address(plan.To), printable(res.Returned))
	case wallet.KindDeploy:
		fmt.Fprintf(a.out, "deployed %s at %s", printable(plan.Contract.Name), keys.Address(plan.To))
	case wallet.KindCall:
		fmt.Fprintf(a.out, "called %s of %s", printable(plan.Contract.Method.Name), keys.Address(plan.To))
	case wallet.KindPay:
		fmt.Fprintf(a.out, "paid %s to contract %s", what, keys.Address(plan.To))
	}
	fmt.Fprintf(a.out, ": %s, inner id %d, fee %s CS\n", where, res.InnerID, res.Fee.String())
	switch plan.Kind {
	case wallet.KindDeploy:
		fmt.Fprintf(a.out, "contract: %s\n", keys.Address(plan.To))
		if !res.Listed {
			fmt.Fprintln(os.Stderr, "warning: the node does not list the contract yet; check it later with 'contract info'")
		}
		if res.SaveErr != nil {
			fmt.Fprintln(os.Stderr, "warning: not saved in the local contract list:", echo.Terminal(res.SaveErr.Error()))
		}
	case wallet.KindCall, wallet.KindPay:
		if res.Result != nil {
			fmt.Fprintf(a.out, "returned: %s\n", printable(res.Returned))
		}
		if plan.Kind == wallet.KindCall {
			vars, err := a.w.Node.ContractState(ctx, plan.To)
			printState(a, vars, err)
		}
	}
	return nil
}

func untilText(t time.Time) string {
	if t.IsZero() {
		return " without a time limit"
	}
	return " until " + t.UTC().Format("2006-01-02 15:04:05 UTC") + " (" + t.Local().Format("2006-01-02 15:04 MST") + " local)"
}

func tokenLabel(t *wallet.TokenPlan) string {
	if t.Code != "" {
		return printable(t.Code)
	}
	return "tokens of " + keys.Short(keys.Address(t.Contract))
}

func printPlan(p *wallet.SendPlan) {
	e := os.Stderr
	titles := map[wallet.Kind]string{wallet.KindTransfer: "Send CS", wallet.KindDelegate: "Delegate CS",
		wallet.KindUndelegate: "Withdraw a delegation", wallet.KindToken: "Send tokens", wallet.KindDeploy: "Deploy a contract",
		wallet.KindCall: "Call a contract", wallet.KindPay: "Pay a contract"}
	fmt.Fprintf(e, "%-49s%s\n", titles[p.Kind], p.Network.Label())
	fmt.Fprintf(e, "  From     %s  %s   balance %s CS\n", p.Account, keys.Address(p.From), p.Balance.Grouped())
	switch p.Kind {
	case wallet.KindTransfer:
		fmt.Fprintf(e, "  To       %s\n", keys.Address(p.To))
		fmt.Fprintf(e, "  Amount   %s CS\n", p.Amount.Grouped())
		if p.Data != "" {
			fmt.Fprintf(e, "  Data     %q (public: anyone can read it on the chain)\n", p.Data)
		}
	case wallet.KindDelegate:
		fmt.Fprintf(e, "  Node     %s\n", keys.Address(p.To))
		fmt.Fprintf(e, "  Amount   %s CS delegated%s\n", p.Amount.Grouped(), untilText(p.Until))
	case wallet.KindUndelegate:
		fmt.Fprintf(e, "  Node     %s\n", keys.Address(p.To))
		fmt.Fprintf(e, "  Amount   %s CS back, of the %s CS delegated without a time limit\n", p.Amount.Grouped(), p.Delegated.Grouped())
	case wallet.KindToken:
		t := p.Token
		fmt.Fprintf(e, "  Token    %s  (the contract address: the only identity of a token)\n", keys.Address(t.Contract))
		fmt.Fprintf(e, "  Calls itself  %s %s, %d decimals (as the contract calls itself)\n", printable(t.Code), printable(t.Name), t.Decimals)
		for _, warning := range t.Warnings {
			fmt.Fprintf(e, "  WARNING  %s\n", warning)
		}
		fmt.Fprintf(e, "  To       %s\n", keys.Address(p.To))
		fmt.Fprintf(e, "  Amount   %s %s (token balance %s)\n", t.Amount, printable(t.Code), printable(t.Balance))
	case wallet.KindDeploy:
		c := p.Contract
		fmt.Fprintf(e, "  Source   %s: %s, %s\n", c.File, printable(c.Name), plural(len(c.Deploy.Classes), "class"))
		if c.Methods >= 0 {
			fmt.Fprintf(e, "  Methods  %d\n", c.Methods)
		}
		if c.Deploy.TokenStandard != 0 {
			fmt.Fprintf(e, "  Token    standard %d: the node will list it as a token\n", c.Deploy.TokenStandard)
		}
		fmt.Fprintf(e, "  Address  %s (derived from the account, inner id %d and the byte code)\n", keys.Address(p.To), p.InnerID)
	case wallet.KindCall:
		c := p.Contract
		fmt.Fprintf(e, "  Contract %s  %s\n", keys.Address(p.To), printable(c.Name))
		fmt.Fprintf(e, "  Method   %s%s\n", node.Signature(c.Method), map[bool]string{true: "  (@Getter: 'contract get' reads it for free)"}[c.Getter])
		for i, arg := range c.Method.Arguments {
			fmt.Fprintf(e, "    %-12s %s\n", printable(arg.Name), printable(c.Args[i]))
		}
	case wallet.KindPay:
		fmt.Fprintf(e, "  Contract %s  %s (its payable() runs)\n", keys.Address(p.To), printable(p.Contract.Name))
		fmt.Fprintf(e, "  Amount   %s CS\n", p.Amount.Grouped())
		if p.Data != "" {
			fmt.Fprintf(e, "  Data     %q (public; payable() gets it as userData)\n", p.Data)
		}
	}
	switch p.Kind {
	case wallet.KindDeploy:
		fmt.Fprintf(e, "  Fee      about %s CS plus the constructor's execution (the node's fee table)\n", p.Fee.Fixed(6))
	case wallet.KindToken, wallet.KindCall, wallet.KindPay:
		fmt.Fprintf(e, "  Fee      about %s CS plus the execution (the node's current fees)\n", p.Fee.Fixed(6))
	default:
		fmt.Fprintf(e, "  Fee      %s CS expected (the node's current fee)\n", p.Fee.Fixed(6))
	}
	fmt.Fprintf(e, "  Max fee  %s CS signed: the most the network may charge\n", p.MaxFee.Amount().String())
	fmt.Fprintf(e, "  Node     %s, block %d, inner id %d\n", p.Node, p.Status.LastBlock, p.InnerID)
	fmt.Fprintf(e, "  Network  %s, %s\n", p.Network.Label(), echo.Terminal(p.NetworkCheck.Describe()))
}

// ---- history ----------------------------------------------------------------------------------

// addresses is a repeatable flag of addresses.
type addresses []string

func (l *addresses) String() string     { return strings.Join(*l, ",") }
func (l *addresses) Set(v string) error { *l = append(*l, v); return nil }

func (a *app) cmdHistory(ctx context.Context, args []string) error {
	fs := newFlagSet(a, "history")
	limit := fs.Int("limit", 0, "transactions to show")
	offset := fs.Int("offset", 0, "skip the newest N")
	csvFile := fs.String("csv", "", "write every transaction the source has to FILE")
	force := fs.Bool("force", false, "--csv: replace an existing file")
	tokens := fs.Bool("tokens", false, "token transfers instead of CS transactions")
	var extra addresses
	fs.Var(&extra, "token", "a token to include (repeatable)")
	pos, err := a.commandArgs(fs, args, 1)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("history <account|address>")
	}
	if *limit <= 0 {
		*limit = a.w.Config.HistoryPage
	}
	var extraKeys []ed25519.PublicKey
	for _, t := range extra {
		k, err := keys.ParseAddress(t)
		if err != nil {
			return fmt.Errorf("--token: %w", err)
		}
		extraKeys = append(extraKeys, k)
	}
	_, pub, err := a.w.Account(pos[0])
	if err != nil {
		return err
	}
	src := a.w.History()
	where := "this node"
	if src.Remote() {
		where = "that node"
		fmt.Fprintf(os.Stderr, "history source: %s; it receives only public addresses\n", src.Describe())
	}
	oldest, oldestErr := src.OldestBlock(ctx)
	if *csvFile != "" {
		all, tokenErr, err := history.AllWithTokens(ctx, src, pub, 100, extraKeys)
		if err != nil {
			return err
		}
		mode := os.O_WRONLY | os.O_CREATE | os.O_EXCL
		if *force {
			mode = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		}
		out, err := os.OpenFile(*csvFile, mode, 0o600)
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s exists: choose another name, or add --force to replace it", *csvFile)
		}
		if err != nil {
			return err
		}
		if err := history.WriteCSV(out, keys.Address(pub), all); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "%s written to %s (source: %s)\n", plural(len(all), "transaction"), *csvFile, src.Describe())
		if tokenErr != nil {
			fmt.Fprintf(os.Stderr, "warning: without token transfers: %s\n", echo.Terminal(tokenErr.Error()))
		}
		return nil
	}
	type row struct {
		history.Entry
		Amount string `json:"amount_cs"`
		Fee    string `json:"fee_cs"`
	}
	if *tokens {
		list, err := src.TokenTransfers(ctx, pub, extraKeys)
		if err != nil {
			return err
		}
		total := len(list)
		list = list[min(*offset, total):min(*offset+*limit, total)]
		if a.json {
			rows := []row{}
			for _, e := range list {
				rows = append(rows, row{e, e.Amount.String(), e.Fee.String()})
			}
			return a.printJSON(map[string]any{"address": keys.Address(pub), "source": src.Describe(), "remote": src.Remote(),
				"total": total, "offset": *offset, "token_transfers": rows})
		}
		fmt.Fprintf(a.out, "%s on %s: %s\n", keys.Address(pub), src.Describe(), plural(total, "token transfer"))
		for _, e := range list {
			arrow := map[string]string{"out": "→", "in": "←", "self": "↺"}[e.Direction]
			fmt.Fprintf(a.out, "%s  #%-11d %s %s  %s %s  %s\n", e.Time.Format("2006-01-02 15:04:05"), e.Block, arrow,
				e.Counterparty, signed(printable(e.TokenAmount)), printable(e.TokenCode), keys.Short(e.Token))
		}
		return nil
	}
	page, err := src.Page(ctx, pub, *offset, *limit)
	if err != nil {
		return err
	}
	if a.json {
		rows := []row{}
		for _, e := range page.Entries {
			rows = append(rows, row{e, e.Amount.String(), e.Fee.String()})
		}
		out := map[string]any{"address": keys.Address(pub), "source": src.Describe(), "remote": src.Remote(), "total": page.Total,
			"offset": page.Offset, "transactions": rows}
		if oldestErr == nil {
			out["oldest_block"] = oldest
		}
		return a.printJSON(out)
	}
	fmt.Fprintf(a.out, "%s on %s: %s in all", keys.Address(pub), src.Describe(), plural(page.Total, "transaction"))
	if oldestErr == nil {
		fmt.Fprintf(a.out, ", history from block %d on %s", oldest, where)
		if oldest > 1 {
			fmt.Fprint(a.out, " (pruned: older transactions are not here)")
		}
	}
	fmt.Fprintln(a.out)
	if len(page.Entries) == 0 && page.Total > 0 {
		fmt.Fprintln(a.out, "none of them is on "+where+": it may be pruned; ask a full node for older history (--history-node)")
	}
	for _, e := range page.Entries {
		arrow := map[string]string{"out": "→", "in": "←", "self": "↺"}[e.Direction]
		fmt.Fprintf(a.out, "%s  #%-11d %s %s  %s CS  fee %s  %s", e.Time.Format("2006-01-02 15:04:05"), e.Block, arrow,
			e.Counterparty, signed(e.Amount.Grouped()), e.Fee.Fixed(6), e.Kind)
		if !e.Until.IsZero() {
			fmt.Fprintf(a.out, " until %s", e.Until.Format("2006-01-02 15:04 UTC"))
		}
		if e.TokenAmount != "" {
			fmt.Fprintf(a.out, " %s %s", printable(e.TokenAmount), printable(e.TokenCode))
		}
		if e.Data != "" {
			fmt.Fprintf(a.out, "  data %s", strconv.Quote(e.Data))
		}
		fmt.Fprintln(a.out)
	}
	return nil
}

func signed(s string) string {
	if strings.HasPrefix(s, "-") || s == "0" {
		return s
	}
	return "+" + s
}
