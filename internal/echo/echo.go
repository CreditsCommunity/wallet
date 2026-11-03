// Package echo quotes user input in error messages without leaking secrets: a secret key or a
// seed phrase pasted into the wrong field must not reappear in an error, on screen or in --json.
package echo

import (
	"fmt"
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
