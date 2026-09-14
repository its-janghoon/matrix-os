#!/usr/bin/env bash
#
# Stand up a seller the network vouches for, on a GPU you already own.
#
# WHY THIS EXISTS. A new marketplace is mostly strangers: no settled history to
# tell them apart, and a stake proves capital rather than competence. So the
# people running the network run sellers themselves and say so - and the badge
# that says so is only worth anything because it is a maintainer signature a
# reader checks against their OWN chain.
#
# Producing one by hand is five steps in a specific order, and the order is not
# obvious: the attestation names the node id, the node id lives in the store,
# and the store only exists after the config points at it. Getting it wrong
# produces a seller that lists fine and is quietly unbadged, which looks exactly
# like a working setup.
#
# WHAT IT DOES NOT DO. It does not install or start vLLM, and it does not start
# the node. Those belong to whatever runs services on that host. It prints them.

set -euo pipefail

usage() {
  cat <<'USAGE'
usage: scripts/first-party-provider.sh <provider.yaml> [--out DIR]

  <provider.yaml>   the GPU box's details (see --example)
  --out DIR         where to write the config (default: ./first-party-provider)
  --example         print a template provider.yaml and exit

Writes a complete provider config, issues the maintainer attestation bound to
that node and that payout account, and checks the badge verifies.
USAGE
}

example() {
  cat <<'EXAMPLE'
# A GPU the company already owns, selling on a network that is already running.

# A config from ANY node already on this network. Everything consensus-critical -
# chain id, the validator list, the genesis block, the maintainer account every
# reader checks a badge against - is copied from it.
#
# It is a path rather than six fields to retype because retyping them is how a
# provider ends up running a private chain of one: it comes up, registers its
# backend, announces to nobody, and verifies its own badge against a maintainer
# only it has ever named. Nothing errors.
network_config: "/path/to/an/existing/node/config.yaml"

# One entry per node already on the network, each with that node's own peer id.
bootstrap_peers:
  - "/ip4/203.0.113.10/tcp/9000/p2p/12D3KooWExample1"

provider:
  # The name a reader sees on the badge. It is signed, so it is not a label the
  # seller chose for itself - but it is an IDENTITY claim, not a rating.
  operator: "Matrix OS"

  # The provider id AND the account revenue lands in - one field, not two. Use
  # an address you hold in a wallet, so the node never holds your key.
  payout_account: "eth:0xyour-wallet-address-lowercase"

  # The address BUYERS dial. A node cannot see its own public address.
  endpoint: "https://gpu-1.example.com:9093"

  # Must match the model server's --served-model-name.
  models:
    - "llama-3.3-70b"

  # Loopback. The model server has no auth worth exposing; this node is what
  # authenticates, meters and charges.
  vllm_url: "http://127.0.0.1:8000"

  price_per_unit: 5
  capacity: 1000000

  # ABSOLUTE. The node's identity lives in this store, so a relative path makes
  # that identity depend on the directory the command ran from.
  storage_path: "/var/lib/matrix/gpu-1"

# The MAINTAINER's wallet - the account this chain names as maintainer. Signing
# with any other key produces an attestation that verifies nowhere.
maintainer_wallet: "~/.matrix/wallet.json"

# The protocol refuses more than 30 days. Re-signing is a scheduled chore, on
# purpose: a box gets decommissioned, and a permanent badge on a machine
# somebody else now owns is not recoverable.
attestation_valid_for: "336h"
EXAMPLE
}

INPUT=""
OUT="./first-party-provider"
while [ $# -gt 0 ]; do
  case "$1" in
    --example) example; exit 0 ;;
    --out) OUT="$2"; shift 2 ;;
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

step "building the binaries"
BIN_DIR="$(mktemp -d)"
( cd "$CORE" && go build -o "$BIN_DIR/matrixd" ./cmd/matrixd && go build -o "$BIN_DIR/matrix" ./cmd/matrix ) \
  || fail "the binaries did not build"
ok "matrixd and matrix"

step "reading the provider details"
PLAN="$(python3 "$ROOT/scripts/first_party_provider.py" read "$INPUT")" || fail "the provider file is not usable"
field() { printf '%s' "$PLAN" | python3 -c "import json,sys; print(json.load(sys.stdin)$1)"; }
CHAIN_ID="$(field '["chain_id"]')"
MAINTAINER="$(field '["maintainer_account"]')"
OPERATOR="$(field '["provider"]["operator"]')"
PAYOUT="$(field '["provider"]["payout_account"]')"
WALLET="$(field '["maintainer_wallet"]')"
VALID_FOR="$(field '["attestation_valid_for"]')"
ok "$OPERATOR selling as $PAYOUT"
ok "joining chain $CHAIN_ID, whose maintainer is $MAINTAINER"

# The order below is the whole reason this script exists.
#
# An attestation names the NODE it is for, so it cannot be lifted onto another
# listing. A node's id lives in its store. The store is created from the config.
# So: config first, then identity, then attestation, then the config is amended
# to point at it. Any other order produces a file that is well-formed and
# verifies nowhere.

step "generating the node config"
rm -rf "$OUT"; mkdir -p "$OUT"
( cd "$OUT" && "$BIN_DIR/matrixd" -init -config config.yaml >/dev/null 2>&1 ) || fail "matrixd -init failed"
python3 "$ROOT/scripts/first_party_provider.py" merge "$INPUT" "$OUT/config.yaml" || fail "could not write the provider config"
ok "$OUT/config.yaml"

step "reading this node's identity"
IDENT="$("$BIN_DIR/matrixd" -init-identities -config "$OUT/config.yaml" 2>/dev/null)" || fail "could not read the node identity"
NODE_ID="$(printf '%s' "$IDENT" | python3 -c 'import json,sys; print(json.load(sys.stdin)["consensus_id"])')"
PEER_ID="$(printf '%s' "$IDENT" | python3 -c 'import json,sys; print(json.load(sys.stdin)["peer_id"])')"
[ -n "$NODE_ID" ] || fail "the node reported no consensus id"
ok "node $NODE_ID"
ok "peer $PEER_ID"

step "signing the maintainer's attestation"
WALLET_PATH="${WALLET/#\~/$HOME}"
[ -f "$WALLET_PATH" ] || fail "no maintainer wallet at $WALLET_PATH - an attestation signed by any other key verifies nowhere"
"$BIN_DIR/matrix" attest issue \
  --node "$NODE_ID" \
  --provider "$PAYOUT" \
  --operator "$OPERATOR" \
  --valid-for "$VALID_FOR" \
  --wallet "$WALLET_PATH" \
  --out "$OUT/attestation.json" || fail "attest issue refused"
ok "$OUT/attestation.json"

step "checking the badge is bound to THIS node and THIS seller"
SHOWN="$("$BIN_DIR/matrix" attest show --file "$OUT/attestation.json" 2>&1)" || fail "the attestation does not read back"
printf '%s' "$SHOWN" | grep -q "$NODE_ID" || fail "the attestation does not name this node - it would be refused on arrival"
printf '%s' "$SHOWN" | grep -q "$PAYOUT"  || fail "the attestation does not name this payout account"
ok "bound to node and seller, so it cannot be lifted onto another listing"

step "pointing the seller at it"
python3 "$ROOT/scripts/first_party_provider.py" attach "$OUT/config.yaml" "$OUT/attestation.json" \
  || fail "could not attach the attestation to the backend"
ok "inference.backends[0].attestation set"

step "what is still yours to do"
cat <<CHECKLIST
   1. Put this on the GPU host:
        $OUT/config.yaml
        $OUT/attestation.json   (the config references it by path - keep them together,
                                 or edit inference.backends[].attestation to the real path)

   2. Start the model server first, and check it answers:
        vllm serve <model> --served-model-name <name> --api-key \$MATRIX_VLLM_API_KEY
        curl -s \$VLLM_URL/health

   3. Put MATRIX_VLLM_API_KEY in the node PROCESS environment
      (systemd EnvironmentFile=, root-owned 0600). An empty variable fails
      startup, which is better than advertising capacity that cannot be served.

   4. Start matrixd. It prints its multiaddrs; publish
        /ip4/<this host's public IP>/tcp/9000/p2p/$PEER_ID
      On a cloud instance it will report that none of its bound addresses is
      reachable from another host. That is expected: the public IP belongs to
      the NAT, not to an interface on the machine.

   5. Terminate TLS in front of connect (9093) and eth_rpc (9095). An API key
      travels in an Authorization header. Never open 9090.

   6. Confirm the badge from ANOTHER node, which is the only check that counts -
      a node vouching for itself proves nothing:
        matrix provider directory --addr <another node's market addr, default port 9091>
      Your seller should show VOUCHED BY $OPERATOR. If it does not, the
      attestation verified against a maintainer that chain does not name.

   7. Diary the expiry. This one lapses after $VALID_FOR and the badge simply
      disappears - the seller keeps working, unbadged.
CHECKLIST

printf '\nPROVIDER READY - %s on chain %s, vouched for by %s.\n' "$PAYOUT" "$CHAIN_ID" "$OPERATOR"
