package wallet

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/CreditsCommunity/wallet/internal/config"
)

// A node's key in the current folder: found (never used by itself), with its address; refused on
// unsafe permissions; not offered when a node's key is configured, node_key is off, or the user
// said not to ask again.
func TestFindLocalNodeKey(t *testing.T) {
	t.Setenv(NodeKeyEnv, "")
	data := t.TempDir()
	dir := nodeDir(t)
	if FindLocalNodeKey(dir, data, nil, false) != nil {
		t.Fatal("found without a key")
	}
	os.WriteFile(filepath.Join(dir, "NodePrivate.txt"), []byte(nkSecret), 0o600)
	f := FindLocalNodeKey(dir, data, nil, false)
	if f == nil || f.Refused != nil || f.Address != nkAddress || f.Encrypted || f.Path != filepath.Join(dir, "NodePrivate.txt") {
		t.Fatalf("%+v", f)
	}
	if h := LocalNodeKeyHint(f); !strings.Contains(h, "--node-key NodePrivate.txt") {
		t.Errorf("hint %q", h)
	}
	if FindLocalNodeKey(dir, data, NodeKeyIn(dir, "x"), false) != nil || FindLocalNodeKey(dir, data, nil, true) != nil {
		t.Error("offered with a node's key configured or off")
	}
	if !NodeKeyExplicitlyOff(config.Profile{NodeKey: "off"}, "") || NodeKeyExplicitlyOff(config.Profile{NodeKey: "off"}, "x") || NodeKeyExplicitlyOff(config.Profile{}, "") {
		t.Error("NodeKeyExplicitlyOff")
	}
	if runtime.GOOS != "windows" {
		os.Chmod(filepath.Join(dir, "NodePrivate.txt"), 0o644)
		if f := FindLocalNodeKey(dir, data, nil, false); f == nil || f.Refused != nil || !strings.Contains(f.Warning, "chmod 600") {
			t.Errorf("readable: %+v", f)
		}
		os.Chmod(filepath.Join(dir, "NodePrivate.txt"), 0o666)
		if f := FindLocalNodeKey(dir, data, nil, false); f == nil || f.Refused == nil || f.Address != "" {
			t.Errorf("writable by others: %+v", f)
		}
		os.Chmod(filepath.Join(dir, "NodePrivate.txt"), 0o600)
		os.Chmod(dir, 0o777)
		if f := FindLocalNodeKey(dir, data, nil, false); f == nil || f.Refused == nil || !strings.Contains(f.Refused.Error(), "can be written by other users") {
			t.Errorf("folder writable by others: %+v", f)
		}
		os.Chmod(dir, 0o755)
	}
	// encrypted by the node: the address from NodePublic.txt
	lightNodeKDF(t)
	writeEncryptedNodeKey(t, dir, nkSecret, "pw")
	if f := FindLocalNodeKey(dir, data, nil, false); f == nil || !f.Encrypted || f.Address != nkAddress {
		t.Errorf("encrypted: %+v", f)
	}
	// don't ask again
	if err := IgnoreNodeKey(data, filepath.Join(dir, "NodePrivate.txt")); err != nil {
		t.Fatal(err)
	}
	if err := IgnoreNodeKey(data, "/other/NodePrivate.txt"); err != nil {
		t.Fatal(err)
	}
	if FindLocalNodeKey(dir, data, nil, false) != nil {
		t.Error("offered after don't ask again")
	}
	list, _ := os.ReadFile(filepath.Join(data, NodeKeyIgnoredFile))
	if strings.Count(string(list), "NodePrivate.txt") != 2 {
		t.Errorf("list: %q", list)
	}
	if err := IgnoreNodeKey(data, "relative"); err == nil {
		t.Error("a relative path accepted")
	}
	// use it: the node's key account for this run
	w := New(config.Default(), config.Profile{Address: config.DefaultAddress}, t.TempDir())
	w.UseFoundNodeKey(&FoundNodeKey{Path: filepath.Join(dir, "NodePrivate.txt")}, "test")
	if nf, err := w.NodeAccount(); err != nil || nf == nil || nf.Public != nkAddress || !nf.NodeKeyEncrypted {
		t.Fatalf("%+v %v", nf, err)
	}
}
