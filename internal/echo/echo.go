// Package echo quotes user input in error messages without leaking secrets: a secret key or a
// seed phrase pasted into the wrong field must not reappear in an error, on screen or in --json.
package echo

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
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
// a Go string ("\x1b"), invalid UTF-8 as "\xff", a backslash as "\\" (so the escapes are
// unambiguous), and text past MaxPrintable bytes is cut with "…".
func Printable(s string) string {
	cut := false
	if len(s) > MaxPrintable {
		s, cut = s[:MaxPrintable], true
	}
	q := strconv.Quote(s)
	// a backslash stays escaped ("\\"), so that a text "\x1b" never looks like an escaped ESC
	q = strings.ReplaceAll(q[1:len(q)-1], `\"`, `"`)
	if cut {
		q += "…"
	}
	return q
}

// JSON makes encoded JSON safe to print on a terminal: encoding/json escapes the C0 controls but
// not the C1 controls (U+0080-U+009F, which some terminals obey) nor the Unicode format characters
// (category Cf, such as U+202E RIGHT-TO-LEFT OVERRIDE, which reorder what is shown). They are
// written as \uXXXX escapes (surrogate pairs above U+FFFF): the same JSON for a parser. Such runes
// only occur inside JSON strings, where the escape is valid.
func JSON(b []byte) []byte {
	if utf8.Valid(b) && !strings.ContainsFunc(string(b), unsafeRune) {
		return b
	}
	var out strings.Builder
	for _, r := range string(b) {
		if !unsafeRune(r) {
			out.WriteRune(r)
			continue
		}
		if r > 0xFFFF {
			r1, r2 := utf16.EncodeRune(r)
			fmt.Fprintf(&out, "\\u%04x\\u%04x", r1, r2)
		} else {
			fmt.Fprintf(&out, "\\u%04x", r)
		}
	}
	return []byte(out.String())
}

func unsafeRune(r rune) bool { return (r >= 0x80 && r <= 0x9f) || unicode.Is(unicode.Cf, r) }

// Terminal makes text safe to print on a terminal and keeps it readable: control characters
// (C0, C1), Unicode format characters (such as U+202E) and invalid UTF-8 are escaped, everything else
// (backslashes in Windows paths included) is kept. It is for the output points (error lines),
// where the text is the wallet's own with parts already passed through Printable.
func Terminal(s string) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case r == utf8.RuneError && !strings.HasPrefix(s[i:], "\uFFFD"):
			fmt.Fprintf(&b, "\\x%02x", s[i])
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
