// Command credits-wallet is a terminal wallet for the CREDITS network that runs next to a node.
//
//	credits-wallet                      the terminal interface
//	credits-wallet status
//	credits-wallet account new|import|export|list
//	credits-wallet balance [account|address...]
//	credits-wallet send <account> <to> <amount> [--data TEXT] [--yes]
//	credits-wallet delegate|undelegate <account> <node> <amount> [--until DATE]
//	credits-wallet delegations <account|address>
//	credits-wallet tokens <account|address>
//	credits-wallet token info|send ...
//	credits-wallet history <account|address> [--tokens] [--csv FILE]
//
// See README.md and docs/spec.md.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/echo"
	"github.com/CreditsCommunity/wallet/internal/tui"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

// version is set at build time: -ldflags "-X main.version=..."
var version = "dev"

const usageText = `credits-wallet: a terminal wallet for the CREDITS network, next to a node.

usage: credits-wallet [global flags] [command] [arguments]

  (no command)                          the terminal interface
  status                                the node: last block, round, synced, network
  account new <name> [--words-file FILE | --show-words]
                                        create an account; on a terminal its 24-word phrase is
                                        shown once and checked; without one, the phrase goes to
                                        FILE (new, mode 600) or, with --show-words, the output
  account import <name> [file]          import a secret key (base58), a 24-word phrase or the
                                        official wallets' keys JSON; asks for it without a file
  account export <name> [--format encrypted|base58|official] [--out FILE]
  account list                          the accounts and their addresses
  balance [account|address...]          CS balances (all accounts without arguments)
  send <account> <to> <amount> [--data TEXT] [--yes] [--wait SECONDS]
                                        sign and send CS, with optional user data (a text,
                                        public on the chain); on MAINNET it spends real CS
  delegate <account> <node> <amount> [--until DATE] [--yes]
                                        delegate CS to a node, without a time limit or until
                                        DATE (UTC: 2027-01-31, "2027-01-31 18:00" or RFC 3339)
  undelegate <account> <node> <amount> [--yes]
                                        withdraw CS from a delegation without a time limit
  delegations <account|address>         delegations made and received, with their expiry
  tokens <account|address>              token balances (the node needs [api] tokens_cache=true)
  token info <token>                    a token: code, name, supply, decimals
  token balance <token> <account|address>
                                        one token balance, read from the contract
  token send <account> <token> <to> <amount> [--yes]
                                        sign and send tokens: a call of the token's transfer()
  history <account|address> [--limit N] [--offset N] [--tokens] [--token ADDR]
          [--csv FILE [--force]]        CS transactions, or token transfers with --tokens; the
                                        CSV has both (--token adds a token not held any more)
  config                                print an example configuration file
  version

global flags (anywhere on the line):
  --config FILE         configuration file (default: <data folder>/config.toml)
  --profile NAME        node profile of the configuration
  --node HOST:PORT      node API address (an address no profile declares counts as MAINNET)
  --history-node HOST:PORT
                        read the history from this remote full node (read-only, never used
                        to send); "local" for the node itself; default: the profile's
                        history_node
  --json                machine-readable output
  --password-file FILE  read the password from the first line of FILE (unattended use: the
                        password is on disk in clear, keep the file chmod 600)

data folder: $CREDITS_WALLET_HOME, or ~/.credits-wallet (Linux, macOS), %APPDATA%\credits-wallet (Windows)
`

type app struct {
	configPath   string
	profile      string
	nodeAddr     string
	historyNode  string
	json         bool
	passwordFile string

	dataDir string
	w       *wallet.Wallet
	out     io.Writer
}

// globalFlags registers the flags every command accepts.
func (a *app) globalFlags(fs *flag.FlagSet) {
	fs.StringVar(&a.configPath, "config", "", "configuration file")
	fs.StringVar(&a.profile, "profile", "", "node profile")
	fs.StringVar(&a.nodeAddr, "node", "", "node API HOST:PORT")
	fs.StringVar(&a.historyNode, "history-node", "", "history from a remote full node HOST:PORT")
	fs.BoolVar(&a.json, "json", false, "JSON output")
	fs.StringVar(&a.passwordFile, "password-file", "", "password file")
}

// parse parses flags placed anywhere among the positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func newFlagSet(a *app, name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	a.globalFlags(fs)
	return fs
}

// open loads the configuration and the wallet.
func (a *app) open() error {
	dir, err := config.DataDir()
	if err != nil {
		return err
	}
	a.dataDir = dir
	path, explicit := a.configPath, a.configPath != ""
	if !explicit {
		path = config.DefaultPath(dir)
	}
	cfg, err := config.Load(path, explicit)
	if err != nil {
		return err
	}
	p, err := cfg.Resolve(a.profile, a.nodeAddr)
	if err != nil {
		return err
	}
	if p, err = config.ResolveHistory(p, a.historyNode); err != nil {
		return err
	}
	a.w = wallet.New(cfg, p, dir)
	for _, warning := range config.PermissionWarnings(dir) {
		fmt.Fprintln(os.Stderr, "warning:", warning)
	}
	return nil
}

func (a *app) printJSON(v any) error {
	enc := json.NewEncoder(a.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	a := &app{out: os.Stdout}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	cmd := findCommand(args)
	if cmd == "help" || hasHelpFlag(args) {
		fmt.Print(usageText)
		return 0
	}
	if cmd == "version" {
		fmt.Println("credits-wallet", version)
		return 0
	}
	if cmd == "config" {
		fmt.Print(config.Example)
		return 0
	}

	// each command parses the whole line with its own flags
	err := a.dispatch(ctx, cmd, args)
	if err != nil {
		var u usageError
		if errors.As(err, &u) {
			fmt.Fprintf(os.Stderr, "credits-wallet: %v\n\n%s", err, usageText)
			return 2
		}
		if a.json {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"error": err.Error()})
		}
		fmt.Fprintln(os.Stderr, "credits-wallet:", err)
		return 1
	}
	return 0
}

// valueFlags are the flags that take a value (the next argument, unless written --flag=value).
var valueFlags = map[string]bool{"config": true, "profile": true, "node": true, "password-file": true,
	"format": true, "out": true, "csv": true, "limit": true, "offset": true, "wait": true, "words-file": true,
	"history-node": true, "data": true, "until": true, "token": true}

// findCommand returns the first argument that is neither a flag nor a flag's value.
func findCommand(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			return arg
		}
		name := strings.TrimLeft(arg, "-")
		if !strings.Contains(name, "=") && valueFlags[name] {
			i++
		}
	}
	return ""
}

func hasHelpFlag(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "-help" {
			return true
		}
	}
	return false
}

type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

func usagef(format string, args ...any) error { return usageError{fmt.Sprintf(format, args...)} }

func (a *app) dispatch(ctx context.Context, cmd string, args []string) error {
	switch cmd {
	case "":
		fs := newFlagSet(a, "tui")
		if _, err := parse(fs, args); err != nil {
			return usagef("%v", err)
		}
		if err := a.open(); err != nil {
			return err
		}
		return tui.Run(a.w, a.dataDir)
	case "status":
		return a.cmdStatus(ctx, args)
	case "account":
		return a.cmdAccount(ctx, args)
	case "balance":
		return a.cmdBalance(ctx, args)
	case "send":
		return a.cmdSend(ctx, args)
	case "history":
		return a.cmdHistory(ctx, args)
	case "delegate", "undelegate":
		return a.cmdDelegate(ctx, cmd, args)
	case "delegations":
		return a.cmdDelegations(ctx, args)
	case "tokens":
		return a.cmdTokens(ctx, args)
	case "token":
		return a.cmdToken(ctx, args)
	}
	return usagef("unknown command %s", echo.Quote(cmd))
}

// commandArgs parses a command's line with its flags and returns the positional arguments after
// the command words (skip: how many words name the command).
func (a *app) commandArgs(fs *flag.FlagSet, args []string, skip int) ([]string, error) {
	pos, err := parse(fs, args)
	if err != nil {
		return nil, usagef("%v", err)
	}
	if len(pos) < skip {
		return nil, usagef("missing arguments")
	}
	return pos[skip:], a.open()
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// printable keeps text that a contract or another sender chose (token names, user data) from
// carrying terminal escape sequences: control characters are shown escaped.
func printable(s string) string {
	q := strconv.Quote(s)
	q = q[1 : len(q)-1]
	return strings.ReplaceAll(q, `\"`, `"`)
}
