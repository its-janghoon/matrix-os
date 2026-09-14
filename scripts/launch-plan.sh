#!/usr/bin/env bash
#
# Turn the three launch decisions into a reviewed, preflighted config per node.
#
# WHAT THIS IS FOR. docs/runbooks/evm-relaunch.md is correct and long, and most
# of its length is mechanical: the same chain id and the same validator list
# copied into three files by hand, then checked by eye. That is the step where a
# launch quietly breaks - a transposed character in one validator id gives a
# network that comes up holding nothing, several layers from its cause - and it
# is exactly the kind of work a machine should do.
#
# WHAT IT DELIBERATELY DOES NOT DO. It does not freeze the old chain, read the
# genesis snapshot (that needs the old store on the real host), start anything,
# re-bond, or announce. Those are judgement and ceremony, and a script that did
# them would be a script nobody should run. It prints them as a checklist
# instead.
#
# It is safe to run repeatedly: it writes into a fresh output directory and
# touches nothing else.

set -euo pipefail

usage() {
  cat <<'USAGE'
usage: scripts/launch-plan.sh <launch.yaml> [--out DIR] [--skip-chain-check]

  <launch.yaml>        the decisions, in one reviewable file (see --example)
  --out DIR            where to write the configs (default: ./launch-plan)
  --skip-chain-check   do not reach the network to check the chain id
  --example            print a template launch.yaml and exit

Produces one complete config per node, runs the production preflight on each,
and proves the three differ only where they are supposed to.
USAGE
}

example() {
  cat <<'EXAMPLE'
# Every launch decision, in one file two people can review and diff.
chain_id: 61337

# The genesis block printed by:  matrixd -genesis-snapshot -config <OLD config>
# Paste the file path here. For a brand new chain, write the genesis yourself.
genesis_file: ./genesis-block.yaml

# Identical on every node.
round_timeout: 2s
epoch_length: 100
min_bond: 100000000000
bond: 100000000000

nodes:
  # consensus_id and peer_id come from, on each host, AFTER storage.path points
  # at that host's NEW data directory:
  #     matrixd -init-identities -config <that node's new config>
  - name: val-1
    consensus_id: "0000000000000000000000000000000000000000000000000000000000000000"
    peer: "/ip4/203.0.113.10/tcp/9000/p2p/12D3KooWExample1"
    endpoint: "https://val-1.example.com:9093"
    # ABSOLUTE, on that host. A node's identity lives in this store, so a
    # relative path makes it depend on the directory the command ran from.
    storage_path: "/var/lib/matrix/data"
  - name: val-2
    consensus_id: "1111111111111111111111111111111111111111111111111111111111111111"
    peer: "/ip4/203.0.113.11/tcp/9000/p2p/12D3KooWExample2"
    endpoint: "https://val-2.example.com:9093"
  - name: val-3
    consensus_id: "2222222222222222222222222222222222222222222222222222222222222222"
    peer: "/ip4/203.0.113.12/tcp/9000/p2p/12D3KooWExample3"
    endpoint: "https://val-3.example.com:9093"
EXAMPLE
}

INPUT=""
OUT="./launch-plan"
SKIP_CHAIN_CHECK=0

while [ $# -gt 0 ]; do
  case "$1" in
    --example) example; exit 0 ;;
    --out) OUT="$2"; shift 2 ;;
    --skip-chain-check) SKIP_CHAIN_CHECK=1; shift ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "unknown flag: $1" >&2; usage >&2; exit 2 ;;
    *) INPUT="$1"; shift ;;
  esac
done

[ -n "$INPUT" ] || { usage >&2; exit 2; }
[ -f "$INPUT" ] || { echo "no such file: $INPUT" >&2; exit 2; }

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CORE="$ROOT/services/core"

step() { printf '\n== %s\n' "$1"; }
ok()   { printf '   ok  %s\n' "$1"; }
fail() { printf '\nFAILED: %s\n' "$1" >&2; exit 1; }

# ---------------------------------------------------------------- the binary
step "building matrixd"
BIN="$(mktemp -d)/matrixd"
( cd "$CORE" && go build -o "$BIN" ./cmd/matrixd ) || fail "matrixd did not build"
ok "built $($BIN -version 2>/dev/null | head -1 || echo matrixd)"

# ---------------------------------------------------------------- the inputs
step "reading the decisions"
PLAN_JSON="$(python3 "$ROOT/scripts/launch_plan.py" read "$INPUT")" || fail "the launch file is not usable"
CHAIN_ID="$(printf '%s' "$PLAN_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["chain_id"])')"
NODE_COUNT="$(printf '%s' "$PLAN_JSON" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["nodes"]))')"
ok "chain id $CHAIN_ID, $NODE_COUNT validators"

# ------------------------------------------------------------- the chain id
# A chain id goes inside every signature a wallet makes, and changing it later
# invalidates every signature already made for the old value. It is the one
# decision here that cannot be revised, which is why it is checked twice: a
# check that is silently broken looks exactly like a free id.
if [ "$SKIP_CHAIN_CHECK" = "0" ]; then
  step "checking the chain id is unclaimed"
  CODE="$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 \
    "https://raw.githubusercontent.com/ethereum-lists/chains/master/_data/chains/eip155-${CHAIN_ID}.json" || echo "000")"
  case "$CODE" in
    404) ok "not in the registry's source (404)" ;;
    200) fail "chain id $CHAIN_ID is already registered - pick another" ;;
    000) fail "could not reach the registry; re-run, or pass --skip-chain-check and check by hand" ;;
    *)   fail "the registry answered $CODE, which is neither free nor taken - check by hand" ;;
  esac

  NAME="$(curl -s --max-time 20 https://chainid.network/chains_mini.json \
    | python3 -c "import json,sys; print(next((c['name'] for c in json.load(sys.stdin) if c['chainId']==$CHAIN_ID), 'free'))" 2>/dev/null || echo "unreachable")"
  case "$NAME" in
    free) ok "second source agrees it is free" ;;
    unreachable) fail "the second check could not run; a single check is not enough for an irreversible decision" ;;
    *) fail "the second source says chain id $CHAIN_ID is \"$NAME\" - pick another" ;;
  esac
else
  step "chain id check skipped"
  printf '   !!  %s\n' "you are choosing $CHAIN_ID without checking it. It goes inside every signature and cannot be changed later."
fi

# ---------------------------------------------------------------- the configs
step "generating one config per node"
rm -rf "$OUT"
mkdir -p "$OUT"

NODE_NAMES="$(printf '%s' "$PLAN_JSON" | python3 -c 'import json,sys; print("\n".join(n["name"] for n in json.load(sys.stdin)["nodes"]))')"
while IFS= read -r NAME; do
  [ -n "$NAME" ] || continue
  NODE_DIR="$OUT/$NAME"
  mkdir -p "$NODE_DIR"
  # -init generates this node's own secrets (admin key, ACLs on, safe defaults).
  # They are generated PER NODE and never copied between them, which is why the
  # merge below adds launch values to a generated file rather than writing one.
  ( cd "$NODE_DIR" && "$BIN" -init -config "config.yaml" >/dev/null 2>&1 ) \
    || fail "matrixd -init failed for $NAME"
  python3 "$ROOT/scripts/launch_plan.py" merge "$INPUT" "$NAME" "$NODE_DIR/config.yaml" \
    || fail "could not merge the launch values into $NAME's config"
  ok "$NAME -> $NODE_DIR/config.yaml"
done <<< "$NODE_NAMES"

# ---------------------------------------------------------------- preflight
# The same validation the launch itself runs, before anything starts.
step "running the production preflight on every config"
while IFS= read -r NAME; do
  [ -n "$NAME" ] || continue
  if OUTPUT="$("$BIN" -preflight-production -config "$OUT/$NAME/config.yaml" 2>&1)"; then
    ok "$NAME passes"
  else
    printf '%s\n' "$OUTPUT" >&2
    fail "$NAME does not pass the production preflight"
  fi
done <<< "$NODE_NAMES"

# ---------------------------------------------------------------- the diff
# The runbook asks for a diff between the files showing only the node's own
# identity differing. Doing it by eye across three files is the step that misses
# a transposed character, so it is asserted instead.
step "checking the configs differ only where they should"
if python3 "$ROOT/scripts/launch_plan.py" diff "$OUT" ; then
  ok "chain id, validator set and consensus timing are identical on every node"
else
  fail "the configs disagree on something consensus-critical"
fi

# ---------------------------------------------------------------- what's left
step "what is still yours to do"
cat <<CHECKLIST
   These are judgement and ceremony, not typing, so this script does not do them.

   BEFORE you copy anything out of $OUT:
   1. Freeze the old chain and announce the window. Nothing is safe while
      balances are still moving.
   2. Run 'matrix bridge reconcile' and record its output with the Base block
      totalSupply() was read at.
   3. Confirm the genesis block in your launch file came from
      'matrixd -genesis-snapshot' against the OLD store, and that its supply
      line closes exactly at the cap.
   4. Two people review $OUT/*/config.yaml.

   THEN:
   5. Copy each $OUT/<name>/config.yaml to that host. Each carries its own
      generated admin key - do not copy one node's config to another host.
   6. Point the bridge watcher's start block at the CURRENT Base head, not the
      contract's deployment block. This is where money leaks if it is going to.
   7. Start the validators. Confirm they commit past the first epoch boundary.
   8. Re-bond. Bonds do not carry across a relaunch.
   9. Verify the escrow against the contract before announcing anything.
  10. Point a wallet at it and send yourself a transfer.

   'make devnet' does step 10 against three throwaway nodes. Run it first.
CHECKLIST

printf '\nPLAN READY - %s validators, chain %s, every config preflighted.\n' "$NODE_COUNT" "$CHAIN_ID"
