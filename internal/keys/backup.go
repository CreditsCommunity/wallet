package keys

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// The backup container (spec 4.5): one file that holds every account of the key store, sealed
// under a backup password. The accounts' key files go in as they are stored (each still
// encrypted under its own password); this layer adds a second encryption over all of them, so
// that the file says nothing (not even names or addresses) without the backup password.
//
//	offset size field
//	0      8    magic "CWBACKUP"
//	8      1    format version (1)
//	9      1    key derivation (1: Argon2id)
//	10     4    Argon2id time (passes), big-endian
//	14     4    Argon2id memory in KiB, big-endian
//	18     1    Argon2id threads
//	19     16   salt (random, per file)
//	35     24   nonce (random, XChaCha20-Poly1305)
//	59     ...  ciphertext and its 16-byte tag
//
// The 59-byte header is the associated data: changing any of its bytes fails the tag check.

// BackupMagic opens every backup file.
const BackupMagic = "CWBACKUP"

// BackupVersion is the version of the container this wallet writes and reads.
const BackupVersion = 1

const (
	backupKDFArgon2id = 1
	// BackupHeaderSize is the size of the header (the associated data).
	BackupHeaderSize = 8 + 1 + 1 + 4 + 4 + 1 + saltSize + chacha20poly1305.NonceSizeX
	// MaxBackupSize bounds a backup file read by the wallet (the accounts' key files are a few
	// hundred bytes each; the local lists at most 4 MiB each).
	MaxBackupSize = 64 << 20
)

var (
	// ErrBackupOpen is what a wrong password and a damaged file both say: the tag check cannot
	// tell them apart, and the wallet does not try to.
	ErrBackupOpen = errors.New("could not open the backup: wrong backup password, or a damaged file")
	// ErrNotBackup is a file that does not start as a backup.
	ErrNotBackup = errors.New("not a credits-wallet backup file")
)

// BackupKDF is the key derivation of new backup files: the key files' (DefaultKDF); lighter only
// in tests.
var BackupKDF = DefaultKDF

// IsBackup tells whether data starts as a backup file (the magic only: nothing is checked).
func IsBackup(data []byte) bool { return bytes.HasPrefix(data, []byte(BackupMagic)) }

func backupHeader(kdf KDFParams, salt, nonce []byte) []byte {
	h := make([]byte, 0, BackupHeaderSize)
	h = append(h, BackupMagic...)
	h = append(h, BackupVersion, backupKDFArgon2id)
	h = binary.BigEndian.AppendUint32(h, kdf.Time)
	h = binary.BigEndian.AppendUint32(h, kdf.MemoryKiB)
	h = append(h, kdf.Threads)
	h = append(h, salt...)
	return append(h, nonce...)
}

// SealBackup encrypts plaintext under the backup password. The caller zeroes plaintext.
func SealBackup(plaintext, password []byte, params KDFParams) ([]byte, error) {
	if len(password) == 0 {
		return nil, errors.New("the backup password is empty")
	}
	salt := make([]byte, saltSize)
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	params.Name = "argon2id"
	params.Salt = base64.StdEncoding.EncodeToString(salt)
	key, err := deriveFileKey(password, params) // the key files' bounds apply
	if err != nil {
		return nil, err
	}
	defer Zero(key)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	header := backupHeader(params, salt, nonce)
	if len(header)+len(plaintext)+aead.Overhead() > MaxBackupSize {
		return nil, fmt.Errorf("the backup would be larger than %d MiB: refused", MaxBackupSize>>20)
	}
	return aead.Seal(header, nonce, plaintext, header), nil
}

// OpenBackup checks the header, derives the key (within the key files' bounds) and opens the
// backup. A wrong password and a damaged file are both ErrBackupOpen. The caller zeroes the
// returned plaintext.
func OpenBackup(data, password []byte) ([]byte, error) {
	if len(data) > MaxBackupSize {
		return nil, fmt.Errorf("the file is larger than %d MiB: not a backup of this wallet", MaxBackupSize>>20)
	}
	if !IsBackup(data) {
		return nil, ErrNotBackup
	}
	if len(data) < len(BackupMagic)+1 {
		return nil, ErrBackupOpen
	}
	if v := data[len(BackupMagic)]; v != BackupVersion {
		return nil, fmt.Errorf("backup format version %d is not supported: this wallet reads version %d (a newer credits-wallet may read it)", v, BackupVersion)
	}
	if len(data) < BackupHeaderSize+chacha20poly1305.Overhead {
		return nil, ErrBackupOpen // truncated
	}
	header := data[:BackupHeaderSize]
	p := header[len(BackupMagic)+1:]
	if p[0] != backupKDFArgon2id {
		return nil, ErrBackupOpen
	}
	kdf := KDFParams{Name: "argon2id", Time: binary.BigEndian.Uint32(p[1:5]), MemoryKiB: binary.BigEndian.Uint32(p[5:9]), Threads: p[9]}
	salt := p[10 : 10+saltSize]
	nonce := p[10+saltSize : 10+saltSize+chacha20poly1305.NonceSizeX]
	kdf.Salt = base64.StdEncoding.EncodeToString(salt)
	if kdf.Time < MinKDF.Time || kdf.Time > MaxKDF.Time || kdf.MemoryKiB < MinKDF.MemoryKiB || kdf.MemoryKiB > MaxKDF.MemoryKiB ||
		kdf.Threads < MinKDF.Threads || kdf.Threads > MaxKDF.Threads {
		// a damaged header (or a file not made by the wallet): never derived, said as the rest
		return nil, ErrBackupOpen
	}
	if len(password) == 0 {
		return nil, ErrBackupOpen
	}
	key, err := deriveFileKey(password, kdf)
	if err != nil {
		return nil, ErrBackupOpen
	}
	defer Zero(key)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonce, data[BackupHeaderSize:], header)
	if err != nil {
		return nil, ErrBackupOpen
	}
	return plain, nil
}
