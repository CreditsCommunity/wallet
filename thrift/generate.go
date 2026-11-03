// Package thrift holds the Go code of the node API, generated from the node's IDL
// (third-party/thrift-interface-definitions, a git submodule) by the Thrift compiler 0.19.0:
//
//	git submodule update --init
//	go generate ./thrift
//
// The generated packages (api, apidiag, general) are not committed. apidiag is the node's diagnostic
// API (its diag port), used only for GetNodeInfo: the network identity of a community node.
package thrift

//go:generate sh -c "rm -rf api apidiag general && thrift -r --gen go:package_prefix=github.com/CreditsCommunity/wallet/thrift/,skip_remote -out . -I ../third-party/thrift-interface-definitions ../third-party/thrift-interface-definitions/api.thrift"
//go:generate thrift -r --gen go:package_prefix=github.com/CreditsCommunity/wallet/thrift/,skip_remote -out . -I ../third-party/thrift-interface-definitions ../third-party/thrift-interface-definitions/apidiag.thrift
