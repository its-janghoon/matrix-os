# Handoff

Where the system is, and what to pick up next. Everything here was run and
observed, not inferred; where something is unverified it says so.

For the long chronological record - adversarial passes, timing side channels,
what to know before changing consensus - see
[`docs/handoff-archive.md`](docs/handoff-archive.md). That file is history and
says so. Operating procedure lives in [`docs/runbooks/`](docs/runbooks/).

**State of `main`: `3fbe110`. Nothing is waiting on a branch.**
Last verified 2026-09-21.

---

## The live network, as of 2026-09-23

Chain `8170`. Protocol version 4 is scheduled at height 4337. The released
binary is `v0.5.8`; the boxes ran `v0.5.7` at the time of writing, so check
what is actually deployed rather than trusting this line — `systemctl show
matrixd -p ActiveEnterTimestamp --value` on a box tells you when it last
restarted, which is when it last took a release.

| box | role | address | config |
|---|---|---|---|
| validator-1..3 | validator | `validator-N.ecirlabs.com` | `/home/ubuntu/config.yaml` |
| validator-4 | validator | `3.36.163.59` (aarch64) | `/home/ubuntu/config.yaml` |
| gpu | seller | `13.220.232.122` (x86_64) | `/etc/matrix/config.yaml` |

Protocol upgrades scheduled: `1497=2` (spend budgets), `1831=3` (inference
escrow), `4337=4` (refuse a non-canonical `eth:` recipient). The first two are
long since activated.

### The box list every script wants, ready to paste

`rollout.sh`, `advance-height.sh`, `budget-smoke.sh` and `escrow-smoke.sh` all
refuse to start without `MATRIX_ROLLOUT_BOXES`, and it lives in the shell rather
than on disk — so a new terminal has lost it, which has now stopped a rollout
twice. This is that table in the format they parse:

```bash
export MATRIX_ROLLOUT_BOXES="validator-1|validator-1.ecirlabs.com|$HOME/Downloads/matrix.pem|validator
validator-2|validator-2.ecirlabs.com|$HOME/Downloads/matrix.pem|validator
validator-3|validator-3.ecirlabs.com|$HOME/Downloads/matrix.pem|validator
validator-4|3.36.163.59|$HOME/Downloads/matrix.pem|validator
gpu|13.220.232.122|$HOME/Downloads/tta-temp.pem|seller"
```

The four validators share `matrix.pem`; the GPU box needs `tta-temp.pem`. Those
paths are where they sit on the operator's laptop — correct them, do not commit
a key, and note the variable holds paths only, never key material.

**Pass a protocol version ONLY when the release changes a block-validity rule.**
`./scripts/rollout.sh v0.5.8` updates binaries and nothing else; adding a second
argument writes a new activation schedule. A version number is refused unless it
is past the one already scheduled, so the mistake usually fails loudly — but on
a release that genuinely needs no version, a schedule written by accident is a
rule change nobody asked for.

The marketplace is up. One seller, `eth:0x856e3fff…`, 1000 base units per unit,
serving `qwen3.6-27b`. Confirm from anywhere, with no keys:

```bash
./scripts/shopfront-check.sh      # exit 0 when every public node offers a seller
```

### How to check the network is actually working

Two traps, both of which have already cost a day each:

**An idle chain produces no blocks.** The engine returns nil rather than minting
an empty block, so "the height went up" fails on a healthy quiet network. What
to check instead is that every validator reports the same head hash AND the same
state root. `scripts/rollout.sh` does this at every step.

**Heads are only comparable at the same height.** Ask for a NAMED block
(`eth_getBlockByNumber`), never "latest" on two nodes a second apart.

---

## What shipped in the 2026-09-18..21 pass

| PR | what it was |
|---|---|
| #20 | the mobile menu was transparent, so it opened on top of the page |
| #21 | a seller vanished from the whole network 24h after its last restart |
| #22 | `shopfront-check.sh`, the detector that would have caught #21 |
| #23 | money sent to the address a wallet displays went to nobody |
| #24 | the `/v1` door described one node's own shelf as if it were the network |
| #25 | three ways the rollout tooling lied, found by running it |

Two of those were live faults nobody knew about.

**#21** is why `/market` was empty for three days. A quote's validity window was
stamped once at registration and nothing moved it; past it the seller drops out
of its own order book, so the announce loop has nothing to announce and every
other node ages it out. `Market.RenewProviderQuotes` now restates the offer at
half the window, preserving price, cost, markup and quote identity, and bumping
`QuoteVersion` because a listener accepts a moved window only under a strictly
newer version. Each renewal prints a line: `journalctl -u matrixd | grep
'restated its quote'`.

**#23** was a money-loss path. An `eth:` account is keyed lowercase, but every
wallet and explorer displays the EIP-55 mixed-case form, so the string a person
copies is not the string the ledger keys their account by. `wallet send --to`,
`fund --account` and the bridge's burn recipient all took it verbatim.
`token.CanonicalAccountID` (and `canonicalAccountId` in the web) settle it
before signing, because the recipient is inside the signature and no node can
correct it afterwards.

**Audited: nobody lost money to #23.** All 212 committed transfers were walked
from outside on 2026-09-21; nine went to an `eth:` recipient across two ids,
both canonical lowercase. All three public validators returned the same history
digest `0e7037ef8d66180a`, so the transfer history has not diverged either.

---

## Next tasks

Ordered by what a session can finish. Each says what is known, what is not, and
what would have to be decided.

### 1. Consensus-side refusal of a non-canonical `eth:` recipient

**The rule is written and gated; what remains is scheduling it.**
`ProtocolVersionCanonicalEthRecipient = 4` refuses a transaction whose recipient
names an Ethereum-controlled account in any form but the one the ledger keys it
by - at block validation, at proposal selection, and at submit, where the sender
is actually told. The rule itself is `token.RequireCanonicalEthRecipient`, which
calls `CanonicalAccountID` rather than restating it, so the client and the chain
cannot come to different conclusions about which account a string names.

To activate it, pick a height far enough out that every validator is on the new
binary, and schedule it the way version 3 was:

```bash
./scripts/rollout.sh <tag> 4
```

**Re-run the outside transfer audit before the height lands.** Afterwards a
stranded balance can only have arrived before activation, so the audit stops
being repeatable and becomes history.

The background, for whoever schedules it: PR #23 closed every client path, but a
hand-rolled client could still strand money, because consensus accepted any
string as `tx.To` and credited it. The recipient is inside the signature, so a
node cannot rewrite it - refusal is the only move, and refusal changes block
validity, which is why this needed a version and a height rather than a restart.

### 2. A provider that has fallen off consensus keeps selling - CLOSED

A seller kept announcing and taking reservations when its node had stopped
participating in consensus. Settlement goes through consensus, so a buyer was
routed to a seller that could not complete the transaction.

**The signal is round-level, not height.** `Height()` is the obvious candidate and
is the wrong one, for the idle-chain reason above: no blocks are minted on a quiet
network, so a height-watching seller takes itself off the market for being unbusy.
A round times out every `round_timeout` and rotates the leader, so proposals and
votes flow whether or not anything commits. The engine now records when a verified
proposal or vote from ANOTHER validator last arrived, and
`ParticipatingInConsensus()` reads it against four worst-case rounds of silence,
floored at 10s because `round_timeout` is configurable down to milliseconds.

Recorded after verification and after the author is confirmed to be in the set, so
a peer cannot forge it, and never for this node's own messages, since a node alone
in a partition still proposes to itself and votes for its own proposals.

Consulted in two places. `announceOnce` falls silent, which is already the signal
a dead node sends and the one every listener ages a provider out on. The
reservation path refuses through a settlement guard the node installs on the
inference service, checked before capacity is held - so a buyer who reached the
node directly is told before they have committed anything.

Two deliberate non-behaviours. A validator set of ONE is always participating:
there is nobody to hear from, so silence carries no information, and a devnet
would otherwise never sell. A node that has heard nothing YET is not
participating: a fresh start is indistinguishable from a dead network, so it fails
closed and sells within a round of hearing one.

Work already reserved is never cancelled. A partition cannot be told apart from a
node that was mid-settlement, and abandoning funded reservations on that evidence
is the more expensive mistake - the same conclusion `inference_health.go` reached
about killing live jobs.

**Decided: immediately, on the first missed signal, and no grace window.** The two
mistakes do not cost the same. Refusing to sell while partitioned loses a sale;
selling while partitioned takes a buyer's money for work that cannot settle.
"Partitions are usually transient" is a claim about how often the mistake happens,
not about who pays when it does. The seller's own recovery is the grace window:
announcing resumes on the next tick after one message is heard.

### 3. Let the OpenAI door actually buy from a remote seller

**Decided: no. A validator stays a node that tells you who is selling.**
Brokering would make it an intermediary holding a buyer's money in flight, which
is a different trust and custody story than this network has agreed to, and
steps 3-5 below are where a seller dying mid-stream turns into somebody else's
funded reservation. `/v1` names the seller and the caller points at it. The
sketch stays here because declining it is a decision that can be revisited, not
a gap nobody noticed.

PR #24 made `/v1` tell the truth about what the market sells and where to buy it,
but a caller pointed at a validator still has to change `base_url` themselves.
The door names the seller; it does not broker the purchase.

Brokering would mean the node becomes an inference client of another node:

1. pick a remote seller for the model - `exchange.ListRemoteProviders`, exists
2. reserve against it - `marketexchange.SubmitRemoteJob`, exists, gossip-based
3. call that seller's inference endpoint over the network
4. stream the completion back through the SSE path
5. settle, and return the change

Steps 3-5 are the substantial part, including what happens when the seller dies
mid-stream after the reservation is funded. Comparable in size to the escrow work.

### 4. Smaller, self-contained

Closed in this pass: **an expired budget now comes back by itself.** "At and after
the expiry anyone may close a budget" was a rule nobody acted on, so a remaining
balance sat in escrow until its buyer happened to return to `/chat`. Every node now
scans the ledger each minute for `spend/escrow/` accounts past their expiry with a
balance, and submits the close the rules already allow. It needs no protocol
version: the refund destination is inside the account's NAME, so the sweeper
chooses nothing but when to ask. The nonce and timestamp are derived from the terms
rather than from the clock, which is what makes running it every minute idempotent
- every tick produces byte-identical bytes, so the mempool dedups them and the
committed set refuses the rest as a replay. Watch it with
`journalctl -u matrixd | grep 'submitted a close'`.

Still open:

- **A buyer's default deadline silently overrides the provider's
  `request_timeout`.** A buyer who sets nothing gets a default that can be
  shorter than what the provider asked for, and the job dies mid-run.
- **The job view labels the charge as "units".** It is base units of native
  MATRIX; "units" is also the name of the compute quantity being bought, so the
  label reads as a count of the thing rather than a price.
- **A stranger cannot get an API key.** Issuance is operator-only, so the
  hosted-wallet door has no self-serve path.
- **The live `/v1` door's key account is the provider's own**, so the OpenAI
  path bills the seller for its own work. Needs a look on the GPU box.
- **Re-encrypt or retire `prod-smoke-buyer.json`**, a plaintext key on the GPU
  box from an early smoke test.
- **Tool calls do not exist in the protocol**, only in the UI's ambition.
  **Decided: build them.** So the UI's claim stops being false by being made
  true, rather than by being removed. Nothing is designed yet - the first
  question is whether a tool call is a protocol-level field on the inference
  job or something the seller's own model server handles behind the door, and
  that answer decides whether this needs a protocol version at all.

### 5. Open questions with no owner

- **The ledger divergence at height 1105 that healed by itself.** Never
  explained. One data point since: on 2026-09-21 all three public validators
  returned an identical transfer-history digest, so whatever happened left no
  mark on committed transfers.
- **Whether the pre-payment leak was ever used on the live chain.** A buyer
  could once run a client-signed job, read the completion through a public
  `GetInferenceJob`, and never sign the payment. Fixed in the service accessor.
  Answering "was it used" needs the GPU box's job store - look for completed
  jobs with no matching settlement - and cannot be done from outside.

---

## Working on this repository

```bash
make            # proto + build
make test
make devnet     # three real validators on this machine, asserts rather than prints
```

The web app uses **yarn**, not npm (`link:` protocol). Verify with
`next build && next start`; `next dev` breaks assistant-ui's composer.

### Things that have bitten before

- **Go `len()` counts bytes; JS `.length` counts UTF-16 code units.** They
  disagree on any non-ASCII prompt, and a digest computed on one side and
  checked on the other will not match.
- **ssh does not carry argument boundaries.** It joins command words with spaces
  and the remote shell re-splits. `scripts/rollout.sh` uses `printf %q` per
  argument; anything else sending a command over ssh must do the same.
- **A tool on PATH is not a tool that runs.** Windows Git Bash carries a
  Microsoft Store alias for `python3` that satisfies `command -v` and then
  prints an error to stdout. Probe by running, not by finding.
- **A rename breaks release asset downloads.** GitHub redirects a renamed
  repository's pages and git operations but not `releases/download/...`. Scripts
  must name the canonical owner.
- **An error response can look like an answer.** Connect returns errors as JSON,
  so a refusal parsed with `.get("key") or []` becomes an empty result -
  indistinguishable from a real empty. Distinguish on the key, not the count.
