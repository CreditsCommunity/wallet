# AGENTS.md: credits-wallet

Instructions for coding agents working in this repository. Read this before changing anything.

## What this is

`credits-wallet`, an open-source terminal wallet for the CREDITS network, in Go. It runs next to a
node, keeps the keys on the machine (encrypted), signs every transaction itself and sends it through
the node's Thrift API (`127.0.0.1:9090` by default). It opens no port. One static binary per
platform, with a full-screen interface (Bubble Tea) and scriptable commands (`--json`).

Related repositories of [CreditsCommunity](https://github.com/CreditsCommunity):

- `node`: the community node. Its packages and Docker image ship the wallet release named in its
  `packaging/wallet-tag`, pinned by hash in `packaging/wallet-sha256`; its `samples/` are the source
  of the wallet's signing vectors, fake node and contract templates.
- `thrift-interface-definitions`: the API's IDL, the submodule `third-party/thrift-interface-definitions`
  (branch `community/network-id`, the commit the node pins).
- `contract-executor`, `smart-contract-api`: the contracts the wallet deploys and calls run there.

## Layout

| Path | Contents |
|---|---|
| `cmd/credits-wallet/` | `main`: flags, commands, prompts, process hardening |
| `internal/keys/` | key generation, 24-word phrase, encrypted key store, node key files, backup, locked memory |
| `internal/tx/` | building and signing transfers, delegations, contract deploys and calls |
| `internal/node/` | API client (calls, retries, sync state, diag port, contracts, tokens) |
| `internal/wallet/` | what the commands and the interface share: accounts, transfer steps, contracts |
| `internal/tui/` | terminal interface screens |
| `internal/amount/`, `internal/config/`, `internal/history/`, `internal/templates/` | amounts and fee encoding, TOML config and data folder, history sources, embedded example contracts |
| `thrift/` | `generate.go`; the generated packages (`api`, `apidiag`, `general`) are not committed |
| `testdata/` | fake node (Python), signing vectors, public test keys |
| `ci/privnet/e2e.sh`, `ci/release/build.sh` | end-to-end test on a private network, reproducible release build |
| `docs/spec.md`, `docs/guide.md`, `docs/guide.it.md` | specification, user guide (English, Italian) |

## Build and test

Go 1.26 or later (`go.mod` names the toolchain go1.27.3, which `go` fetches itself) and the Thrift
compiler **0.19.0** as `thrift` on the `PATH` (it must match `github.com/apache/thrift v0.19.0`).

```sh
git submodule update --init
go generate ./thrift
go build -o credits-wallet ./cmd/credits-wallet
go vet ./... && gofmt -l cmd internal          # gofmt must print nothing
go test -count=1 ./...
go test -race -count=1 -run LockedKey ./internal/keys
```

The offline tests against `testdata/fake_node.py` need `python3` with
`pip install --require-hashes -r testdata/requirements.txt` (in a venv); without it they are skipped,
and `CREDITS_WALLET_FAKE_NODE=1` makes them mandatory as in CI.

## CI

- `ci.yml` (every push to `main` and every PR): `go generate`, `go vet`, gofmt, `govulncheck`, the
  tests with the fake node, the race test of the locked key, cross-compiles for linux/amd64,
  linux/arm64, windows/amd64, darwin/arm64. Must pass.
- `privnet.yml` (PRs to `main`, on demand): `ci/privnet/e2e.sh` on 5 community nodes with their
  executors; the node is built from a pinned release commit with `-DCREDITS_TESTNET=ON`.
- `release.yml`: on PRs only builds. A tag `wallet-vX.Y.Z` on `main` publishes a release,
  `wallet-vX.Y.Z-rcN` a pre-release; a second runner must reproduce the same `SHA256SUMS`. Release
  tags are protected by a ruleset (no deletion or update) and only maintainers create them. A node
  release then bumps `packaging/wallet-tag` and `packaging/wallet-sha256` together.

## Security rules (keys and the recovery phrase)

- Keys never leave the machine; the wallet never sends a secret to the node. Only signed
  transactions go out.
- Never print, log or return in `--json` a private key, a password or the 24-word phrase, except
  through the explicit paths the spec defines (`--show-words`, `--words-file`, the recovery file,
  key export on request).
- Secret files are written with `WriteSecretFile` (mode 0600, created new, never over an existing
  file); key files are written whole and atomically. Keep the folder checks (0700) and warnings.
- Decrypt a key only to sign and zero it after; the interface keeps the unlocked key in locked
  memory (`keys.LockedKey`). Do not copy secrets into Go strings or long-lived buffers.
- Mainnet is explicit: sending asks for confirmation with amount, fee, signed max fee and recipient,
  and an unsynced node or a network mismatch is refused. Do not weaken these checks.
- Keys in `testdata/` and `internal/keys/testdata/` are public test values: never fund them, and
  never add a real key to the repository.
- Changes to keys, signing or the key store need a security review before release.

## Platform constraints (contracts)

- Mainnet runs the official node 4.2.532 with executor build 1518. On mainnet a contract cannot call
  another contract (1518 fails every `invokeExternalContract`, read-only calls included), and no
  value passes from one contract to another (the node refuses a contract's `sendTransaction` to a
  contract). Do not offer features that rely on either.
- A call must pass each argument as the `Variant` of the declared type (amounts in a contract's
  `sendTransaction` are `double`s): a wrong type makes the call fail, and the fee is still paid.
  The wallet reads the declared types from the node before building a call.
- A signed transaction names no network: one signed for a test network is valid on mainnet.

## Conventions

- One topic per pull request, against `main`. Docs, comments and commit messages in English
  (`docs/guide.it.md` is the Italian translation: update it with `docs/guide.md`).
- Keep `docs/spec.md`, the README and the guide in line with behaviour changes.
- Never `git add -A` or `git add .`: stage explicit paths; check that the submodule gitlink did not
  change. Never delete branches after a merge.

## More

- `README.md` (threat model, tests, releases), `docs/spec.md` (design, trust in the node, quality),
  `docs/guide.md`
- The node's `samples/contracts/README.md`, `samples/clients/README.md`, `docs/tokens.md`
- Maintainers' agents: a shared Basic Memory knowledge base (project `credits`) holds platform facts,
  procedures and incident notes; use it when your environment provides it.
