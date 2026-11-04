package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/poly1305" // the original ChaCha20-Poly1305 of libsodium has no Go package
)

// A NodePrivate.txt the node encrypted itself (--encryptkey, or "[1] Encrypt with password" at its
// first start): cscrypto's PrivateKey::getEncrypted (cscrypto/src/privatekey.cpp), libsodium 1.0.17
// or later:
//
//	key  = crypto_pwhash(32 bytes, password, salt "IDI NAHUY BLYAT'" (16 fixed bytes),
//	                     OPSLIMIT_SENSITIVE = 4, MEMLIMIT_SENSITIVE = 1 GiB, ALG_DEFAULT = Argon2id v1.3,
//	                     parallelism 1)
//	file = base58(nonce (8 random bytes) || crypto_aead_chacha20poly1305_encrypt(secret key (64 bytes),
//	                     no associated data, nonce, key))      = 8 + 64 + 16 = 88 bytes
//
// crypto_aead_chacha20poly1305 is the original construction (not the IETF one): a 64-bit nonce and
// a 64-bit block counter; the Poly1305 key is the first 32 bytes of block 0, the text is encrypted
// from block 1, and the tag is over ad || le64(len(ad)) || ciphertext || le64(len(ciphertext))
// without padding. The password is passed as its bytes (std::strlen: up to the first NUL); the
// node reads only printable ASCII (32-126) from its console.
//
// The salt is the same for every node: the derived key depends on the password only, so a weak
// password is weaker here than in the wallet's own key files (random salt per file).

// NodeKDF are the Argon2id parameters of the node's encryption. They are the node's and must not
// change; tests make them lighter to keep fast.
var NodeKDF = struct {
	Time      uint32
	MemoryKiB uint32
}{Time: 4, MemoryKiB: 1024 * 1024}

var nodeSalt = []byte{73, 68, 73, 32, 78, 65, 72, 85, 89, 32, 66, 76, 89, 65, 84, 39}

const (
	nodeNonceSize = 8
	// NodeEncryptedSize is the size of an encrypted NodePrivate.txt once decoded from base58.
	NodeEncryptedSize = nodeNonceSize + ed25519.PrivateKeySize + poly1305.TagSize
)

// ErrNodeKeyMemory: the machine has not the memory the node's Argon2id needs (opening it anyway
// would end the process when the allocation fails).
var ErrNodeKeyMemory = errors.New("the node's key needs 1 GiB of free memory to open (as the node does)")

// memoryAvailable tells how much memory can be allocated now (false: unknown); tests replace it.
var memoryAvailable = availableMemory

// checkNodeKDFMemory refuses before the derivation when the free memory is short of what Argon2id
// takes (the KDF's memory plus a fifth for the process).
func checkNodeKDFMemory() error {
	need := uint64(NodeKDF.MemoryKiB) * 1024
	if avail, ok := memoryAvailable(); ok && avail < need+need/5 {
		return ErrNodeKeyMemory
	}
	return nil
}

// ErrNodeKeyPassword is the only answer to a node-encrypted key that does not open: no detail.
var ErrNodeKeyPassword = errors.New("wrong password (or the node's key file was changed)")

func nodeFileKey(password []byte) []byte {
	return argon2.IDKey(password, nodeSalt, NodeKDF.Time, NodeKDF.MemoryKiB, 1, 32)
}

// chachaOriginal returns the cipher of the original construction at block 0: the IETF cipher with
// the 8-byte nonce after four zero bytes has the same key stream while the counter fits 32 bits
// (here two blocks).
func chachaOriginal(key, nonce []byte) (*chacha20.Cipher, []byte, error) {
	n := make([]byte, 12)
	copy(n[4:], nonce)
	c, err := chacha20.NewUnauthenticatedCipher(key, n)
	if err != nil {
		return nil, nil, err
	}
	polyKey := make([]byte, 64)
	c.XORKeyStream(polyKey, polyKey)
	c.SetCounter(1)
	return c, polyKey[:32], nil
}

func nodeTag(polyKey, ciphertext []byte) [poly1305.TagSize]byte {
	var k [32]byte
	copy(k[:], polyKey)
	msg := make([]byte, 0, 8+len(ciphertext)+8)
	msg = binary.LittleEndian.AppendUint64(msg, 0) // no associated data
	msg = append(msg, ciphertext...)
	msg = binary.LittleEndian.AppendUint64(msg, uint64(len(ciphertext)))
	var tag [poly1305.TagSize]byte
	poly1305.Sum(&tag, msg, &k)
	Zero(k[:])
	return tag
}

// DecryptNodeKey opens the decoded content of a node-encrypted NodePrivate.txt with the node's
// password, as the node's PrivateKey::readFromEncrypted does. Any failure is ErrNodeKeyPassword.
// The caller zeroes the key after use. It costs what the node's opening costs: 1 GiB of memory and
// a few seconds; with less memory free it is refused (ErrNodeKeyMemory). golang.org/x/crypto/argon2
// does not zero its 1 GiB of working memory: it is left to the garbage collector.
func DecryptNodeKey(encrypted, password []byte) (ed25519.PrivateKey, error) {
	if len(encrypted) != NodeEncryptedSize {
		return nil, ErrNodeKeyPassword
	}
	if err := checkNodeKDFMemory(); err != nil {
		return nil, err
	}
	fileKey := nodeFileKey(password)
	defer Zero(fileKey)
	return decryptNodeKey(encrypted, fileKey)
}

func decryptNodeKey(encrypted, fileKey []byte) (ed25519.PrivateKey, error) {
	if len(encrypted) != NodeEncryptedSize {
		return nil, ErrNodeKeyPassword
	}
	c, polyKey, err := chachaOriginal(fileKey, encrypted[:nodeNonceSize])
	if err != nil {
		return nil, ErrNodeKeyPassword
	}
	defer Zero(polyKey)
	ct := encrypted[nodeNonceSize : nodeNonceSize+ed25519.PrivateKeySize]
	tag := nodeTag(polyKey, ct)
	if subtle.ConstantTimeCompare(tag[:], encrypted[nodeNonceSize+ed25519.PrivateKeySize:]) != 1 {
		return nil, ErrNodeKeyPassword
	}
	secret := make([]byte, ed25519.PrivateKeySize)
	defer Zero(secret)
	c.XORKeyStream(secret, ct)
	key, err := FromSecret(secret)
	if err != nil {
		return nil, ErrNodeKeyPassword
	}
	return key, nil
}

// EncryptNodeKey is the node's PrivateKey::getEncrypted: what --encryptkey writes, decoded. The
// wallet never writes it (it never encrypts the node's key file); the tests use it to prove the
// round trip with the node's code. nonce nil: random.
func EncryptNodeKey(key ed25519.PrivateKey, password, nonce []byte) ([]byte, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("a secret key is 64 bytes, got %d", len(key))
	}
	if nonce == nil {
		nonce = make([]byte, nodeNonceSize)
		if _, err := rand.Read(nonce); err != nil {
			return nil, err
		}
	}
	if len(nonce) != nodeNonceSize {
		return nil, errors.New("the node's nonce is 8 bytes")
	}
	if err := checkNodeKDFMemory(); err != nil {
		return nil, err
	}
	fileKey := nodeFileKey(password)
	defer Zero(fileKey)
	return encryptNodeKey(key, fileKey, nonce)
}

func encryptNodeKey(key ed25519.PrivateKey, fileKey, nonce []byte) ([]byte, error) {
	c, polyKey, err := chachaOriginal(fileKey, nonce)
	if err != nil {
		return nil, err
	}
	defer Zero(polyKey)
	out := make([]byte, NodeEncryptedSize)
	copy(out, nonce)
	ct := out[nodeNonceSize : nodeNonceSize+ed25519.PrivateKeySize]
	c.XORKeyStream(ct, key)
	tag := nodeTag(polyKey, ct)
	copy(out[nodeNonceSize+ed25519.PrivateKeySize:], tag[:])
	return out, nil
}
