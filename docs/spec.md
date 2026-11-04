# Credits community wallet — specification

Status: milestones 1 to 4 implemented, 2026-11-03.

## 1. Purpose

An open-source wallet for the CREDITS network that runs **next to a node**, on the same machine, in a
**terminal**. It does what the original desktop wallet of the official release did (keys, transfers,
delegations, smart contracts compiled, deployed and called through the local node), without a
browser, a web server or open ports, on Linux, Windows and macOS, over SSH as well as on a desktop.

Non-goals for the first versions: a web or mobile interface (possible later on the same Go core), a
hardware-wallet integration, holding keys on a server.

## 2. Principles

- **Local first.** The wallet talks to the node's Thrift API on `127.0.0.1:9090` (configurable). It
  opens no listening socket.
- **Keys never leave the machine.** Keys are generated, stored and used on the disk of the user; every
  transaction is built and signed in the wallet; only signed transactions reach the node.
- **One static binary per platform**, no runtime to install, shipped as its own release and in the
  community node packages next to `node` and `setup.sh`.
- **The same binary is interactive and scriptable**: a full-screen terminal interface (TUI) by default,
  and plain commands (`credits-wallet send …`) for scripts and automation, with machine-readable output
  (`--json`).
- **Mainnet is explicit.** The network the node belongs to is shown on every screen; sending on mainnet
  always asks for a confirmation that shows the amount, the fee and the recipient.

## 3. Technology

| Part | Choice | Why |
|---|---|---|
| Language | Go (1.26+; `go.mod` pins the toolchain) | static binaries for linux/amd64, linux/arm64, windows/amd64, darwin/arm64; the sample Go client of the node already implements the API calls, signing, deploy and calls |
| Terminal UI | [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Lip Gloss](https://github.com/charmbracelet/lipgloss), [Bubbles](https://github.com/charmbracelet/bubbles) | modern TUI (panels, tables, forms, keyboard and mouse), works in Windows Terminal, macOS Terminal, Linux terminals and over SSH |
| Node API | Thrift 0.19 generated Go code from `CreditsCommunity/node` `third-party/thrift-interface-definitions` | the node's binary protocol, buffered transport |
| Crypto | `crypto/ed25519` (Go standard library), `golang.org/x/crypto` (blake2s for contract addresses, Argon2id for key files), `crypto/rand` | no C dependencies |
| Licence | AGPL-3.0, as the node | |

Repository: `CreditsCommunity/wallet`.

```
cmd/credits-wallet/      main: flags, commands, starts the TUI
internal/node/           API client: connection, calls, retries, sync state
internal/tx/             transfers, delegations, contract deploy and calls: building and signing
internal/keys/           key generation, import, the encrypted key store
internal/history/        history sources (local node, a remote full node)
internal/tui/            screens (Bubble Tea models)
internal/amount/         exact CS amounts and the node's 16-bit fee encoding
internal/config/         configuration file (TOML), node profiles, data folder
internal/wallet/         what the commands and the TUI share: accounts, the steps of a transfer,
                         contracts and their local list
internal/templates/      the example contracts (the node's samples/contracts, embedded)
thrift/                  generated code (go generate, not committed): api, apidiag, general
docs/                    this specification, the user guide (guide.md, guide.it.md)
ci/release/build.sh      the reproducible release archives
```

## 4. Keys

### 4.1 Formats

| Import or export | Format |
|---|---|
| Private key | base58 of the 64-byte ed25519 secret key (seed + public key), the node's `NodePrivate.txt` format (4.3); or base58 of the 32-byte seed |
| The node's key files | `NodePrivate.txt` and `NodePublic.txt` (4.3): used in place by a node's wallet (4.4), written by `account node-key new` and `account export --format node` |
| Seed phrase | the node's 24 words (English list of `cscrypto`): 256-bit master seed + 8-bit checksum, key derived with libsodium `crypto_kdf_derive_from_key` (key id 0, empty context) then `crypto_sign_seed_keypair` — the same derivation as `node` when it generates its keys; the checksum is verified |
| Keys JSON of the official web wallet (wallet.credits.com) and desktop wallet | `{"key":{"public":"<base58 32 bytes>","private":"<base58 64 bytes>"}}`: the private key is the 64-byte ed25519 secret key (seed + public key), the same as `NodePrivate.txt`, **not encrypted**. Imported (both halves checked against each other); after the import the wallet suggests deleting the plain file. Exported in the same format only on request, with a warning, for use in the official wallets |

### 4.2 Key store

- One file per account in the wallet's data folder (`~/.credits-wallet/` on Linux and macOS,
  `%APPDATA%\credits-wallet\` on Windows), readable by the user only.
- Encrypted with a password: Argon2id (time 3, memory 256 MiB, parallelism 4, **random 16-byte salt
  per file**) → XChaCha20-Poly1305 with a random nonce. The file records the format version, the KDF
  parameters, the salt, the nonce, the ciphertext and the public key (to list accounts without the
  password).
- The commands decrypt the secret key only to sign, hold it in memory as briefly as possible and
  zero it after use. The terminal interface keeps the key of the account it unlocked for the
  session (6, "Unlock"): in memory of its own outside Go's heap (`mmap`, `VirtualAlloc`), locked
  against swapping (`mlock`, `VirtualLock`), left out of core dumps (Linux `MADV_DONTDUMP`) and
  inaccessible (`PROT_NONE`, `PAGE_NOACCESS`) except to copy it out for one signature
  (`keys.LockedKey`); the key is zeroed and the memory freed on lock, on a switch of account, on
  the inactivity timer and at exit, and a garbage collection follows the lock and the unlock. When
  the system refuses the page or the lock (an `mlock` limit), the key is kept anyway and the
  interface says once "the key could not be locked in memory: it may reach swap". **What is not
  wiped:** each signature works on a copy of the key on Go's heap, zeroed after it, but
  `crypto/ed25519` derives from it an expanded key on the heap (its cache) that the standard
  library never zeroes; the password typed in the interface passes through its text input as Go
  strings, which cannot be zeroed; `golang.org/x/crypto/argon2` does not zero its working memory
  (256 MiB, 1 GiB for a node's key). All of them stay in the process's memory until it is reused
  (core dumps are off: 4.2 above).
- Key files are written whole to a temporary file (mode 0600), synced, then linked to their name:
  never half-written, never replacing an existing account. Files whose KDF parameters are outside
  time 1-16, memory 64 MiB-1 GiB, parallelism 1-32 are refused (more costs too much to open; less
  was not written by the wallet). Unreadable key files are reported, not skipped.
- New keys come from `crypto/rand` (the operating system's CSPRNG); a new account shows its 24-word
  phrase once on the terminal and asks the user to type back three of its words. The phrase never
  goes to the output (nor `--json`) by default; without a terminal the command needs
  `--words-file FILE` (created, mode 0600) or `--show-words`.
- The recovery file: on a terminal, after the check, without `--json`, `--words-file` or
  `--show-words` (CLI), and in the TUI's account creation, the wallet asks "Save the 24 words to a
  file as well? [y/N]"; the default is no. On yes it proposes `<home>/credits-recovery-<name>.txt`
  (home: `%USERPROFILE%` on Windows; elsewhere `$HOME` if the effective user owns it, otherwise the
  effective user's home from the password database, so `sudo` without `-H` never puts it in another
  user's home; `config.DataDir` uses the same rule, and the wallet warns when `$HOME` is not the
  effective user's or when it runs as root) and accepts another path (`~/` expanded). Refused, with the question repeated (three
  tries, then no file): an existing file or a symbolic link at the path, a folder that does not
  exist or cannot be listed, a path in the data folder or the accounts folder (by name, then by
  identity: `os.SameFile` against every ancestor of the folder as typed and as resolved, which
  covers links, Windows junctions, bind mounts and case-insensitive file systems), a folder that
  holds, or is below a folder that holds, key files: `NodePrivate*`/`NodePublic*`, and by content
  (files up to 16 KiB, the first 4096 of a folder, links followed) wallet key files whatever their
  name, the official wallets' keys JSON, a base58 64-byte secret key. Rationale: the key files are encrypted under the
  password; the words beside them would bypass it. The file holds the account name, address, date
  (UTC), the 24 numbered words and a one-line warning. It is written by the same writer as
  `--words-file` and `account export --out`. On Linux an unnamed file (`O_TMPFILE`, mode 0600) in
  the target folder, written and synced, then named with `linkat` through `/proc/self/fd` (fails
  if anything exists there): nothing is left by a crash. Elsewhere, or without `O_TMPFILE`,
  `PATH.partial` created exclusively and kept open, written and synced, then hard-linked to the
  name (a leftover `.partial`, from a crash, makes the next write refuse and is documented as to
  delete); without hard links, `O_CREAT|O_EXCL`. After naming, the file at the path must be the
  open one (`os.SameFile` with its descriptor), or it is removed and the write fails (a swap by
  another user in a shared folder); on Windows the mode is not applied and a path
  outside `%USERPROFILE%` gets a warning. The file is written before the key file and removed if
  the account cannot be saved. The words never go to the logs, an error message or the JSON; the
  buffer holding the file's content is zeroed after writing and the slices of words are cleared
  (`clear`; Go strings cannot be wiped). At start-up the wallet makes itself non-dumpable
  (`prctl(PR_SET_DUMPABLE, 0)`, Linux) and sets `RLIMIT_CORE` to 0 (Linux, macOS), best effort. Afterwards the wallet prints the full
  path and "This file holds your account in clear: copy it to paper or an offline USB key, then
  delete it from this computer." (the TUI shows the same).

### 4.3 The node's key files

The node reads its key at start-up from `NodePrivate.txt` and `NodePublic.txt` in its working
folder (`--private-key-file`, `--public-key-file` name others; client/config/config.cpp,
`Config::readKeys`):

| File | Content (exactly) | How the node reads it |
|---|---|---|
| `NodePrivate.txt` | base58 (Bitcoin alphabet) of the 64-byte ed25519 secret key, seed then public key, **no newline** | the first line (`std::getline`), `DecodeBase58` (spaces around it skipped): 64 bytes is a plain key; more is a key the node encrypted itself (`--encryptkey`, or "[1] Encrypt with password" at its first start), opened with the node's password (below); fewer: "Bad private key file", the node stops |
| `NodePublic.txt` | base58 of the 32-byte public key, **no newline** | its first line must equal, as text, the base58 public key of `NodePrivate.txt`; otherwise the node stops and asks on its console whether to rewrite it (`f`) |

**A key the node encrypted** (cscrypto `PrivateKey::getEncrypted` / `readFromEncrypted`,
`cscrypto/src/privatekey.cpp`, libsodium 1.0.17 or later): `NodePrivate.txt` is base58 of 88 bytes,
an 8-byte random nonce followed by `crypto_aead_chacha20poly1305_encrypt` (the original
ChaCha20-Poly1305 with a 64-bit nonce, not the IETF one; no associated data) of the 64-byte secret
key: 64 bytes of ciphertext and a 16-byte tag. The key is `crypto_pwhash` with `ALG_DEFAULT`
(Argon2id v1.3), `OPSLIMIT_SENSITIVE` (4 passes), `MEMLIMIT_SENSITIVE` (1 GiB), parallelism 1, a
32-byte output, and the **fixed** 16-byte salt `IDI NAHUY BLYAT'` that every node shares (so the
derived key depends on the password only). The password is passed as its bytes up to the first NUL;
the node reads only printable ASCII (32-126) from its console, 3 to 128 characters when it
encrypts. The node can also use a file's content as the password ("f"); the wallet asks only for a
typed password. `NodePublic.txt` stays plain. The wallet decrypts it in Go (`keys.DecryptNodeKey`:
`golang.org/x/crypto/argon2`, `chacha20`, `poly1305`), never writes the decrypted key anywhere and
never encrypts a node's key itself. Argon2id takes 1 GiB, and an allocation that fails ends the
process (the terminal left raw): before deriving, the wallet checks the free memory (Linux
`MemAvailable` of `/proc/meminfo`, else `sysinfo`; Windows `GlobalMemoryStatusEx`; macOS only the
physical memory, `hw.memsize`, as it reports no available figure and swaps rather than kills) and
refuses with "the node's key needs 1 GiB of free memory to open (as the node does)" below 1.2 GiB.
`x/crypto/argon2` does not zero that memory; the wallet returns it to the system after the
unlock. Proven by `internal/keys/testdata/node-encrypted-vectors.json`,
files encrypted and opened by the node's own code (`internal/keys/testdata/node-key-crypt.cpp`,
built against the node), which the wallet opens and re-encrypts byte for byte with the same nonce;
with `NODE_KEY_CRYPT` set, a round trip in both directions (the node opens what the wallet
encrypts, the wallet opens what the node encrypts with a new nonce).

Without `NodePrivate.txt` the node asks on its console to generate a key (`g`), shows its 24 words
(the same derivation as the wallet's, 4.1) and offers to encrypt the file. `--seed` makes the node
ask for the 24 words instead of reading the files. The node writes both files itself with
`std::ofstream`, without a newline; the wallet writes the same bytes. Proven by
`internal/keys/testdata/node-key-check.cpp` (the steps of `Config::readKeys` with the node's own
cscrypto, base58 and libsodium, then a signature checked with the public key) and by the private
network test, whose last node starts with a key the wallet made and names it as its own in its
first round table.

### 4.4 The node's key as an account (the wallet of a node)

Operators keep their stake on the node's key, and the node's rewards and the delegations it
receives go to that address. Next to its node the wallet therefore uses the node's own key pair,
**in place**, as an account:

- **The wallet of a node**, always by an explicit choice: `--node-key FILE` (a key file or a
  node's folder, relative to the current folder), else the node profile's `node_key` (relative to
  the configuration file's folder: the set-up writes `node_key = "NodePrivate.txt"` in
  `csnode/wallet.toml` when told to use the node's key, `"off"` otherwise, and keeps a value set by
  hand), else `$CREDITS_WALLET_NODE_KEY` (an absolute path; the node's Docker image
  sets it to `/data/keys/NodePrivate.txt`). `node_key = "off"` turns it off. It is **never guessed
  from the current folder**: a folder someone else controls (an archive, a shared folder, `/tmp`)
  could hold a planted key pair, which would become the default account, so that the address the
  user copies and the key that signs are the planter's. The public key file is `NodePublic.txt`
  next to the private one.
- **Checks before use** (Linux, macOS; Windows ACLs are not checked): the key file and its folder
  owned by the user the wallet runs as (or root) and writable by nobody else (no group or world
  write: `chmod 600`, `chmod go-w`); the key file a regular file, or a link whose target and the
  target's folder pass the same checks. Otherwise the key is refused, not used, with the reason.
  The key file is opened first, then checked (`fstat`) and read through the same descriptor. A key
  file other users can read (the node writes it 0644) is used with a warning naming `chmod 600`.
  `account node-key` says on Windows that the checks are not done, and lists the
  `NodePrivate.txt.bak-*` / `NodePublic.txt.bak-*` backups (old keys in clear: to move offline and
  delete). Signing refuses a key whose public half is not the plan's sender.
  The first time the account is read in a session the wallet says it with its address, its file
  and why it is used ("node key account node: ADDRESS, read from FILE (node_key in wallet.toml)",
  on stderr; the TUI shows the file under the address), so a substituted key is visible.
- **The account.** Named `node` (`node-key` when the key store already has an account `node`, an
  earlier import of the node's key for instance; the store's account keeps its name), listed
  first, the default account (selected at start in the TUI; `account address` without a name), and
  marked everywhere: `NODE KEY` in `account list` (and `"node_key"`, `"default"` in its JSON), in
  the TUI's account list and details, in every review. `NodePrivate.txt` is read where the node
  reads it: never copied into the key store, never encrypted by the wallet. A plain key asks no
  password (the user's choice; the review asks to type `send` on mainnet, as for any account): it
  is read to find the address and again to sign, then zeroed; a key that changed in between is
  refused. `account export node --format encrypted` is refused (`account import NAME
  NodePrivate.txt` makes an encrypted copy, which adds no protection while the original stays in
  clear); `--format base58` and `official` need no password.
- **A key the node encrypted** (4.3; `--encryptkey`): the account's address is the
  `NodePublic.txt` next to it, read with the same checks as the key (owner, not writable by others,
  a regular file; without a valid one the key is reported, not used: the node would not start
  either), marked `encrypted by the node` (`"node_key_encrypted"` in JSON) and tagged "(from
  NodePublic.txt, unverified until unlocked)" in `account list`, `account address` (on stderr: the
  output stays the address alone; `"address_unverified"` in JSON) and the found-key prompt. The node's
  password opens it: asked on the TUI's unlock screen like any account's, and by the commands when
  they sign (the terminal or `--password-file`). Once opened, its public key must be the
  `NodePublic.txt`'s, or it is refused. The TUI's session keeps the opened key in locked memory and
  the file's SHA-256: a file changed since (the node re-encrypted it, say) is refused until the
  account is unlocked again. Opening costs the node's Argon2id (1 GiB of memory, some seconds). It
  is never exported in clear (`--format base58`, `official` and `node` refused) nor written
  anywhere decrypted. A wrong password says only that the account could not be unlocked.
- **Found in the current folder.** The key is never used because the wallet was started where a
  `NodePrivate.txt` is (see above). When the terminal interface starts in such a folder and no
  node's key is configured (neither `--node-key`, `node_key` nor `$CREDITS_WALLET_NODE_KEY`; and
  not `off`), it says so on its first screen, with the key's address and full path, after the same
  checks (refused when the file or its folder can be written by others, a warning when others can
  read it), and offers: **use it for this session**; **use it and remember it** (`node_key` with the
  absolute path written into the active profile of the configuration file in use, or of
  `config.toml` in the data folder, created if missing (mode 600); the file, a link's target and
  their folders must be the user's (or root's) and writable by nobody else, as the key, or nothing
  is written: an existing `node_key` line of that
  profile's `[nodes.NAME]` table is replaced, otherwise the line goes under the table's header;
  comments and every other line are kept, the new text must parse to the same configuration but
  for that value, written to a temporary file in the same folder, synced, renamed over it with the
  file's mode; a profile written as an inline table, or `--node` without a profile, is refused with
  the line to add by hand); **ignore it**; or **ignore it and do not ask again** for that path
  (`node-keys-ignored.txt` in the data folder, one absolute path per line). A refused key can only
  be ignored. The chosen key then goes to the unlock screen (its password for a key the node
  encrypted). The commands only print, on stderr, `This folder holds a node key: to use it,
  --node-key NodePrivate.txt (not used without it)`, and never use it.
- **No key yet.** `account node-key new` (TUI: `g` on the main screen of a node's wallet whose node
  has no key) makes the key from a new 24-word phrase with the derivation of 4.1, shows and checks
  the words as `account new` does (three typed back; the recovery-file question; `--words-file` or
  `--show-words` without a terminal) and writes `NodePrivate.txt` and `NodePublic.txt` (4.3) in the
  node's folder (`--out FOLDER` for another one) for the node's first start: the node's identity
  gets a paper backup. Refused, before any word is shown: a key file (or a link) of either name
  there, a `.partial` left by an interrupted write, the node running, a folder that is not the
  wallet user's or that every user can write to (Linux, macOS; a link to the folder is followed).
  Written with the recovery file's writer (`WriteSecretFile`: mode 0600, created new, never
  overwriting, never through a link, whole or not at all), `NodePrivate.txt` first, then both read
  back as the node reads them; on any failure nothing is left (the words file and the recovery file
  are removed too). The recovery file and `--words-file` are refused in the node's folder (by name
  and identity, subfolders too), as next to any key file.
- **A half-written key** (`NodePrivate.txt` without a matching `NodePublic.txt`: the node would stop
  at its start and ask on its console, and a node without one hangs): `node-key new` says so and
  points to `account node-key repair`, which writes `NodePublic.txt` from the readable
  `NodePrivate.txt`, the node stopped (a mismatching file kept as `NodePublic.txt.bak-DATE`).
- **Is the node running?** The community node holds an exclusive lock on `db/.lock` (`flock`;
  `LockFileEx` on Windows) for its whole run, from before it reads its key: the wallet tries the
  same lock (read-only, released at once). It also connects to `127.0.0.1` at the `[api] port` of
  the folder's `config.ini`, and on Linux looks for a process named `node` whose working folder is
  the node's (the official node takes no lock). With the keys in a subfolder without `config.ini`
  (Docker: `/data/keys`), the folder above is checked too. The node writes no PID file.
- **Export to the node's format.** `account export NAME --format node --out FOLDER` writes an
  account's key as `NodePrivate.txt` and `NodePublic.txt`, with the same checks and writer; a key
  already there is never overwritten. `--replace` (with `--format node` only) replaces the node's
  identity: the node must be stopped, the old files are first copied to
  `NAME.bak-YYYYMMDD-HHMMSS` (mode 0600, new files), then removed, then the new ones written; if
  that fails the old ones are put back. Both ask to type `export` (`replace` when a key is
  replaced) on a terminal, or `--yes`. The `--replace` warning says to stop the node first: the
  detection of a running node is best effort. The wallet says that the node's identity follows its key:
  its address, the delegations it received, its history as a trusted node and its rewards; a node
  started with another key is another node.
- **Warnings**, short, once per use (the TUI's start, `account list`, `account node-key`, the
  generation, the export): the node's key is kept in clear on the server because the node needs
  it, so CS on it are as exposed as the server; operators usually keep their stake there already:
  keep on it only what the node needs, and savings in a separate account.

### 4.5 The backup file

`backup export FILE.dat` (and `b` then `e` in the TUI) writes every account of the key store into
one file; `backup import FILE.dat` (`b` then `i`) restores them. The backup never decrypts an
account: it carries the key files as stored, each still encrypted under its own password (4.2),
and seals all of them under a **backup password** chosen at export.

**Content** (the plaintext, JSON): `{"format": "credits-wallet backup", "version": 1, "created":
UTC time, "accounts": [{"name", "address", "key_file", "tokens", "contracts"}]}`. `key_file` is the
account's key file byte for byte (base64 in the JSON), `tokens` and `contracts` its local lists
(`tokens/<address>.json`, `contracts/<address>.json`, public data), absent when it has none; the
name is the key file's name in `accounts/`. **Not included:** the node's key (`NodePrivate.txt`
belongs to the node's folder and is never copied: 4.4), an imported copy of it apart (it is an
encrypted account of the store), `config.toml` and any other setting of this machine. Key files the
wallet cannot read are reported and left out; a list that is not a regular file is left out with a
warning.

**Container** (binary, all of it written by `keys.SealBackup`):

| Offset | Size | Field |
|---|---|---|
| 0 | 8 | magic `CWBACKUP` |
| 8 | 1 | format version, 1 |
| 9 | 1 | key derivation, 1 = Argon2id |
| 10 | 4 | Argon2id time (passes), big-endian |
| 14 | 4 | Argon2id memory in KiB, big-endian |
| 18 | 1 | Argon2id threads |
| 19 | 16 | salt, random per file |
| 35 | 24 | nonce, random (XChaCha20-Poly1305) |
| 59 | rest | ciphertext and its 16-byte tag |

The key is Argon2id of the backup password with the key files' parameters (time 3, 256 MiB, 4
threads) and the file's salt; the cipher XChaCha20-Poly1305; the whole 59-byte header is the
associated data, so no byte of it (version, parameters, salt, nonce) can change without the tag
check failing. On reading, parameters outside the key files' bounds (4.2) are refused before any
derivation, an unknown version is refused with its number ("backup format version N is not
supported: this wallet reads version 1"), a file without the magic is "not a credits-wallet backup
file", and every other failure (a wrong password, a damaged or truncated file, a damaged header)
says the same "could not open the backup: wrong backup password, or a damaged file". Files over
64 MiB are not read; inside, at most 10,000 accounts, key files up to 16 KiB and lists up to 4 MiB,
each name valid, each key file a wallet key file of its account's address, each list that
account's, no name or address twice; otherwise the content is refused. The derived key and the
plaintext buffers (the JSON, the decoded key files and lists) are zeroed after use (4.2: the
limits of what Go can wipe apply).

**Export.** The path gets the recovery file's checks (4.2): a new file, in an existing folder,
outside the data folder, the accounts folder and the node's folder (by identity), not in or below
a folder holding key files; the CLI names it, the TUI proposes `~/credits-wallet-backup-DATE.dat`.
The backup password is typed twice and must not be empty; under 12 characters the wallet adds a
hint ("a short password…"; it never refuses). With `--password-file` the first line of the file is
the backup password. The file is written by `WriteSecretFile` (4.2: mode 0600, never over a file,
never through a link, whole or not at all). The wallet prints the path and the number of accounts
and says: "This file contains your accounts, each still protected by its own password and the
whole file by the backup password; keep it off this computer, e.g. on a USB key.", and what is not
in it (the node's key, `config.toml`).

**Import.** The backup password is asked once; the wallet lists the backup's accounts (name and
address) with what it will do, then asks "Restore N accounts? [y/N]" (CLI: `--yes` skips it, and
without a terminal it is required). An account whose address is already in the wallet (the node's
key included) is skipped and reported; its token and contract list entries the wallet lacks are
merged. A name taken by another address needs a new name: `--rename-suffix S` (the name plus S),
or on a terminal a name typed for each (Enter skips the account); the TUI asks it in a field;
without either, the account is skipped. Each account is written by the key store's writer (4.2:
whole, never over an account), then its lists merged (an entry already there is kept). Restored
accounts keep their own passwords, those they had when the backup was made: the backup password
opens the file, not the accounts.

**`--json`** prints metadata only, never the content: export `{"file", "count", "accounts": [{"name",
"address"}], "not_included", "problems"}`; import `{"file", "created", "accounts": [{"name",
"address", "result": "restored|skipped|failed", "restored_as", "reason", "warning"}], "restored",
"skipped", "failed"}`.

## 5. Functions

### 5.1 Node and network

- Connect to the node (address and port from the configuration, default `127.0.0.1:9090`); show the
  last block, the round, the number of neighbours if available, and whether the node is synced
  (`SyncStateGet`, `PoolListGet`).
- Refuse to send while the node is not at the head (a transaction sent to a lagging node is rejected
  or delayed), with a clear message.
- Show which network the node is on, and treat it accordingly:
  - **mainnet by default**: without other information the wallet assumes mainnet, shows "MAINNET" and
    asks for the full confirmation before sending; the only possible mistake is too much caution;
  - a **test or private network** is declared in the configuration (`network = testnet` or
    `network = private`, per node profile) and shown in another colour with a lighter confirmation;
    a `--node` address that no profile declares counts as mainnet;
  - the confirmation on mainnet is typing `send` (TUI and commands) and, for the commands, the
    password; on a test network Enter (TUI) or `y` and the password (commands). The TUI asked the
    password once, when the account was unlocked (6, "Unlock"); a plain node's key (4.4) asks none;
  - the **community node** (from CreditsCommunity/node#162, release community.17) tells its network in
    its diagnostic API: `API_DIAG.GetNodeInfo` returns `NodeInfo.networkUuid` (field 9, optional i64:
    the first 8 bytes of the hash of block 1 as a little-endian uint64, the same 64 bits in the
    signed i64; known to pruned and snapshot-started nodes) and `NodeInfo.communityVersion` (field
    10, the package version). The IDL comes from the fork `CreditsCommunity/thrift-interface-definitions`,
    branch `community/network-id` (commit `0b6e6d0d`, the pinned `35620f5d` plus these two optional
    fields; the main API is unchanged). The wallet asks the node's diag port (`[api] diag_port`, 9088
    in the release packages, off when 0): the profile's `diag = "HOST:PORT"`, by default the node
    address's host with port 9088, `diag = "off"` not to ask; 3 s at most. Mainnet is uuid
    `11024959585341937636` (`0x99008654835817e4`, -7421784488367613980 as received; the node's own
    constant `uuidMainNet`). Before every send: a profile that says mainnet (declared, or assumed)
    needs the mainnet uuid, one that says testnet or private needs another, and a profile may pin
    `network_uuid = "N"`, which must match; otherwise the wallet refuses. When the node reports no
    uuid (an official node or an older community release, the diag port closed or off, a node
    without block 1) the configuration decides, except that a profile which pins `network_uuid`, or
    sets `require_network_uuid = true`, then refuses to send. Every review (TUI and commands) says
    whether the node confirmed the network ("confirmed by the node (uuid …)") or not ("not
    confirmed by the node (why)"); `status` and the TUI's status screen show what the node says and
    any mismatch.

#### Trust in the node

The wallet talks to one node and believes what it answers: balances, the fee, the sender's last
inner id, the sync state, the history. It never sends the node a secret, and a signed transaction
cannot be altered, so a dishonest or broken node cannot spend the user's CS on its own. What it can
do: show false balances or history; give a wrong inner id (the transfer is then rejected, or must
wait); claim a transaction is in a block when it is not, or drop it; and make a transfer cost up to
the **signed max fee** (shown in the confirmation, next to the expected fee). For contracts it also
compiles the source: a deploy carries the byte code the node returns, and the contract's address
is derived from it, so a dishonest node could have the user deploy other code than the source (the
same holds for the method list, getters' results and the state it reports).

Every string, binary and list an answer announces (API and diag port) is limited to 8 MiB
(Thrift's `MaxMessageSize`/`MaxFrameSize`; the node's largest legitimate value, a contract's
source or byte code, is below 100 KiB), so a broken or hostile node cannot make the wallet allocate
more.

**A signed transaction names no network.** Its bytes are the same on every network, and an
account (its key) exists on every network at once. A transaction signed for a test network is
therefore valid on mainnet too, as long as its inner id is the account's next one there: anyone
who runs the test node, or sees its traffic, can send it to mainnet. Use **separate accounts** for
test networks, never an account that holds CS on mainnet. The network identity (5.1) protects
against sending to the wrong node by mistake, not against such a replay. An unsynced node is
refused, a missing status in an answer is an error, an inner id is guessed only for a wallet the
node reports "not found", and a transfer is reported in a block only when the sender's last inner
id equals its own, or a later one is recorded and the transfer is among the account's latest
transactions. Running the wallet next to the user's own node is what the design assumes.

### 5.2 Accounts and balances

- List accounts, select one; create, import, export (encrypted file or base58 key after the password).
  In a node's wallet the node's key is the first account and the default (4.4).
- Balance in CS (`WalletBalanceGet`), delegated amounts and the delegations made and received
  (`WalletDataGet`, `DelegatedItem`), token balances (`TokenBalancesGet`; needs `[api] tokens_cache=true`
  on the node, otherwise the screen says so: the node answers "This node doesn't provide such info").
  The node lists only tokens with a balance above 0. Without the token cache one token's balance is
  still read from its contract (`balanceOf` as a getter: `token balance TOKEN ACCOUNT`).
- **Every token held** (`tokens ACCOUNT`, the TUI's `[t]` screen and the main screen's token line),
  each with its full contract address, its code and name (self-declared), decimals (`getDecimal()`,
  read once per run), balance and the look-alike warnings (5.3) against the other tokens listed:
  1. the local node's token cache (`TokenBalancesGet`), plus the tokens of the local token list it
     does not name, listed when the account holds some (or always, when the user added them): a
     cache turned on after the node pruned, or on a node started from a snapshot without one,
     finds every token and reads correct balances but learns holders only from the calls in the
     blocks it stores (CreditsCommunity/node docs/tokens.md);
  2. without it, the token cache of the profile's `history_node`, for the token addresses only (the
     balances are read from the local node);
  3. otherwise the account's history: the latest 1000 transactions (`TransactionsGet`) on the history
     source and, when that is a remote node, on the local node; the targets of the account's calls,
     deploys, contract payments and token transfers, and the tokens of its token transfers; plus
     the account's **local token list**. Each candidate is checked as a token (5.3: `transfer` and
     `getDecimal`), its balance read with `balanceOf`; tokens at 0 are counted, not listed. The
     list is labelled "found from history (may be incomplete on a pruned node)" and kept a minute.
     The node indexes a transaction by its source and target only, so a token **received** through
     another wallet's call is not in the account's history: such tokens appear only in a token
     cache or once added. `TokenTransfersListGet` (every transfer of the network) is served from the
     same cache as `TokenBalancesGet`, so it adds nothing where the cache is off.
  The **local token list** is `tokens/<address>.json` in the data folder (mode 600, written whole then
  renamed, only a regular file is read; public data only): address, network, how (`added` by
  `token add` or the TUI's `a`, always listed with its balance, 0 included; `sent` by the wallet)
  and since when: only tokens the user acted on, never what a token cache lists. `token remove` (the
  TUI's `x`) takes a token off. A token found only in a transfer the account received (in the
  history, a token transfer whose receiver is the account) is marked "received from ADDRESS: not
  added by you": anyone can send a token, look-alikes included. In the TUI the selected token's
  address stands alone on a line (Shift+drag selects it) and `y` sends it to the clipboard with OSC
  52 (through tmux or screen when the wallet runs in one).
- Amounts are exact decimals (CS has 18 fractional digits); never floating point.

### 5.3 Transfers

- Send CS: recipient (base58, checked), amount, optional user data; fee from `ActualFeeGet`; inner id
  from `WalletTransactionsCountGet`; signed and sent with `TransactionFlow`; the result followed until
  it is in a block. The node's `TransactionsStateGet` is not implemented (it answers "not
  implemented"), so the wallet relies on `TransactionFlow`, which answers once the transaction is in a
  block, and when it answers "in progress" polls `WalletTransactionsCountGet` until the sender's last
  inner id reaches the transfer's.
- The signed max fee is "1 CS" (encoded 0x4c66, 0.99609375 CS) as in the node's samples and
  `ci/testnet/fund.py`; the node charges the actual fee, which is what the confirmation shows.
- **User data** (`--data TEXT`, the TUI's Data field): a text, UTF-8, 1 to 4096 bytes, no control
  characters but newline and tab. It is the user field `ordinary::Text` (id 1); the transaction signs
  it as `csdb::Transaction::to_byte_stream_for_sig()` does a string field: count 1, then u32 size +
  bytes; the API carries the text as it is in `Transaction.userFields` (the node reads a `userFields`
  whose first byte is not 0 as the text, `APIHandler::checkTransaction`). Below 5 KiB a transfer pays
  the smallest fee. The text is public: anyone reads it on the chain. Vectors: `testdata/sign-vectors.py`,
  built as `ci/testnet/fund.py` builds a string field, independently of the Go code; checked on a
  private network of the community node (CI).
- **Send tokens**: a signed call (`TT_ContractCall`, amount 0) of the token contract's
  `transfer(String to, BigDecimal amount)` (or `transfer(String, String)`, older tokens), built as the
  node's sample clients build a call (`samples/clients/go`, `call`): the `SmartContractInvocation`
  with every non-optional field set, its Thrift binary serialization as the one user field (u32 size
  + bytes in the signed bytes). Before signing the wallet reads the token's methods
  (`SmartContractGet`, `ContractAllMethodsGet`), its decimals (`getDecimal()` as a getter), the
  symbol and name (`TokenInfoGet`, or the getters `getSymbol`/`getName`) and the sender's token
  balance (`balanceOf`); the amount must have at most the token's decimals and be within the balance.
  The signed max fee is the same 1 CS; the node holds it back from the CS balance while a contract
  call runs, so the balance must cover it. The fee shown is twice `ActualFeeGet` of the call's size
  (the call and the new state it makes, 0.0175 CS on the private network) plus the execution, which
  the node charges later. A token is identified only by its contract address: its code and name are what the contract
  says of itself. The review shows the full address, labels code and name as self-declared, and
  warns when the code or name looks like "CS" or "Credits", or another held token's code looks the
  same: compared by a skeleton (confusable Cyrillic and Greek letters mapped to Latin ones, NFKD
  compatibility folding such as fullwidth letters, combining marks and zero-width or bidi format
  characters dropped, upper case); and whenever a code or name has any character outside printable
  ASCII. A `transfer` that returns `false` is reported as a failure (no tokens moved,
  the fee paid).

### 5.4 Delegations

- Delegate CS to a node: without time limit (user field `delegated` = 1) or until a date (value = the
  UTC expiry time, unix seconds, at least 4), and withdraw a delegation without time limit (value =
  2); the same encoding as the node and `ci/testnet/fund.py`: the transaction signs count 1 + the
  value as u64, and the API carries flag 0, count 1, id 5 (u32), type 1 (integer), the u64.
- The node's rules, checked before signing: at least 5 CS (`Consensus::MinStakeDelegated`) for a
  delegation and a withdrawal; not to the sending account; the expiry at least a minute ahead
  (`--until 2027-01-31`, `"2027-01-31 18:00"` in UTC, or RFC 3339 with an offset); a withdrawal only
  from the delegation without a time limit to that node and at most its amount (a delegation with a
  time limit ends by itself: the nodes release it once a block is past the expiry), with a CS
  balance that covers the signed max fee (the node refuses a withdrawal below it). A delegation takes
  the amount from the balance; a withdrawal gives it back.
- List the delegations made and received with their amount and expiry (`WalletDataGet`,
  `DelegatedItem.validUntil`; 0 = no limit): `credits-wallet delegations`, the TUI's `[d]` screen.

### 5.5 Smart contracts

- **Templates**: the node's `samples/contracts` (Counter, TestToken, Token, TipJar, Faucet), embedded
  as they are at `CreditsCommunity/node` commit `079e788d` (AGPL-3.0, attribution in
  `internal/templates`; the privnet job compares them with the node repository at that commit).
  `contract templates` lists them, `contract new NAME FILE` writes one (never over a file without
  `--force`); the TUI's `n` does the same.
- **Compile** through the node (`SmartContractCompile`, done by its executor). A source is UTF-8, at
  most 48 KiB. Errors are listed with their line (`FILE:LINE: message`, the TUI marks the lines):
  the executor reports `LINE:message` per error in its `CompilationException`, without a column (a
  column is shown when a message carries one). **The executors released so far lose that list**:
  `ContractExecutorHandler.compileSourceCode` builds its failure answer without the byte-code list
  and then reads that list's size for its log, so the node answers "Internal error processing
  compileSourceCode" (seen on a private network with the community.16 executor); the wallet then
  says the source does not compile and that the executor lost the errors (an executor fix restores
  the lines). A source naming a library the node forbids gets an empty answer, reported as such. The compiled byte code's methods come from `ContractAllMethodsGet`
  (classes and methods counted). Messages, class and method names are the executor's or the
  author's text: escaped.
- **Deploy**: compiled again at the deploy, signed as the node's sample clients do (`samples/clients`
  `deploy`): `TT_ContractDeploy`, amount 0, one user field, the invocation (no method, the source,
  the byte code, the token standard the node found); its target is the contract's address
  `blake2s-256(deployer key ‖ inner id, 6 bytes little endian ‖ every class's byte code in order)`
  (`SmartContracts::get_valid_smart_address`, checked by every node), tested against vectors made
  with Python's `hashlib` the way `credits_client.py` and `ci/testnet/contracts.py` derive it.
  **Fee**: the node prices a deploy from its own column of the fee table (`csnode/src/fee.cpp`:
  0.0874 CS below 5 KiB, 0.1748 below 20 KiB, 0.4375 below 50 KiB, 17.87 below 100 KiB) plus the
  cheapest new state (0.00874), plus the constructor's execution; `ActualFeeGet` knows only the
  column of the other transactions, so the wallet computes the deploy fee from the table and shows
  it as an estimate. The **signed max fee is the same 1 CS** as for transfers and calls (the samples
  sign 10 CS; on the CI's private network a call costs about 0.0175 CS and a deploy of TestToken
  well below 0.2 CS): it covers every deploy the wallet sends, which must stay below 50 KiB (larger
  ones cost 17.87 CS and are refused). The balance must cover the max fee, which the node holds while
  the constructor runs. After the deploy is in a block the wallet waits until the node lists the
  contract (`SmartContractGet`), shows its address, and adds it to the account's **local contract
  list**: `contracts/<deployer address>.json` in the data folder (mode 600, written whole then
  renamed; only a regular file is read or replaced, a link or device in its place is refused; two
  wallets writing at once: the last one wins), public data only: address, class name, source file
  path, network and node, time, block, inner id, token standard. Sources are read the same way by
  the commands and the TUI: a regular file, at most 48 KiB, UTF-8.
- **Methods** (`SmartContractGet` + `ContractAllMethodsGet`, as the samples; `ContractMethodsGet`
  is not needed): name, argument types and names, return type. **Getters** cannot be told apart
  through the node's API today: the contract API's `@Getter` (`com.credits.scapi.annotations.Getter`,
  kept at run time; the executor lists it by that name in its method annotations) only documents
  the author's intent, and the node's `ContractAllMethodsGet` drops the annotations anyway (it copies
  names and types only, `api/src/apihandler.cpp`). So the user chooses: `contract get` (and the
  TUI's `g`, `ctrl+g`) runs any method as a getter, free; `contract call` (`c`, `ctrl+t`, Enter)
  signs a call of any method, through the review and its confirmation. A method is marked "getter"
  (and Enter reads it) only when the node passes the annotation on (a node that keeps them; the
  fake node does).
- **Get**: `TransactionFlow` with `forgetNewState`, a zero signature, nothing paid or kept; the
  caller the contract sees is `--from ACCOUNT` (or the selected account in the TUI), a zero address
  otherwise. **Call**: a signed `TT_ContractCall` (as token transfers, 5.3), arguments converted to
  the declared types (String, BigDecimal, int, long, boolean, double and their wrappers, byte[] as
  hex; overloads by number of arguments; arguments starting with `-` after `--`); the fee shown is
  twice `ActualFeeGet` of the call's size plus the execution, the max fee 1 CS. A method that throws
  is answered with an error by the node (the fee is paid). The result (the method's return value)
  and then the contract's state (`SmartContractDataGet`: its **public** fields, all the executor
  exposes, `getClass().getFields()`; the samples keep theirs private and offer getters) are shown,
  escaped; collections are shown to a bounded size and depth.
- **Payments** (`contract pay ACCOUNT CONTRACT AMOUNT [--data TEXT]`, the TUI's `payable` entry):
  the executor never runs `payable()` as a call ("payable method cannot be called"); the node runs it
  when **CS are transferred to the contract** (`samples/contracts` README, TipJar): a plain
  transfer to the contract's address, its user data (a text, 5.3) arriving as `userData`. Throwing
  refuses the payment and the node gives the CS back (the fee is paid). The wallet checks that a
  contract with a `payable` method is deployed there; the fee is twice the transfer's (the transfer
  and the new state) plus the execution; the balance must cover the amount and the max fee.
- **Contract list** (`contract list ACCOUNT`, the TUI's `[c]` screen): `SmartContractsListGet` by
  deployer (pages of 100, at most 1000) merged with the local list. The node records a payment to a
  contract (a transfer that runs `payable()`: no invocation, an empty method) as a deploy of the
  payer (`APIHandler`: `is_smart_deploy` is `method.empty()`) and then fails every page holding it
  ("No more data to read"); the wallet reads such a page again one entry at a time and reports the
  entries it had to skip. The list is one row per address, newest
  first; a contract only in the local list is shown with the network it was deployed on ("local
  only: not on this node"); a node error still shows the local rows.
- **TUI** (`[c]`): the list; `o` open a `.java`, `n` new from a template, `a` a contract by address;
  the source screen compiles on opening, marks error lines, `e` edits in `$VISUAL`/`$EDITOR` (a
  command and its arguments, no shell; Notepad on Windows, otherwise nano or vi) and compiles again
  on return (a file name starting with `-` or `+` is passed as `./NAME`; an editor that is not found
  is reported), `d` deploys through the same review and confirmation as a transfer (on mainnet: type
  `send` and the password); a review lists every argument of a call in full, wrapped, and one taller
  than the screen scrolls (PgUp/PgDn) and is signed only once it was shown to its end; the contract screen lists the methods (getter and pay marks), a form
  per method typed by argument, the result and the state.
- Contract-to-contract calls are out of scope (disabled on mainnet).

### 5.6 History

A pruned node keeps only recent blocks, so the history of an account may not be on the local node.

- **History source**, configurable:
  1. the local node (default): what it has, with the oldest block it holds stated on screen ("history
     from block N"; the API has no call for it: the wallet finds it with a binary search over
     `PoolInfoGet`, about 28 calls on mainnet);
  2. a **remote full node**, read-only, for the history only (`TransactionsGet`,
     `TokenBalancesGet`, `TokenWalletTransfersGet`): an operator's full node or the user's own full
     node with backfill, `history_node = "HOST:PORT"` in the node profile or `--history-node
     HOST:PORT` (`--history-node local` for the profile's own node). It receives only public
     addresses; it is never used to send: the wallet holds its client only behind the read-only
     history source. Every history output names its source ("local node …" or "remote full node …
     (read-only)"), the TUI in its own colour.
- The history screen pages through transactions (date, block, counterparty, amount, fee, kind:
  transfer, delegation with its expiry, withdrawal, deploy, call, token transfer, user data) and can
  export them to CSV.
- Token transfers (`history --tokens`, `[t]` on the history screen): `TokenWalletTransfersGet` for the
  tokens the account holds (`TokenBalancesGet`), those of its outgoing token transfers, and any given
  with `--token ADDR` (a token whose whole balance was sent is no longer listed by the node). They
  need the token cache on the source node.
- The CSV export has the CS transactions and the token transfers in one table, newest first: the
  token transfer the account initiated is merged with its contract call (same block and index). Its
  columns: `time_utc, block, index, kind, direction, account, counterparty, amount_cs, fee_cs,
  inner_id, token, token_code, token_amount, data, delegation_until_utc`. Every column whose text
  others choose (user data, a token's code, a token amount that is not a plain number) gets a `'`
  in front when it starts like a spreadsheet formula (`= + - @`, tab, CR). A merged row keeps the
  call's own kind, direction and counterparty; the token transfer fills only the token columns.
  Without the token cache the file has the CS transactions and the command warns.
- Messages of the node in errors are escaped when the error is built, and every error line is
  escaped again where it is printed (stderr, the TUI).
- Text chosen by others (user data, token names and codes) is shown with control characters and
  backslashes escaped, on screen and in the commands' output; `--json` also escapes the C1 controls
  (U+0080-U+009F) and the Unicode format characters (such as U+202E) as `\uXXXX`.

## 6. Terminal interface

Keyboard first (arrows, Tab, Enter, Esc, `?` for help), mouse where the terminal supports it, light
and dark themes following the terminal.

Addresses in and out:

- **Copy.** `y` copies the selected account's own address (main screen, and the screen after an
  account is created or imported), as `y` copies a token's address: OSC 52 (wrapped for tmux or
  screen), which reaches the local clipboard over SSH; only a valid base58 address is ever copied;
  a short note says so. The address is also printed whole, alone on its line, for terminals that
  block OSC 52 (Shift+drag). CLI: `account address NAME` prints the address alone (`--json`:
  name and address), to pipe into `xclip`, `pbcopy` or `clip`.
- **Paste.** The terminal's paste reaches the interface as a bracketed paste (one key message,
  `Paste` set). In a form: the text is trimmed (spaces, newlines) and a leading `credits:` removed.
  If it is a base58 32-byte address it replaces the field's value, and a line under the field shows
  it with its first and last 6 characters highlighted (bold, underlined) to compare with the
  source (clipboard hijacking); the review highlights the recipient, node, token and contract the
  same way. In an address-only field (send To, delegate Node, token Token and To, token add,
  contract address) anything else is refused, the field unchanged; the refusal shows only the
  length of the pasted text (it may be a secret key or a phrase pasted by mistake) and goes when
  the field loses focus. In a free field (contract arguments) other text is inserted without
  control characters. A paste into a secret field (password, imported key) is passed on unchanged
  and never shown. The wallet never reads the clipboard itself: the text inputs' Ctrl+V (which
  runs external programs) is disabled.

Mock-ups (80 columns):

```
┌ credits-wallet ─────────────────────────── MAINNET ─ node 127.0.0.1:9090 ✓ synced ┐
│ Accounts            │ C31Lvn…5GQnVTeb                                              │
│ > main              │ Balance        12,345.678 CS                                 │
│   savings           │ Delegated       1,000.000 CS (2 delegations)                 │
│   test-contracts    │ Tokens         CTT 998,900.50                                │
│                     │                                                              │
│ [n] new  [i] import │ Last transactions (from block 186,699,000 on this node)      │
│                     │ 2026-11-03 14:02  #187,210,331  → u6PJ8D…q2w   -12.5   CS     │
│                     │ 2026-11-03 13:40  #187,209,120  ← 8Rxst6…cNRUS +40.0   CS     │
│                     │ 2026-11-02 09:12  #187,140,002  delegation   1,000.0  CS     │
├─────────────────────┴──────────────────────────────────────────────────────────────┤
│ [s] send  [d] delegate  [c] contracts  [h] history  [t] tokens  [?] help  [q] quit │
└────────────────────────────────────────────────────────────────────────────────────┘
```

```
┌ Send CS ─────────────────────────────────────────── MAINNET ┐
│ From      main  C31Lvn…5GQnVTeb   balance 12,345.678 CS      │
│ To        u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w   ✓    │
│ Amount    12.5                                              │
│ Fee       0.008740 CS                                       │
│ Data      (none)                                            │
│                                                             │
│ This sends real CS on MAINNET.        [Enter] sign and send │
│ Confirm   send                        [Esc]   cancel        │
└─────────────────────────────────────────────────────────────┘
```

```
┌ Contract  TestToken.java ─────────────────────────────────────────────────────┐
│ 12  public class TestToken extends SmartContract implements ExtensionTokenSt… │
│ 13      private final String owner;                                           │
│ 14      private BigDecimal totalCoins = new BigDecimal(1_000_000).setScale(2… │
│ ...                                                                            │
├────────────────────────────────────────────────────────────────────────────────┤
│ ✓ compiled: 1 class, 14 methods        [d] deploy  [e] edit in $EDITOR  [Esc] │
└────────────────────────────────────────────────────────────────────────────────┘
```

```
┌ Contract  9vX2…kP4a  (TestToken) ────────────────────────────────────────────┐
│ Methods                         │ transfer(String to, String amount) : boolean │
│   balanceOf(String) : String    │                                              │
│   allowance(String,String)      │ to      u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjV… │
│ > transfer(String,String)       │ amount  250                                  │
│   approve(String,String)        │                                              │
│   getName() : String     getter │ fee ≈ 0.0175 CS        [Enter] sign and call │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Unlock

The interface opens on the **unlock screen**, which shows the accounts' names and addresses only
(and whether one is the node's key). The user picks an account and types its password; nothing of
an account (balance, delegations, history, tokens, contracts) is loaded or shown before. A plain
node's key needs no password. A wrong password says only "could not unlock: wrong password, or a
damaged key file"; there is no limit on tries, but each failure in a row adds a wait before the
next one (2 s, doubling, at most 30 s) on top of Argon2id's own cost.

Unlocked, signing asks no password again (the user's choice); on MAINNET the review still asks to
type `send`, and shows everything as before. `l` locks; `a` switches account (it locks first);
the session locks by itself after `lock_after_minutes` (top level of `config.toml`, default 10;
`0` never locks: then anyone at the terminal can sign with the unlocked account) without a key
press or click, but never while a transaction is on its way. After an account is created or
imported (or the node's key made), the session that was open is locked and the unlock screen comes
back on the new account. Locking zeroes the key (4.2) and forgets the account's data; the
interface zeroes it at exit too. The commands are unchanged: each one asks the password when it
signs.

The unlock screen and the main screen offer `b`, the backup (4.5): `e` export, `i` import. It
asks no account's password: the key files go in and come back encrypted.

Editing contract sources happens in the user's editor (`$EDITOR`, Notepad on Windows); the wallet shows
the file, compiles it and reports errors.

## 7. Commands (scriptable)

```
credits-wallet                          # the TUI
credits-wallet status
credits-wallet account new|import|export|list|address
credits-wallet account node-key | node-key new [--out FOLDER]       # the node's key (4.4)
credits-wallet account export <account> --format node --out FOLDER [--replace]
credits-wallet backup export <FILE.dat> | import <FILE.dat> [--rename-suffix S] [--yes]   # 4.5
credits-wallet balance [account]
credits-wallet send <account> <to> <amount> [--data TEXT] [--yes]
credits-wallet delegate <account> <node> <amount> [--until DATE] | undelegate <account> <node> <amount>
credits-wallet delegations <account|address>
credits-wallet tokens <account|address>
credits-wallet token info <token> | balance <token> <account|address> | send <account> <token> <to> <amount>
credits-wallet token add|remove <account> <token>
credits-wallet history <account> [--tokens] [--token ADDR] [--csv FILE]
credits-wallet contract templates | new <template> <file.java> [--force]
credits-wallet contract compile <file.java>
credits-wallet contract deploy <account> <file.java> [--yes]
credits-wallet contract methods <address> | info <address>
credits-wallet contract get <address> <method> [args…] [--from ACCOUNT]
credits-wallet contract call <account> <address> <method> [args…] [--yes]
credits-wallet contract pay <account> <address> <amount> [--data TEXT] [--yes]
credits-wallet contract list <account|address>
global: --node HOST:PORT  --history-node HOST:PORT  --json  --config FILE
```

The password is asked on the terminal when a command signs (each command on its own: there is no
session outside the TUI), or read from a file named by `--password-file` for unattended use
(documented with its risks). A node's key the node encrypted asks the node's password the same way.

## 8. Quality

- Unit tests: signing (byte-identical to the node's tests and `ci/testnet/fund.py`; user data,
  delegations and token calls against `testdata/sign-vectors.txt`, made by `testdata/sign-vectors.py`
  the way `fund.py` and `samples/python/token_client.py` build them), key store
  round-trips and wrong passwords, seed phrases (vectors generated with the node's own code), amount
  parsing, contract address derivation and the deploy (vectors from `testdata/sign-vectors.py`, as
  `credits_client.py` builds them), argument typing, the local contract list and its merge with the
  node's, compile error parsing, the network-identity rules.
- Offline: the commands against `testdata/fake_node.py`, which checks every signature over the bytes
  `fund.py` would sign (transfers with and without user fields, delegations, token calls) and plays a
  token and a node without the token cache; for the contracts it compiles (with errors), accepts a
  deploy only at the address the node derives and with a valid signature, lists contracts, answers
  getters and typed calls, runs `payable()` on a transfer, returns a state, and serves
  `GetNodeInfo` on a diag port with or without a network uuid (mainnet's or another).
- Integration (`.github/workflows/privnet.yml`, `ci/privnet/e2e.sh`): a private network of 5 community
  nodes with their contract executors; the wallet's commands create and import accounts, transfer
  with user data, delegate with and without a time limit, withdraw, read the history from node 1 and
  from node 2 as a remote history node, read node 1's network identity from its diag port (the
  mismatch check when the node reports a uuid, "the configuration decides" when not), and with the
  wallet itself: check its templates against the node's `samples/contracts`, report a compile error
  with its line, deploy `TestToken` and send its tokens, deploy `Counter`, list its methods, get and
  call (including a call the contract refuses), deploy `TipJar` and pay it (`payable()`), and list
  the accounts' contracts; checking balances, delegations, token balances, the token history, the
  CSV, getters' results and the contract lists. The executor and its JDK come from the community release package pinned in the workflow
  (tag, the commit it must point to, and the package's SHA-256, bumped deliberately; an input tries
  another release with its expected commit); the release's
  node binary is a mainnet build (its genesis funds go to the mainnet genesis key), so the job builds
  the same release commit with `-DCREDITS_TESTNET=ON`, cached per release commit.
- Releases (`.github/workflows/release.yml`, `ci/release/build.sh`): a tag `wallet-vX.Y.Z` on main
  publishes, `wallet-vX.Y.Z-rcN` publishes a pre-release; pull requests build only. linux/amd64,
  windows/amd64 and darwin/arm64 are cross-compiled on one Linux runner with CGO off (Go and the
  libraries are pure Go: macOS needs no native runner to build); before publishing, the Windows
  and macOS binaries run on their own systems (`version`, `help`, `status` without a node), and a
  second runner builds the tag again and must get the same `SHA256SUMS`. Only the push of a tag
  publishes (a manual run on a tag builds only); the notes name the tag's commit. The
  build is reproducible: `-trimpath -buildvcs=true`, ldflags `-s -w -buildid= -X
  main.version=X.Y.Z`, archives dated at the commit's time (SOURCE_DATE_EPOCH), owner 0:0, fixed
  modes, name order, gzip `-n`, zip `-X`; Go 1.27.3 and Thrift 0.19.0 pinned. Each archive holds
  `credits-wallet-X.Y.Z/` with the binary, README.md, LICENSE, guide.md and guide.it.md, listed in
  `SHA256SUMS`. Actions pinned by commit; only the publishing job may write.
- ci.yml also cross-compiles linux/arm64, which is not released yet.
- Security review of keys, signing and the key store before the first release.

## 9. Open points

1. ~~The keys JSON of the official wallets~~: settled (section 4.1), checked with a file generated on
   wallet.credits.com on 2026-11-03.
2. ~~How the wallet tells mainnet from another network~~: settled (section 5.1): mainnet by default,
   test networks declared in the configuration, and the network uuid in the community node's
   diagnostic API (CreditsCommunity/node#162, no consensus change), checked before every send.
3. ~~Fee estimate for contract calls~~: twice `ActualFeeGet` of the call's size (the call and its new
   state; 0.0175 CS on the private network) plus the execution, which the node charges afterwards;
   shown as an estimate.
4. ~~Whether the community node packages ship the wallet~~: settled, from community.17 on
   (milestone 4): the node pins a wallet release in `packaging/wallet-tag`, downloads its archives,
   checks them against the release's `SHA256SUMS` and ships the binary and the guide in every
   package and the Docker image; its set-up offers to create or import an account.

## 10. Milestones

1. Accounts (new, import, key store), status, balances, transfers, history from the local node
   (with its CSV export); the TUI shell and the commands. User data on transfers moves to 2. Done.
2. Delegations; user data on transfers; tokens (balances, info, transfers); history from a remote
   full node; token transfers in the history and the CSV export; CI on a private network. Done.
3. Smart contracts: compile, deploy, methods, getters and calls, payments; templates; the contract
   list; the network identity of community nodes. Done.
4. Packaging in the node releases; user guide (docs/guide.md, docs/guide.it.md); reproducible
   release archives; every token held with its full address, also without a token cache. Done.
   After it: the recovery file; the node's key as the account of a node's wallet, made from 24
   words when the node has none, and the export to the node's format (4.3, 4.4).

Later (not in milestone 4):

- a linux/arm64 release (built by ci.yml, not released; the node has no arm64 package yet);
- signed binaries (Windows Authenticode, macOS notarization): today the user clears the macOS
  quarantine by hand and Windows may warn;
- tokens received from others on a node without a token cache: they need an index the node does
  not keep (the node indexes source and target only); the wallet lists them once added;
- the node's diagnostic API listens on every interface; a bind address for it (localhost only)
  belongs to the node;
- a package manager entry (Homebrew, winget, apt).
