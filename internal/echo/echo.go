// Package echo quotes user input in error messages without leaking secrets: a secret key or a
// seed phrase pasted into the wrong field must not reappear in an error, on screen or in --json.
package echo

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxLen is the longest input quoted back: the length of an address (44 base58 characters).
const MaxLen = 44

// Quote returns the input quoted when it is short and on one word, otherwise only its length.
func Quote(s string) string {
	if utf8.RuneCountInString(s) > MaxLen || strings.ContainsAny(s, " \t\r\n") {
		return fmt.Sprintf("(input of %d characters, not shown)", utf8.RuneCountInString(s))
	}
	return fmt.Sprintf("%q", s)
}

// MaxPrintable is the longest text chosen by others (token names, user data) shown in full.
const MaxPrintable = 4096

// Printable shows text that a contract or another sender chose (token names and codes, contract
// results, user data) without letting it drive the terminal: control characters are escaped as in
// a Go string ("\x1b"), invalid UTF-8 as "\xff", and text past MaxPrintable bytes is cut with "…".
func Printable(s string) string {
	cut := false
	if len(s) > MaxPrintable {
		s, cut = s[:MaxPrintable], true
	}
	q := strconv.Quote(s)
	q = strings.ReplaceAll(q[1:len(q)-1], `\"`, `"`)
	q = strings.ReplaceAll(q, `\\`, `\`)
	if cut {
		q += "…"
	}
	return q
}
