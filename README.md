# credits-wallet

An open-source terminal wallet for the CREDITS network that runs next to a node, on Linux, Windows and
macOS: keys kept on the machine, transfers, delegations, and smart contracts compiled, deployed and
called through the local node. No browser, no web server, no open ports.

Status: **milestone 1** (accounts, key store, status, balances, transfers, history from the local
node, the terminal interface and the scriptable commands). Delegations, tokens, a remote history
node and smart contracts come in the next milestones — see [docs/spec.md](docs/spec.md) §10.

Part of the [CreditsCommunity](https://github.com/CreditsCommunity) projects, next to the
[community node](https://github.com/CreditsCommunity/node).

## Build

Needs Go 1.23 or later and the Thrift compiler **0.19.0** as `thrift` on the `PATH`
(`sudo apt install thrift-compiler` on Ubuntu 24.04; it must match `github.com/apache/thrift v0.19.0`
in `go.mod`).

```sh
git clone --recurse-submodules https://github.com/CreditsCommunity/wallet.git
cd wallet
go generate ./thrift        # Go code of the node API, from the IDL submodule (not committed)
go build -o credits-wallet ./cmd/credits-wallet
go test ./...
```

The node API's definitions are the git submodule `third-party/thrift-interface-definitions`
(`CREDITSCOM/thrift-interface-definitions` at the commit the community node pins); after a plain
clone run `git submodule update --init`. Cross-compiling needs no C compiler:
`CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./cmd/credits-wallet`.

The offline transfer test runs the node's fake node (`testdata/fake_node.py`) and needs `python3`
with `thriftpy2`, `base58` and `PyNaCl`; without them it is skipped.

## Run

```sh
./credits-wallet                       # the terminal interface
./credits-wallet status                # the node: last block, round, synced, network
./credits-wallet account new main      # a new account: write down its 24 words
./credits-wallet account import old ~/NodePrivate.txt
./credits-wallet balance               # every account
./credits-wallet history main --csv main.csv
./credits-wallet send main u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w 12.5   # SPENDS REAL CS ON MAINNET
./credits-wallet help
```

Global flags, anywhere on the line: `--node HOST:PORT`, `--profile NAME`, `--config FILE`, `--json`,
`--password-file FILE`.

### First steps

1. Run a node with its API on (`[api] port=9090` in its `config.ini`, the default); the wallet talks
   to `127.0.0.1:9090`.
2. `credits-wallet status` must say `synced: yes`; the wallet refuses to send otherwise.
3. Create an account (`account new NAME`, or `[n]` in the interface) and write its 24 words on
   paper: they are shown once, and restore the account (`account import NAME` and type them).
   Or import an existing key: the node's `NodePrivate.txt` (base58), a 24-word phrase of the node, or
   the keys JSON of the official web and desktop wallets. Delete the plain file afterwards.
4. Send: the wallet shows the recipient, the amount and the fee; on mainnet it asks you to type
   `send`, then the password.

### Accounts and files

- Data folder: `~/.credits-wallet` (Linux, macOS), `%APPDATA%\credits-wallet` (Windows), or
  `$CREDITS_WALLET_HOME`. Key files are in `accounts/`, one per account, mode 600.
- Key files are encrypted with your password: Argon2id (3 passes, 256 MiB, 4 threads, a random
  salt per file) and XChaCha20-Poly1305. There is no way to recover a forgotten password: keep the
  24 words.
- `account export NAME` copies the encrypted file; `--format base58` prints the secret key after the
  password; `--format official` writes the official wallets' **unencrypted** keys JSON, with a
  warning.

### Networks

The wallet assumes **mainnet** unless the configuration declares the node a test or private
network; mainnet is shown in red and asks for the full confirmation. `credits-wallet config` prints
an example `config.toml` (put it in the data folder):

```toml
node = "local"
[nodes.local]
address = "127.0.0.1:9090"
network = "mainnet"      # or "testnet", "private"
```

`--node` with an address that no profile declares is treated as mainnet.

### Scripts

`send --yes` skips the question, for scripts only; without a terminal the wallet refuses to send
without it. `--password-file FILE` reads the password from the first line of FILE: the password is
then on disk in clear, so keep the file `chmod 600`, on an encrypted disk, and only where
unattended sending is really needed. `--json` prints machine-readable results (and errors).

### History on a pruned node

A pruned node keeps only its latest blocks. The wallet says from which block the node's history
starts ("history from block N") and how many transactions the account has in all; older ones are on
full nodes (a remote history source comes in milestone 2).

## Licence

AGPL-3.0, as the node. `testdata/fake_node.py`, `testdata/sign-expected.txt` and
`testdata/test-key.txt` come from the community node's `samples/clients/testdata`. The keys in
`testdata/` and `internal/keys/testdata/` are public test values: never send funds to them.
