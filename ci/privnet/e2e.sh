#!/usr/bin/env bash
# The wallet end to end on a private network of the community node, on this machine.
#
# Starts NODES (default 5) nodes of a CREDITS_TESTNET build of the community node, all of them initial
# confidants, each with its contract executor (CONTRACTS=1), the genesis funds going to a master key
# made here; then runs the wallet's commands against node 1's API: status, account import and new,
# balance, send (with user data), delegate (with and without a time limit), delegations, undelegate,
# history (from node 1, and from node 2 as a remote history node), and with CONTRACTS=1 deploys the
# node's samples/contracts/TestToken.java with the node's Python sample client, then sends tokens
# with the wallet and checks token balances, the token history and the CSV export.
#
# The network part is a minimal subset of CreditsCommunity/node ci/testnet/run.sh (AGPL-3.0): the
# same node configuration (hosts.txt, trusted.txt, config.ini, executor.sh, settings.properties),
# ports (p2p 6000 + i, API 9090 for node 1 and 9100 + i for the others, apiexec 9200 + i, executor
# 9300 + i) and genesis/starter key hooks (CS_TESTNET_GENESIS_KEY, CS_TESTNET_STARTER_KEY, read only by
# -DCREDITS_TESTNET=ON builds).
#
# Env: NODE_BIN    the node binary (a -DCREDITS_TESTNET=ON build)
#      PKG         the csnode folder of the community release package (contract-executor.jar, its JDK)
#      NODE_FILES  folder with credits_client.py and TestToken.java of the node repository (CONTRACTS=1)
#      IDL         the Thrift IDL folder (this repository's submodule)
#      WALLET      the credits-wallet binary
#      WORK        work folder (default ./privnet-run), NODES (5), CONTRACTS (1)
# python3 must have PyNaCl, base58 and thriftpy2 (testdata/requirements.txt).
set -euo pipefail

: "${NODE_BIN:?}" "${WALLET:?}" "${IDL:?}"
NODES=${NODES:-5}
CONTRACTS=${CONTRACTS:-1}
WORK=$(mkdir -p "${WORK:-privnet-run}" && cd "${WORK:-privnet-run}" && pwd)
if [ "$CONTRACTS" = 1 ]; then : "${PKG:?}" "${NODE_FILES:?}"; fi
START=$(date +%s)
PIDS=()

log() { echo "[$(( $(date +%s) - START ))s] $*"; }
fail() { echo "FAIL: $*"; exit 1; }

stop_all() {
    local rc=$?
    for pid in "${PIDS[@]}"; do kill "$pid" 2>/dev/null || true; done
    for _ in $(seq 1 10); do
        alive=0
        for pid in "${PIDS[@]}"; do kill -0 "$pid" 2>/dev/null && alive=1; done
        [ "$alive" = 1 ] || break
        sleep 1
    done
    for pid in "${PIDS[@]}"; do kill -9 "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; done
    pkill -f "[D]credits.wallet.e2e=$WORK/" 2>/dev/null || true
    if [ "$rc" -ne 0 ]; then
        for i in $(seq 1 "$NODES"); do
            echo "---- n$i node.log (tail)"; tail -n 25 "$WORK/n$i/node.log" 2>/dev/null || true
            [ "$CONTRACTS" != 1 ] || { echo "---- n$i executor.log (tail)"; tail -n 15 "$WORK/n$i/executor.log" 2>/dev/null || true; }
        done
    fi
    log "done, exit $rc"
}
trap stop_all EXIT

# ---- the network ---------------------------------------------------------------------------------
rm -rf "${WORK:?}"/n* "$WORK/wallet"
mapfile -t KEYS < <(python3 -c '
import os, sys, base58, nacl.signing
for i in range(1, int(sys.argv[2]) + 1):
    d = os.path.join(sys.argv[1], f"n{i}"); os.makedirs(d, exist_ok=True)
    k = nacl.signing.SigningKey.generate(); pub = bytes(k.verify_key)
    open(os.path.join(d, "NodePublic.txt"), "w").write(base58.b58encode(pub).decode())
    open(os.path.join(d, "NodePrivate.txt"), "w").write(base58.b58encode(bytes(k) + pub).decode())
    print(base58.b58encode(pub).decode())' "$WORK" "$NODES")
read -r MASTER_SEED MASTER_PUB < <(python3 -c 'import base58, nacl.signing; k = nacl.signing.SigningKey.generate(); print(base58.b58encode(bytes(k)).decode(), base58.b58encode(bytes(k.verify_key)).decode())')
export CS_TESTNET_GENESIS_KEY=$MASTER_PUB CS_TESTNET_STARTER_KEY=$MASTER_PUB
log "master key $MASTER_PUB, nodes: ${KEYS[*]}"

api_port() { if [ "$1" -eq 1 ]; then echo 9090; else echo $((9100 + $1)); fi; }

if [ "$CONTRACTS" = 1 ]; then
    JAR=$PKG/contract-executor.jar
    JDK=$(find "$PKG" -maxdepth 2 -type d -name 'java-11-openjdk-*' | head -1)
    [ -f "$JAR" ] && [ -x "$JDK/bin/java" ] || fail "no executor or JDK in $PKG"
fi

for i in $(seq 1 "$NODES"); do
    dir=$WORK/n$i
    : > "$dir/hosts.txt"
    : > "$dir/trusted.txt"
    for j in $(seq 1 "$NODES"); do
        echo "${KEYS[$((j - 1))]}" >> "$dir/trusted.txt"
        [ "$j" -eq "$i" ] || echo "127.0.0.1:$((6000 + j)) ${KEYS[$((j - 1))]}" >> "$dir/hosts.txt"
    done
    api="port=$(api_port "$i")
apiexec_port=0"
    if [ "$CONTRACTS" = 1 ]; then
        api="port=$(api_port "$i")
apiexec_port=$((9200 + i))
executor_port=$((9300 + i))
executor_command=$dir/executor.sh
executor_multi_instance=true
tokens_cache=true
executor_send_timeout=60000
executor_receive_timeout=60000
executor_check_version_delay=60000"
        cat > "$dir/settings.properties" <<PROPS
node.api.host=127.0.0.1
node.api.port=$(api_port "$i")
contract.executor.port=$((9300 + i))
contract.executor.node.api.port=$((9200 + i))
contract.executor.node.api.host=127.0.0.1
contract.executor.read.client.timeout=0
jdk.path=$JDK
PROPS
        printf '#!/bin/sh\ncd "$(dirname "$0")"\nexec "%s" -Xmx256m -XX:MaxMetaspaceSize=192m -Dcredits.wallet.e2e=%s -jar "%s" >> executor.log 2>&1\n' \
            "$JDK/bin/java" "$dir/" "$JAR" > "$dir/executor.sh"
        chmod +x "$dir/executor.sh"
    fi
    cat > "$dir/config.ini" <<CFG
[params]
hosts_filename=hosts.txt
init_trusted_filename=trusted.txt
ipv6=false
traverse_nat=false
min_neighbours=1
max_neighbours=16
round_elapse_time=20000

[host_input]
ip=127.0.0.1
port=$((6000 + i))

[api]
$api
ajax_port=0
diag_port=0

[snapshot]
fetch=off

[Core]
Filter="%Severity% >= info"

[Sinks.file]
Destination=TextFile
FileName=node.log
AutoFlush=true
Append=true
Format="[%TimeStamp%] %Severity% %Message%"
CFG
done

for i in $(seq 1 "$NODES"); do
    (cd "$WORK/n$i" && exec "$NODE_BIN" --db-path db --config-file config.ini </dev/null >> stdout.log 2>&1) &
    PIDS+=($!)
done
log "$NODES nodes started"

# ---- the wallet ----------------------------------------------------------------------------------
export CREDITS_WALLET_HOME=$WORK/wallet
mkdir -p "$CREDITS_WALLET_HOME" && chmod 700 "$CREDITS_WALLET_HOME"
cat > "$CREDITS_WALLET_HOME/config.toml" <<CFG
node = "n1"
[nodes.n1]
address = "127.0.0.1:9090"
network = "private"
[nodes.n2]
address = "127.0.0.1:9102"
network = "private"
CFG
PW=$WORK/password
echo "e2e password" > "$PW" && chmod 600 "$PW"
w() { "$WALLET" --password-file "$PW" "$@"; }
# expect TEXT CMD...: the command succeeds and prints TEXT
expect() {
    local want=$1 out
    shift
    out=$(w "$@" 2>&1) || { echo "$out"; fail "credits-wallet $*"; }
    echo "$out" | sed 's/^/    /'
    grep -qF -- "$want" <<< "$out" || fail "credits-wallet $*: no '$want'"
}
refused() {
    local out
    if out=$(w "$@" 2>&1); then echo "$out"; fail "credits-wallet $* was not refused"; fi
    echo "    refused: $(tail -1 <<< "$out")"
}
json() { w "$@" --json; }

# the network is up once node 1 is synced with a few blocks
for _ in $(seq 1 120); do
    st=$(json status 2>/dev/null || true)
    if [ "$(jq -r '.synced // false' <<< "$st" 2>/dev/null)" = true ] && [ "$(jq -r '.last_block' <<< "$st")" -ge 5 ]; then break; fi
    sleep 2
done
[ "$(jq -r '.synced // false' <<< "${st:-{\}}")" = true ] || fail "node 1 never got synced: ${st:-no answer}"
log "network up: block $(jq -r .last_block <<< "$st")"
expect "network      : PRIVATE" status

echo "$MASTER_SEED" > "$WORK/master.txt"
python3 -c 'import base58, nacl.signing, sys; s = base58.b58decode(open(sys.argv[1]).read().strip()); k = nacl.signing.SigningKey(s); print(base58.b58encode(s + bytes(k.verify_key)).decode())' \
    "$WORK/master.txt" > "$WORK/master-key.txt"
chmod 600 "$WORK/master.txt" "$WORK/master-key.txt"
expect "$MASTER_PUB" account import master "$WORK/master.txt"
ALICE=$(json account new alice --words-file "$WORK/alice-words" | jq -r .address)
BOB=$(json account new bob --show-words | jq -r .address)
N3=${KEYS[2]}
N4=${KEYS[3]}
log "alice $ALICE, bob $BOB"
expect "249,471,071 CS" balance master

# sends, with the in-block confirmation of the node
expect "sent 100,000 CS to $ALICE" send master "$ALICE" 100000 --yes
expect "sent 100 CS to $BOB" send master "$BOB" 100 --yes
expect "sent 12.5 CS to $BOB" send alice "$BOB" 12.5 --data "e2e invoice 42 — paid" --yes
refused send bob "$ALICE" 1000 --yes
expect "112.5 CS" balance bob

# delegations
expect "delegated 1,000 CS to $N3 without a time limit" delegate alice "$N3" 1000 --yes
UNTIL=$(date -u -d '+2 hours' '+%Y-%m-%d %H:%M')
expect "delegated 500 CS to $N4 until" delegate alice "$N4" 500 --until "$UNTIL" --yes
refused delegate alice "$N3" 4 --yes
d=$(json delegations alice)
echo "$d" | jq -c .
[ "$(jq -r .delegated_out_cs <<< "$d")" = 1500 ] && [ "$(jq '.made | length' <<< "$d")" = 2 ] || fail "delegations of alice"
[ "$(jq -r '[.made[] | select(.until != null)] | length' <<< "$d")" = 1 ] || fail "the timed delegation has no expiry"
expect "delegations received: 1,000 CS in 1 delegation" delegations "$N3"
refused undelegate alice "$N4" 500 --yes   # a delegation with a time limit ends by itself
refused undelegate alice "$N3" 1001 --yes
expect "withdrew 600 CS from the delegation to $N3" undelegate alice "$N3" 600 --yes
[ "$(json delegations alice | jq -r .delegated_out_cs)" = 900 ] || fail "after the withdrawal"

# history: node 1, then node 2 as a remote history node
expect "data \"e2e invoice 42 — paid\"" history alice
expect "delegation withdrawn" history alice
local_total=$(json history alice | jq .total)
remote=$(json history alice --history-node 127.0.0.1:9102)
[ "$(jq -r .remote <<< "$remote")" = true ] && [ "$(jq .total <<< "$remote")" = "$local_total" ] || fail "remote history: $remote"
expect "on remote full node 127.0.0.1:9102 (read-only)" history alice --history-node 127.0.0.1:9102
w history alice --csv "$WORK/alice.csv"
grep -q ",delegation,out,$ALICE,$N3," "$WORK/alice.csv" || fail "CSV without the delegation"
log "CS, delegations and history: ok"

if [ "$CONTRACTS" = 1 ]; then
    # TestToken, deployed by the master key with the node's Python sample client
    out=$(python3 "$NODE_FILES/credits_client.py" --node 127.0.0.1:9090 --idl "$IDL" deploy "$WORK/master-key.txt" "$NODE_FILES/TestToken.java" 2>&1) \
        || { echo "$out"; fail "TestToken deploy"; }
    echo "$out" | sed 's/^/    /'
    TOKEN=$(sed -n 's/^contract: //p' <<< "$out")
    [ -n "$TOKEN" ] || fail "no contract address"
    log "TestToken at $TOKEN"
    expect "decimals  : 2" token info "$TOKEN"
    refused token send master "$TOKEN" "$ALICE" 0.001 --yes
    expect "sent 250.5 CTT to $ALICE (transfer returned true)" token send master "$TOKEN" "$ALICE" 250.50 --yes
    expect "sent 0.5 CTT to $BOB" token send alice "$TOKEN" "$BOB" 0.5 --yes
    expect "250.00 CTT" token balance "$TOKEN" alice
    # the token cache follows the blocks: give it a moment
    for _ in $(seq 1 30); do json tokens alice | jq -e '.tokens[] | select(.code == "CTT")' >/dev/null && break; sleep 2; done
    expect "CTT" tokens alice
    expect "-0.5 CTT" history alice --tokens
    expect "+250.5 CTT" history alice --tokens --history-node 127.0.0.1:9102
    w history alice --csv "$WORK/alice-tokens.csv"
    grep -q ",$TOKEN,CTT,250.5," "$WORK/alice-tokens.csv" || { cat "$WORK/alice-tokens.csv"; fail "CSV without the token transfers"; }
    log "tokens: ok"
fi
log "PASS"
