#!/usr/bin/env bash
# Prove the ESCROWED inference path works on a live network, end to end, after
# protocol version 3 has activated.
#
#   MATRIX_ROLLOUT_BOXES="label|host|keyfile|role"   (same format as rollout.sh)
#   ./scripts/escrow-smoke.sh [prompt] [units]
#
# WHAT IT PROVES, AND WHY IT IS NOT A UNIT TEST. A budget could be checked by
# reading a balance off every node. An escrow is a SEQUENCE - reserve, fund,
# stream, settle - and the thing that can be wrong is the money at the end of it.
# So this buys a real answer from the real seller and then asks the one question
# a unit test cannot: what did the wallet actually pay?
#
# THE NUMBER THAT MATTERS IS THE COST, NOT THE EXIT CODE. Every command can
# succeed and the test still fail: if the settlement did not apply, the provider
# is holding the whole reservation and the buyer paid the cap for a sentence.
# That is exactly the failure this path exists to prevent, and from the outside
# it looks like success. So the cost is compared against the reservation, and a
# cost at or near the reservation is reported as a FAILURE.
#
# It spends real money - a few units - which is the point: an escrow that is
# never settled cannot be told apart from one that is, except by the balance.

set -uo pipefail

PROMPT=${1:-"Answer in one short sentence: what is a marketplace for?"}
UNITS=${2:-200}
: "${MATRIX_ROLLOUT_BOXES:?set MATRIX_ROLLOUT_BOXES to lines of label|host|keyfile|role}"

mapfile -t BOXES <<< "$(echo "$MATRIX_ROLLOUT_BOXES" | sed "s/^[[:space:]]*//;s/[[:space:]]*$//" | grep -v "^$")"

say()  { printf "\n== %s\n" "$*"; }
info() { printf "   %s\n" "$*"; }
die()  { printf "\nABORT: %s\n" "$*" >&2; exit 1; }
field() { echo "$1" | cut -d"|" -f"$2"; }

# keypath expands a leading ~.
#
# `ssh -i "$key"` does NOT expand a tilde - it is inside quotes, and ssh does no
# expansion of its own - so a box list written with ~/Downloads/k.pem reaches ssh
# as those literal characters and fails with "no such identity file", which reads
# as a missing key rather than as a path the shell never resolved. Everyone
# writes ~ and it is not their mistake to make.
keypath() { printf %s "${1/#\~/$HOME}"; }

# checkKeys reads every key in the box list before the first ssh.
#
# Up front rather than at the first use: the alternative is finding out on box
# three, which on a rollout is after two nodes have already been restarted.
checkKeys() {
  local b k
  for b in "${BOXES[@]}"; do
    k=$(keypath "$(field "$b" 3)")
    [ -f "$k" ] || die "no key file at $k, for $(field "$b" 1). Write the path as \$HOME/... or in full - a ~ inside the quoted box list is never expanded by the shell, and ssh does not expand one either."
    [ -r "$k" ] || die "cannot read the key file at $k, for $(field "$b" 1)."
  done
}

rsh() {
  local host=$1 key script=$3 arg=${4:-} arg2=${5:-} arg3=${6:-}
  key=$(keypath "$2")
  ssh -i "$key" -o StrictHostKeyChecking=accept-new -o ConnectTimeout=15 \
      -o BatchMode=yes "ubuntu@$host" bash -s -- "$arg" "$arg2" "$arg3" <<< "$script"
}

# The passphrase travels as the FIRST LINE of the script that goes over ssh's
# stdin. Not as an argument: `bash -s -- "$pass"` puts it in argv, where `ps` on
# that box shows it to anyone logged in.
PASS_PREFIX=""
if [ -n "${MATRIX_WALLET_PASSPHRASE:-}" ]; then
  PASS_PREFIX="export MATRIX_WALLET_PASSPHRASE=$(printf %q "$MATRIX_WALLET_PASSPHRASE")
"
fi

read -r -d "" R_COMMON <<'EOS'
P=$(pgrep -x matrixd || true)
[ -z "$P" ] && { echo "ERR matrixd not running"; exit 1; }
CMD=$(sudo tr "\0" "\n" < "/proc/$P/cmdline")
CFG=$(echo "$CMD" | grep -A1 -x -- -config | tail -1)
[ -z "$CFG" ] && CFG=$(echo "$CMD" | sed -n "s/^-config=//p")
[ -z "$CFG" ] && { echo "ERR no -config on the command line"; exit 1; }
M=$(sudo awk '/^market:/{f=1;next} f&&/^[^[:space:]#]/{f=0} f&&/addr:/{print $2; exit}' "$CFG" | tr -d "\"" | sed "s/.*://")
M=${M:-9091}
I=$(sudo awk '/^inference:/{f=1;next} f&&/^[^[:space:]#]/{f=0} f&&/addr:/{print $2; exit}' "$CFG" | tr -d "\"" | sed "s/.*://")
I=${I:-9092}
export MATRIX_ADMIN_API_KEY=$(sudo sed -n "s/^[[:space:]-]*key:[[:space:]]*//p" "$CFG" | head -1 | tr -d "\"")
PASS_FROM="supplied"
if [ -z "${MATRIX_WALLET_PASSPHRASE:-}" ]; then
  PASS_FROM="none"
  for envfile in /etc/matrix/matrixd.env "$(dirname "$CFG")/matrixd.env"; do
    if sudo test -r "$envfile"; then
      V=$(sudo sed -n "s/^[[:space:]]*\(export[[:space:]]\+\)\?MATRIX_WALLET_PASSPHRASE=//p" "$envfile" | head -1)
      V=${V%\"}; V=${V#\"}; V=${V%\'}; V=${V#\'}
      if [ -n "$V" ]; then export MATRIX_WALLET_PASSPHRASE="$V"; PASS_FROM="$envfile"; break; fi
    fi
  done
fi
EOS

# Heredocs rather than quoted concatenation: these payloads contain single quotes
# of their own (awk programs, JSON bodies), and '...' would have ended the string
# at the first one. `bash -n` does not catch it - the result still parses.
read -r -d "" R_CHAIN_BODY <<'EOS'
R=$(sudo awk '/^eth_rpc:/{f=1;next} f&&/^[^[:space:]#]/{f=0} f&&/addr:/{print $2; exit}' "$CFG" | tr -d '"' | sed "s/.*://")
# The LAST scheduled height, not the first: this chain has already run one
# upgrade, and reading the version 2 entry would report escrow as long active.
SCHED=$(sudo sed -n "s/^[[:space:]-]*height:[[:space:]]*//p" "$CFG" | tail -1)
VER=$(sudo sed -n "s/^[[:space:]-]*version:[[:space:]]*//p" "$CFG" | tail -1)
if [ -z "$R" ]; then echo "CHAIN|0|none|${SCHED:-none}|${VER:-none}"; exit 0; fi
J=$(curl -s --max-time 6 -X POST "http://127.0.0.1:$R" -H "content-type: application/json" \
      -d '{"jsonrpc":"2.0","id":1,"method":"matrix_getChainInfo","params":[]}')
field_of() { echo "$J" | tr "," "\n" | sed -n "s/.*\"$1\":\"\?\([^\",]*\).*/\1/p" | head -1; }
echo "CHAIN|$(field_of height)|$(field_of state_root)|${SCHED:-none}|${VER:-none}"
EOS
R_CHAIN="$R_COMMON
$R_CHAIN_BODY"

read -r -d "" R_BLOCK_BODY <<'EOS'
# One NAMED height asked of every node. Two validators read a second apart are
# legitimately on different heads, and comparing those cries wolf about the one
# condition that must never be reported falsely.
R=$(sudo awk '/^eth_rpc:/{f=1;next} f&&/^[^[:space:]#]/{f=0} f&&/addr:/{print $2; exit}' "$CFG" | tr -d '"' | sed "s/.*://")
[ -z "$R" ] && { echo "BLOCK|none"; exit 0; }
HEX=$(printf "0x%x" "$1")
J=$(curl -s --max-time 6 -X POST "http://127.0.0.1:$R" -H "content-type: application/json" \
      -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"eth_getBlockByNumber\",\"params\":[\"$HEX\",false]}")
HASH=$(echo "$J" | tr "," "\n" | sed -n 's/^"hash":"\([^"]*\)".*/\1/p' | head -1)
echo "BLOCK|${HASH:-none}"
EOS
R_BLOCK="$R_COMMON
$R_BLOCK_BODY"

R_WALLET="$PASS_PREFIX$R_COMMON"'
if [ ! -f "$HOME/.matrix/wallet.json" ]; then echo "NOWALLET"; exit 0; fi
A=$(matrix wallet show 2>/dev/null | sed -n "s/^account:[[:space:]]*//p" | head -1)
B=$(matrix wallet balance --addr "127.0.0.1:$M" 2>/dev/null | awk "{print \$NF}" | head -1)
echo "WALLET|${A:-unknown}|${B:-unknown}|$PASS_FROM|${#MATRIX_WALLET_PASSPHRASE}"
'

# The order book, read from a node rather than assumed. Inference is served by the
# node that OWNS the provider, so the seller's own endpoint is what a buyer has
# to reach - a reserve sent anywhere else is refused by a node that has never
# heard of the job.
R_SELLERS="$R_COMMON"'
matrix provider list --addr "127.0.0.1:$M" --json 2>&1 | tr -d " \n"
echo
'

R_BUY="$PASS_PREFIX$R_COMMON"'
OUT=$(matrix inference submit --escrowed --inference-addr "127.0.0.1:$I" \
        --buyer "$1" --provider "$2" --prompt "$3" --units "'"$UNITS"'" 2>&1)
RC=$?
echo "OUTPUT_BEGIN"
echo "$OUT"
echo "OUTPUT_END"
echo "RC|$RC"
'

# ---------------------------------------------------------------- run

checkKeys

say "0. Protocol version 3 must have activated, and the nodes must agree"
LO=0; HI=0; SCHEDULED=""; SCHEDVER=""; VLABELS=(); VHOSTS=(); VKEYS=()
for b in "${BOXES[@]}"; do
  label=$(field "$b" 1); host=$(field "$b" 2); key=$(field "$b" 3); role=$(field "$b" 4)
  out=$(rsh "$host" "$key" "$R_CHAIN" 2>&1 | tail -1)
  case "$out" in CHAIN\|*) ;; *) die "$label: $out";; esac
  h=$(field "$out" 2); sr=$(field "$out" 3); sc=$(field "$out" 4); ve=$(field "$out" 5)
  info "$(printf '%-12s height=%-7s root=%s last schedule=%s version=%s' "$label" "${h:-n/a}" "${sr:0:14}" "$sc" "$ve")"
  [ -z "$SCHEDULED" ] && [ "$sc" != none ] && { SCHEDULED=$sc; SCHEDVER=$ve; }
  [ "$sc" = "$SCHEDULED" ] || die "$label's last schedule is $sc but another box says $SCHEDULED. Every node must carry the same heights or they disagree about block validity."
  [ "$role" = validator ] || continue
  VLABELS+=("$label"); VHOSTS+=("$host"); VKEYS+=("$key")
  [ "$LO" -eq 0 ] && LO=$h
  [ "$h" -lt "$LO" ] && LO=$h
  [ "$h" -gt "$HI" ] && HI=$h
done
[ -n "$SCHEDULED" ] || die "no box carries a protocol_upgrades height"
[ "$SCHEDVER" = 3 ] || die "the last scheduled version is $SCHEDVER, not 3. Escrow is version 3 - run ./scripts/rollout.sh <tag> 3 first."
[ "$LO" -gt 0 ] || die "no validator reported a height"

SPREAD=$((HI - LO))
[ "$SPREAD" -gt 0 ] && info "heights span $LO..$HI; comparing at a height they all have"
[ "$SPREAD" -le 5 ] || die "validators span $SPREAD blocks ($LO..$HI). One is not keeping up - past an activation height that is what a node without the new rules looks like."

if [ "$LO" -lt "$SCHEDULED" ]; then
  say "NOT YET. Slowest validator at $LO, escrow activates at $SCHEDULED, $((SCHEDULED - LO)) blocks to go."
  echo
  echo "Nothing is wrong. An escrowed request before the height is refused as"
  echo "\"not yet\" and waits in the mempool - but the CLIENT gives up first, so it"
  echo "surfaces as a timeout on the deposit rather than as a clear refusal. Wait."
  exit 0
fi

CMP=$((LO - 1))
say "0a. Every validator must have the same block $CMP"
REF=""; REF_L=""
for i in "${!VLABELS[@]}"; do
  out=$(rsh "${VHOSTS[$i]}" "${VKEYS[$i]}" "$R_BLOCK" "$CMP" 2>&1 | tail -1)
  case "$out" in BLOCK\|*) ;; *) die "${VLABELS[$i]}: $out";; esac
  hash=$(field "$out" 2)
  info "$(printf '%-12s block %s = %s' "${VLABELS[$i]}" "$CMP" "$hash")"
  [ "$hash" = none ] && die "${VLABELS[$i]} does not have block $CMP"
  if [ -z "$REF" ]; then REF=$hash; REF_L=${VLABELS[$i]}; continue; fi
  [ "$hash" = "$REF" ] || die "${VLABELS[$i]} has a different block $CMP than $REF_L. This is a fork, not a timing difference - both nodes are asked for the SAME height."
done
info "all ${#VLABELS[@]} validators committed the same block $CMP, past the activation at $SCHEDULED"

say "0b. Find a box that can both pay and serve"
# ONE BOX, NOT TWO. Inference is served by the node that OWNS the provider - a
# reserve sent anywhere else is refused by a node that has never heard of the job
# - and the wallet that pays has to be on the machine running the command,
# because --escrowed signs with a local key and there is no way to hand a signed
# deposit across a network from a shell script without the key travelling. So the
# buy runs where those two coincide, and the script says so plainly when nothing
# does rather than producing a confusing refusal from the wrong node.
BUY_BOX=""; BUYER=""; BEFORE=""; PROVIDER=""; PASSSRC=none
HAS_WALLET=""; HAS_PROVIDER=""
for b in "${BOXES[@]}"; do
  label=$(field "$b" 1); host=$(field "$b" 2); key=$(field "$b" 3)

  w=$(rsh "$host" "$key" "$R_WALLET" 2>&1 | tail -1)
  acct=""; bal=""; psrc=none
  case "$w" in
    WALLET\|*) acct=$(field "$w" 2); bal=$(field "$w" 3); psrc=$(field "$w" 4)
               HAS_WALLET="$HAS_WALLET $label" ;;
  esac

  # No --include-remote: a provider this node lists LOCALLY is one it serves. The
  # order book of the whole network would name sellers this box cannot run.
  sl=$(rsh "$host" "$key" "$R_SELLERS" 2>&1 | tail -1)
  prov=$(echo "$sl" | tr "{" "\n" | grep -o '"id":"[0-9a-f]\{64\}"' | head -1 | cut -d'"' -f4)
  [ -n "$prov" ] && HAS_PROVIDER="$HAS_PROVIDER $label"

  info "$(printf '%-12s wallet=%-12s balance=%-10s serves=%s' "$label" \
        "${acct:0:10}${acct:+...}" "${bal:-none}" "${prov:0:10}${prov:+...}")"

  if [ -z "$BUY_BOX" ] && [ -n "$acct" ] && [ -n "$prov" ]; then
    case "$bal" in ''|*[!0-9]*) ;; *)
      BUY_BOX=$b; BUYER=$acct; BEFORE=$bal; PROVIDER=$prov; PASSSRC=$psrc ;;
    esac
  fi
done

if [ -z "$BUY_BOX" ]; then
  die "no single box both holds a wallet and serves a provider.
   boxes with a wallet:  ${HAS_WALLET:- none}
   boxes with a provider:${HAS_PROVIDER:- none}
   The escrowed path signs locally and must be served by the provider's owner, so
   those two have to be the same machine. Put a funded wallet on a seller box, or
   run the buy from a laptop that can reach the seller's inference port."
fi
[ "$PASSSRC" != none ] || die "that wallet is encrypted and no passphrase is available. Put MATRIX_WALLET_PASSPHRASE in the box's root-owned /etc/matrix/matrixd.env, or set it in YOUR OWN shell before running this - it travels over ssh's stdin, never in argv and never on screen."
[ "$BEFORE" -gt "$UNITS" ] || die "the buyer holds $BEFORE and the reservation is $UNITS; fund it first"
info "buying on $(field "$BUY_BOX" 1) as ${BUYER:0:16}..., from provider ${PROVIDER:0:16}..., reserving $UNITS units"

say "1. Reserve, fund, stream and settle one real inference"
out=$(rsh "$(field "$BUY_BOX" 2)" "$(field "$BUY_BOX" 3)" "$R_BUY" "$BUYER" "$PROVIDER" "$PROMPT" 2>&1)
echo "$out" | sed -n "/OUTPUT_BEGIN/,/OUTPUT_END/p" | sed "1d;\$d" | sed "s/^/   | /"
RC=$(echo "$out" | sed -n "s/^RC|//p" | tail -1)
[ "${RC:-1}" = 0 ] || die "the escrowed run failed. If it timed out waiting for the deposit, check that $SCHEDULED has passed on the node you bought from - before the height, the deposit is refused as \"not yet\" and the client gives up first."

say "2. What the wallet actually paid"
out=$(rsh "$(field "$BUY_BOX" 2)" "$(field "$BUY_BOX" 3)" "$R_WALLET" 2>&1 | tail -1)
AFTER=$(field "$out" 3)
case "$AFTER" in ''|*[!0-9]*) die "could not read the balance back";; esac
COST=$((BEFORE - AFTER))
info "wallet $BEFORE -> $AFTER, cost $COST base units against a reservation of $UNITS"

# THE CHECK. Every command can succeed and this still be a failure: an escrow
# that was funded and never settled leaves the provider holding the cap, and
# from the outside that is indistinguishable from a sale that went well.
if [ "$COST" -ge "$UNITS" ]; then
  die "the run cost $COST, at or above the $UNITS reserved. The settlement did not apply, so the provider is holding the whole reservation and will claim it at the expiry. This is the exact failure the escrowed path exists to prevent, and it looks like success from every other angle - read the job with 'matrix inference get --id ...' on the seller's node."
fi
if [ "$COST" -le 0 ]; then
  die "the run cost nothing, which consensus refuses for a settlement. Either the balance was read before the settlement applied, or the job was never charged."
fi

say "PASS. One answer bought down the escrowed path: $UNITS reserved, $COST paid, $((UNITS - COST)) returned."
