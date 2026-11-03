#!/usr/bin/env bash
# Reproducible release archives of credits-wallet: build.sh <version> <out dir>
#
# For linux-amd64, windows-amd64 and darwin-arm64, all cross-compiled on one machine with CGO off
# (static binaries, no C toolchain; macOS needs no native runner to build). Run from the repository
# root after `go generate ./thrift` (Thrift 0.19.0), with the Go toolchain of go.mod.
#
# The same commit, version and toolchain give byte-identical archives:
#   - go build -trimpath -buildvcs=true (the commit is recorded: `go version -m credits-wallet`)
#     with fixed ldflags: -s -w -buildid= -X main.version=<version>;
#   - every file of the archives dated at the commit's time (SOURCE_DATE_EPOCH), owned by 0:0,
#     mode 755 or 644, in name order; gzip without a name or time; zip without extra fields.
#
# Each archive holds a folder credits-wallet-<version>/ with the binary (credits-wallet, or
# credits-wallet.exe), README.md, LICENSE and the user guide (guide.md, guide.it.md). SHA256SUMS
# lists the archives.
set -euo pipefail

version=$1 out=$2
echo "$version" | grep -qE '^[0-9A-Za-z][0-9A-Za-z._-]*$' || { echo "bad version '$version'" >&2; exit 1; }
[ -f go.mod ] && [ -d thrift/api ] || { echo "run from the repository root, after go generate ./thrift" >&2; exit 1; }

export CGO_ENABLED=0 GOFLAGS= GOTOOLCHAIN=${GOTOOLCHAIN:-auto} TZ=UTC LC_ALL=C
SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct)}
export SOURCE_DATE_EPOCH
umask 022

mkdir -p "$out"
out=$(cd "$out" && pwd)
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT

for target in linux/amd64 windows/amd64 darwin/arm64; do
    os=${target%/*} arch=${target#*/}
    name=credits-wallet-$version
    dir=$stage/$os-$arch/$name
    mkdir -p "$dir"
    exe=credits-wallet
    [ "$os" != windows ] || exe=credits-wallet.exe
    GOOS=$os GOARCH=$arch go build -trimpath -buildvcs=true \
        -ldflags "-s -w -buildid= -X main.version=$version" -o "$dir/$exe" ./cmd/credits-wallet
    install -m 644 README.md LICENSE docs/guide.md docs/guide.it.md "$dir/"
    chmod 755 "$dir/$exe"
    find "$dir" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +
    archive=$name-$os-$arch
    if [ "$os" = windows ]; then
        rm -f "$out/$archive.zip"
        (cd "$stage/$os-$arch" && find "$name" -print | sort | zip -X -D -q "$out/$archive.zip" -@)
    else
        (cd "$stage/$os-$arch" && tar --sort=name --owner=0 --group=0 --numeric-owner --format=gnu \
            --mtime="@$SOURCE_DATE_EPOCH" -cf - "$name") | gzip -9n > "$out/$archive.tar.gz"
    fi
    echo "$out/$archive"
done

(cd "$out" && sha256sum credits-wallet-"$version"-*.tar.gz credits-wallet-"$version"-*.zip > SHA256SUMS && cat SHA256SUMS)
