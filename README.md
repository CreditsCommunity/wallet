# credits-wallet

An open-source terminal wallet for the CREDITS network that runs next to a node, on Linux, Windows and
macOS: keys kept on the machine, transfers, delegations, and smart contracts compiled, deployed and
called through the local node. No browser, no web server, no open ports.

Status: **milestone 4**: accounts, key store, the node's own key as the account of a node's wallet,
status, balances, transfers with optional user data, delegations, tokens (every token held with its
contract address, also without the node's token cache; info, transfers), the history from the local
node or a remote full node with the token transfers and a CSV export, smart contracts (templates,
compile, deploy, methods, getters, calls, payments, the contract list), the network identity of
community nodes, the terminal interface and the scriptable commands; reproducible release archives,
shipped in the community node packages from release community.17 on. What is left for later:
[docs/spec.md](docs/spec.md) §10.

**User guide: [docs/guide.md](docs/guide.md)** (in Italian: [docs/guide.it.md](docs/guide.it.md)).

Part of the [CreditsCommunity](https://github.com/CreditsCommunity) projects, next to the
[community node](https://github.com/CreditsCommunity/node).

## Install

- **With the community node**: every package from release community.17 on has `credits-wallet`
  (`credits-wallet.exe` on Windows) in its `csnode` folder, next to the node, with this guide as
  `wallet-guide.md`; the Docker image has it on the `PATH`. Next to its node the wallet uses the
  node's own key as the account `node` (below); the node's set-up offers it, or makes the node's
  key from a 24-word phrase when the node has none yet.
- **On its own**: the [releases](https://github.com/CreditsCommunity/wallet/releases) have an
  archive for Linux x86-64 (static), Windows x64 and macOS Apple Silicon, and `SHA256SUMS`.

See the [guide](docs/guide.md#1-install-and-first-start).

## Build

Needs Go 1.26 or later (`go.mod` names the toolchain, go1.27.4, which `go` fetches by itself) and the Thrift compiler **0.19.0** as `thrift` on the `PATH`
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
(`CreditsCommunity/thrift-interface-definitions`, branch `community/network-id`, the commit the
community node pins: the original definitions plus the two optional network-identity fields of the
diagnostic API); after a plain clone run `git submodule update --init` (an older clone: `git
submodule sync` first, the URL changed). Cross-compiling needs no C compiler:
`CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./cmd/credits-wallet`.

The offline transfer test runs the node's fake node (`testdata/fake_node.py`) and needs `python3`
with the packages of `testdata/requirements.txt` (pinned with hashes:
`pip install --require-hashes -r testdata/requirements.txt` in a venv); without them it is skipped.

## Run

```sh
./credits-wallet                       # the terminal interface
./credits-wallet status                # the node: last block, round, synced, network
./credits-wallet account new main      # a new account: write down its 24 words
./credits-wallet account import old ~/keys.json    # then delete the plain file
./credits-wallet --config wallet.toml account list # in csnode: the node's key is the account "node"
./credits-wallet account node-key new              # the node has no key yet: make it from 24 words
./credits-wallet account export main --format node --out csnode   # an account's key as the node's
./credits-wallet balance               # every account
./credits-wallet history main --csv main.csv
./credits-wallet send main u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w 12.5   # SPENDS REAL CS ON MAINNET
./credits-wallet send main u6PJ8D…q2w 12.5 --data "invoice 42"           # with user data (public)
./credits-wallet delegate main NODE_ADDRESS 1000                       # delegate, no time limit
./credits-wallet delegate main NODE_ADDRESS 500 --until 2027-01-31     # until a UTC date
./credits-wallet undelegate main NODE_ADDRESS 1000                     # withdraw (no-limit delegations)
./credits-wallet delegations main                                      # made and received
./credits-wallet tokens main                                           # token balances
./credits-wallet token send main TOKEN_ADDRESS u6PJ8D…q2w 250.5        # a call of the token's transfer()
./credits-wallet history main --tokens                                 # token transfers
./credits-wallet contract new Counter Counter.java                     # a template
./credits-wallet contract compile Counter.java                         # errors with their line
./credits-wallet contract deploy main Counter.java                     # SPENDS CS ON MAINNET
./credits-wallet contract methods CONTRACT                             # types, getters
./credits-wallet contract get CONTRACT getValue                        # free, changes nothing
./credits-wallet contract call main CONTRACT add 4                     # a signed call
./credits-wallet contract pay main CONTRACT 3 --data "thanks"          # CS to its payable()
./credits-wallet contract list main
./credits-wallet backup export /media/usb/wallet.dat      # every account, under the wallet password
./credits-wallet backup import /media/usb/wallet.dat      # its wallet's password, lists, restores
./credits-wallet wallet migrate                           # a wallet from before: one password
./credits-wallet wallet password                          # change the wallet password
./credits-wallet help
```

Global flags, anywhere on the line: `--node HOST:PORT`, `--profile NAME`, `--config FILE`, `--json`,
`--password-file FILE`, `--history-node HOST:PORT`.

**One password for the whole wallet.** The wallet password, chosen with the first account, opens
every account: the interface asks it once, each command once. The node's key is the exception: a
plain `NodePrivate.txt` needs no password, and a key the node encrypted keeps the node's own
password. A wallet from before (a password per account) asks at its first start to choose the
wallet password, which may be one of the accounts' passwords, and each other account's password
once (`wallet migrate` on the command line); an account whose password you do not have stays as it
is ("old password") and can be moved later (`M`). `wallet password` (or `P`) changes it.

The terminal interface opens **locked**: it lists the accounts' names and addresses only, and the
wallet password unlocks the wallet and the chosen account (nothing of an account is shown before).
Signing in the session asks no password again (on mainnet you still type `send` on the full
review); `[l]` locks the wallet, `[a]` switches account (no password while the wallet is open), and
the wallet locks by itself (the open wallet too, with no account unlocked) after
`lock_after_minutes` without a key press (top of `config.toml`, default 10; `0` never locks, so
anyone at the terminal could open any account and sign). The account locks after another
is created or imported. The commands ask the wallet password once, when they sign.

In the terminal interface: `[s]` send (with an optional Data field), `[d]` delegations (`n` delegate,
`w` withdraw the selected one), `[t]` tokens (`s` send the selected token, `a` a token by its
address), `[h]` history (`t` token transfers, `e` CSV export), `[c]` contracts (`o` open a `.java`,
`n` new from a template, `a` a contract by address; on a source `e` edits it in your editor, `d`
deploys; on a contract Enter reads a getter or signs a call, `g` get, `c` call), `[S]` node status,
`[b]` backup (also on the unlock screen: `e` export, `i` import), `[P]` change the wallet password,
`[M]` move the accounts with an old password under the wallet password.

### First steps

1. Run a node with its API on (`[api] port=9090` in its `config.ini`, the default); the wallet talks
   to `127.0.0.1:9090`.
2. `credits-wallet status` must say `synced: yes`; the wallet refuses to send otherwise.
3. Create an account (`account new NAME`, or `[n]` in the interface: the first one also chooses
   the wallet password, typed twice) and write its 24 words on
   paper: they are shown once on the terminal (never in the output, `--json` included), you type
   three of them back, and they restore the account (`account import NAME` and type them). The
   screen is cleared afterwards, but a terminal's scrollback, tmux/screen or a session log may keep
   them. After the check the wallet asks whether to save the words to a file as well (default
   **no**): the file goes to `~/credits-recovery-NAME.txt` or a path you type, never in the data
   folder or a folder holding key files (the words beside the encrypted keys would make the
   password useless), mode 600, never overwriting; copy it to paper or an offline USB key and
   delete it from the computer. Without a terminal (scripts) `account new` needs
   `--words-file FILE` (a new file, mode 600) or `--show-words` (the words in the output).
   Or import an existing key: a base58 key, the keys JSON of the official web and desktop wallets
   (delete the plain file afterwards), or a 24-word phrase. Next to a node, the node's own key is
   already an account (see "The node's key" below): no import needed, and **never delete the
   node's `NodePrivate.txt`** (the node needs it).
   `y` in the interface copies the account's address (OSC 52, over SSH too; it is also shown whole
   on its own line for Shift+drag), and `account address NAME` prints it alone for `xclip`,
   `pbcopy` or `clip`.
4. Send: paste the recipient with the terminal's paste (Ctrl+Shift+V, right click): it is trimmed,
   a leading `credits:` is dropped, anything but an address is refused, and the address is shown
   with its first and last 6 characters highlighted to compare with the source. The review shows
   the recipient, the amount and the fee; on mainnet it asks you to type `send` (the commands then
   ask the password; the interface asked it when you unlocked the account).

### Accounts and files

- Data folder: `~/.credits-wallet` (Linux, macOS), `%APPDATA%\credits-wallet` (Windows), or
  `$CREDITS_WALLET_HOME`. Key files are in `accounts/`, one per account, mode 600.
- The wallet password opens `accounts/wallet.key`, a random master key sealed with Argon2id (3
  passes, 256 MiB, 4 threads, a random salt) and XChaCha20-Poly1305; the master key encrypts each
  account file (XChaCha20-Poly1305, a random nonce each). One derivation opens the whole wallet.
  `wallet.key` is needed by every account: it is in every backup. Lost or replaced, the accounts
  are listed as orphaned and nothing new is created until `backup import` puts them back (the
  orphaned files are kept in `accounts/.orphaned/`). There is no way to recover a forgotten
  password: keep the 24 words. Format, migration and change of password: docs/spec.md 4.2.
- `account import NAME FILE` also takes an encrypted key file (another wallet's export): its own
  password opens it, then it goes under the wallet password. `account export NAME` writes the key
  as an encrypted file of its own, under the wallet password by default or another one you type
  (`--out FILE`); `--format base58` prints the secret key after the wallet password; `--format official` writes the official wallets' **unencrypted** keys JSON, with a
  warning.
- `backup export FILE.dat` (`b` then `e` in the interface) writes **every account** into one file:
  the account files and `wallet.key`, the names and the local token and contract lists, all
  encrypted with **the wallet password** (Argon2id with the key files' parameters and its own salt,
  XChaCha20-Poly1305, the versioned header as associated data). The backup is only as strong as
  the wallet password, and a change of the wallet password does not re-encrypt backups already
  made: they keep opening with the old password. Never the node's key (`NodePrivate.txt` stays in
  the node's folder) nor `config.toml`. A new file, mode 600, never in the data folder or next to
  key files: keep it off the computer, on a USB key. `backup import FILE.dat` (`b` then `i`) asks
  the password of the wallet the backup comes from, lists the accounts and restores them once
  confirmed, under this wallet's password (asked once if it differs; `--wallet-password-file` for
  scripts): an address already in the wallet is skipped, a name taken by another address gets a
  new name (asked, or `--rename-suffix S`). Format: docs/spec.md 4.5.

### The node's key

Next to its node the wallet is **the node's wallet**: it uses the node's own key pair, in place, as
the account `node`, listed first, the default, and marked `NODE KEY`. That is always an explicit
choice: the package's `csnode/wallet.toml` (whose profile says `node_key = "NodePrivate.txt"` when the set-up was told to use the node's key),
`node_key` in a node profile of `config.toml`, `--node-key FILE`, or the node's Docker image
(`CREDITS_WALLET_NODE_KEY`); never a guess from the current folder, where someone could have
planted a key pair. The key file and its folder must be yours (or root's) and writable by nobody
else, and the wallet says the account's address and file the first time it reads it. The node's rewards and the delegations it receives go to that address,
and operators usually keep their stake there.

- `NodePrivate.txt` is read where the node reads it: never copied into the key store, never
  encrypted by the wallet (the node needs its file), no password asked to sign with a plain key.
  `node_key = "off"` turns it off.
- **A key the node encrypted** (`--encryptkey`, or "[1] Encrypt with password" at the node's first
  start) is opened with the node's password: on the interface's unlock screen, or when a command
  signs. The wallet implements the node's format (Argon2id 1 GiB with the node's fixed salt,
  original ChaCha20-Poly1305; docs/spec.md 4.3), checks the key against `NodePublic.txt`, and never
  writes the opened key anywhere or exports it in clear.
- **A key in the current folder** is never used by itself. The interface, started in a folder with
  `NodePrivate.txt` and no node's key configured, shows its address and path and offers: use it for
  this session, use it and remember it (`node_key` written into the active profile of
  `config.toml`, comments and other settings kept), ignore it, or ignore it for good. The commands
  print a one-line hint (`--node-key NodePrivate.txt`) and never use it.
- **The node's key is kept in clear on the server**, because the node needs it: CS on it are as
  exposed as the server. Keep on it only what the node needs, and savings in a separate account
  (`account new`).
- **No key yet**: `account node-key new` (or `g` in the interface) makes it from a new 24-word
  phrase, shown and checked as for `account new`, and writes `NodePrivate.txt` and
  `NodePublic.txt` in the node's folder, in exactly the node's format (mode 600, never over an
  existing file, never through a link, refused while the node runs): the node uses it at its first
  start, and its identity has a paper backup. The recovery file never goes in the node's folder.
- **An account's key as the node's**: `account export NAME --format node --out CSNODE_FOLDER`
  writes it in the node's format; it never overwrites the node's key. `--replace` replaces the
  node's identity (the node stopped; the old files kept as `NodePrivate.txt.bak-DATE` and
  `NodePublic.txt.bak-DATE`, mode 600). **The node's identity follows its key**: its address, the
  delegations it received, its history as a trusted node and its rewards.
- `account import NAME NodePrivate.txt` still makes an encrypted copy in the key store, but the
  original stays in clear for the node, so the copy protects nothing more.
- `account node-key` shows the key's file, its address, whether `NodePublic.txt` matches, whether
  the node is running and the `.bak-*` backups (old keys in clear: move them offline, then delete
  them); `account node-key repair` writes a missing or mismatching `NodePublic.txt` (the node
  stopped). A key file other users can read is used with a warning: `chmod 600 NodePrivate.txt`.

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

**Use separate accounts for test networks.** A signed transaction does not say which network it is
for: one signed on a test network can be replayed on mainnet by whoever runs or watches that test
node, if the account exists there (every key does) with the same next inner id. Never use an
account that holds CS on mainnet on another network.

A community node from release community.17 on tells its network through its diagnostic API
(`[api] diag_port` in its `config.ini`, 9088 in the release packages). The wallet asks it
(`diag = "HOST:PORT"` in the profile, by default the node's host with port 9088, `diag = "off"` not
to ask) and **refuses to send** when the node is on mainnet and the profile says testnet or private,
or the other way round (a profile without `network` counts as mainnet: declare a test network). A
profile can also pin the network: `network_uuid = "…"` (the uuid `status` shows), and
`require_network_uuid = true` sends only when the node confirms its network. An official node or
an older release does not tell its network: the configuration decides (but a pinned or required
uuid then refuses). Every confirmation says whether the node confirmed the network; `status` shows
what the node says.

### Scripts

`send --yes` skips the question, for scripts only; without a terminal the wallet refuses to send
without it. `--password-file FILE` reads the password from the first line of FILE: the password is
then on disk in clear, so the wallet refuses the file unless it is `chmod 600` (Linux, macOS); keep
it on an encrypted disk, and use it only where unattended sending is really needed. `--json`
prints machine-readable results (and errors). `history --csv FILE` never replaces an existing
file unless `--force` is given.

### Delegations

`delegate ACCOUNT NODE AMOUNT` delegates at least 5 CS to a node without a time limit, or with
`--until DATE` until that UTC date (`2027-01-31`, `"2027-01-31 18:00"`, or RFC 3339 with an offset);
the nodes release a delegation with a time limit by themselves once it expires. `undelegate` withdraws
CS from a delegation without a time limit (at most its amount, at least 5 CS). `delegations` lists
what an account delegated and received, with the expiry. On mainnet each asks for the same
confirmation as `send`.

### User data

`send … --data TEXT` attaches a text (UTF-8, at most 4096 bytes, no control characters but newline
and tab) to a transfer. **It is public**: anyone can read it on the chain. The history shows it, and
the CSV has it in its `data` column (a text that starts like a spreadsheet formula gets a `'` in
front).

### Tokens

`tokens ACCOUNT` (and `[t]` in the interface) lists every token the account holds, each with its full
contract address, decimals and balance, and the look-alike warnings; the main screen shows the
balances and the count. The list comes from the node's token cache (`tokens_cache=true` in the
`[api]` section of its `config.ini`, off by default), plus the tokens of the local token list (below)
that the cache does not name: a cache turned on after the node pruned, or on a node started from a
snapshot, knows every token but may miss holders whose last use of a token is older than the
node's oldest block. Without it the wallet uses the token cache of
the profile's `history_node` (balances still from the local node), or else finds the tokens in the
account's history (the latest 1000 transactions on the history node and the local node: the
contracts it called, deployed, paid or sent tokens of) and in its local token list
(`tokens/<address>.json` in the data folder: the tokens you added with `token add` or sent with the
wallet, nothing else), and reads each balance from the contract (`balanceOf`, free). A token the
account only received is marked "received from ADDRESS: not added by you". Such a list is labelled "found
from history (may be incomplete on a pruned node)": tokens received from others are not in the
account's own history. `token add ACCOUNT TOKEN` keeps a token listed (0 included), `token remove`
takes it off. In the interface `y` copies the selected address (OSC 52), `a` adds a token, `x` removes it. `token
balance TOKEN ACCOUNT` reads one balance from the token contract. `token info TOKEN` shows code, name and
decimals (and supply and holders with the cache). `token send ACCOUNT TOKEN TO AMOUNT` signs a call of
the token's `transfer(to, amount)`: the amount may have at most the token's decimals and must be
within the token balance; the CS balance must cover the signed max fee (1 CS), which the node holds
while the call runs, and the fee is about 0.0175 CS plus the execution. A token is identified only by
its contract address: anyone can deploy a token that calls itself "CS" or copies another token's
code. The confirmation shows the full address and warns about such look-alikes; check the address.

### Smart contracts

Contracts are Java classes compiled by the node's contract executor (see the node's
[samples/contracts](https://github.com/CreditsCommunity/node/tree/master/samples/contracts)).
`contract templates` lists the examples the wallet carries (the node's samples: Counter, TestToken,
Token, TipJar, Faucet); `contract new NAME FILE` writes one. `contract compile FILE` asks the node to
compile it and lists the errors as `FILE:LINE: message`, or the classes and methods.

`contract deploy ACCOUNT FILE` compiles again, shows the review (the contract's address, derived
from the account, the inner id and the byte code; the fee, about 0.1 CS for a small contract; the
signed max fee, 1 CS) and, on mainnet, asks you to type `send` and the password. Once the deploy is
in a block the wallet waits until the node lists the contract and saves it in the account's local
contract list (`contracts/` in the data folder: public data only). Sources up to 48 KiB, deploys
below 50 KiB (larger ones cost 17.87 CS).

`contract methods CONTRACT` lists the methods with their types (the node does not tell which ones
only read: its API drops the contracts' `@Getter` annotations; methods are marked "getter" only by
a node that passes them on). `contract get CONTRACT METHOD ARGS…` runs a method without a transaction: free,
nothing is kept (`--from ACCOUNT` sets the caller the contract sees). `contract call ACCOUNT
CONTRACT METHOD ARGS…` signs a call, about 0.0175 CS plus the execution; the arguments are converted
to the types the method declares (put `--` before an argument that starts with `-`), and the result
and the contract's state are shown. In the terminal interface the review lists every argument in
full; when it is taller than the screen, scroll it to the end (PgDn) before signing. A method that throws is refused by the node; the fee is paid.
`contract pay ACCOUNT CONTRACT AMOUNT [--data TEXT]` sends CS to a contract: the node runs its
`payable()`, which may refuse them (they come back, the fee is paid). `contract info CONTRACT` shows
the deployer and the state (the public fields: the node shows no others); `contract list ACCOUNT` the contracts the account deployed (the node's
list and the local one). Everything a contract says about itself (names, results, state) is shown
escaped.

### History on a pruned node, and a remote full node

A pruned node keeps only its latest blocks. The wallet says from which block the node's history
starts ("history from block N") and how many transactions the account has in all; older ones are on
full nodes. A node profile can read its history from a remote full node:

```toml
[nodes.local]
address = "127.0.0.1:9090"
history_node = "203.0.113.7:9090"   # read-only: history and token transfers, never used to send
```

or `--history-node HOST:PORT` on the line (`--history-node local` for the node itself). That node
receives the account's public address only; every history output names the source it comes from.
`history --csv FILE` writes the CS transactions and the token transfers in one table.

## Threat model and limits

- **What the wallet protects:** keys at rest (encrypted key files, mode 600 in a folder of mode
  700; the wallet warns when the data folder or `accounts/` is open to other users), keys in use
  (decrypted only to sign, then overwritten; in the interface, held locked for the unlocked
  session only), and the user against mistakes (mainnet assumed,
  explicit confirmation, the signed max fee shown, refusal while the node is not synced).
- **What it cannot protect against:** malware or another user with access to your account on this
  machine (they can read the terminal, replace the binary, or swap a key file for one of their
  own, so that what you receive goes to them); a forgotten password (keep the 24 words).
- **Memory:** Go cannot guarantee that secrets are wiped. The wallet zeroes the byte slices it
  controls; the interface keeps the unlocked key in memory of its own, locked against swapping, out
  of core dumps and inaccessible between signatures, zeroed on lock and exit (it says so when the
  system refuses the lock). Copies may remain: the TUI's text inputs hold passwords and pasted keys
  as immutable strings, `crypto/ed25519` keeps the expanded key it signs with on the heap without
  zeroing it, Argon2id's working memory is freed and not wiped, and a core dump or swap may hold
  any of them. Disable core dumps and use encrypted swap on machines holding funds.
- **The node:** the wallet trusts the node it talks to for balances, fees, inner ids, sync state
  and history, but never sends it a secret; see docs/spec.md, "Trust in the node".
- **Windows:** the wallet does not set ACLs itself. `%APPDATA%\credits-wallet` is private to the
  user by default; with `CREDITS_WALLET_HOME` outside the user's profile the wallet warns, and the
  folder's permissions are yours to check (Properties > Security).

## Tests and CI

- `go test ./...`: the node's key files byte for byte as the node writes them (with
  `NODE_KEY_CHECK` set to `internal/keys/testdata/node-key-check.cpp` built against the node, read
  by the node's own code), the node's encrypted key files (`testdata/node-encrypted-vectors.json`,
  encrypted and opened by the node's code; with `NODE_KEY_CRYPT` set to
  `internal/keys/testdata/node-key-crypt.cpp` built the same way, a round trip both ways), the
  unlock screen, the session and its locks, the wallet password (one password, the migration and
  the change of password, each interrupted at every step and resumed, the import and export of a
  single key, the node key's exception), the found-key prompt, the backup file (round trip,
  wrong password, damaged and truncated files, refused places, existing addresses and name
  clashes, the interface's flow), signing (byte-identical to the node's samples, and for user data, delegations,
  token calls, deploys and contract addresses to `testdata/sign-vectors.txt`, made by
  `testdata/sign-vectors.py` the way the node's `ci/testnet/fund.py`, `samples/python/token_client.py`
  and `samples/clients/python/credits_client.py` build them), key store, amounts, the network-identity
  rules, the commands against `testdata/fake_node.py` (contracts and its diag API included), the
  screens at 80x24.
- `.github/workflows/ci.yml`: vet, govulncheck, the tests, the four cross-compiled builds.
- `.github/workflows/release.yml`: the release archives (below); on pull requests it only builds.
- `.github/workflows/privnet.yml` (pull requests to main, on demand): `ci/privnet/e2e.sh` starts a
  private network of 5 community nodes with their contract executors and runs the wallet's commands
  end to end: the last node started with a key the wallet made (`account node-key new`), the node's
  key as an account (a send signed without a password, the refusals while a node runs), the
  export to the node's format with `--replace` (that node started again with the exported key),
  transfers, delegations, history, the network identity, and the contracts deployed,
  called and paid with the wallet (TestToken, Counter, TipJar). The executor comes from the community release package pinned in the workflow (tag, commit and
  package SHA-256, bumped deliberately); the
  node is the release commit built with `-DCREDITS_TESTNET=ON` (the package's node is a mainnet build,
  which cannot fund a private network), cached per release, so only the first run after a release
  builds it. Locally: `NODE_BIN=… PKG=…/csnode NODE_FILES=… IDL=third-party/thrift-interface-definitions
  WALLET=./credits-wallet ci/privnet/e2e.sh` (see the script's header).

## Releases

A tag `wallet-vX.Y.Z` (on main) publishes a release; `wallet-vX.Y.Z-rcN` a pre-release, from any
branch. `.github/workflows/release.yml` cross-compiles linux-amd64, windows-amd64 and darwin-arm64
on one Linux runner with CGO off (`ci/release/build.sh`), runs each binary on its own system
(`version` must print the tag's version), builds the tag again on a second runner (the same
`SHA256SUMS` or nothing is published), then publishes the archives and `SHA256SUMS`. Only the push
of a tag publishes. Each archive
holds `credits-wallet-X.Y.Z/` with the binary, README.md, LICENSE, guide.md and guide.it.md.

The build is reproducible: `-trimpath -buildvcs=true` (the commit is recorded, `go version -m
credits-wallet`), fixed ldflags (`-s -w -buildid= -X main.version=X.Y.Z`), every archived file dated
at the commit's time, owned by 0:0, in name order. To check a release:

```sh
git checkout wallet-v0.1.0 && git submodule update --init
go generate ./thrift                   # Thrift 0.19.0
ci/release/build.sh 0.1.0 dist         # Go 1.27.4 (go.mod's toolchain)
diff dist/SHA256SUMS <(curl -sSL https://github.com/CreditsCommunity/wallet/releases/download/wallet-v0.1.0/SHA256SUMS)
```

The community node pins the release it ships in `packaging/wallet-tag` and the archives' hashes in
`packaging/wallet-sha256`: release a wallet tag first, then bump both files, then release the node.

**Right after the repository goes public**, protect the release tags with a ruleset (no deletion, no
update; rulesets are not available on this private repository):

```sh
gh api -X POST repos/CreditsCommunity/wallet/rulesets --input - <<'EOF'
{"name": "release tags", "target": "tag", "enforcement": "active",
 "conditions": {"ref_name": {"include": ["refs/tags/wallet-v*"], "exclude": []}},
 "rules": [{"type": "deletion"}, {"type": "update"}, {"type": "non_fast_forward"}]}
EOF
```

## Licence

AGPL-3.0, as the node. `testdata/fake_node.py`, `testdata/sign-expected.txt` and
`testdata/test-key.txt` come from the community node's `samples/clients/testdata`;
`testdata/sign-vectors.py` follows the node's `ci/testnet/fund.py`, `samples/python/token_client.py` and
`samples/clients/python/credits_client.py`; the contract templates in `internal/templates/java` are the
node's `samples/contracts` (commit `079e788d`), unchanged;
`ci/privnet/e2e.sh` is a subset of the node's `ci/testnet/run.sh`; the contract-call and argument
code of `internal/tx` comes from the node's `samples/clients/go`. The keys in
`testdata/` and `internal/keys/testdata/` are public test values: never send funds to them.
