package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mr-tron/base58"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

// The key store: one JSON file per account, <dir>/<name>.json, mode 0600, holding the 32-byte
// ed25519 seed encrypted with XChaCha20-Poly1305 under a key derived from the password with
// Argon2id. The associated data binds the format version, the KDF parameters and the public key
// to the ciphertext, so none of them can be changed without the password check failing.

// FileVersion is the version of the key file format.
const FileVersion = 1

// KDFParams are the Argon2id parameters of a key file.
type KDFParams struct {
	Name      string `json:"name"` // "argon2id"
	Time      uint32 `json:"time"`
	MemoryKiB uint32 `json:"memory_kib"`
	Threads   uint8  `json:"threads"`
	Salt      string `json:"salt"` // base64, 16 random bytes
}

// DefaultKDF is what new files use (spec 4.2): time 3, 256 MiB, 4 threads.
var DefaultKDF = KDFParams{Name: "argon2id", Time: 3, MemoryKiB: 256 * 1024, Threads: 4}

// File is the JSON of a key file.
type File struct {
	Version    int       `json:"version"`
	Name       string    `json:"name"`
	Public     string    `json:"public"` // base58 address, to list accounts without the password
	KDF        KDFParams `json:"kdf"`
	Cipher     string    `json:"cipher"` // "xchacha20-poly1305"
	Nonce      string    `json:"nonce"`  // base64, 24 random bytes
	Ciphertext string    `json:"ciphertext"`
	Created    time.Time `json:"created"`
}

const saltSize = 16

var (
	// ErrWrongPassword is returned when the file does not decrypt.
	ErrWrongPassword = errors.New("wrong password (or the key file was changed)")
	// ErrNotFound is returned for an account that has no key file.
	ErrNotFound = errors.New("no such account")

	nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)
)

// ValidName tells whether a name can be an account name (and a file name on every system).
func ValidName(name string) error {
	if !nameRE.MatchString(name) || strings.HasSuffix(name, ".") {
		return fmt.Errorf("%q: an account name has 1 to 32 letters, digits, '.', '_' or '-'", name)
	}
	return nil
}

func associatedData(version int, kdf KDFParams, public string) []byte {
	return []byte(fmt.Sprintf("credits-wallet key file v%d|%s t=%d m=%d p=%d salt=%s|%s",
		version, kdf.Name, kdf.Time, kdf.MemoryKiB, kdf.Threads, kdf.Salt, public))
}

func deriveFileKey(password []byte, kdf KDFParams) ([]byte, error) {
	if kdf.Name != "argon2id" {
		return nil, fmt.Errorf("unknown key derivation %q", kdf.Name)
	}
	// bounds against a file that would make the wallet hang or exhaust memory
	if kdf.Time < 1 || kdf.Time > 64 || kdf.MemoryKiB < 8*1024 || kdf.MemoryKiB > 4*1024*1024 || kdf.Threads < 1 {
		return nil, fmt.Errorf("key derivation parameters out of range: t=%d m=%d KiB p=%d", kdf.Time, kdf.MemoryKiB, kdf.Threads)
	}
	salt, err := base64.StdEncoding.DecodeString(kdf.Salt)
	if err != nil || len(salt) != saltSize {
		return nil, errors.New("bad salt in the key file")
	}
	return argon2.IDKey(password, salt, kdf.Time, kdf.MemoryKiB, kdf.Threads, chacha20poly1305.KeySize), nil
}

// Encrypt makes the key file of a key. params is DefaultKDF except in tests; the salt is drawn here.
func Encrypt(name string, key ed25519.PrivateKey, password []byte, params KDFParams) (*File, error) {
	if err := ValidName(name); err != nil {
		return nil, err
	}
	if len(password) == 0 {
		return nil, errors.New("the password is empty")
	}
	salt := make([]byte, saltSize)
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	f := &File{Version: FileVersion, Name: name, Public: Address(key.Public().(ed25519.PublicKey)), KDF: params,
		Cipher: "xchacha20-poly1305", Nonce: base64.StdEncoding.EncodeToString(nonce), Created: time.Now().UTC().Truncate(time.Second)}
	f.KDF.Salt = base64.StdEncoding.EncodeToString(salt)
	fileKey, err := deriveFileKey(password, f.KDF)
	if err != nil {
		return nil, err
	}
	defer Zero(fileKey)
	aead, err := chacha20poly1305.NewX(fileKey)
	if err != nil {
		return nil, err
	}
	sealed := aead.Seal(nil, nonce, key.Seed(), associatedData(f.Version, f.KDF, f.Public))
	f.Ciphertext = base64.StdEncoding.EncodeToString(sealed)
	return f, nil
}

// Decrypt opens a key file with its password and checks the key against the recorded public key.
// The caller zeroes the returned key after use (keys.Zero).
func (f *File) Decrypt(password []byte) (ed25519.PrivateKey, error) {
	if f.Version != FileVersion {
		return nil, fmt.Errorf("key file version %d is not supported (this wallet reads version %d)", f.Version, FileVersion)
	}
	if f.Cipher != "xchacha20-poly1305" {
		return nil, fmt.Errorf("unknown cipher %q", f.Cipher)
	}
	nonce, err := base64.StdEncoding.DecodeString(f.Nonce)
	if err != nil || len(nonce) != chacha20poly1305.NonceSizeX {
		return nil, errors.New("bad nonce in the key file")
	}
	sealed, err := base64.StdEncoding.DecodeString(f.Ciphertext)
	if err != nil {
		return nil, errors.New("bad ciphertext in the key file")
	}
	fileKey, err := deriveFileKey(password, f.KDF)
	if err != nil {
		return nil, err
	}
	defer Zero(fileKey)
	aead, err := chacha20poly1305.NewX(fileKey)
	if err != nil {
		return nil, err
	}
	seed, err := aead.Open(nil, nonce, sealed, associatedData(f.Version, f.KDF, f.Public))
	if err != nil {
		return nil, ErrWrongPassword
	}
	defer Zero(seed)
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("the key file holds a key of the wrong size")
	}
	key := ed25519.NewKeyFromSeed(seed)
	if Address(key.Public().(ed25519.PublicKey)) != f.Public {
		Zero(key)
		return nil, errors.New("the key file's key does not match its public key")
	}
	return key, nil
}

// PublicKey returns the account's public key.
func (f *File) PublicKey() (ed25519.PublicKey, error) { return ParseAddress(f.Public) }

// Store is the folder of key files.
type Store struct{ Dir string }

func (s Store) path(name string) string { return filepath.Join(s.Dir, name+".json") }

// FilePath is the key file of an account.
func (s Store) FilePath(name string) string { return s.path(name) }

// Exists tells whether an account of that name exists.
func (s Store) Exists(name string) bool {
	_, err := os.Stat(s.path(name))
	return err == nil
}

// Save writes a new key file; it never overwrites an existing account.
func (s Store) Save(f *File) error {
	if err := ValidName(f.Name); err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	out, err := os.OpenFile(s.path(f.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("an account named %q already exists", f.Name)
	}
	if err != nil {
		return err
	}
	if _, err := out.Write(append(data, '\n')); err != nil {
		out.Close()
		os.Remove(s.path(f.Name))
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Load reads the key file of an account (without decrypting it).
func (s Store) Load(name string) (*File, error) {
	if err := ValidName(name); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if err != nil {
		return nil, err
	}
	return ParseFile(data)
}

// ParseFile reads a key file's JSON.
func ParseFile(data []byte) (*File, error) {
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("not a key file: %w", err)
	}
	if f.Version == 0 || f.Public == "" || f.Ciphertext == "" {
		return nil, errors.New("not a credits-wallet key file")
	}
	if _, err := base58.Decode(f.Public); err != nil {
		return nil, errors.New("bad public key in the key file")
	}
	return &f, nil
}

// List returns the accounts, sorted by name.
func (s Store) List() ([]*File, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []*File
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() || ValidName(name) != nil {
			continue
		}
		f, err := s.Load(name)
		if err != nil {
			continue // not ours: skipped, not fatal
		}
		f.Name = name // the file name wins
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}
