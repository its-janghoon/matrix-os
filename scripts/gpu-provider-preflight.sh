#!/usr/bin/env bash
#
# Check a GPU box before it starts selling, from the box itself.
#
# WHY THIS EXISTS. Every failure this looks for was found by running a real
# provider, and none of them announce themselves. A node with no backend starts
# fine and sells nothing. A node with the wrong genesis connects to its peers,
# never advances, and reports that two nodes "reached different balances" -
# several layers from the config line that caused it. A payout id that is not an
# account nobody holds the key to still settles, and the money is simply gone.
# A model server that reports no usage counts settles at an approximation of
# somebody else's tokeniser. Each is cheap here and expensive after the first
# start: genesis is applied once and recorded, so getting it wrong costs a store
# deletion and a resync.
#
# WHAT IT DOES NOT DO. It writes nothing, starts nothing, and settles nothing.
# It does not check that other hosts can reach your P2P port - no box can answer
# that about itself - and it prints the command for that instead.
#
# Run it as the user the node will run as, with the node's environment loaded.
# A key that is in your shell and not in the unit's EnvironmentFile is a pass
# here and a failed start later.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  cat <<'USAGE'
usage: scripts/gpu-provider-preflight.sh <config.yaml> [options]

  <config.yaml>     the node config this box will start with
  --skip-model      do not send a probe prompt to the model server
  --skip-peers      do not try to reach the bootstrap peers
  --matrixd PATH    the matrixd to run the production preflight with
                    (default: matrixd on PATH)

Exit status is 0 when nothing failed. Warnings do not fail the run: they are
decisions to make deliberately, not mistakes.
USAGE
}

CONFIG=""
SKIP_MODEL=0
SKIP_PEERS=0
MATRIXD="${MATRIXD:-matrixd}"

while [ $# -gt 0 ]; do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    --skip-model) SKIP_MODEL=1; shift ;;
    --skip-peers) SKIP_PEERS=1; shift ;;
    --matrixd) MATRIXD="${2:-}"; shift 2 ;;
    -*) printf 'unknown option: %s\n\n' "$1" >&2; usage >&2; exit 2 ;;
    *) CONFIG="$1"; shift ;;
  esac
done

[ -n "$CONFIG" ] || { usage >&2; exit 2; }
[ -r "$CONFIG" ] || { printf 'cannot read %s\n' "$CONFIG" >&2; exit 2; }

FAILURES=0
WARNINGS=0

step() { printf '\n== %s\n' "$1"; }
pass() { printf '   ok    %s\n' "$*"; }
warn() { WARNINGS=$((WARNINGS + 1)); printf '   warn  %s\n' "$*"; }
bad()  { FAILURES=$((FAILURES + 1)); printf '   FAIL  %s\n' "$*"; }
note() { printf '         %s\n' "$*"; }

# ------------------------------------------------------------------ the tools
step "the tools this needs"

command -v python3 >/dev/null 2>&1 || {
  printf 'this needs python3 to read the config\n' >&2
  exit 2
}
command -v curl >/dev/null 2>&1 || { printf 'this needs curl\n' >&2; exit 2; }

# Both binaries, because a daemon its operator cannot inspect is a node nobody
# is watching. Two upgrades in a row replaced matrixd and left the CLI behind,
# and the gap is invisible until a command that exists in the new release is
# missing from the old one.
if command -v "$MATRIXD" >/dev/null 2>&1; then
  MATRIXD_VERSION="$("$MATRIXD" -version 2>&1 | head -1)"
  pass "matrixd: $MATRIXD_VERSION"
else
  bad "matrixd is not on PATH (pass --matrixd PATH), so the production preflight cannot run"
  MATRIXD_VERSION=""
fi

if command -v matrix >/dev/null 2>&1; then
  # One dash on matrixd and two on matrix: the daemon uses the standard flag
  # package and the CLI is a cobra command. `matrix -version` fails with
  # "unknown shorthand flag: 'e' in -ersion", which reads like a broken binary.
  MATRIX_VERSION="$(matrix --version 2>&1 | head -1)"
  pass "matrix:  $MATRIX_VERSION"
  if [ -n "$MATRIXD_VERSION" ]; then
    MATRIXD_NUM="$(printf '%s' "$MATRIXD_VERSION" | grep -oE 'v?[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true)"
    MATRIX_NUM="$(printf '%s' "$MATRIX_VERSION" | grep -oE 'v?[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true)"
    if [ -n "$MATRIXD_NUM" ] && [ -n "$MATRIX_NUM" ] && [ "$MATRIXD_NUM" != "$MATRIX_NUM" ]; then
      bad "matrixd is $MATRIXD_NUM and matrix is $MATRIX_NUM. One of them was replaced by hand."
    fi
  fi
else
  warn "matrix is not on PATH. It is how you read what the daemon is doing; a release archive carries both."
fi

# ----------------------------------------------------------------- the config
step "the config, read as the node reads it"

INSPECTION="$(python3 "$ROOT/scripts/gpu_provider_preflight.py" "$CONFIG")" || exit 2

FINDINGS="$(printf '%s' "$INSPECTION" | python3 -c '
import json, sys
for f in json.load(sys.stdin)["findings"]:
    print("%s\t%s\t%s" % (f["level"], f["label"], f["detail"]))
')"

while IFS="$(printf '\t')" read -r LEVEL LABEL DETAIL; do
  [ -n "$LEVEL" ] || continue
  case "$LEVEL" in
    PASS) pass "$LABEL${DETAIL:+: $DETAIL}" ;;
    WARN) warn "$LABEL: $DETAIL" ;;
    FAIL) bad  "$LABEL: $DETAIL" ;;
  esac
done <<< "$FINDINGS"

# --------------------------------------------------------- production preflight
# Delegated rather than reimplemented. The node's own rules are the ones that
# decide whether it starts, and a second copy of them here would drift.
if [ -n "$MATRIXD_VERSION" ]; then
  step "the node's own production preflight"
  if PREFLIGHT="$("$MATRIXD" -preflight-production -config "$CONFIG" 2>&1)"; then
    pass "passed"
  else
    bad "refused this config:"
    printf '%s\n' "$PREFLIGHT" | sed 's|^|         |'
  fi
fi

# -------------------------------------------------------------------- the GPU
step "the GPU"

if command -v nvidia-smi >/dev/null 2>&1; then
  GPUS="$(nvidia-smi --query-gpu=name,memory.total,driver_version --format=csv,noheader 2>&1 || true)"
  if [ -n "$GPUS" ]; then
    while IFS= read -r LINE; do
      [ -n "$LINE" ] && pass "$LINE"
    done <<< "$GPUS"
  else
    bad "nvidia-smi is installed and reported no GPU"
  fi
else
  warn "no nvidia-smi. Expected if you are reselling a vendor's API rather than serving your own weights."
fi

# ----------------------------------------------------------- the model server
step "the model server"

BACKEND_LINES="$(printf '%s' "$INSPECTION" | python3 -c '
import json, sys
for b in json.load(sys.stdin)["probe"]["backends"]:
    print("%s\t%s\t%s\t%s\t%s\t%s" % (
        b["id"], b["kind"], b["base_url"], b["api_key_env"], b["model"],
        b["health_check_path"]))
')"

if [ -z "$BACKEND_LINES" ]; then
  note "no backend to probe"
fi

while IFS="$(printf '\t')" read -r BID BKIND BURL BKEYENV BMODEL BHEALTH; do
  [ -n "$BID" ] || continue

  # The key has to be in the NODE's environment, not only in this shell. Print
  # the length rather than the value: a preflight that puts a secret in the
  # scrollback has traded one problem for another.
  if [ -n "$BKEYENV" ]; then
    KEYVAL="${!BKEYENV:-}"
    if [ -n "$KEYVAL" ]; then
      pass "$BID: \$$BKEYENV is set here (${#KEYVAL} chars)"
      note "the node reads it from ITS environment. Under systemd that is EnvironmentFile=, root-owned and 0600."
    else
      bad "$BID: \$$BKEYENV is empty in this shell. The backend fails construction on an empty key and the node will not start."
    fi
  fi

  [ "$SKIP_MODEL" = "0" ] || { note "$BID: probe skipped"; continue; }
  [ -n "$BURL" ] || { note "$BID: no base_url to probe"; continue; }

  if [ -n "$BHEALTH" ]; then
    CODE="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "${BURL%/}${BHEALTH}" || echo 000)"
    case "$CODE" in
      200) pass "$BID: ${BHEALTH} answers 200, which is what suspends the listing when it stops" ;;
      000) bad  "$BID: ${BURL%/}${BHEALTH} did not answer. The node suspends this provider on the first failed probe." ;;
      *)   bad  "$BID: ${BHEALTH} answered $CODE, so the node will treat this backend as down" ;;
    esac
  fi

  # One real completion. It is what settlement is computed from, so the counts
  # matter more than the text: a missing or zero `usage` means the node derives
  # the number locally from an approximation of somebody else's tokeniser.
  if [ "$BKIND" = "openai" ]; then
    PROBE_URL="${BURL%/}/v1/chat/completions"
    BODY="$(curl -s --max-time 60 "$PROBE_URL" \
      ${BKEYENV:+-H "Authorization: Bearer ${!BKEYENV:-}"} \
      -H 'Content-Type: application/json' \
      -d "{\"model\":\"$BMODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"max_tokens\":8}" \
      || true)"
  else
    PROBE_URL="${BURL%/}/api/chat"
    BODY="$(curl -s --max-time 60 "$PROBE_URL" \
      -H 'Content-Type: application/json' \
      -d "{\"model\":\"$BMODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"stream\":false}" \
      || true)"
  fi

  if [ -z "$BODY" ]; then
    bad "$BID: $PROBE_URL did not answer. Nothing can be sold through a backend that is not there."
    continue
  fi

  USAGE="$(printf '%s' "$BODY" | python3 -c '
import json, sys
try:
    body = json.load(sys.stdin)
except Exception:
    print("unparseable"); raise SystemExit
if "error" in body:
    err = body["error"]
    print("error\t%s" % (err.get("message") if isinstance(err, dict) else err))
    raise SystemExit
usage = body.get("usage") or {}
prompt = usage.get("prompt_tokens", body.get("prompt_eval_count", 0)) or 0
completion = usage.get("completion_tokens", body.get("eval_count", 0)) or 0
print("counts\t%d\t%d" % (prompt, completion))
' 2>/dev/null || echo unparseable)"

  case "$USAGE" in
    counts*)
      PROMPT_TOKENS="$(printf '%s' "$USAGE" | cut -f2)"
      COMPLETION_TOKENS="$(printf '%s' "$USAGE" | cut -f3)"
      if [ "$PROMPT_TOKENS" -gt 0 ] && [ "$COMPLETION_TOKENS" -gt 0 ]; then
        pass "$BID: answered as \"$BMODEL\", reporting $PROMPT_TOKENS + $COMPLETION_TOKENS tokens"
        note "one unit is one token, and those two numbers summed are what settles."
      else
        warn "$BID: answered but reported $PROMPT_TOKENS + $COMPLETION_TOKENS tokens. The node then derives the count itself, which is an approximation of somebody else's tokeniser."
      fi
      ;;
    error*)
      bad "$BID: the model server refused: $(printf '%s' "$USAGE" | cut -f2)"
      note "if it names the model, \"$BMODEL\" does not match --served-model-name."
      ;;
    *)
      bad "$BID: $PROBE_URL answered something that is not a completion"
      ;;
  esac
done <<< "$BACKEND_LINES"

# ------------------------------------------------------------- what is listening
step "what this box is listening on"

if command -v ss >/dev/null 2>&1; then
  LISTENING="$(ss -ltnH 2>/dev/null || true)"
  # The model server has no auth worth exposing and no metering. The node in
  # front of it is the thing that authenticates and charges, so a model server
  # on a public interface is free inference for whoever finds the port.
  while IFS="$(printf '\t')" read -r BID BKIND BURL _ _ _; do
    [ -n "$BURL" ] || continue
    MPORT="$(printf '%s' "$BURL" | sed -E 's|^[a-z]+://||; s|/.*$||' | awk -F: '{print $2}')"
    [ -n "$MPORT" ] || continue
    EXPOSED="$(printf '%s' "$LISTENING" | awk -v p=":$MPORT" '$4 ~ p && $4 !~ /^(127\.|\[::1\])/ {print $4}' | head -1 || true)"
    if [ -n "$EXPOSED" ]; then
      bad "the model server is bound to $EXPOSED, not loopback. That is free inference for anyone who reaches the port."
    else
      pass "the model server is not listening off loopback"
    fi
  done <<< "$BACKEND_LINES"

  ADMIN_EXPOSED="$(printf '%s' "$LISTENING" | awk '$4 ~ /:9090$/ && $4 !~ /^(127\.|\[::1\])/ {print $4}' | head -1 || true)"
  if [ -n "$ADMIN_EXPOSED" ]; then
    bad "something is serving the admin port on $ADMIN_EXPOSED. Its key can move funds."
  fi
else
  warn "no ss, so what is bound where could not be checked. Confirm the model server is on 127.0.0.1 only."
fi

# ------------------------------------------------------------ the bootstrap peers
step "reaching the network"

PEERS="$(printf '%s' "$INSPECTION" | python3 -c '
import json, re, sys
for p in json.load(sys.stdin)["probe"]["bootstrap_peers"]:
    m = re.search(r"/ip4/([0-9.]+)/tcp/(\d+)", p) or re.search(r"/dns[46]?/([^/]+)/tcp/(\d+)", p)
    if m:
        print("%s\t%s" % (m.group(1), m.group(2)))
')"

if [ "$SKIP_PEERS" = "1" ]; then
  note "skipped"
elif [ -z "$PEERS" ]; then
  bad "no bootstrap peer carries an address this can reach"
else
  REACHED=0
  while IFS="$(printf '\t')" read -r PHOST PPORT; do
    [ -n "$PHOST" ] || continue
    if timeout 8 bash -c "exec 3<>/dev/tcp/$PHOST/$PPORT" 2>/dev/null; then
      pass "outbound to $PHOST:$PPORT"
      REACHED=$((REACHED + 1))
    else
      warn "cannot reach $PHOST:$PPORT. Check their security group allows this box, and yours allows the outbound."
    fi
  done <<< "$PEERS"
  [ "$REACHED" -gt 0 ] || bad "no bootstrap peer is reachable, so this node cannot join"
fi

# Inbound is the half no box can answer about itself: a local bind proves the
# socket, not the path through the NAT and the security group.
LISTEN_ADDR="$(printf '%s' "$INSPECTION" | python3 -c 'import json,sys; print(json.load(sys.stdin)["probe"]["listen_addr"])')"
P2P_PORT="$(printf '%s' "$LISTEN_ADDR" | grep -oE '/tcp/[0-9]+' | head -1 | cut -d/ -f3 || true)"
P2P_PORT="${P2P_PORT:-9000}"
note ""
note "Inbound on $P2P_PORT cannot be checked from here. After the node is up, from another host:"
note "    nc -vz <this box's public IP> $P2P_PORT"
note "And publish this for other operators' bootstrap_peers:"
note "    /ip4/<this box's public IP>/tcp/$P2P_PORT/p2p/\$($MATRIXD -init-identities -config $CONFIG | python3 -c 'import json,sys; print(json.load(sys.stdin)[\"peer_id\"])')"

# ----------------------------------------------------------------- the verdict
printf '\n'
if [ "$FAILURES" -gt 0 ]; then
  printf 'NOT READY: %d failed, %d to decide.\n' "$FAILURES" "$WARNINGS"
  printf 'Genesis is applied once and the fact is recorded, so a wrong first start costs a store deletion and a resync. Fix these first.\n'
  exit 1
fi
if [ "$WARNINGS" -gt 0 ]; then
  printf 'READY, with %d decision%s to make deliberately rather than inherit.\n' \
    "$WARNINGS" "$([ "$WARNINGS" = 1 ] || echo s)"
else
  printf 'READY.\n'
fi
printf 'Nothing here proves buyers can reach you. Test inbound from another host, then buy from yourself once before telling anyone the endpoint exists.\n'
