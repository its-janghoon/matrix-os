#!/usr/bin/env bash
# Prove spend budgets work on a live network, end to end, after the protocol
# version that enables them has activated.
#
#   MATRIX_ROLLOUT_BOXES="label|host|keyfile|role"   (same format as rollout.sh)
#   ./scripts/budget-smoke.sh [amount] [per-job-cap] [max-price-per-unit] [ttl]
#
# WHAT IT PROVES, AND WHY IT IS THIS AND NOT A UNIT TEST. A budget is an account
# whose NAME carries the terms its owner signed, and its remaining balance is
# part of the state root. So the question a live network has to answer is not
# "does the code parse the name" - it is "do all the validators agree on what is
# left in it". Four identical answers is the check. One different answer means
# the nodes are applying different rules to the same blocks, which after an
# activation height cannot be rolled back.
#
# THE DELEGATE IS A KEY NOBODY HOLDS. It is 32 random bytes used as an ed25519
# public key, so there is no private key anywhere that could sign a draw against
# it. The budget can be opened, read and closed; it cannot be spent. That is what
# makes this safe to run against real money.
#
# It never leaves money behind: the close returns the whole remaining balance to
# the owner, and the script reports the wallet balance before and after so the
# cost is visible rather than assumed.

set -uo pipefail

AMOUNT=${1:-100000}
PER_JOB_CAP=${2:-10000}
MAX_PRICE=${3:-10000}
TTL=${4:-1h}
: "${MATRIX_ROLLOUT_BOXES:?set MATRIX_ROLLOUT_BOXES to lines of label|host|keyfile|role}"

mapfile -t BOXES <<< "$(echo "$MATRIX_ROLLOUT_BOXES" | sed "s/^[[:space:]]*//;s/[[:space:]]*$//" | grep -v "^$")"

say()  { printf "\n== %s\n" "$*"; }
info() { printf "   %s\n" "$*"; }
die()  { printf "\nABORT: %s\n" "$*" >&2; exit 1; }
field() { echo "$1" | cut -d"|" -f"$2"; }

rsh() {
  local host=$1 key=$2 script=$3 arg=${4:-} arg2=${5:-}
  ssh -i "$key" -o StrictHostKeyChecking=accept-new -o ConnectTimeout=15 \
      -o BatchMode=yes "ubuntu@$host" bash -s -- "$arg" "$arg2" <<< "$script"
}

read -r -d "" R_COMMON <<'EOS'
P=$(pgrep -x matrixd || true)
[ -z "$P" ] && { echo "ERR matrixd not running"; exit 1; }
CMD=$(sudo tr "\0" "\n" < "/proc/$P/cmdline")
CFG=$(echo "$CMD" | grep -A1 -x -- -config | tail -1)
[ -z "$CFG" ] && CFG=$(echo "$CMD" | sed -n "s/^-config=//p")
[ -z "$CFG" ] && { echo "ERR no -config on the command line"; exit 1; }
M=$(sudo awk '/^market:/{f=1;next} f&&/^[^[:space:]#]/{f=0} f&&/addr:/{print $2; exit}' "$CFG" | tr -d "\"" | sed "s/.*://")
M=${M:-9091}
# The node's own key, read on the node and never printed. --api-key would put it
# in argv, where ps shows it to anyone on the box; the environment does not.
export MATRIX_ADMIN_API_KEY=$(sudo sed -n "s/^[[:space:]-]*key:[[:space:]]*//p" "$CFG" | head -1 | tr -d "\"")
EOS

R_WALLET="$R_COMMON"'
if [ ! -f "$HOME/.matrix/wallet.json" ]; then echo "NOWALLET"; exit 0; fi
A=$(matrix wallet show 2>/dev/null | sed -n "s/^account:[[:space:]]*//p" | head -1)
B=$(matrix wallet balance --addr "127.0.0.1:$M" 2>/dev/null | awk "{print \$NF}" | head -1)
echo "WALLET|${A:-unknown}|${B:-unknown}"
'

R_OPEN="$R_COMMON"'
D=$(openssl rand -hex 32)
OUT=$(matrix budget open --addr "127.0.0.1:$M" --amount "$1" --delegate "$D" \
        --per-job-cap "$2" --max-price-per-unit "'"$MAX_PRICE"'" --ttl "'"$TTL"'" 2>&1)
ACC=$(echo "$OUT" | sed -n "s/^budget:[[:space:]]*//p" | head -1)
if [ -z "$ACC" ]; then echo "ERR open failed:"; echo "$OUT"; exit 1; fi
echo "ACCOUNT|$ACC"
'

R_SHOW="$R_COMMON"'
matrix budget show --addr "127.0.0.1:$M" --account "$1" --json 2>&1 | tr -d " \n"
echo
'

R_CLOSE="$R_COMMON"'
OUT=$(matrix budget close --addr "127.0.0.1:$M" --account "$1" 2>&1) || { echo "ERR close failed:"; echo "$OUT"; exit 1; }
echo "CLOSED"
'

remaining_on_opener() { # -> the number, or "" if the account is not there yet
  rsh "$(field "$OPENER" 2)" "$(field "$OPENER" 3)" "$R_SHOW" "$1" 2>&1 | tail -1 \
    | sed -n "s/.*\"remaining\":\([0-9]*\).*/\1/p" | head -1
}

# Wait for a transaction to COMMIT before reading anything back.
#
# Without this the test lies. `budget open` returns as soon as the transfer is
# submitted, and this chain mints a block per transaction rather than on a timer,
# so for a few seconds every node truthfully reports that the account does not
# exist. Five nodes agreeing that nothing is there is not the check passing - it
# is the check being asked too early.
wait_for_remaining() {
  local account=$1 want=$2 tries=${3:-20} i got
  for ((i=1;i<=tries;i++)); do
    got=$(remaining_on_opener "$account")
    [ "${got:-unset}" = "$want" ] && { info "committed: remaining=$got (after $((i*10))s)"; return 0; }
    sleep 10
  done
  info "last reading: remaining=${got:-<account not found>}, wanted $want"
  return 1
}

# ---------------------------------------------------------------- run

say "0. Find a box with a funded wallet"
OPENER=""
for b in "${BOXES[@]}"; do
  label=$(field "$b" 1); host=$(field "$b" 2); key=$(field "$b" 3)
  out=$(rsh "$host" "$key" "$R_WALLET" 2>&1 | tail -1)
  case "$out" in
    WALLET\|*) info "$(printf '%-12s account=%s balance=%s' "$label" "$(field "$out" 2)" "$(field "$out" 3)")"
               [ -z "$OPENER" ] && OPENER=$b && BEFORE=$(field "$out" 3) ;;
    NOWALLET)  info "$(printf '%-12s no wallet' "$label")" ;;
    *)         info "$(printf '%-12s %s' "$label" "$out")" ;;
  esac
done
[ -n "$OPENER" ] || die "no box has a wallet at ~/.matrix/wallet.json; open the budget from the browser instead"
info "opening from $(field "$OPENER" 1), balance $BEFORE"

say "1. Open a budget of $AMOUNT base units"
out=$(rsh "$(field "$OPENER" 2)" "$(field "$OPENER" 3)" "$R_OPEN" "$AMOUNT" "$PER_JOB_CAP" 2>&1 | tail -3)
case "$out" in ACCOUNT\|*) ;; *) die "open failed: $out";; esac
ACCOUNT=${out#ACCOUNT|}
info "$ACCOUNT"

say "2. Wait for the open to commit"
wait_for_remaining "$ACCOUNT" "$AMOUNT" || die "the budget never showed $AMOUNT on the opener, so there is nothing to compare. Check that the activation height has passed - before it, a budget recipient is refused as \"not yet\" and sits in the mempool."

say "3. Every node must report the same thing"
REF=""; REF_LABEL=""; BAD=0
for b in "${BOXES[@]}"; do
  label=$(field "$b" 1); host=$(field "$b" 2); key=$(field "$b" 3)
  out=$(rsh "$host" "$key" "$R_SHOW" "$ACCOUNT" 2>&1 | tail -1)
  info "$(printf '%-12s %s' "$label" "${out:0:110}")"
  case "$out" in *\"remaining\":$AMOUNT*) ;; *) info "  ^^ does not report remaining=$AMOUNT"; BAD=1 ;; esac
  if [ -z "$REF" ]; then REF=$out; REF_LABEL=$label; continue; fi
  [ "$out" = "$REF" ] || { info "  ^^ DIFFERS from $REF_LABEL"; BAD=1; }
done

say "4. Close it and check the money comes back"
out=$(rsh "$(field "$OPENER" 2)" "$(field "$OPENER" 3)" "$R_CLOSE" "$ACCOUNT" 2>&1 | tail -3)
case "$out" in CLOSED) info "close submitted" ;; *) info "close: $out"; BAD=1 ;; esac
wait_for_remaining "$ACCOUNT" 0 || { info "the close did not land"; BAD=1; }
out=$(rsh "$(field "$OPENER" 2)" "$(field "$OPENER" 3)" "$R_WALLET" 2>&1 | tail -1)
AFTER=$(field "$out" 3)
info "wallet $BEFORE -> $AFTER (cost $((BEFORE - AFTER)) base units in fees)"

if [ "$BAD" -ne 0 ]; then
  die "the nodes did not agree, or the close did not land. Past an activation height there is no rollback: do not restart nodes onto an older binary, and read the state root on every validator before doing anything else."
fi
say "PASS. A budget opened, every node agreed on it, and closing it returned the balance."
