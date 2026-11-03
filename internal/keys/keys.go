// Package keys generates, imports and stores the wallet's ed25519 keys.
//
// SECURITY REVIEW: this package, with mnemonic.go and store.go, is all the code that touches
// secret keys. Formats are those of the node (spec section 4.1).
package keys

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mr-tron/base58"

	"github.com/CreditsCommunity/wallet/internal/echo"
)

// Address is the base58 text of a public key, the form addresses take everywhere.
func Address(pub ed25519.PublicKey) string { return base58.Encode(pub) }

// ParseAddress reads a base58 address and checks that it is 32 bytes long.
func ParseAddress(text string) (ed25519.PublicKey, error) {
	b, err := base58.Decode(strings.TrimSpace(text))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%s is not an address (base58 of 32 bytes)", echo.Quote(strings.TrimSpace(text)))
	}
	return ed25519.PublicKey(b), nil
}

// Short abbreviates an address for narrow screens: "C31Lvn…5GQnVTeb".
func Short(addr string) string {
	if len(addr) <= 16 {
		return addr
	}
	return addr[:6] + "…" + addr[len(addr)-8:]
}

// Zero overwrites a secret in memory. Go may have copied it elsewhere (the garbage collector
// moves nothing, but slices passed by value to the standard library may be copied); this is the
// best a Go program can do, and it shortens the time the secret stays readable.
func Zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// FromSecret builds a key from a 32-byte ed25519 seed or a 64-byte secret key (seed followed by
// the public key, the node's NodePrivate.txt). With 64 bytes the public half must match the seed.
func FromSecret(secret []byte) (ed25519.PrivateKey, error) {
	if len(secret) != ed25519.SeedSize && len(secret) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("a secret key is 32 or 64 bytes, got %d", len(secret))
	}
	key := ed25519.NewKeyFromSeed(secret[:ed25519.SeedSize])
	if len(secret) == ed25519.PrivateKeySize && !bytes.Equal(key[32:], secret[32:]) {
		Zero(key)
		return nil, errors.New("the public half of the secret key does not match its seed")
	}
	return key, nil
}

// FromBase58 reads a base58 secret key (64 bytes, NodePrivate.txt) or a base58 32-byte seed.
func FromBase58(text string) (ed25519.PrivateKey, error) {
	secret, err := base58.Decode(strings.TrimSpace(text))
	if err != nil {
		return nil, errors.New("not base58 text")
	}
	defer Zero(secret)
	return FromSecret(secret)
}

// officialJSON is the keys file of the official web and desktop wallets:
// {"key":{"public":"<base58 32 bytes>","private":"<base58 64 bytes>"}}, not encrypted.
type officialJSON struct {
	Key struct {
		Public  string `json:"public"`
		Private string `json:"private"`
	} `json:"key"`
}

// LooksLikeOfficialJSON tells whether text is a JSON object (the official wallets' keys file).
func LooksLikeOfficialJSON(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "{")
}

// FromOfficialJSON reads the keys JSON of the official wallets. The private key (seed + public
// key) must agree with itself and with the "public" field.
func FromOfficialJSON(data []byte) (ed25519.PrivateKey, error) {
	var f officialJSON
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("not a keys JSON file: %w", err)
	}
	if f.Key.Public == "" || f.Key.Private == "" {
		return nil, errors.New(`not a keys JSON file: expected {"key":{"public":…,"private":…}}`)
	}
	pub, err := ParseAddress(f.Key.Public)
	if err != nil {
		return nil, fmt.Errorf("keys JSON, public: %w", err)
	}
	secret, err := base58.Decode(f.Key.Private)
	if err != nil || len(secret) != ed25519.PrivateKeySize {
		Zero(secret)
		return nil, errors.New("keys JSON, private: expected base58 of 64 bytes")
	}
	defer Zero(secret)
	key, err := FromSecret(secret)
	if err != nil {
		return nil, fmt.Errorf("keys JSON, private: %w", err)
	}
	if !bytes.Equal(key[32:], pub) {
		Zero(key)
		return nil, errors.New("keys JSON: the public key does not match the private key")
	}
	return key, nil
}

// OfficialJSON writes a key in the official wallets' format. The result holds the secret key in
// clear: the caller warns the user.
func OfficialJSON(key ed25519.PrivateKey) []byte {
	var f officialJSON
	f.Key.Public = base58.Encode(key[32:])
	f.Key.Private = base58.Encode(key)
	out, _ := json.Marshal(f)
	return out
}

// Base58 is the 64-byte secret key in base58, the node's NodePrivate.txt format.
func Base58(key ed25519.PrivateKey) string { return base58.Encode(key) }

// ParseSecret reads any of the import formats (spec 4.1): the official wallets' keys JSON, a
// 24-word seed phrase, or a base58 secret key (64 bytes) or seed (32 bytes). It also names the
// format it found.
func ParseSecret(text string) (ed25519.PrivateKey, string, error) {
	text = strings.TrimSpace(text)
	switch {
	case LooksLikeOfficialJSON(text):
		k, err := FromOfficialJSON([]byte(text))
		return k, "keys JSON of the official wallets", err
	case len(strings.Fields(text)) > 1:
		k, err := FromWords(SplitPhrase(text))
		return k, "seed phrase", err
	default:
		k, err := FromBase58(text)
		return k, "secret key", err
	}
}
