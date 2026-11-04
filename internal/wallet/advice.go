package wallet

import (
	"os"
	"path/filepath"
	"strings"
)

// PlainKeyAdvice is what to do with a file a key was imported from. A node's own key file
// (NodePrivate.txt, or a file next to NodePublic.txt) must stay: the node needs it, in clear, so
// the encrypted copy protects nothing the original does not expose; the wallet of the node uses
// that file in place anyway (the account "node"). Any other file holds the key in clear and should
// go.
func PlainKeyAdvice(path string) string {
	if IsNodeKeyFile(path) {
		return path + " is a node's key file: keep it, the node needs it. The original stays in clear for the node," +
			" so this encrypted copy adds no protection; next to the node the wallet uses that file directly as the" +
			" account \"node\" (node_key in wallet.toml). " + NodeKeyWarning
	}
	return path + " holds the key in clear: now that it is in the encrypted key store, delete it (or keep it offline)."
}

// IsNodeKeyFile tells whether path looks like a node's key file.
func IsNodeKeyFile(path string) bool {
	if strings.EqualFold(filepath.Base(path), "NodePrivate.txt") {
		return true
	}
	_, err := os.Stat(filepath.Join(filepath.Dir(path), "NodePublic.txt"))
	return err == nil
}
