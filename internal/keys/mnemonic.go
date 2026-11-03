package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/blake2s"
)

// The node's seed phrase (cscrypto mnemonic.cpp and keysderivation.cpp):
//
//   - a 256-bit master seed, plus an 8-bit checksum: the first byte of BLAKE2s-256 of the seed
//     (cscrypto::calculateHash);
//   - the 264 bits are cut in 24 words of 11 bits, from a little-endian bit stream: bit i of the
//     stream is bit (i mod 8) of byte (i / 8), and bit k of word w is stream bit 11w + k;
//   - the key is libsodium crypto_kdf_derive_from_key(32 bytes, id 0, context 8 zero bytes,
//     master seed), then crypto_sign_seed_keypair. That KDF is BLAKE2b with key = master seed,
//     salt = id (little endian, padded to 16 bytes) and personalisation = context (padded to 16
//     bytes) over an empty message. With id 0 and the zero context, salt and personalisation are
//     all zeros, which is the same as plain keyed BLAKE2b-256: no other parameters are needed.
//
// The node does not check the checksum when it reads words back; the wallet does.

const (
	MasterSeedSize = 32
	PhraseWords    = 24
	bitsPerWord    = 11
)

// NewMasterSeed returns a random master seed from the operating system's CSPRNG.
func NewMasterSeed() ([MasterSeedSize]byte, error) {
	var seed [MasterSeedSize]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return seed, fmt.Errorf("random: %w", err)
	}
	return seed, nil
}

// DeriveKey derives the account key of a master seed as the node does (key id 0, empty context).
func DeriveKey(master *[MasterSeedSize]byte) ed25519.PrivateKey {
	h, err := blake2b.New256(master[:]) // keyed BLAKE2b-256: crypto_kdf_derive_from_key(id 0, ctx 0)
	if err != nil {
		panic(err) // a 32-byte key is always accepted
	}
	sub := h.Sum(nil)
	defer Zero(sub)
	return ed25519.NewKeyFromSeed(sub) // crypto_sign_seed_keypair
}

func checksum(master *[MasterSeedSize]byte) byte {
	sum := blake2s.Sum256(master[:])
	return sum[0]
}

// stream bit i: bit (i % 8) of byte (i / 8)
func getBit(b []byte, i int) uint    { return uint(b[i/8]>>(i%8)) & 1 }
func setBit(b []byte, i int, v uint) { b[i/8] |= byte(v << (i % 8)) }

// Words returns the 24-word phrase of a master seed.
func Words(master *[MasterSeedSize]byte) []string {
	bits := make([]byte, MasterSeedSize+1)
	defer Zero(bits)
	copy(bits, master[:])
	bits[MasterSeedSize] = checksum(master)
	words := make([]string, PhraseWords)
	for w := range words {
		index := uint(0)
		for k := 0; k < bitsPerWord; k++ {
			index |= getBit(bits, w*bitsPerWord+k) << k
		}
		words[w] = wordList[index]
	}
	return words
}

// SplitPhrase splits a typed phrase into lower-case words.
func SplitPhrase(text string) []string {
	return strings.Fields(strings.ToLower(text))
}

// MasterSeedFromWords reads a 24-word phrase back and checks its checksum.
func MasterSeedFromWords(words []string) ([MasterSeedSize]byte, error) {
	var master [MasterSeedSize]byte
	if len(words) != PhraseWords {
		return master, fmt.Errorf("a seed phrase has %d words, got %d", PhraseWords, len(words))
	}
	bits := make([]byte, MasterSeedSize+1)
	defer Zero(bits)
	for w, word := range words {
		index, ok := wordIndex[strings.ToLower(word)]
		if !ok {
			return master, fmt.Errorf("word %d is not in the word list", w+1)
		}
		for k := 0; k < bitsPerWord; k++ {
			setBit(bits, w*bitsPerWord+k, (index>>k)&1)
		}
	}
	copy(master[:], bits[:MasterSeedSize])
	if checksum(&master) != bits[MasterSeedSize] {
		Zero(master[:])
		return master, errors.New("the seed phrase checksum is wrong: a word is mistyped or out of order")
	}
	return master, nil
}

// FromWords derives the account key of a 24-word phrase.
func FromWords(words []string) (ed25519.PrivateKey, error) {
	master, err := MasterSeedFromWords(words)
	if err != nil {
		return nil, err
	}
	defer Zero(master[:])
	return DeriveKey(&master), nil
}

var wordIndex = func() map[string]uint {
	m := make(map[string]uint, len(wordList))
	for i, w := range wordList {
		m[w] = uint(i)
	}
	return m
}()
