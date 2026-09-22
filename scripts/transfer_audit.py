#!/usr/bin/env python3
"""Walk every committed transfer from outside and answer two questions.

WHY THIS EXISTS AS A SCRIPT. The audit that cleared PR #23 was done by hand:
somebody read the history off three validators and compared it. That is the wrong
shape for a check that has to be re-run before protocol version 4 activates,
because after the activation a stranded balance can only have arrived BEFORE the
height - so the audit stops being repeatable and becomes history. A hand-run
check that nobody can repeat identically is not a baseline.

WHAT IT ANSWERS.

1. DID ANY MONEY GO TO AN ACCOUNT NOBODY CAN SPEND FROM? An `eth:` account is
   keyed by the lowercase address, but every wallet DISPLAYS the EIP-55 mixed-case
   form. A transfer to the displayed form succeeds and credits a key no private
   key controls, and it cannot be undone by anyone, ever. So every `eth:`
   recipient in history is held to the one form the ledger keys an account by:
   "eth:0x" followed by exactly 40 lowercase hex characters.

2. DO THE VALIDATORS AGREE ABOUT WHAT HAPPENED? A digest over the ordered history
   from each node, compared. Divergence here is a different and much larger
   problem than one stranded transfer, and it is invisible from a single node -
   which is the whole reason this reads all three.

WHAT IT DELIBERATELY DOES NOT DO. It needs no keys and changes nothing: the
history endpoint is public, and this only reads. It does not judge whether a
stranded transfer should be refunded - it cannot be, which is the point.

AN ERROR RESPONSE CAN LOOK LIKE AN ANSWER. Connect returns errors as JSON, so a
refusal parsed with `.get("transactions") or []` becomes an empty list,
indistinguishable from a genuinely empty history. Every read below is judged on
whether the KEY is present, never on the count.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
import urllib.error
import urllib.request

DEFAULT_NODES = [
    "https://validator-1.ecirlabs.com",
    "https://validator-2.ecirlabs.com",
    "https://validator-3.ecirlabs.com",
]

LIST_PATH = "/matrix.market.v1.MarketService/ListTransactions"
PAGE = 200

# The one form the ledger keys an Ethereum-controlled account by. Anchored, and
# lowercase-only: a mixed-case body is what a wallet shows and what strands money.
CANONICAL_ETH = re.compile(r"^eth:0x[0-9a-f]{40}$")


class ReadFailed(Exception):
    """A node did not answer with a history. Never confused with an empty one."""


def post(node: str, path: str, body: dict, timeout: float) -> dict:
    req = urllib.request.Request(
        node.rstrip("/") + path,
        data=json.dumps(body).encode(),
        headers={"content-type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode()
    except urllib.error.HTTPError as exc:
        raise ReadFailed(f"HTTP {exc.code}: {exc.read().decode()[:200]}") from exc
    except Exception as exc:  # network, TLS, DNS
        raise ReadFailed(str(exc)[:200]) from exc
    try:
        return json.loads(raw)
    except ValueError:
        raise ReadFailed(f"not JSON: {raw.splitlines()[0][:160] if raw else 'empty body'}")


def history(node: str, timeout: float) -> list[dict]:
    """Every committed transfer, in index order, or ReadFailed.

    THE CURSOR IS `startIndex`, NOT AN OFFSET. The field was re-purposed from
    start_height and the old name is reserved, so a request naming anything else
    is not rejected - it is IGNORED, and the server returns page one again. The
    first version of this script sent `offset` and looped on the same 200 rows
    until it was killed. A wrong cursor name therefore does not fail, it hangs,
    which is why the guard below exists rather than trusting the page size.
    """
    out: list[dict] = []
    start = 0
    seen_indices: set[str] = set()
    while True:
        doc = post(node, LIST_PATH, {"startIndex": start, "limit": PAGE}, timeout)
        if "transactions" not in doc:
            # The distinction this whole function exists to keep: a refusal names
            # no transactions key, an empty history names it with an empty list.
            raise ReadFailed(f"no 'transactions' in the answer: {json.dumps(doc)[:200]}")
        page = doc["transactions"] or []
        if not page:
            break
        fresh = [t for t in page if str(t.get("index")) not in seen_indices]
        if not fresh:
            raise ReadFailed(
                f"the cursor did not advance past index {start}: the server returned "
                f"{len(page)} transfers it had already sent. The request's cursor field "
                f"is being ignored, so this would page for ever"
            )
        for t in fresh:
            seen_indices.add(str(t.get("index")))
        out.extend(fresh)
        if len(page) < PAGE:
            break
        start += len(page)
    out.sort(key=lambda t: int(t.get("index", 0)))
    return out


def digest(transfers: list[dict]) -> str:
    """A stable digest over what each node believes happened.

    Only the fields that are the transfer itself. A timestamp is in the block and
    identical everywhere, but including anything a node could format differently
    would turn a formatting difference into a false fork report.
    """
    h = hashlib.sha256()
    for t in transfers:
        h.update(
            "|".join(
                str(t.get(k, "")) for k in ("index", "from", "to", "amount", "nonce", "blockHeight")
            ).encode()
        )
        h.update(b"\n")
    return h.hexdigest()[:16]


def stranded(transfers: list[dict]) -> list[dict]:
    """Transfers whose recipient is an eth id in a form no key controls."""
    bad = []
    for t in transfers:
        to = str(t.get("to", ""))
        if not to.strip().lower().startswith("eth:"):
            continue
        if CANONICAL_ETH.match(to):
            continue
        bad.append(t)
    return bad


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--node", action="append", dest="nodes", metavar="URL",
                    help="a node to read (repeatable; default: the three public validators)")
    ap.add_argument("--timeout", type=float, default=20.0)
    args = ap.parse_args()
    nodes = args.nodes or DEFAULT_NODES

    print("== Reading every committed transfer, from outside, with no keys\n")

    read: dict[str, list[dict]] = {}
    for node in nodes:
        try:
            transfers = history(node, args.timeout)
        except ReadFailed as exc:
            print(f"   {node:<42} UNREADABLE  {exc}")
            continue
        read[node] = transfers
        print(f"   {node:<42} {len(transfers)} transfers  digest={digest(transfers)}")

    if not read:
        print("\n== Verdict\n   No node answered with a history. Nothing was audited.")
        return 2

    print("\n== 1. Did any money go to an account nobody can spend from?\n")
    any_stranded = False
    for node, transfers in read.items():
        bad = stranded(transfers)
        if not bad:
            print(f"   {node:<42} none of its eth recipients is non-canonical")
            continue
        any_stranded = True
        print(f"   {node:<42} {len(bad)} STRANDED:")
        for t in bad:
            print(f"      index {t.get('index')} block {t.get('blockHeight')} "
                  f"amount {t.get('amount')} -> {t.get('to')!r}")

    eth_counts = {n: sum(1 for t in ts if str(t.get("to", "")).startswith("eth:")) for n, ts in read.items()}
    print(f"\n   eth recipients seen: " + ", ".join(f"{n.split('//')[-1]}={c}" for n, c in eth_counts.items()))

    print("\n== 2. Do the validators agree about what happened?\n")
    digests = {node: digest(ts) for node, ts in read.items()}
    agreed = len(set(digests.values())) == 1
    for node, d in digests.items():
        print(f"   {node:<42} {d}")
    if agreed:
        print(f"\n   All {len(digests)} node(s) returned the same history digest.")
    else:
        print("\n   THE NODES DISAGREE. This is a divergence, not a stranded transfer,")
        print("   and it is a larger problem than the one this audit was written for.")

    print("\n== Verdict")
    if any_stranded:
        print("   Money is stranded. It cannot be recovered - there is no key to sign with.")
        print("   Protocol version 4 would refuse such a transfer in future, and does not")
        print("   undo these. Decide what to record about them before activating it.")
        return 1
    if not agreed:
        print("   No stranded transfer, but the nodes do not agree on history. Resolve that")
        print("   before scheduling any rule change.")
        return 1
    if len(read) < len(nodes):
        print("   Clean on every node that answered, but not every node answered. An audit")
        print("   that skipped a node is not a baseline; re-run it when all are reachable.")
        return 1
    print("   Clean. No transfer has gone to a non-canonical eth recipient, and every")
    print("   node agrees on the whole history. Safe to schedule protocol version 4.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
