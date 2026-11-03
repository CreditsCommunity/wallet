package node

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/apache/thrift/lib/go/thrift"

	"github.com/CreditsCommunity/wallet/internal/echo"
	"github.com/CreditsCommunity/wallet/thrift/apidiag"
)

// The network identity of a community node (spec 5.1): its diagnostic API ([api] diag_port of the
// node's config.ini, 9088 in the release packages, off when 0) answers GetNodeInfo with
// NodeInfo.networkUuid (field 9) and NodeInfo.communityVersion (field 10), optional fields the
// community node fills from release community.17 (CreditsCommunity/node#162). The official node
// and older community releases leave them out: the configuration decides.

// MainnetUUID is the uuid of mainnet: the first 8 bytes of the hash of block 1 as a little-endian
// uint64 (BlockChain::uuid()), the constant uuidMainNet the node itself uses to recognise mainnet
// (csnode/src/node.cpp). Over the API's i64 it arrives as -7421784488367613980.
const MainnetUUID uint64 = 11024959585341937636

// OldTestnetUUID is the uuid of the old CREDITS test network.
const OldTestnetUUID uint64 = 5283967947175248524

// DiagPort is the diagnostic port of the node release packages (packaging/mainnet/config.ini).
const DiagPort = "9088"

// NetworkID is what a community node says of its network.
type NetworkID struct {
	UUID    uint64 // 0: the node did not say (no block 1 yet)
	Version string // the community package version, "" from an official node
}

// Known tells whether the node named its network.
func (n NetworkID) Known() bool { return n.UUID != 0 }

// IsMainnet: the node is on mainnet.
func (n NetworkID) IsMainnet() bool { return n.UUID == MainnetUUID }

// Describe is the identity for the screen: "mainnet (uuid …)", "another network (uuid …)".
func (n NetworkID) Describe() string {
	var s string
	switch {
	case !n.Known():
		s = "not reported"
	case n.IsMainnet():
		s = "MAINNET (uuid " + strconv.FormatUint(n.UUID, 10) + ")"
	case n.UUID == OldTestnetUUID:
		s = "the old CREDITS testnet (uuid " + strconv.FormatUint(n.UUID, 10) + ")"
	default:
		s = "not mainnet (uuid " + strconv.FormatUint(n.UUID, 10) + ")"
	}
	if n.Version != "" {
		s += ", community node " + echo.Printable(n.Version)
	}
	return s
}

// ErrNoNetworkID: the node does not tell its network: its diagnostic API is off or unreachable
// (diag address empty), or it is an official node or an older community release.
var ErrNoNetworkID = errors.New("the node does not tell its network")

// diagTimeout bounds the GetNodeInfo call: the diag port is optional, a closed one must not slow
// the wallet down.
const diagTimeout = 3 * time.Second

// NetworkIdentity asks the node's diagnostic API (DiagAddress) for its network. It returns
// ErrNoNetworkID (wrapped with the reason) when there is no diag address, nothing answers there, or
// the answer has no uuid.
func (c *Client) NetworkIdentity(ctx context.Context) (NetworkID, error) {
	if c.DiagAddress == "" {
		return NetworkID{}, fmt.Errorf("%w: no diagnostic API configured", ErrNoNetworkID)
	}
	conf := thriftConf(diagTimeout, diagTimeout)
	socket := thrift.NewTSocketConf(c.DiagAddress, conf)
	transport := thrift.NewTBufferedTransport(socket, 8192)
	if err := transport.Open(); err != nil {
		return NetworkID{}, fmt.Errorf("%w: its diagnostic API %s does not answer", ErrNoNetworkID, c.DiagAddress)
	}
	defer transport.Close()
	ctx, cancel := context.WithTimeout(ctx, diagTimeout)
	defer cancel()
	client := apidiag.NewAPI_DIAGClientFactory(transport, thrift.NewTBinaryProtocolFactoryConf(conf))
	r, err := client.GetNodeInfo(ctx, &apidiag.NodeInfoRequest{})
	if err != nil {
		return NetworkID{}, fmt.Errorf("%w: GetNodeInfo on %s: %s", ErrNoNetworkID, c.DiagAddress, echo.Printable(err.Error()))
	}
	return readNodeInfo(r)
}

func readNodeInfo(r *apidiag.NodeInfoRespone) (NetworkID, error) {
	if r == nil || r.Info == nil {
		return NetworkID{}, fmt.Errorf("%w: GetNodeInfo answered nothing", ErrNoNetworkID)
	}
	if r.Result_ != nil && r.Result_.Code != codeSuccess {
		return NetworkID{}, fmt.Errorf("%w: GetNodeInfo: %s", ErrNoNetworkID, echo.Printable(r.Result_.Message))
	}
	var id NetworkID
	if r.Info.CommunityVersion != nil {
		id.Version = *r.Info.CommunityVersion
	}
	if r.Info.NetworkUuid == nil || *r.Info.NetworkUuid == 0 {
		// an official node or an older community release (or a node without block 1 yet)
		return id, ErrNoNetworkID
	}
	// Thrift has no unsigned type: the uint64 travels as the same 64 bits in an i64
	id.UUID = uint64(*r.Info.NetworkUuid)
	return id, nil
}
