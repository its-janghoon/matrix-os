#!/usr/bin/env bash
# Ask the public nodes the one question a visitor's browser asks, and say
# plainly whether the answer is a working marketplace.
#
#   ./scripts/shopfront-check.sh [node-url ...]
#
# It needs no keys, no wallet and no ssh, so it runs from a laptop, from CI, or
# from cron. It spends nothing: every call is a read.
#
# WHY IT EXISTS. The public marketplace was empty for a day and nobody knew. The
# nodes were up, consensus was fine, the GPU box was answering prompts, every
# health check anyone had was green - and ecirlabs.com/market showed a
# marketplace with no sellers in it, because a quote's validity window ran out
# and the seller fell out of every directory. Nothing was down, so nothing
# alerted, and the only detector was a person opening the page.
#
# So this checks the thing the page checks, from outside, the way a stranger's
# browser does: over https, with include_remote, against the address the site
# actually reads. A node that is up but selling nothing is a FAILURE here, which
# is the whole point - "the process is running" was never the question.
#
# EXIT CODES. 0 every node offers a seller. 1 the marketplace is genuinely
# empty, or only part of the network can hear the seller. 2 this says nothing -
# a node could not be reached at all. 3 there is stock, and a node asked the
# checker to slow down; nothing is wrong.
#
# WHAT IT DOES NOT CHECK. Whether a seller can actually serve a prompt. That
# costs money and needs a wallet, and escrow-smoke.sh is where it belongs. This
# answers the cheaper question that comes first: is there anything to buy?

set -uo pipefail

# The site's own default, so this checks the address a visitor is actually sent
# to rather than one that only agrees with it by coincidence.
DEFAULT_NODES=(
  https://validator-1.ecirlabs.com
  https://validator-2.ecirlabs.com
  https://validator-3.ecirlabs.com
)

if [ "$#" -gt 0 ]; then
  NODES=("$@")
else
  NODES=("${DEFAULT_NODES[@]}")
fi

TIMEOUT=${MATRIX_SHOPFRONT_TIMEOUT:-15}
LIST=matrix.market.v1.MarketService/ListProviders

say()  { printf "\n== %s\n" "$*"; }
info() { printf "   %s\n" "$*"; }

need() {
  command -v "$1" >/dev/null 2>&1 || { echo "ABORT: $1 is required but not installed" >&2; exit 2; }
}
need curl

# The JSON reader, chosen by RUNNING it rather than by finding its name.
#
# command -v is not enough on Windows. Git Bash inherits the Store's "App
# Execution Alias" for python3: a stub that sits on PATH, satisfies command -v,
# and then prints "Python was not found; run without arguments to install from
# the Microsoft Store" to stdout instead of interpreting anything. That text
# then flows into the parser below as if it were a node's answer, and every node
# reads as UNREACHABLE - so the script reports a total outage on a network that
# is fine, which is the one wrong answer a checker must never give.
PY=""
for candidate in python3 python; do
  command -v "$candidate" >/dev/null 2>&1 || continue
  if [ "$("$candidate" -c 'print("ok")' 2>/dev/null)" = "ok" ]; then PY=$candidate; break; fi
done
if [ -z "$PY" ]; then
  echo "ABORT: no working python was found." >&2
  echo "  A name on PATH is not enough - this checked that it RUNS, and it did not." >&2
  echo "  On Windows this is usually the Microsoft Store alias rather than Python:" >&2
  echo "    Settings > Apps > Advanced app settings > App execution aliases, turn off python/python3," >&2
  echo "    then install Python, or run this from WSL." >&2
  exit 2
fi

# Count the sellers a node will tell a browser about, and describe them.
#
# include_remote is not optional here and is the easiest thing to get wrong: it
# defaults to FALSE, so a plain '{}' asks only for providers the node serves
# itself. A validator serves none, so the honest-looking answer to the wrong
# question is an empty list - which reads exactly like an empty marketplace and
# is why this is spelled out rather than left to a default.
ask() {
  local node=$1
  curl -sS -m "$TIMEOUT" \
    -H 'content-type: application/json' \
    -d '{"includeRemote":true}' \
    "${node%/}/$LIST" 2>/dev/null
}

summarize() {
  "$PY" -c '
import json, sys

raw = sys.stdin.read().strip()
if raw == "":
    print("UNREACHABLE\tno answer")
    sys.exit(0)
try:
    doc = json.loads(raw)
except ValueError:
    # An HTML error page, a proxy notice, a redirect body: whatever it is, it is
    # not a directory, and printing the first line of it says more than "error".
    print("UNREACHABLE\t" + raw.splitlines()[0][:120])
    sys.exit(0)

# A refusal is not an empty shop.
#
# Connect errors come back as JSON too - {"code":"resource_exhausted",
# "message":"too many requests: slow down and retry"} - and that object has no
# "providers" key, so reading it with .get(...) or [] turned every refusal into
# "this node has no sellers". That is the exact false emptiness this script
# exists to catch, reproduced one level down, and it is how a node that was
# rate-limiting a too-eager checker got reported as a dead marketplace.
#
# So the distinction is drawn on the key, not on the count. An answer with no
# providers key never described a directory at all.
if "code" in doc and "providers" not in doc:
    print("REFUSED\t{}: {}".format(doc.get("code"), (doc.get("message") or "")[:100]))
    sys.exit(0)
if "providers" not in doc:
    print("REFUSED\tthe answer carried no directory: " + raw[:90])
    sys.exit(0)

providers = doc.get("providers") or []
if not providers:
    print("EMPTY\tthe node answered with a directory, and it names no sellers")
    sys.exit(0)

lines = []
for p in providers:
    models = ",".join(p.get("models") or []) or "no advertised model"
    lines.append("{}  {}/unit  {} available  [{}]".format(
        (p.get("id") or "?")[:12],
        p.get("pricePerUnit", "?"),
        p.get("available", "?"),
        models,
    ))
print("SELLING\t{}".format(len(providers)))
for line in lines:
    print("\t" + line)
'
}

say "Asking each public node what a visitor would see"
info "(include_remote, the same call the marketplace page makes)"

# Counted separately rather than collapsed into a severity, because the three
# outcomes have three different causes and the verdict below has to name the
# right one. A node that did not answer says NOTHING about whether there is a
# seller, and folding it in with a node that answered "nobody" would blame
# gossip for what is a reachability problem.
selling=0
empty=0
unreachable=0
refused=0
for node in "${NODES[@]}"; do
  out=$(ask "$node" | summarize)
  verdict=$(printf '%s' "$out" | head -1 | cut -f1)
  detail=$(printf '%s' "$out" | head -1 | cut -f2-)
  printf "\n   %-40s %s\n" "$node" "$verdict"
  case "$verdict" in
    SELLING)
      info "$detail seller(s):"
      printf '%s\n' "$out" | tail -n +2 | sed 's/^\t/     - /'
      selling=$((selling + 1))
      ;;
    EMPTY)
      info "$detail"
      empty=$((empty + 1))
      ;;
    REFUSED)
      # Counted with unreachable, not with empty. A node that declined to answer
      # said NOTHING about whether a seller exists, and filing it under "no
      # sellers" is how a rate limit becomes an outage report. Tracked separately
      # as well, because "it turned me away" and "it never spoke" need different
      # advice: one is a checker being too eager, the other is a broken address.
      info "$detail"
      unreachable=$((unreachable + 1))
      refused=$((refused + 1))
      ;;
    *)
      info "$detail"
      unreachable=$((unreachable + 1))
      ;;
  esac
done

say "Verdict"
answered=$((selling + empty))

if [ "$answered" -eq 0 ]; then
  info "FAIL: no node could be reached, so this says nothing about the marketplace."
  info "Check DNS, TLS and that the public address still points where you think."
  exit 2
fi

if [ "$selling" -eq 0 ]; then
  info "FAIL: every node that answered has no seller to offer."
  info "A visitor to the marketplace sees an empty table right now."
  info ""
  info "Those nodes are healthy - they replied - so this is the seller, not consensus."
  info "Most likely its quote's validity window ran out: that takes it out of its"
  info "own order book, which stops the announce loop finding anything to announce,"
  info "and every other node then ages it out. Check the seller with:"
  info ""
  info "  matrix provider list                 # on the seller's own box"
  info "  journalctl -u matrixd | grep 'restated its quote'"
  info ""
  info "A node carrying the quote-renewal fix restates its own quote twice per"
  info "window and logs each one. On a node without it, restarting matrixd"
  info "re-stamps the window from config and brings the seller back."
  exit 1
fi

if [ "$empty" -gt 0 ]; then
  # Worth its own wording. Some nodes selling and others not is a propagation
  # problem, and it is not the same fault as nobody selling at all: the seller
  # is alive and announcing, and part of the network is not hearing it.
  info "FAIL: $selling of $answered answering nodes offer a seller, so part of the network cannot hear one."
  info "The seller is announcing, since somebody heard it. Look at gossip between"
  info "the nodes that hear it and the ones that do not."
  [ "$unreachable" -gt 0 ] && info "($unreachable node(s) did not answer at all and are not counted either way.)"
  exit 1
fi

# Every node that answered has a seller. An unreachable one is still a fault -
# a visitor sent to it sees nothing - but it is a different one, and calling it
# out separately keeps "the marketplace is empty" meaning only that.
if [ "$unreachable" -gt 0 ]; then
  info "The marketplace has stock: every node that answered offers a seller."
  if [ "$refused" -eq "$unreachable" ]; then
    # Nothing is wrong with the network. This is the node telling the checker to
    # slow down, which running it on a loop during a rollout will do.
    info "$refused node(s) turned this check away rather than failing to answer."
    info "That is a rate limit, not an outage - wait a few minutes and run it again."
    exit 3
  fi
  info "$unreachable node(s) did not answer at all."
  [ "$refused" -gt 0 ] && info "($refused of those turned the check away, which is a rate limit and not an outage.)"
  info "Check DNS, TLS and the address for the ones that are silent."
  exit 2
fi

info "Every node offers at least one seller. The shopfront works."
exit 0
