package templates

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// The embedded files are the node's samples/contracts at the commit of Source, unchanged
// (sha256sum of the files at that commit). ci/privnet/e2e.sh compares them with the node repository.
func TestTemplatesAreTheNodeSamples(t *testing.T) {
	want := map[string]string{
		"Counter":   "d65914bf772f0a3795d63dad428b0015df9ebac8a4e5142630114d2edfc1859c",
		"Faucet":    "324be69212bea79a71290de9f708797bb5dba0f1b9fdff29666f6c00f0f815f0",
		"TestToken": "56d1190372f88b3e031aab07f0dd62593c0c56098b8beaef3758f6b594caea2c",
		"TipJar":    "683ef4b2f7800ee73418370740708f35a9a91c20415477110c1030ee037bc247",
		"Token":     "018567231919b4a7d8054a5abf05c766cd3f1a68a9b43efbe1fa0c3379abd306",
	}
	if len(List()) != len(want) {
		t.Fatalf("%d templates", len(List()))
	}
	for name, sum := range want {
		src, tpl, err := Get(name)
		if err != nil || tpl.Name != name {
			t.Fatalf("%s: %v", name, err)
		}
		got := sha256.Sum256([]byte(src))
		if hex.EncodeToString(got[:]) != sum {
			t.Errorf("%s: sha256 %x", name, got)
		}
	}
	if _, _, err := Get("counter"); err != nil {
		t.Error("names are case-insensitive")
	}
	if _, _, err := Get("Nope"); err == nil {
		t.Error("unknown template")
	}
}
