package keys

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mr-tron/base58"
)

// The throwaway key of the node's sample clients (seed bytes 0, 1, …, 31): public, never fund it.
const (
	testSecret  = "1GMkH3brNXiNNs1tiFZHu4yZSRrzJwxi5wB9bHFtMikjwpAW9DMZzU2Pqakc5it8X3N5vPmqdN7KF4CCUpmKhq"
	testAddress = "FAe4sisG95oZ42w7buUn5qEE4TAnfTTFPiguZUHmhiF"
)

type vector struct {
	MasterSeed string `json:"master_seed"`
	Words      string `json:"words"`
	Public     string `json:"public"`
	Secret     string `json:"secret"`
}

// testdata/seed-vectors.json was printed by testdata/seed-vectors.cpp, linked with the node's own
// cscrypto and libsodium.
func loadVectors(t *testing.T) []vector {
	data, err := os.ReadFile("testdata/seed-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v []vector
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	if len(v) < 6 {
		t.Fatalf("only %d vectors", len(v))
	}
	return v
}

func TestSeedPhraseVectorsFromTheNode(t *testing.T) {
	for _, v := range loadVectors(t) {
		var master [MasterSeedSize]byte
		raw, _ := hex.DecodeString(v.MasterSeed)
		copy(master[:], raw)
		if got := strings.Join(Words(&master), " "); got != v.Words {
			t.Errorf("seed %s: words\n got %s\nwant %s", v.MasterSeed, got, v.Words)
		}
		back, err := MasterSeedFromWords(SplitPhrase(v.Words))
		if err != nil || back != master {
			t.Errorf("seed %s: back from words: %x %v", v.MasterSeed, back, err)
		}
		key := DeriveKey(&master)
		if hex.EncodeToString(key) != v.Secret || hex.EncodeToString(key[32:]) != v.Public {
			t.Errorf("seed %s: key %x, want %s", v.MasterSeed, key, v.Secret)
		}
		k2, err := FromWords(SplitPhrase(strings.ToUpper(v.Words)))
		if err != nil || !bytes.Equal(k2, key) {
			t.Errorf("FromWords: %v", err)
		}
	}
}

func TestSeedPhraseChecksumRejected(t *testing.T) {
	v := loadVectors(t)[3]
	words := SplitPhrase(v.Words)
	// two words swapped
	swapped := append([]string(nil), words...)
	swapped[0], swapped[1] = swapped[1], swapped[0]
	if _, err := MasterSeedFromWords(swapped); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("swapped words: %v", err)
	}
	// the last word (3 seed bits + the 8 checksum bits) replaced
	last := append([]string(nil), words...)
	last[23] = "abandon"
	if _, err := MasterSeedFromWords(last); err == nil {
		t.Error("wrong last word accepted")
	}
	// every other last word with the same seed bits but another checksum is refused too
	accepted := 0
	for _, w := range wordList {
		last[23] = w
		if _, err := MasterSeedFromWords(last); err == nil {
			accepted++
		}
	}
	if accepted != 8 { // 3 free seed bits: 8 seeds, each with exactly one valid checksum
		t.Errorf("%d last words accepted, want 8", accepted)
	}
	if _, err := MasterSeedFromWords(words[:23]); err == nil {
		t.Error("23 words accepted")
	}
	bad := append([]string(nil), words...)
	bad[5] = "notaword"
	if _, err := MasterSeedFromWords(bad); err == nil || !strings.Contains(err.Error(), "word 6") {
		t.Errorf("unknown word: %v", err)
	}
}

func TestNewMasterSeedRoundTrip(t *testing.T) {
	m, err := NewMasterSeed()
	if err != nil {
		t.Fatal(err)
	}
	words := Words(&m)
	k1 := DeriveKey(&m)
	k2, err := FromWords(words)
	if err != nil || !bytes.Equal(k1, k2) {
		t.Fatalf("round trip: %v", err)
	}
}

func TestFromBase58(t *testing.T) {
	key, err := FromBase58(testSecret)
	if err != nil || Address(key.Public().(ed25519.PublicKey)) != testAddress {
		t.Fatalf("64-byte secret: %v", err)
	}
	seed := base58.Encode(key.Seed())
	k2, err := FromBase58(seed)
	if err != nil || !bytes.Equal(k2, key) {
		t.Fatalf("32-byte seed: %v", err)
	}
	// a 64-byte secret whose public half is another key's
	other := ed25519.NewKeyFromSeed(make([]byte, 32))
	mixed := append(append([]byte(nil), key.Seed()...), other[32:]...)
	if _, err := FromBase58(base58.Encode(mixed)); err == nil {
		t.Error("mismatched halves accepted")
	}
	for _, bad := range []string{"", "0OIl", base58.Encode(make([]byte, 33))} {
		if _, err := FromBase58(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestOfficialJSON(t *testing.T) {
	key, _ := FromBase58(testSecret)
	data := OfficialJSON(key)
	want := `{"key":{"public":"` + testAddress + `","private":"` + testSecret + `"}}`
	if string(data) != want {
		t.Fatalf("OfficialJSON = %s", data)
	}
	back, err := FromOfficialJSON(append([]byte("  \n"), data...))
	if err != nil || !bytes.Equal(back, key) {
		t.Fatalf("FromOfficialJSON: %v", err)
	}
	if !LooksLikeOfficialJSON(" " + want) {
		t.Error("LooksLikeOfficialJSON")
	}
	other := ed25519.NewKeyFromSeed(make([]byte, 32))
	wrongPublic := `{"key":{"public":"` + Address(other.Public().(ed25519.PublicKey)) + `","private":"` + testSecret + `"}}`
	if _, err := FromOfficialJSON([]byte(wrongPublic)); err == nil {
		t.Error("public that does not match the private key accepted")
	}
	mixed := base58.Encode(append(append([]byte(nil), key.Seed()...), other[32:]...))
	inconsistent := `{"key":{"public":"` + Address(other.Public().(ed25519.PublicKey)) + `","private":"` + mixed + `"}}`
	if _, err := FromOfficialJSON([]byte(inconsistent)); err == nil {
		t.Error("private key with mismatched halves accepted")
	}
	for _, bad := range []string{`{}`, `{"key":{"public":"x","private":"y"}}`, `[1]`,
		`{"key":{"public":"` + testAddress + `","private":"` + base58.Encode(key.Seed()) + `"}}`} {
		if _, err := FromOfficialJSON([]byte(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

// fastKDF keeps the tests quick (the lowest parameters accepted); the format and code path are
// those of DefaultKDF.
var fastKDF = KDFParams{Name: "argon2id", Time: 1, MemoryKiB: 64 * 1024, Threads: 1}

func TestStoreRoundTrip(t *testing.T) {
	store := Store{Dir: filepath.Join(t.TempDir(), "accounts")}
	key, _ := FromBase58(testSecret)
	f, err := Encrypt("main", key, []byte("correct horse"), fastKDF)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(f); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(f); err == nil {
		t.Error("an existing account was overwritten")
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(store.path("main"))
		if st.Mode().Perm() != 0o600 {
			t.Errorf("key file mode %v", st.Mode().Perm())
		}
		dir, _ := os.Stat(store.Dir)
		if dir.Mode().Perm() != 0o700 {
			t.Errorf("key folder mode %v", dir.Mode().Perm())
		}
	}
	raw, _ := os.ReadFile(store.path("main"))
	if bytes.Contains(raw, []byte(testSecret)) || bytes.Contains(raw, []byte(base58.Encode(key.Seed()))) {
		t.Fatal("the secret is in the file in clear")
	}
	loaded, err := store.Load("main")
	if err != nil || loaded.Public != testAddress {
		t.Fatalf("Load: %v", err)
	}
	got, err := loaded.Decrypt([]byte("correct horse"))
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("Decrypt: %v", err)
	}
	if _, err := loaded.Decrypt([]byte("wrong horse")); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("wrong password: %v", err)
	}
	list, problems, err := store.List()
	if err != nil || len(problems) != 0 || len(list) != 1 || list[0].Name != "main" {
		t.Errorf("List: %v %v %v", list, problems, err)
	}
	if _, err := store.Load("nobody"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Load(nobody): %v", err)
	}
}

func TestStoreTamperingDetected(t *testing.T) {
	key, _ := FromBase58(testSecret)
	f, err := Encrypt("main", key, []byte("pw"), fastKDF)
	if err != nil {
		t.Fatal(err)
	}
	salt1 := f.KDF.Salt
	f2, _ := Encrypt("main", key, []byte("pw"), fastKDF)
	if f2.KDF.Salt == salt1 || f2.Nonce == f.Nonce {
		t.Error("salt or nonce reused between files")
	}
	other := ed25519.NewKeyFromSeed(make([]byte, 32))
	changes := map[string]func(*File){
		"public":  func(c *File) { c.Public = Address(other.Public().(ed25519.PublicKey)) },
		"time":    func(c *File) { c.KDF.Time = 2 },
		"salt":    func(c *File) { c.KDF.Salt = f2.KDF.Salt },
		"nonce":   func(c *File) { c.Nonce = f2.Nonce },
		"version": func(c *File) { c.Version = 2 },
		"bounds":  func(c *File) { c.KDF.MemoryKiB = 1 << 31 },
	}
	for what, change := range changes {
		c := *f
		change(&c)
		if _, err := c.Decrypt([]byte("pw")); err == nil {
			t.Errorf("changed %s: decrypted", what)
		}
	}
	if _, err := Encrypt("main", key, nil, fastKDF); err == nil {
		t.Error("empty password accepted")
	}
}

func TestDefaultKDF(t *testing.T) {
	if DefaultKDF.Time != 3 || DefaultKDF.MemoryKiB != 256*1024 || DefaultKDF.Threads != 4 || DefaultKDF.Name != "argon2id" {
		t.Errorf("DefaultKDF = %+v, spec 4.2 says t=3, 256 MiB, p=4", DefaultKDF)
	}
	if testing.Short() {
		return
	}
	key, _ := FromBase58(testSecret)
	f, err := Encrypt("main", key, []byte("pw"), DefaultKDF)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := f.Decrypt([]byte("pw")); err != nil || !bytes.Equal(got, key) {
		t.Fatal(err)
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"main", "a", "test-contracts", "s_1.2"} {
		if ValidName(ok) != nil {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "../x", "a/b", "-x", ".hidden", "x.", "with space", strings.Repeat("a", 33), "con\x00"} {
		if ValidName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestKDFBounds(t *testing.T) {
	key, _ := FromBase58(testSecret)
	for name, p := range map[string]KDFParams{
		"memory below 64 MiB": {Name: "argon2id", Time: 1, MemoryKiB: 64*1024 - 1, Threads: 1},
		"memory above 1 GiB":  {Name: "argon2id", Time: 1, MemoryKiB: 1024*1024 + 1, Threads: 1},
		"time 0":              {Name: "argon2id", Time: 0, MemoryKiB: 64 * 1024, Threads: 1},
		"time 17":             {Name: "argon2id", Time: 17, MemoryKiB: 64 * 1024, Threads: 1},
		"threads 0":           {Name: "argon2id", Time: 1, MemoryKiB: 64 * 1024, Threads: 0},
		"threads 33":          {Name: "argon2id", Time: 1, MemoryKiB: 64 * 1024, Threads: 33},
		"argon2i":             {Name: "argon2i", Time: 3, MemoryKiB: 256 * 1024, Threads: 4},
	} {
		if _, err := Encrypt("main", key, []byte("pw"), p); err == nil {
			t.Errorf("%s: written", name)
		}
		// a file claiming these parameters is refused on reading too
		f, _ := Encrypt("main", key, []byte("pw"), fastKDF)
		f.KDF.Time, f.KDF.MemoryKiB, f.KDF.Threads, f.KDF.Name = p.Time, p.MemoryKiB, p.Threads, p.Name
		if _, err := f.Decrypt([]byte("pw")); err == nil || errors.Is(err, ErrWrongPassword) {
			t.Errorf("%s: read with %v", name, err)
		}
	}
}

func TestSaveAtomicAndListReportsProblems(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	key, _ := FromBase58(testSecret)
	f, _ := Encrypt("main", key, []byte("pw"), fastKDF)
	if err := store.Save(f); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(store.FilePath("main"))
	other, _ := Encrypt("main", ed25519.NewKeyFromSeed(make([]byte, 32)), []byte("pw"), fastKDF)
	if err := store.Save(other); err == nil {
		t.Fatal("an existing account was replaced")
	}
	after, _ := os.ReadFile(store.FilePath("main"))
	if !bytes.Equal(before, after) {
		t.Fatal("the existing key file changed")
	}
	entries, _ := os.ReadDir(store.Dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temporary file left: %s", e.Name())
		}
	}
	os.WriteFile(filepath.Join(store.Dir, "broken.json"), []byte("{not json"), 0o600)
	os.WriteFile(filepath.Join(store.Dir, "-bad name.json"), []byte("{}"), 0o600)
	list, problems, err := store.List()
	if err != nil || len(list) != 1 || len(problems) != 2 {
		t.Fatalf("List: %d files, problems %v, %v", len(list), problems, err)
	}
	if runtime.GOOS != "windows" {
		os.Chmod(filepath.Join(store.Dir, "broken.json"), 0)
		if os.Getuid() != 0 {
			if _, problems, _ := store.List(); len(problems) != 2 {
				t.Errorf("unreadable file not reported: %v", problems)
			}
		}
	}
}

func TestErrorsDoNotEchoSecrets(t *testing.T) {
	_, err := ParseAddress(testSecret)
	if err == nil || strings.Contains(err.Error(), testSecret[:20]) {
		t.Errorf("ParseAddress echoed the input: %v", err)
	}
	words := SplitPhrase(loadVectors(t)[3].Words)
	words[5] = "zzzsecretword"
	if _, err := MasterSeedFromWords(words); err == nil || strings.Contains(err.Error(), "zzzsecretword") {
		t.Errorf("MasterSeedFromWords echoed a word: %v", err)
	}
	if err := ValidName(testSecret); err == nil || strings.Contains(err.Error(), testSecret[:20]) {
		t.Errorf("ValidName echoed the input: %v", err)
	}
}
