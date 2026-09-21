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

## The live network, as of 2026-09-21

Chain `8170`, protocol version 3 (inference escrow), five boxes on `v0.5.5`.

| box | role | address | config |
|---|---|---|---|
| validator-1..3 | validator | `validator-N.ecirlabs.com` | `/home/ubuntu/config.yaml` |
| validator-4 | validator | `3.36.163.59` (aarch64) | `/home/ubuntu/config.yaml` |
| gpu | seller | `13.220.232.122` (x86_64) | `/etc/matrix/config.yaml` |

Protocol upgrades scheduled: `1497=2` (spend budgets), `1831=3` (inference
escrow). Both long since activated.

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

PR #23 closed every client path, but a hand-rolled client can still strand money:
consensus accepts any string as `tx.To` and credits it.

The recipient is inside the signature, so a node cannot rewrite it - the only
option is to refuse the transaction. **That changes block validity, so it needs
a protocol version and an activation height**, scheduled the way version 3 was:

```bash
./scripts/rollout.sh <tag> <protocol-version>
```

Rule shape: if `tx.To` starts with `eth:` and is not exactly `eth:0x` plus 40
lowercase hex, the transaction is invalid. Mirror `token.CanonicalAccountID` so
the client and the chain cannot disagree about which account a string names.

The audit above means this is preventive. Re-run it before the rule activates,
since afterwards a stranded balance can only have arrived before activation.

### 2. A provider that has fallen off consensus keeps selling

A seller keeps announcing and taking reservations when its node has stopped
participating in consensus. Settlement goes through consensus, so a buyer is
routed to a seller that cannot complete the transaction.

Not the same as `node/inference_health.go`, which watches the MODEL SERVER and
is already correct and fail-closed. Nothing watches the node's own consensus
participation.

**Why it is not a small fix.** The engine exposes no participation signal.
`Height()` is the obvious candidate and is the wrong one, for the idle-chain
reason above. The right signal is round-level: a round times out every
`round_timeout` and rotates the leader, so consensus messages flow even when no
block is minted. "I have not heard a vote or proposal in N rounds" means
partitioned. That means adding a last-heard timestamp to the engine's receive
path and having `announceable()` and the reservation path consult it.

**Decision needed before implementing:** should a seller that has lost consensus
stop selling immediately, or keep selling through a grace window?
`inference_health.go` faces the same asymmetry for the backend case and chose
fail-closed on the first failure, with the reasoning written out - but consensus
partitions are more often transient than a dead model server.

### 3. Let the OpenAI door actually buy from a remote seller

PR #24 made `/v1` tell the truth about what the market sells and where to buy it,
but a caller pointed at a validator still has to change `base_url` themselves.
The door names the seller; it does not broker the purchase.

Brokering means the node becomes an inference client of another node:

1. pick a remote seller for the model - `exchange.ListRemoteProviders`, exists
2. reserve against it - `marketexchange.SubmitRemoteJob`, exists, gossip-based
3. call that seller's inference endpoint over the network
4. stream the completion back through the SSE path
5. settle, and return the change

Steps 3-5 are the substantial part, including what happens when the seller dies
mid-stream after the reservation is funded. Comparable in size to the escrow work.

**Decide deliberately rather than drifting into it:** it changes what a
validator IS. Today it is a node that can tell you who is selling. Brokering
makes it an intermediary holding a buyer's money in flight, which is a different
trust and custody story.

### 4. Smaller, self-contained

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
- **Tool calls do not exist in the protocol**, only in the UI's ambition. Either
  build them or stop implying them.

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
