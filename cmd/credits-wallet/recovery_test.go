package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/CreditsCommunity/wallet/internal/keys"
)

// fixedSeed is the master seed of the recovery tests: its words appear nowhere in the wallet's
// own messages, so finding one in the output means the phrase leaked.
var fixedSeed = [keys.MasterSeedSize]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x01,
	0x12, 0x23, 0x34, 0x45, 0x56, 0x67, 0x78, 0x89, 0x9a, 0xab, 0xbc, 0xcd, 0xde, 0xef, 0xf0, 0x02}

// interactiveNew runs 'account new NAME' as on a terminal, with input typed after the check of
// words 1, 2 and 3. It returns the words, stdout, stderr and the exit code.
func interactiveNew(t *testing.T, name, typed string, extra ...string) ([]string, string, string, int) {
	t.Helper()
	seed := fixedSeed
	words := keys.Words(&seed)
	savedTerm, savedReader, savedSeed, savedPos := stdinIsTerminal, stdinReader, newMasterSeed, checkPositions
	t.Cleanup(func() {
		stdinIsTerminal, stdinReader, newMasterSeed, checkPositions = savedTerm, savedReader, savedSeed, savedPos
	})
	stdinIsTerminal = func() bool { return true }
	newMasterSeed = func() ([keys.MasterSeedSize]byte, error) { return fixedSeed, nil }
	checkPositions = func(int) []int { return []int{0, 1, 2} }
	stdinReader = bufio.NewReader(strings.NewReader(words[0] + "\n" + words[1] + "\n" + words[2] + "\n" + typed))
	out, errOut, code := runWallet2(t, append([]string{"account", "new", name}, extra...)...)
	return words, out, errOut, code
}

// recoveryEnv sets a home and a data folder (the data folder inside the home, as by default).
func recoveryEnv(t *testing.T) (home, data, pwFile string) {
	t.Helper()
	home = t.TempDir()
	data = filepath.Join(home, ".credits-wallet")
	os.MkdirAll(filepath.Join(data, "accounts"), 0o700)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CREDITS_WALLET_HOME", data)
	pwFile = filepath.Join(t.TempDir(), "pw")
	os.WriteFile(pwFile, []byte("pw\n"), 0o600)
	return
}

// display is how the phrase is shown, once, on the terminal.
func display(words []string) string {
	var b strings.Builder
	for i, w := range words {
		fmt.Fprintf(&b, "%3d. %-10s", i+1, w)
		if i%6 == 5 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// assertNoLeak: the phrase is shown once on stderr, and no word of it is anywhere else.
func assertNoLeak(t *testing.T, words []string, out, errOut string) {
	t.Helper()
	shown := display(words)
	if n := strings.Count(errOut, shown); n != 1 {
		t.Fatalf("the phrase is shown %d times, want once:\n%s", n, errOut)
	}
	rest := out + "\n" + strings.Replace(errOut, shown, "", 1)
	tokens := map[string]bool{}
	for _, f := range strings.FieldsFunc(strings.ToLower(rest), func(r rune) bool { return r < 'a' || r > 'z' }) {
		tokens[f] = true
	}
	for _, w := range words {
		if tokens[w] {
			t.Errorf("the word %q is in the output besides the one display:\n%s\n---\n%s", w, out, errOut)
		}
	}
}

func filesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

func TestRecoveryFileDefaultNo(t *testing.T) {
	home, _, pwFile := recoveryEnv(t)
	words, out, errOut, code := interactiveNew(t, "main", "\n", "--password-file", pwFile)
	if code != 0 || !strings.Contains(out, "address: ") {
		t.Fatalf("account new: %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "Save the 24 words to a file as well? [y/N]") {
		t.Errorf("the question was not asked:\n%s", errOut)
	}
	if f := filesIn(t, home); len(f) != 0 {
		t.Errorf("Enter wrote a file: %v", f)
	}
	assertNoLeak(t, words, out, errOut)
}

func TestRecoveryFileYesDefaultPath(t *testing.T) {
	home, _, pwFile := recoveryEnv(t)
	words, out, errOut, code := interactiveNew(t, "main", "y\n\n", "--password-file", pwFile)
	if code != 0 {
		t.Fatalf("account new: %d\n%s\n%s", code, out, errOut)
	}
	path := filepath.Join(home, "credits-recovery-main.txt")
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("no file at the default path: %v\n%s", err, errOut)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	if f := filesIn(t, home); len(f) != 1 {
		t.Errorf("files left in the folder: %v", f) // no temporary file left behind
	}
	text, _ := os.ReadFile(path)
	address := strings.TrimSpace(out[strings.Index(out, "address: ")+len("address: "):])
	for _, want := range []string{"account: main", "address: " + address, "anyone with these words controls the funds", " 1. " + words[0] + "\n", "24. " + words[23] + "\n"} {
		if !strings.Contains(string(text), want) {
			t.Errorf("the file lacks %q:\n%s", want, text)
		}
	}
	if !strings.Contains(errOut, path) || !strings.Contains(errOut, "copy it to paper or an offline USB key, then delete it from this computer") {
		t.Errorf("no path or advice after writing:\n%s", errOut)
	}
	assertNoLeak(t, words, out, errOut)
}

func TestRecoveryFileRefusals(t *testing.T) {
	home, data, pwFile := recoveryEnv(t)
	existing := filepath.Join(home, "existing.txt")
	os.WriteFile(existing, []byte("x"), 0o600)
	link := filepath.Join(home, "link.txt")
	if err := os.Symlink(filepath.Join(home, "target.txt"), link); err != nil {
		t.Skip("no symbolic links here:", err)
	}
	typed := "y\n" + filepath.Join(data, "words.txt") + "\n" + existing + "\n" + link + "\n"
	words, out, errOut, code := interactiveNew(t, "main", typed, "--password-file", pwFile)
	if code != 0 {
		t.Fatalf("account new: %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{"in the wallet's data folder", "already exists: refused", "is a symbolic link: refused", "No file: the words are only on your paper."} {
		if !strings.Contains(errOut, want) {
			t.Errorf("missing %q:\n%s", want, errOut)
		}
	}
	if _, err := os.Lstat(filepath.Join(home, "target.txt")); err == nil {
		t.Error("written through the link")
	}
	if b, _ := os.ReadFile(existing); string(b) != "x" {
		t.Error("the existing file was changed")
	}
	if _, err := os.Stat(filepath.Join(data, "words.txt")); err == nil {
		t.Error("written in the data folder")
	}
	assertNoLeak(t, words, out, errOut)
}

// With --json (or --words-file, --show-words) the question is not asked.
func TestRecoveryFileNotAskedWithJSON(t *testing.T) {
	home, _, pwFile := recoveryEnv(t)
	_, out, errOut, code := interactiveNew(t, "main", "y\n\n", "--password-file", pwFile, "--json")
	if code != 0 || strings.Contains(errOut, "Save the 24 words") || len(filesIn(t, home)) != 0 {
		t.Fatalf("with --json: %d %v\n%s\n%s", code, filesIn(t, home), out, errOut)
	}
}
