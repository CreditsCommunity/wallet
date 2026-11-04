# credits-wallet user guide

credits-wallet is a terminal wallet for the CREDITS network. It runs on the same machine as a node,
on Linux, Windows and macOS, in a terminal window or over SSH. It keeps your keys on your machine,
signs every transaction itself and sends only signed transactions to the node. It opens no port and
needs no browser.

This guide covers everyday use. The full reference is
[docs/spec.md](https://github.com/CreditsCommunity/wallet/blob/main/docs/spec.md) and
`credits-wallet help`. A version in Italian is in `guide.it.md`.

> **Mainnet means real money.** Every amount you send on mainnet is final. Read
> [Security advice](#8-security-advice) before your first transfer.

Contents

1. [Install and first start](#1-install-and-first-start)
2. [Accounts, the recovery phrase and backups](#2-accounts-the-recovery-phrase-and-backups)
3. [Importing a key from the old official wallet](#3-importing-a-key-from-the-old-official-wallet)
4. [Profiles, the local node, the diag port and the network check](#4-profiles-the-local-node-the-diag-port-and-the-network-check)
5. [Sending CS, delegations, tokens and history](#5-sending-cs-delegations-tokens-and-history)
6. [Smart contracts](#6-smart-contracts)
7. [Scripts](#7-scripts)
8. [Security advice](#8-security-advice)
9. [Troubleshooting](#9-troubleshooting)

---

## 1. Install and first start

### From the community node package (recommended)

From release community.17 on, every package of the
[community node](https://github.com/CreditsCommunity/node/releases) ships the wallet in its
`csnode` folder, next to the node:

| Package | Wallet |
|---|---|
| `credits-node-…-linux-x64.tar.gz`, `…-linux-x64-glibc2.31.tar.gz` | `csnode/credits-wallet` |
| `credits-node-…-windows-x64.zip` | `csnode\credits-wallet.exe` |
| `credits-node-…-macos-arm64.tar.gz` | `csnode/credits-wallet` |
| Docker image `ghcr.io/creditscommunity/credits-node` | `credits-wallet` on the `PATH` |

This guide is in the package as `wallet-guide.md` (and `wallet-guide.it.md`).

The guided set-up of the node (`./setup.sh`, or `setup.bat` on Windows) writes a wallet profile
for the node it has just configured (`wallet.toml`, see
[section 4](#4-profiles-the-local-node-the-diag-port-and-the-network-check)) and, by default, makes
the node's own key the wallet's account: when the node has a key, it is used as it is; when it has
none yet, the set-up makes it now from a 24-word phrase (see [The node's key](#the-nodes-key-the-wallet-of-a-node)).
A separate account, for savings, is the other choice.

### On its own

Each wallet release on [GitHub](https://github.com/CreditsCommunity/wallet/releases) has an archive
per system and a `SHA256SUMS` file:

```sh
# Linux x86-64 (static binary: any distribution)
sha256sum -c --ignore-missing SHA256SUMS
tar -xzf credits-wallet-0.1.0-linux-amd64.tar.gz
cd credits-wallet-0.1.0 && ./credits-wallet version
```

- **macOS (Apple Silicon)**: `shasum -a 256 -c --ignore-missing SHA256SUMS`; the binary is not
  signed, so clear the download quarantine once: `xattr -d com.apple.quarantine credits-wallet`.
- **Windows (x64)**: in PowerShell, `Get-FileHash credits-wallet-0.1.0-windows-amd64.zip` and
  compare with `SHA256SUMS`; extract the zip and run `credits-wallet.exe` from Windows Terminal
  (or PowerShell, cmd). SmartScreen may warn about an unsigned program.

The binaries are reproducible: the same tag built again gives the same files (see the README).

### First start

1. **Start the node and let it sync.** The wallet talks to the node's API on `127.0.0.1:9090`
   (`[api] port` in the node's `config.ini`; 9090 in the packages).
2. **Check the node:**

   ```sh
   ./credits-wallet status
   ```

   It shows the network, the last block, whether the node is `synced`, and what the node says
   about its network. The wallet refuses to send while the node is not synced.
3. **Open the terminal interface:**

   ```sh
   ./credits-wallet
   ```

   The top line shows the network (MAINNET in red) and the node. `?` shows the keys, `q` quits.
   Every function also exists as a command (`credits-wallet help`).
4. **Unlock an account.** The interface opens on the unlock screen: the accounts' names and
   addresses, nothing else. Choose one with the arrows, press Enter and type its password; only
   then are its balance, history, tokens and contracts shown. See
   [Unlocking and locking](#unlocking-and-locking).

### Unlocking and locking

- **One password per session.** Once an account is unlocked, sending, delegating and calling
  contracts in the interface ask no password again; on mainnet you still type `send` on the full
  review. The command line is different: each command asks the password when it signs.
- **Lock** with `l`; **switch account** with `a` (the wallet locks first and asks the other
  account's password). After you create or import an account the wallet also goes back to the
  unlock screen, on the new account.
- **Auto-lock**: after 10 minutes without a key press the wallet locks by itself (never while a
  transaction is on its way). Change it with `lock_after_minutes = N` at the top of `config.toml`;
  `0` never locks, which means that whoever reaches your terminal (an SSH session left open, a
  screen not locked) can sign with the unlocked account: keep it short on machines others can
  reach.
- A wrong password says only that the account could not be unlocked; each wrong try in a row adds
  a short wait (2 s, then 4, 8, up to 30 s).
- While unlocked, the key is kept in memory locked against swapping and out of core dumps, and is
  zeroed when the wallet locks or quits. If the system refuses that ("the key could not be locked
  in memory: it may reach swap"), use encrypted swap. Some copies cannot be wiped by a Go program
  (what the signature computes from the key, the password you typed): disable core dumps on
  machines that hold funds.

In Docker the wallet runs inside the node's container, with its data on the `/data` volume
(`/data/wallet`); set `CREDITS_API=on` in `.env` (the wallet talks to the node's API). With the
project's `docker-compose.yml` (service `node`, container `credits-node`):

```sh
docker compose exec node credits-wallet       # or: docker exec -it credits-node credits-wallet
```

---

## 2. Accounts, the recovery phrase and backups

### Create an account

In the interface press `n`; on the command line:

```sh
./credits-wallet account new main
```

1. Choose a **password**. It encrypts the key file on this machine (Argon2id and
   XChaCha20-Poly1305). The interface asks it to unlock the account; the commands, for every
   transaction.
2. The wallet shows the **24-word recovery phrase** once. Write it on paper, in order.
3. Type back the three words it asks for.
4. The wallet asks `Save the 24 words to a file as well? [y/N]`. **The default is no**: press
   Enter and the words are only on your paper. See [The recovery file](#the-recovery-file).

The account's address (its public key, base58) is shown; give it to whoever sends you CS.

**Copying your address.** On the main screen, and after creating or importing an account, `y`
copies the account's full address to the clipboard (OSC 52: it works over SSH too, to the
clipboard of the computer you are sitting at). The address is also shown whole on a line of its
own: if your terminal blocks OSC 52, hold Shift and drag over it. On the command line
`account address NAME` prints the address alone, to pipe into the clipboard:

```sh
./credits-wallet account address main | xclip -selection clipboard   # Linux (X11; wl-copy on Wayland)
./credits-wallet account address main | pbcopy                       # macOS
.\credits-wallet.exe account address main | clip                     # Windows
```

### The recovery phrase

- The 24 words **are** the account: anyone who has them can spend everything in it, from any
  wallet, with no password. Nobody can recover the account without them.
- Write them on paper, never in an e-mail, a chat, a photo or a password manager in the
  cloud. A file only as a passage, see below. Keep the paper where fire and water cannot reach it, ideally in two places.
- The wallet clears the screen afterwards, but a terminal's scrollback, tmux/screen or a session
  log may keep the words: close the terminal window, and clear the scrollback of a long-lived
  session.
- The phrase is the same format as the node's: `account import NAME` and typing the 24 words
  restores the account, on this or another machine.

### The recovery file

If you answer `y` to the question after the check (on the command line or in the interface), the
wallet also writes the 24 words to a file:

- By default `~/credits-recovery-NAME.txt` (on Windows `%USERPROFILE%\credits-recovery-NAME.txt`);
  type another path if you prefer (Enter keeps the default; `~/` stands for your home folder).
- The file holds the account name, its address, the date, the 24 numbered words and a warning. It
  is created new, readable by you only (mode 600; on Windows the user profile's own permissions),
  and never replaces an existing file or follows a symbolic link. On Linux it appears whole or not
  at all. On macOS and Windows the wallet first writes `credits-recovery-NAME.txt.partial` next to
  it and renames it when complete: if the computer crashes or loses power during those few
  milliseconds, **delete the `.partial` file** (it holds the words). The wallet refuses to write
  while one is there.
- **It is never kept next to the keys.** A path in the wallet's data folder, or in a folder that
  holds key files, or is below one, is refused and the wallet asks again. Key files are wallet key
  files and encrypted exports, `NodePrivate.txt` and its copies, a base58 secret key
  (`export --format base58`), the official wallets' keys JSON, and links to any of them. The node's
  folder is refused too, even while the wallet is making the node's key there. A folder
  the wallet cannot list is refused too. The data folder is recognised even under another name (a
  link, a junction, a different case on macOS). Whoever copies the data folder gets the encrypted key files, which
  still need your password; with the words beside them the password would protect nothing.
- **Move it off the machine.** The wallet then says: *This file holds your account in clear: copy
  it to paper or an offline USB key, then delete it from this computer.* Do that now: a file left
  on the computer can be read by malware, by a backup service, or by anyone who gets the disk.

The question is not asked with `--json`, with `--words-file` or `--show-words`, or without a
terminal: scripts keep using `--words-file FILE` (the same writer, the words alone on one line).

Under `sudo`, the wallet uses the home of the user it runs as for both its data folder and this
file. If `$HOME` belongs to someone else (`sudo` without `-H`), or the wallet runs as root, it
warns you.

### Backups

| What | How | Note |
|---|---|---|
| The phrase | on paper | restores everything, without the password |
| The key file | `account export main --out main.key` | encrypted copy; needs the password |
| The data folder | copy it | key files (encrypted), configuration, local contract and token lists |

The data folder is `~/.credits-wallet` on Linux and macOS, `%APPDATA%\credits-wallet` on Windows,
or the folder named by `CREDITS_WALLET_HOME` (`/data/wallet` in the Docker image). Key files are
in `accounts/`, readable by you only.

**A forgotten password cannot be recovered**: import the account again from its 24 words, with a
new password (`account import main2`).

### Other imports

`account import NAME FILE`, or `i` in the interface, also accepts:

- a base58 private key;
- the official wallets' keys JSON (next section);
- the node's own key, `NodePrivate.txt` (base58), or the node's 24-word phrase.

After importing a keys JSON or a base58 key from a file, delete the plain file: the key is then in
the encrypted key store.

Next to a node there is no need to import the node's key: the wallet uses it in place (next
section). An imported copy is encrypted in the key store, but the original stays in clear for the
node, so the copy protects nothing more. **Never delete `csnode/NodePrivate.txt`: the node needs it
to run.**

### The node's key (the wallet of a node)

The node has its own key pair: its address receives the node's rewards and the delegations made to
it, and operators usually keep their stake there. Next to its node the wallet is **the node's
wallet** and uses that key directly, as the account `node`:

- when it runs with the package's `wallet.toml` (`./credits-wallet --config wallet.toml`, in the
  `csnode` folder): its profile says `node_key = "NodePrivate.txt"`;
- when a node profile of your `config.toml` has `node_key = "/path/to/csnode/NodePrivate.txt"`
  (the set-up writes it there with the full path when it creates `config.toml`);
- with `--node-key /path/to/csnode/NodePrivate.txt` on the command line;
- in the node's Docker image (`CREDITS_WALLET_NODE_KEY=/data/keys/NodePrivate.txt`).

`node_key = "off"` in the profile turns it off. The wallet never guesses it from the folder you
run it in: a folder someone else controls could hold a planted key pair, whose address you would
then hand out. Before using the key it checks that `NodePrivate.txt` and its folder are yours (or
root's) and that nobody else can write to them (fix with `chmod 600 NodePrivate.txt` and `chmod
go-w` on the folder; the set-up does both when you choose the node's key), and the first time it
reads the key it says the account's address and the file it comes from: check that they are your
node's. A key file other users can **read** is used, with a warning: the node writes its own key
readable by everyone (mode 644), so run the `chmod 600` the warning shows. On Windows these checks
are not done (`account node-key` says so): check the file's Properties > Security yourself.

The account `node` is listed first and selected when the interface starts; it is marked **NODE
KEY** in the list, on the account's screen, in `account list` and in every review. The wallet reads
`NodePrivate.txt` where the node reads it: the key is **not copied** into the wallet's key store and
**not encrypted** by the wallet (the node needs its file), so a plain key asks no password, on the
unlock screen or to sign; on mainnet you still type `send`. `account node-key` shows the file, the address, whether
`NodePublic.txt` matches and whether the node is running. (If the key store already has an account
named `node`, from an earlier import, the node's key is called `node-key` and the other keeps its
name.)

> **The node's key is kept in clear on the server**, because the node needs it: CS on it are as
> exposed as the server. Keep on it only what the node needs (its stake), and your savings in a
> separate account (`account new`).

**A key the node encrypted.** If the node was started with `--encryptkey`, or you chose "[1]
Encrypt with password" at its first start, `NodePrivate.txt` is encrypted with the node's password.
The wallet opens it with that same password: on the unlock screen of the interface, and on the
command line when a command signs (`--password-file` works too). The account `node` shows the
address of `NodePublic.txt` until then, marked "encrypted by the node", and the wallet checks that
the key it opens is that one; until you unlock it, `account list`, `account address` and the
found-key screen mark the address "(from NodePublic.txt, unverified until unlocked)". Opening takes
some seconds and **1 GiB of free memory**, as in the node: with less, the wallet refuses with
"the node's key needs 1 GiB of free memory to open (as the node does)" (stop something, or open it
on a machine with more memory). The
wallet never writes the opened key anywhere, and refuses to export it in clear. The wallet opens
both of the node's formats: a file the node encrypts now has its own random salt; one encrypted by
an older node uses a salt every node shares, so choose a long password (re-encrypting it with a
current node gives it its own salt).

**A key in the folder you start the wallet in.** When you start the interface in a folder that
holds `NodePrivate.txt` (your `csnode`, say) and no node's key is configured, the wallet says so on
its first screen with the key's address and full path, and asks: `u` use it for this session, `r`
use it and remember it (it writes `node_key` with the full path into your profile in
`config.toml`, keeping everything else), `i` ignore it, `x` ignore it and never ask again for that
file. It never uses it by itself, and refuses it when other users can write to the file or its
folder. Check the address before you choose it. The commands only print a hint:
`This folder holds a node key: to use it, --node-key NodePrivate.txt`.

**The node has no key yet.** A new node folder has no `NodePrivate.txt`; the node would make one at
its first start and ask you questions on its console. Make it with the wallet instead, from a
24-word phrase that is your paper backup of the node's identity:

```sh
./credits-wallet --config wallet.toml account node-key new      # in the csnode folder
```

or `g` on the main screen of the interface. As for `account new`, the wallet shows the 24 words
once, asks for three of them back and offers the recovery file (default no; never in the node's
folder). It then writes `NodePrivate.txt` and `NodePublic.txt` in the node's folder exactly as the
node writes them, readable by you only (mode 600). The node uses them at its next start without a
question. The wallet refuses, before showing any word, when a key file is already there (or a link
with that name), when the node is running, or when the folder belongs to another user or every user
can write to it. `--out FOLDER` names another node's folder. The 24 words restore the key: with
`account import`, or with the node's own `--seed` option.

**An account's key as the node's.** `account export NAME --format node --out CSNODE_FOLDER` writes
an account of the wallet as the node's `NodePrivate.txt` and `NodePublic.txt` (the password of the
account is asked; you type `export` to confirm). It never writes over a key the node has. To
**replace the node's identity**, stop the node, then add `--replace`: the wallet keeps the old files
as `NodePrivate.txt.bak-DATE` and `NodePublic.txt.bak-DATE` (mode 600) and asks you to type
`replace`. **The node's identity follows its key**: its address, the delegations it received, its
history as a trusted node and its rewards all belong to the key; a node started with another key is
another node. **Stop the node first**: the wallet looks for a running node (its `db/.lock`, its API
port, on Linux its process), but only as a safeguard; a node started with other settings or as
another user is not seen, and a node that keeps running keeps the old key until its next start.
The backups hold the old key in clear: once you are sure, copy them offline (paper, an offline USB
key) and delete them from the server. `account node-key` lists them.

**A half-written key.** If `NodePrivate.txt` is there but `NodePublic.txt` is missing or does not
match it (a crash during a write, a file copied by hand), the node stops at its start and asks on
its console: a node without a console hangs. With the node stopped,
`./credits-wallet --config wallet.toml account node-key repair` writes `NodePublic.txt` from
`NodePrivate.txt` (a file that did not match is kept as `NodePublic.txt.bak-DATE`).

---

## 3. Importing a key from the old official wallet

The official web wallet (wallet.credits.com) and desktop wallet save a key as a JSON file:

```json
{"key":{"public":"<base58 public key>","private":"<base58 private key>"}}
```

This file is **not encrypted**: whoever reads it owns the account. To move it into
credits-wallet:

```sh
./credits-wallet account import old ~/Downloads/keys.json
```

or `i` in the interface and the file's path in the Secret field. The wallet checks that the two
halves belong together, asks for a password and stores the key encrypted. Then:

1. Check the address it shows: it must be your old wallet's address.
2. Run `./credits-wallet balance old`: the balance must be the one you know.
3. **Delete the JSON file** (and empty the recycle bin), and any copy of it in Downloads, e-mail
   or cloud folders.

To go back to an official wallet, `account export old --format official --out keys.json` writes
the same unencrypted JSON after a warning.

---

## 4. Profiles, the local node, the diag port and the network check

### Profiles

A profile names a node and its network. Without a configuration file the wallet uses one profile,
`local`: the node at `127.0.0.1:9090`, on mainnet. `credits-wallet config` prints an example
`config.toml`; save it in the data folder to change it:

```toml
node = "local"                 # the profile used by default

[nodes.local]
address = "127.0.0.1:9090"     # the node's API ([api] port)
network = "mainnet"            # "mainnet", "testnet" or "private"
diag = "127.0.0.1:9088"        # the node's diagnostic API ([api] diag_port); "off" not to ask
# history_node = "203.0.113.7:9090"   # a remote full node for the history (read-only)

[nodes.test]
address = "127.0.0.1:9190"
network = "testnet"
```

Choose a profile with `--profile test` on any command. `--node HOST:PORT` talks to another node,
which then always counts as **mainnet**.

The node's set-up (`setup.sh` / `setup.ps1`) writes `wallet.toml` in the `csnode` folder with a
profile for the node it configured (its API port, its diag port, mainnet, and, when you choose the
node's key, `node_key = "NodePrivate.txt"`: the node's key is the account `node`; otherwise
`node_key = "off"`, and a value you set by hand is kept on later runs), and copies it to the data folder as
`config.toml` when there is none yet (with the full path of `NodePrivate.txt`). An existing
`config.toml` is never changed: `credits-wallet --config wallet.toml` uses the set-up's file
instead.

### The network check

Mainnet is the default: the wallet assumes it unless the profile says `testnet` or `private`. On
mainnet the screens are red and every transaction asks you to type `send` (and, on the command
line, the password).

A community node from release community.17 on also **says which network it is on**, through its
diagnostic API (`[api] diag_port`, 9088 in the packages). Before every transaction the wallet asks
it and **refuses to send** when the node and the profile disagree: a profile that says mainnet on
a test node, or the other way round. `status` shows what the node says:

```
network      : MAINNET (declared in the configuration; confirmed by the node)
node says    : MAINNET (uuid 11024959585341937636), community node 4.2.532-community.17
```

When the node does not answer (an official node, an older release, the diag port off or closed),
the configuration decides and the review says "not confirmed by the node". To require the
confirmation, add `require_network_uuid = true` to the profile; `network_uuid = "…"` pins a
network (the uuid `status` shows).

**The diag port and your firewall.** The packages ship the diag port **on** (`diag_port=9088`), and
the node listens on it on **every interface**, like the main API, not only on `127.0.0.1`. The
diagnostic API also has commands that change the node's behaviour: **firewall 9088 (and 9090) so
that no other host reaches them**; the wallet needs them on `127.0.0.1` only. The node's set-up
never turns the diag port on for you. If you set `diag_port=0`, the
wallet still works, without the network confirmation.

---

## 5. Sending CS, delegations, tokens and history

### Send CS

In the interface: `s`, then the recipient, the amount and an optional Data text. On the command
line:

```sh
./credits-wallet send main RECIPIENT_ADDRESS 12.5
./credits-wallet send main RECIPIENT_ADDRESS 12.5 --data "invoice 42"
```

The review shows the recipient in full, the amount, the expected fee (about 0.0087 CS), the signed
**max fee** (1 CS: the most the node may charge), and whether the node confirmed the network. On
mainnet type `send` (on the command line the password follows). The wallet follows the transaction until it is in a block.

- Check the **whole** recipient address, not only its first and last letters.
- **Pasting the recipient.** Use your terminal's paste (Ctrl+Shift+V, right click, Cmd+V on
  macOS) in the recipient field, and in every field that takes an address (a node to delegate to,
  a token, a contract). Spaces, line breaks and a leading `credits:` are removed; anything that is
  not an address (base58 of 32 bytes) is refused and the field stays as it was. The pasted address
  is shown with its first and last 6 characters highlighted: compare them, and the rest, with where
  you copied it from. Some malware replaces addresses in the clipboard with its own. The wallet
  never reads the clipboard by itself (Ctrl+V does nothing in its fields).
- The Data text is **public**: anyone can read it on the chain.
- If the wallet says "sent, but … check history later", **do not send again** before the history
  shows whether the first one arrived.

### Delegations

```sh
./credits-wallet delegate main NODE_ADDRESS 1000                     # no time limit
./credits-wallet delegate main NODE_ADDRESS 500 --until 2027-01-31   # until a UTC date
./credits-wallet undelegate main NODE_ADDRESS 1000                   # withdraw (no-limit only)
./credits-wallet delegations main                                    # made and received
```

In the interface: `d`, then `n` to delegate and `w` to withdraw the selected delegation. At least
5 CS each time. A delegation with a time limit ends by itself at its date.

### Tokens

A token is a smart contract: **only its contract address identifies it**. Anyone can deploy a
token that calls itself "CS" or copies another token's name.

`t` in the interface lists every token the account holds: code, balance and name, and for the
selected token its **full contract address**, decimals and balance, with warnings when its code or
name looks like CS or like another token you hold. `y` copies the address (in terminals that
support OSC 52; otherwise hold Shift and drag to select it), `s` sends the token, `a` adds a token
by its address, `x` takes a token off your list. The main screen shows the balances and the number
of tokens, with `[t]`.

```sh
./credits-wallet tokens main                           # every token, with its address
./credits-wallet token info TOKEN_ADDRESS              # code, name, decimals, supply
./credits-wallet token send main TOKEN_ADDRESS RECIPIENT 250.5
./credits-wallet token add main TOKEN_ADDRESS          # keep a token in the list
./credits-wallet token balance TOKEN_ADDRESS main      # one balance, from the contract
```

**Where the list comes from.** The node keeps a token cache only with `[api] tokens_cache=true`
in its `config.ini` (off by default; the set-up offers to turn it on). Then the list comes from
the cache, plus the tokens of the account's local list (tokens you added, or sent with the
wallet) that the cache does not name. Without the cache, the wallet:

- uses the token cache of the profile's `history_node`, if it has one (the balances are still read
  from your node);
- otherwise looks through the account's history (the latest 1000 transactions: the tokens it sent,
  called or bought, and the ones it received) and its local list (tokens you added or sent), and
  reads each balance from the contract. A token the account only received is marked "received
  from ADDRESS: not added by you": anyone can send you a token, look-alikes included. Such a list is labelled **"found from history (may be incomplete
  on a pruned node)"**: tokens that others sent you are not in your own history, and a pruned node
  keeps only recent blocks. Add them with `a` or `token add`.

The cache costs: at its first start with the cache on, the node rebuilds it (it looks for tokens
among all deployed contracts and their transactions, which takes a while), and it then reads token
balances in the background after each token call.

**The cache is complete only if it was on while the node read every block.** A full node with
`tokens_cache=true` from the start knows every holder, and keeps them when it prunes later. A
pruned node, or one started from the published snapshot, that turns the cache on later lists every
token, and the balances it shows are correct, but it may miss the wallets whose last use of a token
is older than its oldest stored block: for those, the token is not listed until they use it again.
If a token you hold is missing, add it with `a` or `token add` (its balance is then read from the
contract).

A token transfer costs about 0.0175 CS plus the execution; the CS balance must also cover the
signed max fee (1 CS), which the node holds while the call runs.

### History

`h` in the interface (`t` for token transfers, `e` to export a CSV); on the command line:

```sh
./credits-wallet history main
./credits-wallet history main --tokens
./credits-wallet history main --csv main.csv
```

A **pruned** node keeps only recent blocks: the wallet says from which block its history starts.
For the older history, name a full node you trust in the profile (`history_node = "HOST:PORT"`)
or use `--history-node HOST:PORT`. That node only reads: it receives the account's public address
and is never used to send.

---

## 6. Smart contracts

Contracts are Java classes, compiled and run by the node's contract executor. The node must run
it (the packages do).

### Templates

```sh
./credits-wallet contract templates           # Counter, TestToken, Token, TipJar, Faucet
./credits-wallet contract new Counter Counter.java
```

### Compile

```sh
./credits-wallet contract compile Counter.java
```

Errors are listed as `FILE:LINE: message`. In the interface (`c`, then `o` to open a file or `n`
for a template) the source is compiled when it opens, error lines are marked, and `e` opens it in
your editor (`$EDITOR`, Notepad on Windows) and compiles it again when you come back.

### Deploy

```sh
./credits-wallet contract deploy main Counter.java
```

The review shows the contract's future address, the fee (about 0.1 CS for a small contract) and
the max fee. Once the deploy is in a block, the wallet waits until the node lists the contract and
saves it in the account's local contract list (`contract list main`).

### Get, call, pay

```sh
./credits-wallet contract methods CONTRACT            # methods and their types
./credits-wallet contract get CONTRACT getValue       # free: nothing is signed or kept
./credits-wallet contract call main CONTRACT add 4    # signed, about 0.0175 CS + execution
./credits-wallet contract pay main CONTRACT 3 --data "thanks"   # CS to its payable()
./credits-wallet contract info CONTRACT               # deployer and public state
```

- **get** runs a method without a transaction: use it for anything that only reads.
- **call** signs a transaction; arguments are converted to the types the method declares (put
  `--` before an argument that starts with `-`). A method that throws is refused by the node, and
  the fee is paid.
- **pay** sends CS to the contract; its `payable()` may refuse them (they come back, the fee is
  paid).

In the interface, on a contract: Enter reads a getter or signs a call, `g` gets, `c` calls. A long
review scrolls (PgDn): it is signed only once you have seen it to the end.

Everything a contract says about itself (names, results, state) is its author's text: the wallet
shows it escaped, and you should trust it no more than its author.

---

## 7. Scripts

Every command takes `--json` for machine-readable output. To send without questions:

```sh
./credits-wallet send main ADDRESS 1 --yes --password-file ~/.wallet-pw
```

`--password-file` reads the password from the first line of a file that must be `chmod 600`. The
password is then on disk in clear: use it only where unattended sending is really needed, on an
encrypted disk, and for an account holding little. Without a terminal the wallet refuses to send
without `--yes`, and `account new` needs `--words-file FILE` (or `--show-words`).

---

## 8. Security advice

- **Never share the 24 words**, with anyone, for any reason: no support person, admin or "airdrop"
  needs them. Whoever asks for them is stealing.
- **Separate accounts for test networks.** A signed transaction does not say which network it is
  for: one signed on a test network can be replayed on mainnet by whoever runs or watches that test
  node. Never use an account that holds CS on mainnet anywhere else. Declare test nodes as
  `network = "testnet"` (or `private`) in their profile.
- **Lock the wallet** (`l`) before you leave the terminal, and keep `lock_after_minutes` short.
- **The node's key is in clear on the server** (the node needs it): keep on it only what the node
  needs, and your savings in a separate account, with its 24 words on paper.
- **Send a small amount first** to a new recipient, and check that it arrived, before the large
  one. Do the same with a new token or contract.
- **Check the whole address** on the review screen, and the token's contract address, not its name.
- **Keep the machine clean.** The wallet protects the key files at rest, not against malware or
  another user of your system account: they can read the terminal or replace the binary. Use a
  machine you control, keep it updated, disable core dumps and use encrypted swap on machines that
  hold funds.
- **Your own node.** The wallet believes the node it talks to for balances, fees and history. Run
  it next to your own node, and keep the node's ports (9090, 9088, 9080) closed to other hosts.
- **Download only from the official releases** and check `SHA256SUMS`.

---

## 9. Troubleshooting

| Message or symptom | What to do |
|---|---|
| `connection refused` on `127.0.0.1:9090` | The node is not running, or its API is on another port (`[api] port`): start it, or set `address` in the profile. |
| `synced: NO` / "sending is refused" | Wait until the node reaches the head of the chain (`status`). |
| "not confirmed by the node" | The node does not tell its network: an official node, an older release, or the diag port off (`diag_port=0`) or on another port (`diag` in the profile). The configuration decides; check that the profile's network is right. |
| "MISMATCH" / the wallet refuses to send | The profile's network and the node's differ: you are on the wrong node or the wrong profile. Fix the profile; never force it. |
| "wrong password" | Retry. A forgotten password: import the account again from its 24 words. |
| "the node's key file is encrypted by the node" | The node was started with `--encryptkey` (or its first start chose a password): type the node's password (on the unlock screen, or when the command asks; `--password-file` in scripts). |
| "the node's key needs 1 GiB of free memory to open" | Opening a key the node encrypted takes 1 GiB, as in the node: free some memory and try again. |
| "could not unlock" | The password is wrong (or the key file damaged). Wait the few seconds shown and try again; for the account `node` of an encrypted key it is the node's password. |
| "is encrypted by the node and NodePublic.txt does not give its address" | `NodePublic.txt` is missing or damaged: the node would not start either. Restore it from a backup or with the node. |
| "This folder holds a node key" | You ran a command in a folder with `NodePrivate.txt`: the wallet does not use it unless you add `--node-key NodePrivate.txt` (or set `node_key`). |
| "the node is running" (`node-key new`, `--format node`) | Stop the node first: the wallet never writes the node's key while it runs. |
| "NodePrivate.txt already exists" | The node has a key: the wallet never writes over it. To replace the node's identity on purpose: `account export NAME --format node --out FOLDER --replace`. |
| The account `node` is missing | The wallet is not the node's wallet: run it with `--config wallet.toml` in the `csnode` folder, or set `node_key` in the profile (or `--node-key FILE`). |
| "the node's key … can be written by other users" / "belongs to another user" | `chmod 600 NodePrivate.txt`, `chmod go-w` on the node's folder; run the wallet as the user who owns the node's folder. |
| Tokens "found from history (may be incomplete on a pruned node)" | The node keeps no token cache. Turn on `tokens_cache=true` in `[api]` of its `config.ini` and restart it, or add the token with `token add`. |
| A token is missing although the cache is on | The cache was turned on after the node pruned, or the node started from a snapshot: it knows every token, but not the wallets whose last use of a token is older than its oldest block. Add the token with `token add` (its balance is read from the contract). The node's first start with the cache on also takes a while (log: `tokens loaded!`). |
| "the executor lost the errors" when compiling | The node's contract executor did not return the error lines; fix the source with an IDE or update the node's executor. |
| "sent, but … check history later" | Do not send again. Check `history` in a minute: the transaction is in a block, or it expired and you can send again. |
| Garbled screen | Use a terminal with UTF-8 and at least 80×24 (Windows Terminal on Windows). |
| Pasting an address says "not a CREDITS address" | What was pasted is not a whole address: copy it again, all of it. If the address you copied is right and the paste differs, something on this computer changed the clipboard: do not send. |
| `y` copies nothing | Your terminal does not support OSC 52 (or tmux needs `set -g set-clipboard on`): hold Shift and drag over the address to select it. |
| macOS: "cannot be opened" | `xattr -d com.apple.quarantine credits-wallet` (or `xattr -dr com.apple.quarantine csnode` for the whole package). |
| Docker: the accounts are gone | The data folder must be on the volume: the image sets `CREDITS_WALLET_HOME=/data/wallet`; run the wallet with `docker exec` in the container that has the volume. |

Problems and questions: [issues](https://github.com/CreditsCommunity/wallet/issues) (never post
your 24 words, a private key or a keys JSON).
