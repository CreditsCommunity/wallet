# Credits community wallet — specification

Status: milestones 1 and 2 implemented, 2026-11-03.

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
internal/wallet/         what the commands and the TUI share: accounts, the steps of a transfer
thrift/                  generated code (go generate, not committed)
docs/                    this specification, user guide
```

## 4. Keys

### 4.1 Formats

| Import or export | Format |
|---|---|
| Private key | base58 of the 64-byte ed25519 secret key (seed + public key), the node's `NodePrivate.txt` format; or base58 of the 32-byte seed |
| Seed phrase | the node's 24 words (English list of `cscrypto`): 256-bit master seed + 8-bit checksum, key derived with libsodium `crypto_kdf_derive_from_key` (key id 0, empty context) then `crypto_sign_seed_keypair` — the same derivation as `node` when it generates its keys; the checksum is verified |
| Keys JSON of the official web wallet (wallet.credits.com) and desktop wallet | `{"key":{"public":"<base58 32 bytes>","private":"<base58 64 bytes>"}}`: the private key is the 64-byte ed25519 secret key (seed + public key), the same as `NodePrivate.txt`, **not encrypted**. Imported (both halves checked against each other); after the import the wallet suggests deleting the plain file. Exported in the same format only on request, with a warning, for use in the official wallets |

### 4.2 Key store

- One file per account in the wallet's data folder (`~/.credits-wallet/` on Linux and macOS,
  `%APPDATA%\credits-wallet\` on Windows), readable by the user only.
- Encrypted with a password: Argon2id (time 3, memory 256 MiB, parallelism 4, **random 16-byte salt
  per file**) → XChaCha20-Poly1305 with a random nonce. The file records the format version, the KDF
  parameters, the salt, the nonce, the ciphertext and the public key (to list accounts without the
  password).
- The secret key is decrypted only to sign, held in memory as briefly as possible and zeroed after use.
- Key files are written whole to a temporary file (mode 0600), synced, then linked to their name:
  never half-written, never replacing an existing account. Files whose KDF parameters are outside
  time 1-16, memory 64 MiB-1 GiB, parallelism 1-32 are refused (more costs too much to open; less
  was not written by the wallet). Unreadable key files are reported, not skipped.
- New keys come from `crypto/rand` (the operating system's CSPRNG); a new account shows its 24-word
  phrase once on the terminal and asks the user to type back three of its words. The phrase never
  goes to the output (nor `--json`) by default; without a terminal the command needs
  `--words-file FILE` (created, mode 0600) or `--show-words`.

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
  - the confirmation on mainnet is typing `send` and the password (TUI and commands); on a test
    network the password alone (TUI) or `y` (commands);
  - the **community node** gets an API call that returns the network identifier (the `uuid` the node
    derives from block 1, known to pruned nodes too) and its version; when it is there, the wallet
    recognises mainnet by itself, and refuses to send if the configuration says "testnet" for a mainnet
    node or the other way round; with an official node, which lacks the call, the configuration decides.

#### Trust in the node

The wallet talks to one node and believes what it answers: balances, the fee, the sender's last
inner id, the sync state, the history. It never sends the node a secret, and a signed transaction
cannot be altered, so a dishonest or broken node cannot spend the user's CS on its own. What it can
do: show false balances or history; give a wrong inner id (the transfer is then rejected, or must
wait); claim a transaction is in a block when it is not, or drop it; and make a transfer cost up to
the **signed max fee** (shown in the confirmation, next to the expected fee). An unsynced node is
refused, a missing status in an answer is an error, an inner id is guessed only for a wallet the
node reports "not found", and a transfer is reported in a block only when the sender's last inner
id equals its own, or a later one is recorded and the transfer is among the account's latest
transactions. Running the wallet next to the user's own node is what the design assumes.

### 5.2 Accounts and balances

- List accounts, select one; create, import, export (encrypted file or base58 key after the password).
- Balance in CS (`WalletBalanceGet`), delegated amounts and the delegations made and received
  (`WalletDataGet`, `DelegatedItem`), token balances (`TokenBalancesGet`; needs `[api] tokens_cache=true`
  on the node, otherwise the screen says so: the node answers "This node doesn't provide such info").
  The node lists only tokens with a balance above 0. Without the token cache one token's balance is
  still read from its contract (`balanceOf` as a getter: `token balance TOKEN ACCOUNT`).
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
  warns when the code or name is "CS" or "Credits", or when another token the account holds has the
  same code. A `transfer` that returns `false` is reported as a failure (no tokens moved,
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

- Write or open a `.java` file; templates: the examples of the node's `samples/contracts` (Counter,
  TestToken, TipJar, Faucet).
- **Compile** through the node (`SmartContractCompile`); errors listed with their line and column.
- **Deploy**: the contract address derived as the node does (blake2s over the deployer, inner id and
  byte code), signed and sent; the address shown and saved in the account's contract list.
- **Use**: list the methods with their argument and return types (`ContractMethodsGet`,
  `ContractAllMethodsGet`); getters run without a transaction and without fee; other methods are signed
  calls with arguments converted to the declared types; results and contract state shown.
- List the account's contracts (`SmartContractsListGet`).
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
- Text chosen by others (user data, token names and codes) is shown with control characters and
  backslashes escaped, on screen and in the commands' output; `--json` also escapes the C1 controls
  (U+0080-U+009F) and the Unicode format characters (such as U+202E) as `\uXXXX`.

## 6. Terminal interface

Keyboard first (arrows, Tab, Enter, Esc, `?` for help), mouse where the terminal supports it, light
and dark themes following the terminal. Mock-ups (80 columns):

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
│ Password  ••••••••••                  [Esc]   cancel        │
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

Editing contract sources happens in the user's editor (`$EDITOR`, Notepad on Windows); the wallet shows
the file, compiles it and reports errors.

## 7. Commands (scriptable)

```
credits-wallet                          # the TUI
credits-wallet status
credits-wallet account new|import|export|list
credits-wallet balance [account]
credits-wallet send <account> <to> <amount> [--data TEXT] [--yes]
credits-wallet delegate <account> <node> <amount> [--until DATE] | undelegate <account> <node> <amount>
credits-wallet delegations <account|address>
credits-wallet tokens <account|address>
credits-wallet token info <token> | balance <token> <account|address> | send <account> <token> <to> <amount>
credits-wallet history <account> [--tokens] [--token ADDR] [--csv FILE]
credits-wallet contract compile <file.java>
credits-wallet contract deploy <account> <file.java>
credits-wallet contract methods <address>
credits-wallet contract get <address> <method> [args…]
credits-wallet contract call <account> <address> <method> [args…]
global: --node HOST:PORT  --history-node HOST:PORT  --json  --config FILE
```

The password is asked on the terminal, or read from a file named by `--password-file` for unattended
use (documented with its risks).

## 8. Quality

- Unit tests: signing (byte-identical to the node's tests and `ci/testnet/fund.py`; user data,
  delegations and token calls against `testdata/sign-vectors.txt`, made by `testdata/sign-vectors.py`
  the way `fund.py` and `samples/python/token_client.py` build them), key store
  round-trips and wrong passwords, seed phrases (vectors generated with the node's own code), amount
  parsing, contract address derivation.
- Offline: the commands against `testdata/fake_node.py`, which checks every signature over the bytes
  `fund.py` would sign (transfers with and without user fields, delegations, token calls) and plays a
  token and a node without the token cache.
- Integration (`.github/workflows/privnet.yml`, `ci/privnet/e2e.sh`): a private network of 5 community
  nodes with their contract executors; the wallet's commands create and import accounts, transfer
  with user data, delegate with and without a time limit, withdraw, read the history from node 1 and
  from node 2 as a remote history node, and send tokens of the node's `TestToken` (deployed with the
  node's Python sample client), checking balances, delegations, token balances, the token history and
  the CSV. The executor and its JDK come from the community release package pinned in the workflow
  (tag, the commit it must point to, and the package's SHA-256, bumped deliberately; an input tries
  another release with its expected commit); the release's
  node binary is a mainnet build (its genesis funds go to the mainnet genesis key), so the job builds
  the same release commit with `-DCREDITS_TESTNET=ON`, cached per release commit.
- Builds and releases for linux/amd64, linux/arm64, windows/amd64, darwin/arm64 with checksums.
- Security review of keys, signing and the key store before the first release.

## 9. Open points

1. ~~The keys JSON of the official wallets~~: settled (section 4.1), checked with a file generated on
   wallet.credits.com on 2026-11-03.
2. ~~How the wallet tells mainnet from another network~~: settled (section 5.1): mainnet by default,
   test networks declared in the configuration, and a network-identifier call to add to the community
   node's API (a separate node PR, no consensus change).
3. ~~Fee estimate for contract calls~~: twice `ActualFeeGet` of the call's size (the call and its new
   state; 0.0175 CS on the private network) plus the execution, which the node charges afterwards;
   shown as an estimate.
4. Whether the community node packages ship the wallet from the first release or a later one.

## 10. Milestones

1. Accounts (new, import, key store), status, balances, transfers, history from the local node
   (with its CSV export); the TUI shell and the commands. User data on transfers moves to 2. Done.
2. Delegations; user data on transfers; tokens (balances, info, transfers); history from a remote
   full node; token transfers in the history and the CSV export; CI on a private network. Done.
3. Smart contracts: compile, deploy, methods, getters and calls; templates.
4. Packaging in the node releases; user guide.
