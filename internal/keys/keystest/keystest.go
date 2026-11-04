// Package keystest writes NodePrivate.txt contents the way the node encrypts them, for tests only:
// the wallet itself never encrypts a node's key (package keys only opens them). It does not import
// package keys, so that its own tests can use it; package keys proves it byte-exact against files
// the node wrote (testdata/node-encrypted-vectors.json).
package keystest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/poly1305"
)

const (
	saltSize  = 16
	nonceSize = 8
)

// EncryptNodeKey is what the node writes for a new encryption (--encryptkey), decoded: salt (16
// random bytes) || nonce (8 random bytes) || ciphertext || tag, 104 bytes, the key derived with
// Argon2id (time, memoryKiB, parallelism 1: the node's are keys.NodeKDF, tests make them lighter).
func EncryptNodeKey(key ed25519.PrivateKey, password []byte, time, memoryKiB uint32) ([]byte, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("a secret key is 64 bytes")
	}
	head := make([]byte, saltSize+nonceSize)
	if _, err := rand.Read(head); err != nil {
		return nil, err
	}
	fileKey := argon2.IDKey(password, head[:saltSize], time, memoryKiB, 1, 32)
	defer clear(fileKey)
	sealed, err := Seal(key, fileKey, head[saltSize:])
	if err != nil {
		return nil, err
	}
	return append(head[:saltSize], sealed...), nil
}

// Seal is the node's crypto_aead_chacha20poly1305_encrypt (the original construction, no
// associated data) of the key with a derived file key: nonce || ciphertext || tag, the part of
// both formats after the salt.
func Seal(key ed25519.PrivateKey, fileKey, nonce []byte) ([]byte, error) {
	if len(nonce) != nonceSize {
		return nil, errors.New("the node's nonce is 8 bytes")
	}
	n := make([]byte, 12) // the IETF cipher with the 8-byte nonce after four zero bytes
	copy(n[4:], nonce)
	c, err := chacha20.NewUnauthenticatedCipher(fileKey, n)
	if err != nil {
		return nil, err
	}
	block0 := make([]byte, 64)
	c.XORKeyStream(block0, block0)
	defer clear(block0)
	c.SetCounter(1)
	out := make([]byte, nonceSize+len(key)+poly1305.TagSize)
	copy(out, nonce)
	ct := out[nonceSize : nonceSize+len(key)]
	c.XORKeyStream(ct, key)
	var pk [32]byte
	copy(pk[:], block0[:32])
	msg := binary.LittleEndian.AppendUint64(nil, 0) // no associated data
	msg = append(msg, ct...)
	msg = binary.LittleEndian.AppendUint64(msg, uint64(len(ct)))
	var tag [poly1305.TagSize]byte
	poly1305.Sum(&tag, msg, &pk)
	clear(pk[:])
	copy(out[nonceSize+len(key):], tag[:])
	return out, nil
}
