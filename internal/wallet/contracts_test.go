package wallet

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
	"github.com/CreditsCommunity/wallet/thrift/general"
)

func method(name, ret string, types ...string) *general.MethodDescription {
	m := &general.MethodDescription{Name: name, ReturnType: ret}
	for i, t := range types {
		m.Arguments = append(m.Arguments, &general.MethodArgument{Type: t, Name: string(rune('a' + i))})
	}
	return m
}

func TestResolveMethodAndTypedParams(t *testing.T) {
	methods := []*general.MethodDescription{
		method("add", "int", "int"),
		method("add", "int", "int", "int"),
		method("transfer", "boolean", "java.lang.String", "java.math.BigDecimal"),
		method("payable", "java.lang.String", "java.math.BigDecimal", "byte[]"),
		method("getValue", "int"),
	}
	methods[4].Annotations = []*general.Annotation{{Name: node.GetterAnnotation}}
	m, err := ResolveMethod(methods, "add", 2)
	if err != nil || len(m.Arguments) != 2 {
		t.Fatalf("overload by count: %v %v", m, err)
	}
	if _, err := ResolveMethod(methods, "add", 3); err == nil || !strings.Contains(err.Error(), "add(int a) : int") {
		t.Errorf("wrong count: %v", err)
	}
	if _, err := ResolveMethod(methods, "nope", 0); err == nil {
		t.Error("unknown method")
	}
	if !node.IsGetter(methods[4]) || node.IsGetter(methods[0]) {
		t.Error("@Getter")
	}
	if !HasPayable(methods) || HasPayable(methods[:3]) {
		t.Error("payable")
	}
	p, err := TypedParams(methods[2], []string{"u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w", "250.5"})
	if err != nil || p[0].VString == nil || p[1].VBigDecimal == nil || *p[1].VBigDecimal != "250.5" {
		t.Fatalf("typed: %+v %v", p, err)
	}
	if _, err := TypedParams(methods[2], []string{"x", "lots"}); err == nil || !strings.Contains(err.Error(), "argument 2 (BigDecimal b)") {
		t.Errorf("a bad BigDecimal: %v", err)
	}
	if _, err := TypedParams(methods[0], []string{"2147483648"}); err == nil {
		t.Error("an int out of range")
	}
	if _, err := TypedParams(methods[0], nil); err == nil {
		t.Error("missing argument")
	}
	// the names are the contract author's: escaped in messages
	evil := method("x\x1b[2J", "void", "int")
	if s := node.Signature(evil); strings.ContainsRune(s, 0x1b) {
		t.Errorf("signature not escaped: %q", s)
	}
}

func TestCheckSource(t *testing.T) {
	for _, bad := range []string{"", "  \n", strings.Repeat("x", MaxSource+1), "class A { \xff }"} {
		if CheckSource(bad) == nil {
			t.Errorf("accepted %.20q", bad)
		}
	}
	if CheckSource("public class A {}") != nil {
		t.Error("refused a source")
	}
}

func TestNetworkMismatch(t *testing.T) {
	mainnet := node.NetworkID{UUID: node.MainnetUUID, Version: "4.2.532-community.17"}
	other := node.NetworkID{UUID: 1234567890123456789}
	p := func(n config.Network, pin string) config.Profile { return config.Profile{Network: n, NetworkUUID: pin} }
	for _, c := range []struct {
		profile config.Profile
		id      node.NetworkID
		ok      bool
	}{
		{p(config.Mainnet, ""), mainnet, true},
		{p("", ""), mainnet, true}, // mainnet assumed
		{p(config.Testnet, ""), mainnet, false},
		{p(config.Private, ""), mainnet, false},
		{p(config.Mainnet, ""), other, false},
		{p("", ""), other, false},
		{p(config.Private, ""), other, true},
		{p(config.Testnet, ""), node.NetworkID{UUID: node.OldTestnetUUID}, true},
		{p(config.Private, "1234567890123456789"), other, true},
		{p(config.Private, "42"), other, false},
		{p(config.Mainnet, "42"), mainnet, false},
		// the node does not say: the configuration decides
		{p(config.Testnet, ""), node.NetworkID{Version: "4.2.532-community.16"}, true},
		{p(config.Mainnet, ""), node.NetworkID{}, true},
	} {
		err := NetworkMismatch(c.profile, c.id)
		if (err == nil) != c.ok {
			t.Errorf("%+v %+v: %v", c.profile, c.id, err)
		}
	}
}

func TestLocalContractListAndMerge(t *testing.T) {
	dir := t.TempDir()
	w := &Wallet{DataDir: dir}
	me := ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
	addr := func(i byte) ed25519.PublicKey {
		return ed25519.NewKeyFromSeed(append(make([]byte, 31), i)).Public().(ed25519.PublicKey)
	}
	if l, err := w.LocalContracts(me); err != nil || len(l) != 0 {
		t.Fatalf("empty list: %v %v", l, err)
	}
	t0 := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	for i, name := range []string{"Counter", "TipJar", "Old"} {
		c := LocalContract{Address: keys.Address(addr(byte(i + 1))), Name: name, File: "/x/" + name + ".java", Network: "private",
			Node: "127.0.0.1:9090", Deployed: t0.Add(time.Duration(i) * time.Hour), InnerID: int64(i + 1)}
		if err := w.AddLocalContract(me, c); err != nil {
			t.Fatal(err)
		}
	}
	// the same address again replaces its entry
	if err := w.AddLocalContract(me, LocalContract{Address: keys.Address(addr(1)), Name: "Counter", File: "/y/Counter.java",
		Deployed: t0, InnerID: 1}); err != nil {
		t.Fatal(err)
	}
	local, err := w.LocalContracts(me)
	if err != nil || len(local) != 3 {
		t.Fatalf("local: %v %v", local, err)
	}
	path := filepath.Join(ContractsDir(dir), keys.Address(me)+".json")
	if st, err := os.Stat(path); err != nil || (runtime.GOOS != "windows" && st.Mode().Perm() != 0o600) {
		t.Errorf("list file: %v %v", st, err)
	}
	// another account's file under this name is refused
	other := ed25519.NewKeyFromSeed(append(make([]byte, 31), 9)).Public().(ed25519.PublicKey)
	data, _ := os.ReadFile(path)
	os.WriteFile(filepath.Join(ContractsDir(dir), keys.Address(other)+".json"), data, 0o600)
	if _, err := w.LocalContracts(other); err == nil {
		t.Error("a list of another account was read")
	}

	onNode := []node.ContractInfo{
		{Address: addr(1), Classes: []string{"Counter"}, Created: t0.Add(time.Minute), Transactions: 3},
		{Address: addr(4), Classes: []string{"FromElsewhere"}, Created: t0.Add(5 * time.Hour)},
		{Address: addr(4), Classes: []string{"FromElsewhere"}, Created: t0.Add(5 * time.Hour)},
	}
	rows := MergeContracts(onNode, local)
	if len(rows) != 4 {
		t.Fatalf("rows: %+v", rows)
	}
	want := []struct {
		name   string
		onNode bool
		local  bool
	}{{"FromElsewhere", true, false}, {"Old", false, true}, {"TipJar", false, true}, {"Counter", true, true}}
	for i, w := range want {
		r := rows[i]
		if r.Name != w.name || r.OnNode != w.onNode || (r.Local != nil) != w.local {
			t.Errorf("row %d: %+v, want %+v", i, r, w)
		}
	}
	if rows[3].Local.File != "/y/Counter.java" || rows[3].Transactions != 3 {
		t.Errorf("merged row: %+v", rows[3])
	}
}
