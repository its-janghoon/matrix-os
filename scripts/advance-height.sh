#!/usr/bin/env bash
# Drive a quiet chain to a target height, so a scheduled activation actually
# arrives.
#
#   MATRIX_ROLLOUT_BOXES="label|host|keyfile|role"   (same format as rollout.sh)
#   ./scripts/advance-height.sh [target-height] [recipient-account]
#
# An encrypted wallet needs its passphrase. Put it in YOUR OWN shell, never on the
# command line - it travels over ssh's stdin, where `ps` on the box cannot see it:
#
#   read -s -p "passphrase: " MATRIX_WALLET_PASSPHRASE; export MATRIX_WALLET_PASSPHRASE
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
# rest: one base unit at a time, from one funded wallet to one other account the
# operator already controls. A run of a couple of hundred blocks therefore costs a
# couple of hundred base units plus fees, against a wallet holding hundreds of
# millions - which is why it does not bother sending them back.
#
# It needs a RECIPIENT and cannot use the sender: the ledger refuses a
# self-transfer, since it moves nothing while consuming a nonce. One is found
# among the other boxes' wallets, or among the validator account IDs in the
# config - those are accounts on this chain that this operator already runs - or
# you name one as the second argument.
#
# It stops the moment the height is reached, and it never touches a config.

set -uo pipefail

TARGET=${1:-}
RECIPIENT=${2:-}
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

# rsh runs a script on a box, with arguments that survive the trip.
#
# ssh JOINS its command words with spaces and hands one string to the remote
# shell, which splits it again - so a local quote does not cross the wire. A
# prompt of "Answer in one short sentence: ..." arrived on the box as $1="Answer"
# and the rest as separate words, the model was asked one word, and it replied
# asking what the question was. Both sides then billed for that.
#
# %q makes each argument quote itself for the shell that will actually read it.
rsh() {
  local host=$1 key script=$3 q="" a
  key=$(keypath "$2")
  shift 3
  for a in "$@"; do q="$q $(printf %q "$a")"; done
  ssh -i "$key" -o StrictHostKeyChecking=accept-new -o ConnectTimeout=15 \
      -o BatchMode=yes "ubuntu@$host" "bash -s --$q" <<< "$script"
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
#
# round_timeout is read FIRST and unconditionally. It used to sit after an early
# return taken by any box without eth_rpc - a seller, typically - so that box
# reported "none" for a field it had never looked at, and the caller believed it.
RT=$(sudo awk '/^consensus:/{f=1;next} f&&/^[^[:space:]#]/{f=0} f&&/round_timeout:/{print $2; exit}' "$CFG" | tr -d '"')
if [ -z "$RPC" ]; then echo "HEIGHT|0|${RT:-unset}"; exit 0; fi
J=$(curl -s --max-time 6 -X POST "http://127.0.0.1:$RPC" -H "content-type: application/json" \
      -d '{"jsonrpc":"2.0","id":1,"method":"matrix_getChainInfo","params":[]}')
H=$(echo "$J" | tr "," "\n" | sed -n 's/.*"height":\([0-9]*\).*/\1/p' | head -1)
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

# The validator account IDs this node's config names. They are accounts on this
# chain that this operator already runs, which makes any of them a legitimate
# place to put a base unit when nothing else is available to receive one.
read -r -d "" R_VALIDATORS_BODY <<'EOS'
sudo awk '
f && /^[[:space:]]*$/ { next }
f { match($0,/^[[:space:]]*/); if (RLENGTH<=ind) f=0 }
f && /[0-9a-f]{64}/ { a=$0; sub(/^[^0-9a-f]*/,"",a); sub(/[^0-9a-f].*$/,"",a); if (length(a)==64) print "ACCT|" a }
/^[[:space:]]*validators:/ { match($0,/^[[:space:]]*/); ind=RLENGTH; f=1 }
' "$CFG"
EOS
R_VALIDATORS="$R_COMMON
$R_VALIDATORS_BODY"

# Provider accounts on the order book. A registered provider IS an account on
# this chain, and on this network they belong to the same operator - which makes
# one a legitimate place to put a base unit.
R_PROVIDERS="$R_COMMON"'
matrix provider list --addr "127.0.0.1:$M" --json 2>/dev/null \
  | grep -o "\"id\":\"[0-9a-f]\{64\}\"" | cut -d"\"" -f4 | sed "s/^/ACCT|/"
echo "PROVIDERS_DONE"
'

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
  # A box that could not read it must not overwrite a box that could.
  [ "$rt" != unset ] && [ "$rt" != none ] && RT=$rt
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
[ "$RT" != unset ] && info "with no traffic this chain escapes on stall recovery every 60 x $RT, so $NEED blocks would be about $((NEED * 60 * ${RT%s} / 60)) minutes of waiting"
info "$NEED blocks to go"

say "1. A funded wallet to send from, and somewhere to send to"
SENDER_BOX=""; SENDER_ID=""
CANDIDATES=()
for b in "${BOXES[@]}"; do
  label=$(field "$b" 1)
  out=$(verdict "$(rsh "$(field "$b" 2)" "$(field "$b" 3)" "$R_WALLET" 2>&1)" 'WALLET\||NOWALLET|ERR')
  case "$out" in
    WALLET\|*)
      acct=$(field "$out" 2); bal=$(field "$out" 3); psrc=$(field "$out" 4)
      info "$(printf '%-12s account=%s... balance=%-12s passphrase=%s' "$label" "${acct:0:12}" "$bal" "$psrc")"
      [ -n "$acct" ] && [ "$acct" != unknown ] && CANDIDATES+=("$acct")
      [ "$psrc" = none ] && { info "  ^^ encrypted and no passphrase available; it cannot sign"; continue; }
      case "$bal" in ''|*[!0-9]*) continue;; esac
      # Enough for one base unit per block, and room for fees on top.
      [ "$bal" -lt $((NEED * 2)) ] && { info "  ^^ holds $bal, wants at least $((NEED * 2)) for $NEED transfers plus fees"; continue; }
      [ -z "$SENDER_ID" ] && { SENDER_BOX=$b; SENDER_ID=$acct; } ;;
    NOWALLET) info "$(printf '%-12s no wallet' "$label")" ;;
    *)        info "$(printf '%-12s %s' "$label" "${out:-unreachable}")" ;;
  esac
done

if [ -z "$SENDER_ID" ]; then
  die "no box has a wallet that can both sign and cover $NEED transfers.
   An encrypted wallet needs its passphrase. Put it in YOUR OWN shell and run this
   again - it travels over ssh's stdin, never in argv, and is never printed:
     read -s -p \"passphrase: \" MATRIX_WALLET_PASSPHRASE; export MATRIX_WALLET_PASSPHRASE"
fi

# The recipient. Anything but the sender: the ledger refuses a self-transfer,
# which moves nothing while consuming a nonce.
#
# Every source reports what it found, including nothing. The first version fell
# through three of them in silence and then said "no account" - true, useless,
# and giving no way to tell an empty validators list from a reader that was
# looking in the wrong place.
takeAccounts() { # <raw> -> prints each 64-hex account, one per line
  printf '%s\n' "$1" | sed -n 's/^ACCT|\([0-9a-f]\{64\}\)$/\1/p'
}
pickFrom() { # <label> <accounts...> -> sets RECIPIENT if one is not the sender
  local label=$1 a n=0; shift
  for a in "$@"; do
    [ -z "$a" ] && continue
    n=$((n + 1))
    if [ -z "$RECIPIENT" ] && [ "$a" != "$SENDER_ID" ]; then
      RECIPIENT=$a
      info "recipient from $label: ${a:0:12}..."
    fi
  done
  [ -n "$RECIPIENT" ] || info "$label: $n account(s), none usable$([ "$n" -gt 0 ] && echo " (all are the sender)")"
}

if [ -z "$RECIPIENT" ]; then
  pickFrom "another box's wallet" "${CANDIDATES[@]:-}"
fi
if [ -z "$RECIPIENT" ]; then
  # Providers on the order book. A registered provider is an account, and on this
  # network it is this operator's own.
  mapfile -t provs < <(takeAccounts "$(rsh "$(field "$PROBE" 2)" "$(field "$PROBE" 3)" "$R_PROVIDERS" 2>&1)")
  pickFrom "the order book" "${provs[@]:-}"
fi
if [ -z "$RECIPIENT" ]; then
  # The validator accounts the config names. They are this operator's own nodes,
  # so a base unit sent there has not left the building.
  mapfile -t vals < <(takeAccounts "$(rsh "$(field "$PROBE" 2)" "$(field "$PROBE" 3)" "$R_VALIDATORS" 2>&1)")
  pickFrom "the config's validators list" "${vals[@]:-}"
fi
[ -n "$RECIPIENT" ] || die "nothing on this network is a usable recipient, and one is needed because the
   ledger refuses a self-transfer. Name any account you control:

     ./scripts/advance-height.sh $TARGET <64-hex-account-id>

   To make one that is yours and spendable, on any box:
     matrix wallet create --wallet ~/.matrix/height-driver.json
   then pass the account it prints. Its balance is recoverable; it is a real key."
[ "$RECIPIENT" != "$SENDER_ID" ] || die "the recipient is the sender; the ledger refuses a self-transfer"

info "sending 1 base unit from ${SENDER_ID:0:12}... ($(field "$SENDER_BOX" 1)) to ${RECIPIENT:0:12}..., $NEED times"
info "total cost about $NEED base units plus fees, and it lands in an account you run"

say "2. One transfer per block, until $TARGET"
SENT=0; FAILED=0
while [ "$CUR" -lt "$TARGET" ]; do
  out=$(verdict "$(rsh "$(field "$SENDER_BOX" 2)" "$(field "$SENDER_BOX" 3)" "$R_SEND" "$RECIPIENT" 2>&1)" 'SENT|ERR')
  case "$out" in
    SENT) SENT=$((SENT + 1)); FAILED=0 ;;
    *)    FAILED=$((FAILED + 1))
          info "transfer $((SENT + 1)) failed: ${out:-no verdict}"
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
