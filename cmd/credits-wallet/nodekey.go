package main

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

// The node's key as an account (spec 4.4): account node-key shows it, account node-key new makes it
// when the node has none, account export NAME --format node writes an account's key in the node's
// format (--replace: in place of the node's key, kept as a backup).

const notNodeWallet = "this wallet is not a node's wallet: set node_key in the node profile (the package's wallet.toml does), or run the wallet in the node's folder"

// nodeKeyStatus shows the node's key account.
func (a *app) nodeKeyStatus() error {
	nk := a.w.NodeKey
	if nk == nil {
		return errors.New(notNodeWallet)
	}
	f, ferr := a.w.NodeAccount()
	running := wallet.NodeRunning(nk.Dir)
	pubErr := error(nil)
	if f != nil {
		pub, _ := f.PublicKey()
		pubErr = keys.CheckNodePublic(nk.Public, pub)
	}
	if a.json {
		out := map[string]any{"folder": nk.Dir, "source": nk.Source, "private_file": nk.Private, "public_file": nk.Public,
			"running": running != "", "account": a.w.NodeAccountName()}
		if running != "" {
			out["running_why"] = running
		}
		switch {
		case ferr != nil:
			out["error"] = ferr.Error()
		case f == nil:
			out["missing"] = true
		default:
			out["address"] = f.Public
			if pubErr != nil {
				out["public_file_error"] = pubErr.Error()
			}
		}
		return a.printJSON(out)
	}
	fmt.Fprintf(a.out, "node folder : %s (%s)\n", nk.Dir, nk.Source)
	switch {
	case ferr != nil:
		fmt.Fprintf(a.out, "key         : %s: %v\n", nk.Private, ferr)
	case f == nil:
		fmt.Fprintf(a.out, "key         : none yet: 'account node-key new' makes it from a 24-word phrase\n")
	default:
		fmt.Fprintf(a.out, "key         : %s (in clear, as the node needs it)\n", nk.Private)
		fmt.Fprintf(a.out, "account     : %s (NODE KEY, the default account)\n", f.Name)
		fmt.Fprintf(a.out, "address     : %s\n", f.Public)
		if pubErr != nil {
			fmt.Fprintf(a.out, "public file : %v\n", pubErr)
		} else {
			fmt.Fprintf(a.out, "public file : %s matches\n", nk.Public)
		}
	}
	if running != "" {
		fmt.Fprintf(a.out, "node        : running (%s)\n", running)
	} else {
		fmt.Fprintf(a.out, "node        : not seen running\n")
	}
	if f != nil {
		fmt.Fprintln(os.Stderr, wallet.NodeKeyWarning)
	}
	return ferr
}

// nodeKeyNew makes the node's key from a new 24-word phrase and writes it in the node's folder (the
// node's wallet's, or --out), in the node's format: the node uses it at its next start.
func (a *app) nodeKeyNew(out, wordsFile string, showWords bool) error {
	dir := out
	if dir == "" {
		if a.w.NodeKey == nil {
			return errors.New(notNodeWallet + "; or give the node's folder with --out")
		}
		dir = a.w.NodeKey.Dir
	}
	if filepath.Base(dir) == keys.NodePrivateFile {
		dir = filepath.Dir(dir)
	}
	real, err := wallet.CheckNodeKeyTarget(dir, false)
	if err != nil {
		return err
	}
	if !stdinIsTerminal() && wordsFile == "" && !showWords {
		return errors.New("no terminal to show the seed phrase on: add --words-file FILE (or --show-words to print it); nothing was written")
	}
	if wordsFile != "" {
		if wordsFile, err = a.w.CheckRecoveryPath(wordsFile, real); err != nil {
			return fmt.Errorf("--words-file: %w", err)
		}
	}
	if !a.json {
		fmt.Fprintf(os.Stderr, "The node's key for %s, from a new 24-word phrase: the paper backup of the node's identity.\n", real)
		fmt.Fprintln(os.Stderr, wallet.NodeKeyWarning)
		fmt.Fprintln(os.Stderr)
	}
	p, err := a.newPhrase(wallet.NodeAccount, wordsFile, showWords, real)
	if err != nil {
		return err
	}
	defer p.wipe()
	undo, err := p.writeFiles(wallet.NodeAccount, wordsFile)
	if err != nil {
		return err
	}
	if err := wallet.WriteNodeKey(real, p.key); err != nil {
		undo()
		return fmt.Errorf("%w; nothing was written", err)
	}
	address := keys.Address(p.key.Public().(ed25519.PublicKey))
	if a.json {
		out := map[string]any{"folder": real, "private_file": filepath.Join(real, keys.NodePrivateFile),
			"public_file": filepath.Join(real, keys.NodePublicFile), "address": address}
		if showWords {
			out["words"] = strings.Join(p.words, " ")
		}
		if wordsFile != "" {
			out["words_file"] = wordsFile
		}
		return a.printJSON(out)
	}
	p.report(a, showWords, wordsFile)
	fmt.Fprintf(a.out, "node key: %s and %s (mode 600)\naddress : %s\n", filepath.Join(real, keys.NodePrivateFile), keys.NodePublicFile, address)
	fmt.Fprintln(os.Stderr, "The node uses this key at its next start. The 24 words restore it (account import, or the node's --seed).")
	return nil
}

// exportNodeFormat writes an account's key in the node's format in a node's folder (NodePrivate.txt
// and NodePublic.txt): never over a key the node has, unless replace, which keeps the old files as
// backups. The node must be stopped.
func (a *app) exportNodeFormat(name, out string, replace, yes bool) error {
	if out == "" {
		return usagef("--format node needs --out FOLDER, the node's folder (csnode)")
	}
	f, err := a.w.LoadAccount(name)
	if err != nil {
		return err
	}
	if f.IsNodeKey() {
		return fmt.Errorf("%s is the node's key already (%s)", f.Name, f.NodeKey)
	}
	dir := out
	if filepath.Base(dir) == keys.NodePrivateFile {
		dir = filepath.Dir(dir)
	}
	real, err := wallet.CheckNodeKeyTarget(dir, replace)
	if err != nil {
		return err
	}
	current := ""
	if replace {
		if k, err := keys.ReadNodeKey(filepath.Join(real, keys.NodePrivateFile)); err == nil {
			current = keys.Address(k.Public().(ed25519.PublicKey))
			keys.Zero(k)
		} else if !errors.Is(err, os.ErrNotExist) {
			current = "unreadable: " + err.Error()
		}
	}
	fmt.Fprintf(os.Stderr, "This writes the key of %s (%s) in the node's format in %s.\n", f.Name, f.Public, real)
	fmt.Fprintln(os.Stderr, wallet.NodeIdentityNote)
	if replace && current != "" {
		fmt.Fprintf(os.Stderr, "WARNING: this REPLACES the node's key. The node's current identity (%s) stops being this node's:\n", current)
		fmt.Fprintln(os.Stderr, "delegations to it stay with that address, and the node starts as a new node. The old files are kept as")
		fmt.Fprintln(os.Stderr, "NodePrivate.txt.bak-DATE and NodePublic.txt.bak-DATE (mode 600): keep them until you are sure.")
	}
	fmt.Fprintln(os.Stderr, "The key file stays in clear in the node's folder, as the node needs it. "+wallet.NodeKeyWarning)
	if !yes {
		if !stdinIsTerminal() {
			return errors.New("refused without a terminal to confirm on: add --yes")
		}
		word := "export"
		if replace && current != "" {
			word = "replace"
		}
		if ans, _ := ask(fmt.Sprintf("Type '%s' to continue: ", word)); ans != word {
			return errors.New("cancelled: nothing was written")
		}
	}
	pw, err := a.readPassword("Password of "+f.Name+": ", false)
	if err != nil {
		return err
	}
	key, err := f.Decrypt(pw)
	keys.Zero(pw)
	if err != nil {
		return err
	}
	defer keys.Zero(key)
	var backups []string
	if replace {
		backups, err = wallet.ReplaceNodeKey(real, key, time.Now())
	} else {
		err = wallet.WriteNodeKey(real, key)
	}
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(map[string]any{"name": f.Name, "address": f.Public, "folder": real, "backups": backups})
	}
	fmt.Fprintf(a.out, "node key: %s and %s (mode 600), the key of %s\naddress : %s\n", filepath.Join(real, keys.NodePrivateFile), keys.NodePublicFile, f.Name, f.Public)
	for _, b := range backups {
		fmt.Fprintf(a.out, "backup  : %s\n", b)
	}
	fmt.Fprintln(os.Stderr, "The node uses this key at its next start.")
	return nil
}
