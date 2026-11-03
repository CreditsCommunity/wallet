// Package thrift holds the Go code of the node API, generated from the node's IDL
// (third-party/thrift-interface-definitions, a git submodule) by the Thrift compiler 0.19.0:
//
//	git submodule update --init
//	go generate ./thrift
//
// The generated packages (api, general) are not committed.
package thrift

//go:generate sh -c "rm -rf api general && thrift -r --gen go:package_prefix=github.com/CreditsCommunity/wallet/thrift/,skip_remote -out . -I ../third-party/thrift-interface-definitions ../third-party/thrift-interface-definitions/api.thrift"
