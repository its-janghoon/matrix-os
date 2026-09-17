#!/usr/bin/env bash
# Drive a quiet chain to a target height, so a scheduled activation actually
# arrives.
#
#   MATRIX_ROLLOUT_BOXES="label|host|keyfile|role"   (same format as rollout.sh)
#   ./scripts/advance-height.sh [target-height]
#
# WHY THIS IS NEEDED, AND WHY IT IS NOT A HACK. This chain mints a block when
# there is a transaction to put in one. With no traffic it still escapes, through
# stall recovery - but that timer is `60 x consensus.round_timeout`, so a node
# configured with the production 3s produces ONE EMPTY BLOCK EVERY THREE MINUTES.
# An activation 200 blocks out is then ten hours away, and an activation is a
# height rather than a time precisely so that every node switches together. The
# height has to be reached; nothing else moves it.
#
# So this sends the smallest real transfers it can and lets consensus do the
# rest. One base unit, back and forth between two accounts the operator already
# controls, so the money ends up where it started and the only cost is the
# protocol fee. A self-transfer would be free and is REFUSED by the ledger - it
# moves nothing while consuming a nonce - which is why this needs two accounts
# and says so plainly when it can only find one.
#
# It stops the moment the height is reached, and it never touches a config.

set -uo pipefail

TARGET=${1:-}
: "${MATRIX_ROLLOUT_BOXES:?set MATRIX_ROLLOUT_BOXES to lines of label|host|keyfile|role}"

mapfile -t BOXES <<< "$(echo "$MATRIX_ROLLOUT_BOXES" | sed "s/^[[:space:]]*//;s/[[:space:]]*$//" | grep -v "^$")"

say()  { printf "\n== %s\n" "$*"; }
info() { printf "   %s\n" "$*"; }
die()  { printf "\nABORT: %s\n" "$*" >&2; exit 1; }
field() { echo "$1" | cut -d"|" -f"$2"; }

keypath() { printf %s "${1/#\~/$HOME}"; }
verdict() { printf '%s\n' "$1" | grep -m1 -E "^($2)" || true; }
saidWhat() { printf '%s' "$1" | tail -6; }

checkKeys() {
  local b k
  for b in "${BOXES[@]}"; do
    k=$(keypath "$(field "$b" 3)")
    [ -f "$k" ] || die "no key file at $k, for $(field "$b" 1). Write the path as \$HOME/... or in full - a ~ inside the quoted box list is never expanded by the shell."
    [ -r "$k" ] || die "cannot read the key file at $k, for $(field "$b" 1)."
  done
}

rsh() {
  local host=$1 key script=$3 arg=${4:-} arg2=${5:-}
  key=$(keypath "$2")
  ssh -i "$key" -o StrictHostKeyChecking=accept-new -o ConnectTimeout=15 \
      -o BatchMode=yes "ubuntu@$host" bash -s -- "$arg" "$arg2" <<< "$script"
}

# The passphrase travels as the FIRST LINE of the script on ssh's stdin. Not as
# an argument: `bash -s -- "$pass"` puts it in argv, where `ps` on that box shows
# it to anyone logged in.
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
port() {
  sudo awk -v s="$1" '$0 ~ "^" s ":" {f=1;next} f&&/^[^[:space:]#]/{f=0} f&&/addr:/{print $2; exit}' "$CFG" \
    | tr -d "\"" | sed "s/.*://"
}
M=$(port market); M=${M:-9091}
RPC=$(port eth_rpc)
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

read -r -d "" R_HEIGHT_BODY <<'EOS'
# The height, plus what this chain does when nobody is talking to it.
#
# stall recovery is 60 x round_timeout (see consensus/engine.go), and that is the
# ONLY thing moving a chain with no traffic - so it is what turns "200 blocks" into
# a number of hours. Reported rather than assumed.
if [ -z "$RPC" ]; then echo "HEIGHT|0|none"; exit 0; fi
J=$(curl -s --max-time 6 -X POST "http://127.0.0.1:$RPC" -H "content-type: application/json" \
      -d '{"jsonrpc":"2.0","id":1,"method":"matrix_getChainInfo","params":[]}')
H=$(echo "$J" | tr "," "\n" | sed -n 's/.*"height":\([0-9]*\).*/\1/p' | head -1)
RT=$(sudo awk '/^consensus:/{f=1;next} f&&/^[^[:space:]#]/{f=0} f&&/round_timeout:/{print $2; exit}' "$CFG" | tr -d '"')
echo "HEIGHT|${H:-0}|${RT:-unset}"
EOS
R_HEIGHT="$R_COMMON
$R_HEIGHT_BODY"

read -r -d "" R_SCHED_BODY <<'EOS'
# The LAST scheduled height: the one being waited on.
sudo awk '
f && /^[[:space:]]*$/ { next }
f { match($0,/^[[:space:]]*/); if (RLENGTH<=ind) f=0 }
f && /height:[[:space:]]*[0-9]+/  { h=$0; sub(/^.*height:[[:space:]]*/,"",h);  sub(/[^0-9].*$/,"",h) }
f && /version:[[:space:]]*[0-9]+/ { v=$0; sub(/^.*version:[[:space:]]*/,"",v); sub(/[^0-9].*$/,"",v); last=h "=" v }
/^[[:space:]]*protocol_upgrades:/ { match($0,/^[[:space:]]*/); ind=RLENGTH; f=1; if ($0 ~ /\[\][[:space:]]*$/) f=0 }
END { print "SCHED|" (last=="" ? "none" : last) }
' "$CFG"
EOS
R_SCHED="$R_COMMON
$R_SCHED_BODY"

R_WALLET="$PASS_PREFIX$R_COMMON"'
if [ ! -f "$HOME/.matrix/wallet.json" ]; then echo "NOWALLET"; exit 0; fi
A=$(matrix wallet show 2>/dev/null | sed -n "s/^account:[[:space:]]*//p" | head -1)
B=$(matrix wallet balance --addr "127.0.0.1:$M" 2>/dev/null | awk "{print \$NF}" | head -1)
echo "WALLET|${A:-unknown}|${B:-unknown}|$PASS_FROM"
'

# One transfer of one base unit. Each waits for its own settlement, so each is a
# block rather than a batch.
R_SEND="$PASS_PREFIX$R_COMMON"'
OUT=$(matrix wallet transfer --addr "127.0.0.1:$M" --to "$1" --amount 1 2>&1) || { echo "ERR $OUT"; exit 1; }
echo "SENT"
'

# ---------------------------------------------------------------- run

checkKeys

say "0. Where the chain is, and what moves it when nobody does"
CUR=0; RT=unset; PROBE=""
for b in "${BOXES[@]}"; do
  label=$(field "$b" 1)
  out=$(verdict "$(rsh "$(field "$b" 2)" "$(field "$b" 3)" "$R_HEIGHT" 2>&1)" 'HEIGHT\||ERR')
  case "$out" in HEIGHT\|*) ;; *) info "$(printf '%-12s %s' "$label" "${out:-unreachable}")"; continue;; esac
  h=$(field "$out" 2); rt=$(field "$out" 3)
  [ "$h" -gt "$CUR" ] 2>/dev/null && { CUR=$h; PROBE=$b; }
  [ "$rt" != unset ] && RT=$rt
  info "$(printf '%-12s height=%-7s round_timeout=%s' "$label" "$h" "$rt")"
done
[ "$CUR" -gt 0 ] || die "no box reported a height"

if [ -z "$TARGET" ]; then
  out=$(verdict "$(rsh "$(field "$PROBE" 2)" "$(field "$PROBE" 3)" "$R_SCHED" 2>&1)" 'SCHED\||ERR')
  pair=$(field "$out" 2)
  TARGET=${pair%%=*}
  [ "$pair" != none ] && [ -n "$TARGET" ] || die "no scheduled height to aim at; pass one as the first argument"
  info "aiming at the scheduled activation: $pair"
fi
case "$TARGET" in ''|*[!0-9]*) die "target height must be a number, got \"$TARGET\"";; esac
[ "$TARGET" -gt "$CUR" ] || { say "Already at $CUR, past $TARGET. Nothing to do."; exit 0; }

NEED=$((TARGET - CUR))
if [ "$RT" != unset ]; then
  info "with no traffic this chain escapes on stall recovery every 60 x $RT, so $NEED blocks would take that many multiples"
fi
info "$NEED blocks to go"

say "1. Two accounts to move one base unit between"
# TWO, because the ledger REFUSES a self-transfer: it moves nothing while
# consuming a nonce, so it is rejected rather than accepted as a free block.
A_BOX=""; A_ID=""; B_BOX=""; B_ID=""
for b in "${BOXES[@]}"; do
  label=$(field "$b" 1)
  out=$(verdict "$(rsh "$(field "$b" 2)" "$(field "$b" 3)" "$R_WALLET" 2>&1)" 'WALLET\||NOWALLET|ERR')
  case "$out" in
    WALLET\|*)
      acct=$(field "$out" 2); bal=$(field "$out" 3); psrc=$(field "$out" 4)
      info "$(printf '%-12s account=%s... balance=%-10s passphrase=%s' "$label" "${acct:0:12}" "$bal" "$psrc")"
      [ "$psrc" = none ] && { info "  ^^ encrypted with no passphrase available; skipping"; continue; }
      case "$bal" in ''|*[!0-9]*) continue;; esac
      [ "$bal" -lt "$NEED" ] && { info "  ^^ holds $bal, needs at least $NEED to fund the run; skipping"; continue; }
      if   [ -z "$A_ID" ]; then A_BOX=$b; A_ID=$acct
      elif [ -z "$B_ID" ] && [ "$acct" != "$A_ID" ]; then B_BOX=$b; B_ID=$acct
      fi ;;
    *) info "$(printf '%-12s %s' "$label" "$out")" ;;
  esac
done
[ -n "$A_ID" ] && [ -n "$B_ID" ] || die "found fewer than two usable wallets on different accounts.
   One base unit has to go somewhere, and the ledger refuses a self-transfer - it
   moves nothing while consuming a nonce - so this needs two. Put a funded wallet
   on a second box, or drive the height from a laptop with two wallets."
info "moving 1 base unit back and forth between ${A_ID:0:12}... and ${B_ID:0:12}..."

say "2. One transfer per block, until $TARGET"
SENT=0; FAILED=0
while [ "$CUR" -lt "$TARGET" ]; do
  if [ $((SENT % 2)) -eq 0 ]; then FROM=$A_BOX; TO=$B_ID; else FROM=$B_BOX; TO=$A_ID; fi
  out=$(verdict "$(rsh "$(field "$FROM" 2)" "$(field "$FROM" 3)" "$R_SEND" "$TO" 2>&1)" 'SENT|ERR')
  case "$out" in
    SENT) SENT=$((SENT + 1)); FAILED=0 ;;
    *)    FAILED=$((FAILED + 1))
          info "transfer from $(field "$FROM" 1) failed: ${out:-no verdict}"
          # Three in a row is a real fault, not a busy moment. Stopping beats
          # hammering a chain that is telling us something.
          [ "$FAILED" -ge 3 ] && die "three transfers in a row failed; stopping rather than hammering the chain" ;;
  esac

  # Read the height rather than counting transfers: transfers that arrive close
  # together share a block, so a count would overshoot and stop short.
  if [ $((SENT % 5)) -eq 0 ] || [ "$SENT" -le 1 ]; then
    out=$(verdict "$(rsh "$(field "$PROBE" 2)" "$(field "$PROBE" 3)" "$R_HEIGHT" 2>&1)" 'HEIGHT\||ERR')
    case "$out" in HEIGHT\|*) CUR=$(field "$out" 2);; esac
    info "$(printf 'height %-7s  %s to go  (%d transfers sent)' "$CUR" "$((TARGET - CUR))" "$SENT")"
  fi
done

say "DONE. Height $CUR, past the target $TARGET, after $SENT transfers."
echo
echo "The activation is a height and every node switches at it on its own. Next:"
echo "  ./scripts/escrow-smoke.sh"
