package node

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/CreditsCommunity/wallet/thrift/apidiag"
	"github.com/CreditsCommunity/wallet/thrift/general"
)

// The executor's message for a source that does not compile: the root cause's stack trace, as
// ContractExecutorServiceUtils.defineFailureCode sends it, with CompilationException's message
// ("compilation errors in class X :" then "LINE:message" per error).
const compileMessage = "com.credits.general.util.compiler.CompilationException: compilation errors in class Counter :\n" +
	"31:';' expected\n" +
	"55:cannot find symbol\n  symbol:   variable nte\n  location: class Counter\n" +
	"\tat com.credits.general.util.compiler.InMemoryCompiler.compileSourceCode(InMemoryCompiler.java:112)\n" +
	"\tat com.credits.service.contract.ContractExecutorServiceImpl.compileContractClass(ContractExecutorServiceImpl.java:134)"

func TestParseCompileErrors(t *testing.T) {
	errs := ParseCompileErrors(compileMessage)
	if len(errs) != 2 || errs[0].Line != 31 || errs[0].Text != "';' expected" || errs[1].Line != 55 ||
		!strings.HasPrefix(errs[1].Text, "cannot find symbol\n  symbol:   variable nte") {
		t.Fatalf("%+v", errs)
	}
	if e := ParseCompileErrors("x\ncompilation errors in class A :\n3:7: oops"); len(e) != 1 || e[0].Line != 3 || e[0].Column != 7 || e[0].Text != "oops" {
		t.Errorf("with a column: %+v", e)
	}
	if e := ParseCompileErrors("java.lang.RuntimeException: something else\n\tat x"); len(e) != 0 {
		t.Errorf("not a compile error: %+v", e)
	}
	f := &CompileFailure{Errors: []CompileError{{Line: 2, Text: "bad \x1b[2J"}}}
	if strings.ContainsRune(f.Error(), 0x1b) {
		t.Error("compile error not escaped")
	}
}

func TestExecutorCompileBugMessage(t *testing.T) {
	// what a node answers today for a source with errors (seen on a private network with the
	// executor of release community.16): no error list
	if e := ParseCompileErrors(ExecutorCompileBug); len(e) != 0 {
		t.Errorf("%+v", e)
	}
}

func TestReadNodeInfo(t *testing.T) {
	ok := &general.APIResponse{Code: 0}
	wire := int64(-7421784488367613980) // mainnet's uuid as the i64 carries it
	ver := "4.2.532-community.17"
	id, err := readNodeInfo(&apidiag.NodeInfoRespone{Result_: ok, Info: &apidiag.NodeInfo{NetworkUuid: &wire, CommunityVersion: &ver}})
	if err != nil || id.UUID != MainnetUUID || !id.IsMainnet() || id.Version != ver || !strings.Contains(id.Describe(), "MAINNET") {
		t.Fatalf("%+v %v", id, err)
	}
	other := int64(1234)
	if id, err := readNodeInfo(&apidiag.NodeInfoRespone{Result_: ok, Info: &apidiag.NodeInfo{NetworkUuid: &other}}); err != nil || id.IsMainnet() || id.UUID != 1234 {
		t.Errorf("another network: %+v %v", id, err)
	}
	// an official node, or an older community release: no field
	if id, err := readNodeInfo(&apidiag.NodeInfoRespone{Result_: ok, Info: &apidiag.NodeInfo{Version: "532"}}); !errors.Is(err, ErrNoNetworkID) || id.Known() {
		t.Errorf("official: %+v %v", id, err)
	}
	zero := int64(0)
	if _, err := readNodeInfo(&apidiag.NodeInfoRespone{Result_: ok, Info: &apidiag.NodeInfo{NetworkUuid: &zero, CommunityVersion: &ver}}); !errors.Is(err, ErrNoNetworkID) {
		t.Errorf("uuid 0: %v", err)
	}
	if _, err := readNodeInfo(&apidiag.NodeInfoRespone{Result_: &general.APIResponse{Code: 1, Message: "no"}, Info: &apidiag.NodeInfo{NetworkUuid: &wire}}); !errors.Is(err, ErrNoNetworkID) {
		t.Errorf("failure status: %v", err)
	}
	if _, err := readNodeInfo(nil); !errors.Is(err, ErrNoNetworkID) {
		t.Error("nil")
	}
	// nothing listening: no identity, quickly
	c := New("127.0.0.1:1")
	c.DiagAddress = "127.0.0.1:1"
	if _, err := c.NetworkIdentity(t.Context()); !errors.Is(err, ErrNoNetworkID) {
		t.Errorf("closed diag port: %v", err)
	}
}

// hostileServer answers the first call on a connection with a reply that announces a string of
// size bytes, then sends nothing more.
func hostileServer(t *testing.T, size int32) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				// the call: version (4), name (4 + n), seqid (4), then the arguments
				var head [8]byte
				if _, err := io.ReadFull(conn, head[:]); err != nil {
					return
				}
				name := make([]byte, binary.BigEndian.Uint32(head[4:]))
				var seq [4]byte
				if _, err := io.ReadFull(conn, name); err != nil {
					return
				}
				if _, err := io.ReadFull(conn, seq[:]); err != nil {
					return
				}
				var b []byte
				b = binary.BigEndian.AppendUint32(b, 0x80010002) // a reply
				b = binary.BigEndian.AppendUint32(b, uint32(len(name)))
				b = append(b, name...)
				b = append(b, seq[:]...)
				b = append(b, 12, 0, 0)  // success: a struct
				b = append(b, 11, 0, 99) // an unknown string field, skipped by reading it
				b = binary.BigEndian.AppendUint32(b, uint32(size))
				conn.Write(b)
				time.Sleep(5 * time.Second) // and nothing more
			}(conn)
		}
	}()
	return l.Addr().String()
}

// Security review L4: an answer that announces a field larger than MaxThriftSize fails at once,
// instead of the wallet allocating it and waiting for its bytes. Both connections: the API and the
// diag port.
func TestThriftSizeLimit(t *testing.T) {
	addr := hostileServer(t, 64<<20)
	c := New(addr)
	c.SocketTimeout = 3 * time.Second
	start := time.Now()
	_, err := c.Status(t.Context())
	if err == nil || time.Since(start) > 2*time.Second || !strings.Contains(err.Error(), "size") {
		t.Errorf("API: %v after %v", err, time.Since(start))
	}
	c.DiagAddress = addr
	start = time.Now()
	if _, err := c.NetworkIdentity(t.Context()); !errors.Is(err, ErrNoNetworkID) || time.Since(start) > 2*time.Second || !strings.Contains(err.Error(), "size") {
		t.Errorf("diag: %v after %v", err, time.Since(start))
	}
	// below the limit the bytes are awaited (here: until the socket times out)
	small := New(hostileServer(t, 1<<20))
	small.SocketTimeout = 500 * time.Millisecond
	if _, err := small.Status(t.Context()); err == nil || strings.Contains(err.Error(), "size") {
		t.Errorf("a field below the limit: %v", err)
	}
}
