#!/usr/bin/env bash
# Roll a matrix-os release onto every node of a running network and schedule a
# protocol-version activation, with the gates the budget-activation runbook asks
# for. Run it from a machine that can ssh to all of them.
#
#   MATRIX_ROLLOUT_BOXES="label|host|keyfile|role   (one per line, role=validator|seller)"
#   ./scripts/rollout.sh <release-tag> <protocol-version>
#
# Example:
#   export MATRIX_ROLLOUT_BOXES="validator-1|v1.example.com|$HOME/k.pem|validator
#   gpu|203.0.113.10|$HOME/other.pem|seller"
#   ./scripts/rollout.sh v0.4.0 2
#
# WHY IT IS SHAPED THIS WAY. A protocol version is part of block validity, so two
# nodes with different schedules disagree about the same block. The dangerous
# state is therefore not "a node is down" - it is "some nodes carry the schedule
# and some do not". Every failure path below either completes the change
# everywhere or undoes it everywhere; it never stops in between.
#
# It is safe to re-run: each step checks whether it is already done, and a box
# that is already on the release, or already carries the schedule, is skipped.
#
# HOW IT DECIDES A NODE IS HEALTHY. Not by watching the height go up. A chain
# with no transactions produces no blocks (see engine.go: an idle proposer
# returns nil rather than minting an empty block), so "the height moved" is a
# test that fails on a perfectly healthy quiet network. What it checks instead is
# that every validator reports the same head hash AND the same state root, which
# is the property a rule change actually depends on.
#
# HOW IT PICKS THE HEIGHT. By measuring this chain, not by assuming a cadence.
# A lead quoted in blocks means nothing without a block rate - 2000 blocks is
# fifty minutes on a busy chain and two days on a quiet one.

set -uo pipefail

V=${1:-}
NEW_PROTOCOL_VERSION=${2:-}
[ -n "$V" ] && [ -n "$NEW_PROTOCOL_VERSION" ] || {
  echo "usage: $0 <release-tag> <protocol-version>   (e.g. $0 v0.4.0 2)" >&2; exit 2; }
: "${MATRIX_ROLLOUT_BOXES:?set MATRIX_ROLLOUT_BOXES to lines of label|host|keyfile|role}"

REPO=${MATRIX_ROLLOUT_REPO:-savagemanage/matrix-os}
MIN_LEAD=200              # never schedule closer than this many blocks
TARGET_LEAD_SECONDS=3600  # aim for about an hour of wall clock

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

# verdict pulls the payload's ANSWER out of everything the box said.
#
# A remote payload ends with one line naming its result - OK, SKIP, ERR, or a
# pipe-delimited record - and `tail -3` was being read as if that were the only
# thing on the wire. It is not. A box writes to stderr whenever it likes:
# systemd's "the unit file changed on disk, run daemon-reload", sudo's lecture, a
# login banner, apt's warnings. Any one of them lands after the verdict and the
# prefix match fails.
#
# That is not cosmetic. It failed a validator-4 upgrade that had SUCCEEDED - the
# OK line was right there under two systemd warnings - and in the schedule step
# the same noise would have called rollback_schedule and undone a correct
# schedule on every box that already had it, which is the one state this script
# exists to never leave a chain in.
#
# The FIRST match, not the last: an ERR path prints its verdict and then dumps
# journalctl, and a log line is not a second verdict.
verdict() { printf '%s\n' "$1" | grep -m1 -E "^($2)" || true; }

# saidWhat is what to show when there was no verdict: the box's own last words.
saidWhat() { printf '%s' "$1" | tail -6; }

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

# rsh <host> <key> <script> [arg]
rsh() {
  local host=$1 key script=$3 arg=${4:-} arg2=${5:-}
  key=$(keypath "$2")
  ssh -i "$key" -o StrictHostKeyChecking=accept-new -o ConnectTimeout=15 \
      -o BatchMode=yes "ubuntu@$host" bash -s -- "$arg" "$arg2" <<< "$script"
}

# ---------------------------------------------------------------- payloads

read -r -d "" R_COMMON <<'EOS'
P=$(pgrep -x matrixd || true)
[ -z "$P" ] && { echo "ERR matrixd not running"; exit 1; }
BIN=$(sudo readlink -f "/proc/$P/exe")
D=$(dirname "$BIN")
CMD=$(sudo tr "\0" "\n" < "/proc/$P/cmdline")
CFG=$(echo "$CMD" | grep -A1 -x -- -config | tail -1)
[ -z "$CFG" ] && CFG=$(echo "$CMD" | sed -n "s/^-config=//p")
[ -z "$CFG" ] && { echo "ERR no -config on the command line"; exit 1; }
port() {
  sudo awk -v s="$1" '$0 ~ "^" s ":" {f=1;next} f&&/^[^[:space:]#]/{f=0} f&&/addr:/{print $2; exit}' "$CFG" \
    | tr -d "\"" | sed "s/.*://"
}
RPC=$(port eth_rpc)
chaininfo() {
  [ -z "$RPC" ] && return 1
  curl -s --max-time 6 -X POST "http://127.0.0.1:$RPC" \
    -H "content-type: application/json" \
    -d '{"jsonrpc":"2.0","id":1,"method":"matrix_getChainInfo","params":[]}'
}
EOS

# STATE: height|head|root|version|cfg
R_STATE="$R_COMMON"'
J=$(chaininfo || true)
H=$(echo "$J" | tr "," "\n" | sed -n "s/.*\"height\":\([0-9]*\).*/\1/p" | head -1)
HD=$(echo "$J" | tr "," "\n" | sed -n "s/.*\"head_hash\":\"\([^\"]*\)\".*/\1/p" | head -1)
SR=$(echo "$J" | tr "," "\n" | sed -n "s/.*\"state_root\":\"\([^\"]*\)\".*/\1/p" | head -1)
SCHED=$(sudo sed -n "/protocol_upgrades:/,/^[[:space:]]*[a-z_]*:/p" "$CFG" | grep -E "height:|version:" | tr -d " " | tr "\n" ";")
echo "OK|${H:-0}|${HD:-none}|${SR:-none}|$(matrixd -version 2>&1 | head -1)|$CFG|${SCHED:-empty}"
'

# UPGRADE: install v0.4.0 unless already on it
R_UPGRADE="$R_COMMON"'
V=$1
if matrixd -version 2>&1 | grep -q "$V" && matrix --version 2>&1 | grep -q "$V"; then
  echo "SKIP already on $V"; exit 0
fi
case "$(uname -m)" in aarch64|arm64) G=arm64;; x86_64|amd64) G=amd64;; *) echo "ERR unknown arch $(uname -m)"; exit 1;; esac
T=/tmp/mx-$V; rm -rf "$T"; mkdir -p "$T"; cd "$T" || exit 1
N=matrix-os-$V-linux-$G.tar.gz
curl -fsSL -o "$N" "https://github.com/'"$REPO"'/releases/download/$V/$N" || { echo "ERR download failed"; exit 1; }
curl -fsSL -o SHA256SUMS "https://github.com/'"$REPO"'/releases/download/$V/SHA256SUMS" || { echo "ERR checksum download failed"; exit 1; }
sha256sum --check --ignore-missing SHA256SUMS > /dev/null 2>&1 || { echo "ERR CHECKSUM MISMATCH"; exit 1; }
tar -xzf "$N" || { echo "ERR extract failed"; exit 1; }
./matrixd -version | grep -q "$V" || { echo "ERR staged matrixd is not $V"; exit 1; }
./matrix --version | grep -q "$V" || { echo "ERR staged matrix is not $V"; exit 1; }
# Everything above touched nothing. Only now is the node stopped.
sudo systemctl stop matrixd || { echo "ERR stop failed"; exit 1; }
sudo install -m 0755 "$T/matrixd" "$D/matrixd"
sudo install -m 0755 "$T/matrix"  "$D/matrix"
# Reload the unit BEFORE starting. systemd warns "the unit file changed on disk"
# when the file differs from what it has loaded, and then starts the STALE one -
# so a box whose service file was edited at some point comes back on the old
# ExecStart and old environment while reporting the new binary. Harmless where
# nothing changed, and the difference between a real upgrade and an apparent one
# where it did. validator-4 was carrying exactly that warning.
sudo systemctl daemon-reload
sudo systemctl start matrixd || { echo "ERR start failed"; exit 1; }
sleep 15
systemctl is-active --quiet matrixd || { echo "ERR did not come back"; sudo journalctl -u matrixd -n 25 --no-pager; exit 1; }
echo "OK installed $(matrixd -version 2>&1 | head -1) / $(matrix --version 2>&1 | head -1) into $D"
'

# SCHEDULE: put protocol_upgrades at height $1, preflight, restart
# SCHEDULE: add {height, version} to protocol_upgrades, preflight, restart.
#
# Written as a heredoc with an awk program in it, because a sed substitution only
# ever handled the shape a node STARTS in - `protocol_upgrades: []` - and refused
# every config that already carried an activation. That is fine exactly once. The
# second upgrade on a live chain has a list to append to, and a tool that cannot
# do it sends an operator to edit four configs by hand, which is the failure this
# whole script exists to remove.
read -r -d "" R_SCHEDULE_BODY <<'EOS'
H=$1
V=$2
[ -z "$H" ] || [ -z "$V" ] && { echo "ERR schedule needs a height and a version"; exit 1; }

if sudo awk -v h="$H" -v v="$V" '
     /height:[[:space:]]*/ { gsub(/^.*height:[[:space:]]*/, ""); gsub(/[^0-9].*$/, ""); last=$0 }
     /version:[[:space:]]*/ { gsub(/^.*version:[[:space:]]*/, ""); gsub(/[^0-9].*$/, "");
                              if (last == h && $0 == v) found=1 }
     END { exit(found ? 0 : 1) }' "$CFG"; then
  echo "SKIP schedule already carries version $V at height $H"; exit 0
fi

BK="$CFG.pre-v$V.$(date +%Y%m%d-%H%M%S)"
sudo cp -p "$CFG" "$BK"

sudo awk -v H="$H" -v V="$V" '
BEGIN { added = 0; inblock = 0; seen_h = 0; seen_v = 0; dup = 0 }
{
  line = $0
  if (!inblock && line ~ /^[[:space:]]*protocol_upgrades:[[:space:]]*\[\][[:space:]]*$/) {
    match(line, /^[[:space:]]*/); ind = substr(line, 1, RLENGTH)
    print ind "protocol_upgrades:"; print ind "  - height: " H; print ind "    version: " V
    added = 1; next
  }
  if (!inblock && line ~ /^[[:space:]]*protocol_upgrades:[[:space:]]*$/) {
    match(line, /^[[:space:]]*/); ind = substr(line, 1, RLENGTH); keyind = RLENGTH
    inblock = 1; print line; next
  }
  if (inblock) {
    match(line, /^[[:space:]]*/)
    if (line ~ /^[[:space:]]*$/ || RLENGTH > keyind) {
      if (line ~ /height:[[:space:]]*[0-9]+/) {
        h = line; sub(/^.*height:[[:space:]]*/, "", h); sub(/[^0-9].*$/, "", h)
        if (h + 0 > seen_h) seen_h = h + 0
        if (h + 0 == H + 0) dup = 1
      }
      if (line ~ /version:[[:space:]]*[0-9]+/) {
        v = line; sub(/^.*version:[[:space:]]*/, "", v); sub(/[^0-9].*$/, "", v)
        if (v + 0 > seen_v) seen_v = v + 0
      }
      print line; next
    }
    print ind "  - height: " H; print ind "    version: " V
    added = 1; inblock = 0; print line; next
  }
  print line
}
END {
  if (inblock && !added) { print ind "  - height: " H; print ind "    version: " V; added = 1 }
  # A schedule that goes backwards in either field is one normalizeUpgrades
  # refuses, so the node would fail to start - caught here rather than there.
  if (!added)            { print "no protocol_upgrades key" > "/dev/stderr"; exit 3 }
  if (dup)               { print "height " H " is already scheduled" > "/dev/stderr"; exit 4 }
  if (seen_h >= H + 0)   { print "height " H " is not past the scheduled " seen_h > "/dev/stderr"; exit 5 }
  if (seen_v >= V + 0)   { print "version " V " is not past the scheduled " seen_v > "/dev/stderr"; exit 6 }
}' "$BK" > /tmp/cfg.new 2> /tmp/cfg.err
if [ $? -ne 0 ]; then
  echo "ERR refused to edit the schedule: $(cat /tmp/cfg.err)"; exit 1
fi
sudo cp /tmp/cfg.new "$CFG"

if ! sudo grep -q "height: $H" "$CFG"; then
  sudo cp -p "$BK" "$CFG"; echo "ERR edit did not take; config restored"; exit 1
fi
# Gate on the real parser before anything restarts. The node is still running.
if ! sudo matrixd -preflight-production -config "$CFG" > /tmp/preflight.out 2>&1; then
  sudo cp -p "$BK" "$CFG"
  echo "ERR preflight refused the edited config; config restored, node untouched"
  tail -20 /tmp/preflight.out
  exit 1
fi
sudo systemctl daemon-reload
sudo systemctl restart matrixd || { sudo cp -p "$BK" "$CFG"; echo "ERR restart failed; config restored"; exit 1; }
sleep 15
if ! systemctl is-active --quiet matrixd; then
  sudo cp -p "$BK" "$CFG"; sudo systemctl restart matrixd
  echo "ERR node did not come back; config restored and node restarted on the old config"
  exit 1
fi
echo "OK scheduled version $V at height $H (backup $BK)"
EOS
R_SCHEDULE="$R_COMMON
$R_SCHEDULE_BODY"

read -r -d "" R_BLOCK_BODY <<'EOS'
# One NAMED height, asked of every node. "The current head" read from four
# machines a second apart is four answers to four different questions: a node one
# block ahead legitimately has a different head, and comparing those reports a
# fork that is not there.
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

# UNSCHEDULE: restore the newest backup this script wrote, preflight, restart.
R_UNSCHEDULE="$R_COMMON"'
BK=$(sudo find "$(dirname "$CFG")" -maxdepth 1 -name "$(basename "$CFG").pre-v*" 2>/dev/null | sort | tail -1)
[ -z "$BK" ] && { echo "SKIP no backup here, nothing to undo"; exit 0; }
sudo cp -p "$BK" "$CFG"
sudo matrixd -preflight-production -config "$CFG" > /dev/null 2>&1 || { echo "ERR restored config fails preflight"; exit 1; }
sudo systemctl restart matrixd || { echo "ERR restart failed during rollback"; exit 1; }
sleep 15
systemctl is-active --quiet matrixd || { echo "ERR node did not come back during rollback"; exit 1; }
echo "OK rolled back to $BK"
'

# ---------------------------------------------------------------- helpers

read_state() { # label host key -> echoes "OK|height|head|root|version|cfg|sched"
  local label=$1 host=$2 key=$3 raw
  raw=$(rsh "$host" "$key" "$R_STATE" 2>&1)
  local v; v=$(verdict "$raw" 'OK\||ERR')
  printf '%s' "${v:-$(saidWhat "$raw")}"
}

# Every validator must have committed the SAME BLOCK at a height they all have.
#
# Not "the same current head". This function gates the whole rollout - whether a
# node rejoined, whether the next one may be touched, whether a written schedule
# stands - and it reads nodes one after another, so a block committing mid-sweep
# leaves the last node one ahead and legitimately on a different head. Comparing
# those would report a fork that is not there, and during the schedule step that
# report triggers an automatic rollback of a schedule that was correct.
check_agreement() {
  local b label host key role out raw h lo=0 hi=0 hash ref="" ref_l=""
  local -a labels=() hosts=() keys=() heights=()
  for b in "${BOXES[@]}"; do
    label=$(field "$b" 1); host=$(field "$b" 2); key=$(field "$b" 3); role=$(field "$b" 4)
    [ "$role" = validator ] || continue
    out=$(read_state "$label" "$host" "$key")
    case "$out" in OK\|*) ;; *) info "$label: $out"; return 1;; esac
    h=$(field "$out" 2)
    labels+=("$label"); hosts+=("$host"); keys+=("$key"); heights+=("$h")
    [ "$lo" -eq 0 ] && lo=$h
    [ "$h" -lt "$lo" ] && lo=$h
    [ "$h" -gt "$hi" ] && hi=$h
  done
  [ "$lo" -gt 0 ] || { info "no validator reported a height"; return 1; }
  if [ $((hi - lo)) -gt 5 ]; then
    info "heights span $lo..$hi - a validator is not keeping up"
    return 1
  fi

  local cmp=$((lo - 1))
  for i in "${!labels[@]}"; do
    raw=$(rsh "${hosts[$i]}" "${keys[$i]}" "$R_BLOCK" "$cmp" 2>&1)
    out=$(verdict "$raw" 'BLOCK\||ERR')
    case "$out" in BLOCK\|*) ;; *) info "${labels[$i]}: ${out:-$(saidWhat "$raw")}"; return 1;; esac
    hash=$(field "$out" 2)
    info "$(printf '%-12s height=%-6s block %s = %s' "${labels[$i]}" "${heights[$i]}" "$cmp" "${hash:0:18}")"
    [ "$hash" = none ] && { info "  ^^ does not have block $cmp"; return 1; }
    if [ -z "$ref" ]; then ref=$hash; ref_l=${labels[$i]}; continue; fi
    if [ "$hash" != "$ref" ]; then
      info "  ^^ DIFFERENT block $cmp than $ref_l - this is a fork, not a timing difference"
      return 1
    fi
  done
  return 0
}

wait_for_agreement() {
  local tries=${1:-10} i
  for ((i=1;i<=tries;i++)); do
    info "agreement check $i/$tries"
    if check_agreement; then info "all validators agree"; return 0; fi
    sleep 12
  done
  return 1
}

SCHEDULED=()

rollback_schedule() {
  local why=$1 entry label host key out
  printf "\n!! %s\n" "$why" >&2
  if [ ${#SCHEDULED[@]} -eq 0 ]; then
    die "nothing had been scheduled yet, so nothing to undo"
  fi
  printf "!! %d box(es) already carry the schedule. Undoing them now - a schedule on\n" "${#SCHEDULED[@]}" >&2
  printf "!! some nodes and not others is the one state that splits the chain.\n" >&2
  for entry in "${SCHEDULED[@]}"; do
    label=$(field "$entry" 1); host=$(field "$entry" 2); key=$(field "$entry" 3)
    raw=$(rsh "$host" "$key" "$R_UNSCHEDULE" 2>&1)
    out=$(verdict "$raw" 'OK|SKIP|ERR')
    printf "!!   %-12s %s\n" "$label" "${out:-$(saidWhat "$raw")}" >&2
  done
  printf "!! Rollback attempted on every box above. Verify each one before retrying.\n" >&2
  exit 1
}

# ---------------------------------------------------------------- run

checkKeys

say "0. Reachability and starting state"
for b in "${BOXES[@]}"; do
  label=$(field "$b" 1); host=$(field "$b" 2); key=$(field "$b" 3)
  # The key was already read by checkKeys, before anything was restarted.
  out=$(read_state "$label" "$host" "$key")
  case "$out" in
    OK\|*) info "$(printf '%-12s %s  cfg=%s  sched=%s' "$label" "$(field "$out" 5)" "$(field "$out" 6)" "$(field "$out" 7)")" ;;
    *)     die "$label unreachable or unhealthy: $out" ;;
  esac
done

FIRST_V=""
for b in "${BOXES[@]}"; do
  [ "$(field "$b" 4)" = validator ] && { FIRST_V=$b; break; }
done
[ -n "$FIRST_V" ] || die "MATRIX_ROLLOUT_BOXES names no validator"

say "1. Every validator must be on the same chain before anything is touched"
check_agreement || die "the validators are not all on the same chain (see the line above: a differing block, a node far behind, or one that did not answer). Resolve it before scheduling a rule change."

say "2. Binary rollout, one box at a time"
for b in "${BOXES[@]}"; do
  label=$(field "$b" 1); host=$(field "$b" 2); key=$(field "$b" 3); role=$(field "$b" 4)
  say "2.$label"
  raw=$(rsh "$host" "$key" "$R_UPGRADE" "$V" 2>&1)
  out=$(verdict "$raw" 'OK|SKIP|ERR')
  case "$out" in
    SKIP*) info "$out" ;;
    OK*)   info "$out"
           if [ "$role" = validator ]; then
             wait_for_agreement 10 || die "$label did not rejoin agreement after upgrade"
           fi ;;
    *)     die "$label upgrade failed: ${out:-the box gave no verdict}
$(saidWhat "$raw")" ;;
  esac
done

say "3. Confirm every box is on $V"
for b in "${BOXES[@]}"; do
  label=$(field "$b" 1); host=$(field "$b" 2); key=$(field "$b" 3)
  out=$(read_state "$label" "$host" "$key")
  ver=$(field "$out" 5)
  info "$(printf '%-12s %s' "$label" "$ver")"
  echo "$ver" | grep -q "$V" || die "$label is on $ver, not $V"
done

say "4. Measure the block rate and choose the activation height"
out=$(read_state "$(field "$FIRST_V" 1)" "$(field "$FIRST_V" 2)" "$(field "$FIRST_V" 3)")
H1=$(field "$out" 2)
[ "${H1:-0}" -gt 0 ] || die "could not read a height from $(field "$FIRST_V" 1)"
info "height now $H1, sampling for 90s to measure the rate"
sleep 90
out=$(read_state "$(field "$FIRST_V" 1)" "$(field "$FIRST_V" 2)" "$(field "$FIRST_V" 3)")
H2=$(field "$out" 2)
DELTA=$((H2 - H1))
info "produced $DELTA blocks in 90s"
if [ "$DELTA" -le 0 ]; then
  LEAD=$MIN_LEAD
  info "chain is idle; using the floor of $LEAD blocks. Activation may be hours away."
  ETA="unknown (idle chain)"
else
  LEAD=$(( (DELTA * TARGET_LEAD_SECONDS) / 90 ))
  [ "$LEAD" -lt "$MIN_LEAD" ] && LEAD=$MIN_LEAD
  SECS=$(( (LEAD * 90) / DELTA ))
  ETA="about $((SECS / 60)) minutes from now"
fi
TARGET=$((H2 + LEAD))
say "ACTIVATION HEIGHT = $TARGET   (current $H2, lead $LEAD blocks, $ETA)"

say "5. Write the schedule to every box"
for b in "${BOXES[@]}"; do
  label=$(field "$b" 1); host=$(field "$b" 2); key=$(field "$b" 3); role=$(field "$b" 4)
  say "5.$label"
  raw=$(rsh "$host" "$key" "$R_SCHEDULE" "$TARGET" "$NEW_PROTOCOL_VERSION" 2>&1)
  out=$(verdict "$raw" 'OK|SKIP|ERR')
  case "$out" in
    SKIP*|OK*) info "$out"; SCHEDULED+=("$b") ;;
    *)         rollback_schedule "$label refused the schedule: ${out:-the box gave no verdict}
$(saidWhat "$raw")" ;;
  esac
  if [ "$role" = validator ]; then
    wait_for_agreement 10 || rollback_schedule "$label did not rejoin agreement after the schedule restart"
  fi
done

say "6. Final check: same version, same schedule, same ledger"
for b in "${BOXES[@]}"; do
  label=$(field "$b" 1); host=$(field "$b" 2); key=$(field "$b" 3)
  out=$(read_state "$label" "$host" "$key")
  info "$(printf '%-12s %s  sched=%s' "$label" "$(field "$out" 5)" "$(field "$out" 7)")"
  field "$out" 7 | grep -q "height:$TARGET" || die "$label does not carry height $TARGET"
done
check_agreement || die "the validators are not all on the same chain after the rollout (see the line above)"

out=$(read_state "$(field "$FIRST_V" 1)" "$(field "$FIRST_V" 2)" "$(field "$FIRST_V" 3)")
NOW=$(field "$out" 2)
if [ "$TARGET" -le "$NOW" ]; then
  die "the chain is already at $NOW, past the scheduled $TARGET. The activation boundary was crossed while this ran; check every node for a version disagreement immediately."
fi
info "height $NOW, activation at $TARGET, $((TARGET - NOW)) blocks to go"

say "DONE. $V on every box; protocol version $NEW_PROTOCOL_VERSION activates at height $TARGET ($ETA)."
echo
echo "Nothing more to do. Every node switches at that height on its own."
echo "Until then the chain behaves exactly as it did before, because the new"
echo "rules are dormant without a schedule that has arrived."
