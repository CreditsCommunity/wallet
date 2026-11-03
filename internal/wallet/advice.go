package wallet

import (
	"os"
	"path/filepath"
	"strings"
)

// PlainKeyAdvice is what to do with a file a key was imported from. A node's own key file
// (NodePrivate.txt, or a file next to NodePublic.txt) must stay: the node needs it. Imported, the
// node's key also signs transfers: it becomes a spending key kept in clear on the server, so a
// separate account is the safer choice for funds. Any other file holds the key in clear and should
// go.
func PlainKeyAdvice(path string) string {
	if IsNodeKeyFile(path) {
		return path + " is a node's key file: keep it, the node needs it. Its key now also spends from this" +
			" wallet, and it stays in clear on this machine for the node: anyone who gets into this server" +
			" can take that account's CS. Keep funds in a separate account (account new)."
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
