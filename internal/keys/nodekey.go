package keys

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mr-tron/base58"
)

// The node's key files (client/config/config.cpp of the node, Config::readKeys), in the node's
// folder (its working directory; --private-key-file and --public-key-file name others):
//
//   - NodePrivate.txt: base58 (the Bitcoin alphabet) of the 64-byte ed25519 secret key, the
//     32-byte seed followed by the 32-byte public key, without a newline. The node reads the first
//     line and decodes it: 64 bytes is a plain key; more is a key the node encrypted itself
//     (--encryptkey, or the "[1] Encrypt with password" choice of its first start), which only the
//     node can open; fewer is refused.
//   - NodePublic.txt: base58 of the 32-byte public key, without a newline. The node compares its
//     first line, as text, with the public key of NodePrivate.txt; when they differ (or the file is
//     missing) it stops and asks on its console whether to rewrite the file.
//
// The node writes both files itself (std::ofstream, no newline) when it generates a key at its
// first start. The wallet writes the same bytes.

const (
	// NodePrivateFile and NodePublicFile are the node's default key file names.
	NodePrivateFile = "NodePrivate.txt"
	NodePublicFile  = "NodePublic.txt"

	maxNodeKeyFile = 4096
)

// ErrNodeKeyEncrypted is returned for a NodePrivate.txt the node encrypted with its own password.
var ErrNodeKeyEncrypted = errors.New("the node's key file is encrypted by the node (--encryptkey): only the node can open it")

// NodeKeyFiles returns the contents of NodePrivate.txt and NodePublic.txt for a key, byte for byte
// what the node writes. The caller zeroes the first one after use.
func NodeKeyFiles(key ed25519.PrivateKey) (private, public []byte) {
	return []byte(base58.Encode(key)), []byte(base58.Encode(key[32:]))
}

// firstLine is what the node's std::getline returns: the text up to the first newline.
func firstLine(data []byte) string {
	line, _, _ := strings.Cut(string(data), "\n")
	return line
}

func readSmall(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxNodeKeyFile+1))
	if err != nil {
		Zero(data)
		return nil, err
	}
	if len(data) > maxNodeKeyFile {
		Zero(data)
		return nil, fmt.Errorf("%s is larger than a node's key file", path)
	}
	return data, nil
}

// ParseNodePrivate reads the content of a NodePrivate.txt as the node does. The caller zeroes the
// key after use.
func ParseNodePrivate(data []byte) (ed25519.PrivateKey, error) {
	// the node's DecodeBase58 skips the spaces around the text (a "\r" of a Windows editor too);
	// only the trimmed line is copied into a string (base58.Decode takes one)
	line := data
	if i := bytes.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	secret, err := base58.Decode(string(bytes.TrimSpace(line)))
	if err != nil {
		return nil, errors.New("not a node's key file: the first line is not base58 text")
	}
	defer Zero(secret)
	switch {
	case len(secret) > ed25519.PrivateKeySize:
		return nil, ErrNodeKeyEncrypted
	case len(secret) < ed25519.PrivateKeySize:
		return nil, fmt.Errorf("not a node's key file: %d bytes, the node needs 64", len(secret))
	}
	return FromSecret(secret)
}

// ReadNodeKey reads a NodePrivate.txt. The caller zeroes the key after use.
func ReadNodeKey(path string) (ed25519.PrivateKey, error) {
	data, err := readSmall(path)
	if err != nil {
		return nil, err
	}
	defer Zero(data)
	key, err := ParseNodePrivate(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return key, nil
}

// CheckNodePublic tells whether a NodePublic.txt is what the node expects for pub: its first line
// equal, as text, to the base58 public key (the node's own comparison).
func CheckNodePublic(path string, pub ed25519.PublicKey) error {
	data, err := readSmall(path)
	if err != nil {
		return err
	}
	if firstLine(data) != base58.Encode(pub) {
		return fmt.Errorf("%s does not hold the public key of the node's private key: the node would stop at its start and ask to rewrite it", path)
	}
	return nil
}
