package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

// One password for the whole wallet (spec 4.2). The wallet password unlocks a random 32-byte
// master key kept in accounts/wallet.key (Argon2id with a random salt, XChaCha20-Poly1305); the
// master key encrypts every account file of version 2 (XChaCha20-Poly1305, a random nonce per
// file). Unlocking costs one Argon2id whatever the number of accounts, and changing the password
// writes a new master key and re-encrypts the accounts under it (resumable: WalletKeyNextFile).
//
// Account files of version 1 (one password each, from before) are still read: they are the
// accounts "with an old password", moved under the wallet password by the migration.

// WalletKeyFile is the wallet key's file name in the accounts folder; WalletKeyNextFile is the
// new key of a change of password in progress (it also holds the previous master key, so that
// the new password alone finishes an interrupted change).
const (
	WalletKeyFile     = "wallet.key"
	WalletKeyNextFile = "wallet.key.next"
)

// AccountFileVersion is the version of account files encrypted under the wallet's master key.
const AccountFileVersion = 2

const masterSize = chacha20poly1305.KeySize

// ErrWalletPassword is a wallet password that does not open wallet.key (or a damaged file).
var ErrWalletPassword = errors.New("wrong wallet password (or the wallet key file was changed)")

// WalletKey is the JSON of wallet.key.
type WalletKey struct {
	Version    int       `json:"version"`
	ID         string    `json:"id"` // hex, 16 random bytes: the account files name the key they use
	KDF        KDFParams `json:"kdf"`
	Cipher     string    `json:"cipher"`
	Nonce      string    `json:"nonce"`
	Ciphertext string    `json:"ciphertext"`
	Created    time.Time `json:"created"`
	// PreviousID: in wallet.key.next only, the key being replaced (its master key is sealed after
	// the new one).
	PreviousID string `json:"previous_id,omitempty"`
}

func walletKeyAD(k *WalletKey) []byte {
	return []byte(fmt.Sprintf("credits-wallet wallet key v%d|%s|prev=%s|%s t=%d m=%d p=%d salt=%s",
		k.Version, k.ID, k.PreviousID, k.KDF.Name, k.KDF.Time, k.KDF.MemoryKiB, k.KDF.Threads, k.KDF.Salt))
}

// NewMasterKey draws a master key and its id.
func NewMasterKey() (master []byte, id string, err error) {
	master = make([]byte, masterSize)
	raw := make([]byte, 16)
	if _, err := rand.Read(master); err != nil {
		return nil, "", err
	}
	if _, err := rand.Read(raw); err != nil {
		return nil, "", err
	}
	return master, hex.EncodeToString(raw), nil
}

// SealWalletKey seals master (and, for wallet.key.next, the previous master key after it) under
// the wallet password.
func SealWalletKey(id string, master []byte, previousID string, previous []byte, password []byte, params KDFParams) (*WalletKey, error) {
	if len(password) == 0 {
		return nil, errors.New("the wallet password is empty")
	}
	salt := make([]byte, saltSize)
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	k := &WalletKey{Version: 1, ID: id, KDF: params, Cipher: "xchacha20-poly1305", Nonce: base64.StdEncoding.EncodeToString(nonce),
		Created: time.Now().UTC().Truncate(time.Second), PreviousID: previousID}
	k.KDF.Name = "argon2id"
	k.KDF.Salt = base64.StdEncoding.EncodeToString(salt)
	fileKey, err := deriveFileKey(password, k.KDF)
	if err != nil {
		return nil, err
	}
	defer Zero(fileKey)
	aead, err := chacha20poly1305.NewX(fileKey)
	if err != nil {
		return nil, err
	}
	plain := append(append(make([]byte, 0, 2*masterSize), master...), previous...)
	defer Zero(plain)
	k.Ciphertext = base64.StdEncoding.EncodeToString(aead.Seal(nil, nonce, plain, walletKeyAD(k)))
	return k, nil
}

// Open returns the master key (and the previous one, for wallet.key.next). The caller zeroes them.
func (k *WalletKey) Open(password []byte) (master, previous []byte, err error) {
	if k.Version != 1 {
		return nil, nil, fmt.Errorf("wallet key version %d is not supported", k.Version)
	}
	if k.Cipher != "xchacha20-poly1305" {
		return nil, nil, fmt.Errorf("unknown cipher %q", k.Cipher)
	}
	nonce, err := base64.StdEncoding.DecodeString(k.Nonce)
	if err != nil || len(nonce) != chacha20poly1305.NonceSizeX {
		return nil, nil, errors.New("bad nonce in the wallet key")
	}
	sealed, err := base64.StdEncoding.DecodeString(k.Ciphertext)
	if err != nil {
		return nil, nil, errors.New("bad ciphertext in the wallet key")
	}
	if len(password) == 0 {
		return nil, nil, ErrWalletPassword
	}
	fileKey, err := deriveFileKey(password, k.KDF)
	if err != nil {
		return nil, nil, err
	}
	defer Zero(fileKey)
	aead, err := chacha20poly1305.NewX(fileKey)
	if err != nil {
		return nil, nil, err
	}
	plain, err := aead.Open(nil, nonce, sealed, walletKeyAD(k))
	if err != nil {
		return nil, nil, ErrWalletPassword
	}
	defer Zero(plain)
	want := masterSize
	if k.PreviousID != "" {
		want = 2 * masterSize
	}
	if len(plain) != want {
		return nil, nil, errors.New("the wallet key holds a key of the wrong size")
	}
	master = append([]byte(nil), plain[:masterSize]...)
	if k.PreviousID != "" {
		previous = append([]byte(nil), plain[masterSize:]...)
	}
	return master, previous, nil
}

// ParseWalletKey reads wallet.key's JSON.
func ParseWalletKey(data []byte) (*WalletKey, error) {
	var k WalletKey
	if err := json.Unmarshal(data, &k); err != nil {
		return nil, fmt.Errorf("not a wallet key file: %w", err)
	}
	if k.Version == 0 || k.ID == "" || k.Ciphertext == "" {
		return nil, errors.New("not a credits-wallet wallet key file")
	}
	return &k, nil
}

// Marshal is the file's content.
func (k *WalletKey) Marshal() []byte {
	data, _ := json.MarshalIndent(k, "", "  ")
	return append(data, '\n')
}

// accountAD is an account file's associated data: the version, the wallet key's id and the public
// key. The account's name is not bound: it is a label, and the file's name decides it (Store.List;
// the "name" inside is not read back). Binding it would make a file renamed by hand unreadable, and
// a swap of two files' names only swaps the labels: the address shown with each name is bound, and
// is what the reviews show and sign from.
func accountAD(version int, keyID, public string) []byte {
	return []byte(fmt.Sprintf("credits-wallet key file v%d|wallet-key %s|%s", version, keyID, public))
}

// EncryptWithMaster makes an account file of version 2: the seed encrypted under the wallet's
// master key (no key derivation: the master key is random).
func EncryptWithMaster(name string, key ed25519.PrivateKey, master []byte, keyID string) (*File, error) {
	if err := ValidName(name); err != nil {
		return nil, err
	}
	if len(master) != masterSize || keyID == "" {
		return nil, errors.New("no wallet key")
	}
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	f := &File{Version: AccountFileVersion, Name: name, Public: Address(key.Public().(ed25519.PublicKey)), KeyID: keyID,
		Cipher: "xchacha20-poly1305", Nonce: base64.StdEncoding.EncodeToString(nonce), Created: time.Now().UTC().Truncate(time.Second)}
	aead, err := chacha20poly1305.NewX(master)
	if err != nil {
		return nil, err
	}
	f.Ciphertext = base64.StdEncoding.EncodeToString(aead.Seal(nil, nonce, key.Seed(), accountAD(f.Version, f.KeyID, f.Public)))
	return f, nil
}

// UnderWalletKey tells whether the account file is encrypted under the wallet's master key
// (version 2); false: its own password (version 1, "old password"), or the node's key.
func (f *File) UnderWalletKey() bool { return !f.IsNodeKey() && f.Version == AccountFileVersion }

// OldPassword tells whether the account still has a password of its own (version 1).
func (f *File) OldPassword() bool { return !f.IsNodeKey() && f.Version == FileVersion }

// DecryptWithMaster opens an account file of version 2 with the wallet's master key.
func (f *File) DecryptWithMaster(master []byte, keyID string) (ed25519.PrivateKey, error) {
	if !f.UnderWalletKey() {
		return nil, errors.New("this account is not under the wallet password")
	}
	if f.KeyID != keyID {
		return nil, fmt.Errorf("the account file is encrypted under another wallet key (%s, this wallet's is %s)", short(f.KeyID), short(keyID))
	}
	nonce, err := base64.StdEncoding.DecodeString(f.Nonce)
	if err != nil || len(nonce) != chacha20poly1305.NonceSizeX {
		return nil, errors.New("bad nonce in the key file")
	}
	sealed, err := base64.StdEncoding.DecodeString(f.Ciphertext)
	if err != nil {
		return nil, errors.New("bad ciphertext in the key file")
	}
	aead, err := chacha20poly1305.NewX(master)
	if err != nil {
		return nil, err
	}
	seed, err := aead.Open(nil, nonce, sealed, accountAD(f.Version, f.KeyID, f.Public))
	if err != nil {
		return nil, errors.New("the key file does not open with the wallet key: it was changed or damaged")
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

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// ---- the files in the store ----------------------------------------------------------------------

// WalletKeyPath is accounts/wallet.key.
func (s Store) WalletKeyPath() string { return filepath.Join(s.Dir, WalletKeyFile) }

// WalletKeyNextPath is accounts/wallet.key.next.
func (s Store) WalletKeyNextPath() string { return filepath.Join(s.Dir, WalletKeyNextFile) }

// HasWalletKey tells whether the wallet has a wallet password.
func (s Store) HasWalletKey() bool {
	_, err := os.Lstat(s.WalletKeyPath())
	return err == nil
}

// LoadWalletKey reads wallet.key (or, next, wallet.key.next); os.ErrNotExist when there is none.
func (s Store) LoadWalletKey(next bool) (*WalletKey, error) {
	path := s.WalletKeyPath()
	if next {
		path = s.WalletKeyNextPath()
	}
	data, err := readSmall(path)
	if err != nil {
		return nil, err
	}
	return ParseWalletKey(data)
}

// SaveWalletKey writes the wallet key: a new file (never over one) when replace is false, or an
// atomic replacement (written whole, synced, renamed over the old one).
func (s Store) SaveWalletKey(k *WalletKey, next, replace bool) error {
	path := s.WalletKeyPath()
	if next {
		path = s.WalletKeyNextPath()
	}
	return s.writeAtomic(path, k.Marshal(), replace)
}

// Replace writes an account file over the existing one, atomically (a temporary file, synced,
// renamed): the old file or the new one, never a half-written one.
func (s Store) Replace(f *File) error {
	if err := ValidName(f.Name); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return s.writeAtomic(s.path(f.Name), append(data, '\n'), true)
}

func (s Store) writeAtomic(final string, data []byte, replace bool) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	if st, err := os.Lstat(final); err == nil {
		if !replace {
			return fmt.Errorf("%s already exists", final)
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file: refused", final)
		}
	} else if replace {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if replace {
		if err := os.Rename(tmp.Name(), final); err != nil {
			return err
		}
	} else if err := os.Link(tmp.Name(), final); err != nil {
		// a new file: linked to its name, which fails when the name exists; on a file system
		// without hard links, renamed after the check above (a race with another writer is then
		// possible: the accounts folder is the user's own, mode 0700)
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists", final)
		}
		if err := os.Rename(tmp.Name(), final); err != nil {
			return err
		}
	}
	syncDir(s.Dir)
	return nil
}

// NewLockedSecret keeps a secret of any size (the wallet's master key) in locked memory, as
// NewLockedKey does for a signing key. The caller zeroes its own copy.
func NewLockedSecret(secret []byte) *LockedKey {
	mem, locked := newLockedMem(len(secret))
	copy(mem.b, secret)
	mem.protect()
	lk := &LockedKey{mem: mem}
	if !locked {
		lk.warn = NotLockedWarning
	}
	return lk
}

// UseSecret lends a copy of the secret to fn (zeroed afterwards).
func (k *LockedKey) UseSecret(fn func([]byte) error) error {
	return k.Use(func(b ed25519.PrivateKey) error { return fn(b) })
}

// EncryptedKeyFile tells whether text is a wallet key file (an account file, or an encrypted
// export), and returns it.
func EncryptedKeyFile(text string) (*File, bool) {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "{") {
		return nil, false
	}
	f, err := ParseFile([]byte(t))
	if err != nil {
		return nil, false
	}
	return f, true
}
