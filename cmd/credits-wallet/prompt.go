package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"golang.org/x/term"

	"github.com/CreditsCommunity/wallet/internal/keys"
)

// The terminal: passwords are read without echo from the terminal, or from --password-file.

var stdinReader = bufio.NewReader(os.Stdin)

// stdinIsTerminal tells whether the standard input is a terminal (replaced in tests).
var stdinIsTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// readPassword reads a password: the first line of --password-file, or from the terminal.
// confirm asks twice (new key files).
func (a *app) readPassword(prompt string, confirm bool) ([]byte, error) {
	if a.passwordFile != "" {
		return readPasswordFile(a.passwordFile)
	}
	if !stdinIsTerminal() {
		return nil, errors.New("no terminal to ask the password on: use --password-file")
	}
	fmt.Fprint(os.Stderr, prompt)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return nil, err
	}
	if len(pw) == 0 {
		return nil, errors.New("empty password")
	}
	if confirm {
		fmt.Fprint(os.Stderr, "Repeat the password: ")
		again, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		defer keys.Zero(again)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(pw, again) {
			keys.Zero(pw)
			return nil, errors.New("the passwords differ")
		}
	}
	return pw, nil
}

// readPasswordFile reads the first line of a password file. The file holds the password in clear:
// on Linux and macOS it must be accessible by its owner only, or it is refused.
func readPasswordFile(path string) ([]byte, error) {
	if runtime.GOOS != "windows" {
		st, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if st.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("%s is accessible by other users (mode %v): refused, chmod 600 it first", path, st.Mode().Perm())
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	defer keys.Zero(data)
	line, _, _ := bytes.Cut(data, []byte("\n"))
	line = bytes.TrimSuffix(line, []byte("\r"))
	if len(line) == 0 {
		return nil, fmt.Errorf("%s: the first line (the password) is empty", path)
	}
	return append([]byte(nil), line...), nil
}

// readSecret reads a secret line without echo (replaced in tests).
var readSecret = readSecretLine

// readSecretLine reads a secret (key or phrase) from the terminal without echo.
func readSecretLine(prompt string) (string, error) {
	if !stdinIsTerminal() {
		line, err := stdinReader.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	defer keys.Zero(b)
	return strings.TrimSpace(string(b)), err
}

// ask reads a visible answer line from the terminal.
func ask(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	line, err := stdinReader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
