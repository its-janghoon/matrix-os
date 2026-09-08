# Handoff

State of `main` as of `cb4db0c`, plus the product-surface work on
`claude/handoff-md-checklist-s0ptw1` through `4c7eb0d`. Everything below was run, not inferred; where
something is unverified it says so.

## Build and check

```sh
make                     # buf generate + build (proto/gen is gitignored, generate first)
make fmt                 # fails and names files that are not gofmt-clean
make vet
make test

cd apps/web && yarn lint && yarn typecheck && yarn test && yarn build
cd packages/sdk && yarn typecheck && yarn test
```

CI runs exactly those four groups (`.github/workflows/ci.yaml`). All green on
`main`.

Two things worth running by hand before trusting a consensus change:

```sh
cd services/core
go test ./internal/consensus/ -count=3 -race -timeout 1800s
```

and a real node, because several defects this session were invisible to the
tests and obvious to a running binary:

```sh
matrixd -init -config config.yaml     # writes the config, generates an admin API key, 0600
matrixd -config config.yaml           # prints the node's consensus identity
matrix --api-key <key> quickstart     # wallet, funding, provider, job, settlement
```

## What the consensus layer does now

`services/core/internal/consensus/`, roughly in dependency order:

| File | What it holds |
| --- | --- |
| `validators.go` | The set, voting power, weighted quorum, leader schedule |
| `setchange.go` | Membership as chain state: add / remove / slash, epoch boundaries, `SetStore` |
| `evidence.go` | Equivocation proof and its store |
| `stake.go` | Bonds, withdrawal gating, slashing |
| `fees.go` | The protocol fee and its split |
| `rewards.go` | The provider registry and the emission |
| `engine.go` | The round engine that ties those together |

Things that changed how the layer behaves:

**Leadership rotates per commit.** It was `ids[round % N]`, and the round resets
on every commit, so `ids[0]` proposed every block for the life of a healthy
chain. It is `ids[(height+round) % N]` now. A transaction submitted to any other
node was previously never proposed at all.

**The validator set is chain state.** A change rides in a committed block and
takes effect at an epoch boundary, so every node switches at the same height. It
needs a quorum of *operators* to have listed it under
`consensus.approved_changes`. Proven equivocation is approved automatically,
because the evidence proves itself.

**Bonded stake** (`consensus.stake.enabled`, off by default). Voting power is
bonded MATRIX, admission needs `min_bond`, withdrawal waits `unbonding_period`
blocks after leaving the set, and a proven offence moves the whole bond to the
reward pool. A bond is a balance in `consensus/stake/bond/<id>` on the same
ledger everything else settles on.

**A fee and an emission** (`consensus.fee_basis_points`, `consensus.rewards`).
The engine's zero-value config is still fee-free and emission-free; what changed
is that a *generated* config (`matrixd -init`) now ships the operator's chosen
policy. The fee is a cut of each committed value transfer paid to the validator
set pro rata by power, capped at 100 basis points *in code*, and the generated
config sets it to that 100bp cap. The emission is a fixed halving per-block
budget from the genesis pool, shared among registered providers a block paid;
the generated config arms the recommended schedule (`per_block` 280,000,000,000,
`half_life` 1,000,000, about `1.44 x per_block x half_life` ≈ 4e17 base units,
~40% of the 1e18 native cap). It pays nobody yet: `rewards.approved_providers`
is empty and a registration needs an operator quorum.

**Signed value transfers settle through consensus.** `matrix wallet transfer`
and `matrix.market.v1 SubmitSignedTransfer` no longer append to the per-node
`token.SettledLedger`; a `node.TransferSettlementCoordinator` verifies the
signature and authZ, submits the signed transfer into the engine, and waits for
it to commit and apply, so two nodes agree on balances and the transfer pays the
protocol fee like every other committed transfer. `tx list` / `ListTransactions`
/ `GetTransaction` read the committed-block transfer history by a stable index.
The proto `Transaction` message was reshaped (wire-breaking; the retired field
names are reserved).

## Joining a running network

This is the operational path and it has two requirements that are easy to get
wrong. Both are covered by `internal/consensus/join_test.go`, including a test
for the failure mode, and this procedure is now also on the site at
`/docs/guides/network-setup`.

1. **`consensus.validators` must be the GENESIS set, not the set in force when
   you join.** A joining node replays the chain from height zero, and to accept
   the block at each height it checks the proposer led *that* height against the
   set as it stood then. Handed the current set it refuses block 0 and never
   starts. It then replays the set changes and arrives at the current set by
   itself.

2. **`genesis:` must match the network's.** The chain carries transactions and
   never the balances they started from; allocations and the reward pool are
   applied from the node's own config at first start. A node with a different
   genesis config ends up with the same blocks and different balances, which
   agrees on history while disagreeing about money.

Beyond that:

```yaml
network:
  listen_addr: /ip4/0.0.0.0/tcp/9000
  bootstrap_peers:
    - /ip4/<a peer>/tcp/9000/p2p/<its peer id>
consensus:
  validators: [ ...the genesis ids... ]
  epoch_length: 100          # must match every node
```

The node prints its own consensus identity at startup; that hex id is what goes
in other operators' `approved_changes` to make it a validator. Until a quorum
approves it, it follows and applies blocks without voting, which is a useful
state in itself.

On a staked network it also has to bond before it can be admitted: fund its
consensus account, set `consensus.stake.bond`, and the node bonds the shortfall
itself and keeps topping it up. It says so plainly when the account is empty.

The node prints its own **peer id and full multiaddrs** at startup too, which is
what goes in the other node's `bootstrap_peers`. It labels a loopback address as
loopback, because pasting `127.0.0.1` into a remote node's config produces a
peer that silently never connects.

### What two running nodes verified, and what they did not

Two separate `matrixd` processes, separate data directories, real libp2p TCP
between them, each config naming only the *other* peer's validator id:

- Both nodes bootstrap from an identical `genesis:` and report identical
  balances for the allocated account.
- A signed transfer submitted to A appears on B, and one submitted to B appears
  on A, at the same index, nonce and block, with matching balances after the
  1% fee (250 -> 248, 100 -> 99).
- A's peer id is unchanged across a restart, and B stays joined through it.
- `FundAccount` is refused on both, naming 2 validators.

### Then two separate hosts, one of them behind a NAT

The two-process run above was one machine, so it was redone with two containers
on separate bridge networks: **distinct network namespaces, distinct hostnames,
distinct routable IPs, distinct routing tables, and no loopback path between
them** - `10.77.0.10` and `192.168.50.20`, with an ESTABLISHED TCP connection
between the two addresses. Node B was then moved behind a **NAT**: the host
MASQUERADEs its private LAN outbound and drops everything inbound, so A sees B's
traffic arriving from the NAT address `10.77.0.1` and cannot initiate to B at
all (verified: `nc` to B's address from A fails, while B reaches A fine).

Verified there: identical genesis balances, a signed transfer submitted from
either side of the NAT committing at the same index/nonce/block on both, and
`FundAccount` refused on both naming 2 validators.

Still not verified: two separate physical machines, real WAN latency, and packet
loss. Latency and loss could not be injected at all - this kernel
(`6.18.44-fc-v24`) has no loadable modules and no `sch_netem` built in, so `tc
qdisc ... netem` fails with "Specified qdisc kind is unknown". Two containers
sharing one kernel is not two machines, and this note should not be read as if
it were.

**Two more defects that only separate hosts exposed**, on top of the three
below:

**4. A restart permanently partitioned the network.** `node.Start` dialed
`bootstrap_peers` exactly once and nothing ever re-dialed. Restarting A left
**zero connections and neither node able to commit, indefinitely** - confirmed
by four transfers that are absent from the chain - and the only cure was
restarting B, the NATted node an operator can least easily reach, because B is
the only side that *can* dial. The same one-shot dial also means two nodes
started together never connect if the dialed-to one is not listening yet.
Fixed by `Node.startBootstrapDialer`: one pass at startup, then a 10s
`Connectedness` check per configured peer with a re-dial, logging transitions
only. `p2p.Host.IsConnected` is the new primitive.

A success line is now logged too. Before, only failure was logged, so a working
join and "no bootstrap peers configured" looked identical - answering "did it
connect?" meant reading `/proc/net/tcp` inside the container's namespace.

**5. One nonce could authorize two payments.** This is the serious one. A client
asks the node for its next nonce, signs, submits; if it submits again before the
first commits, the node reports the *same* next nonce and the client signs a
second, different transfer at it. Both used to commit and both moved money:
observed as history index 10 and 11 **both carrying nonce 10**, with both
recipients credited 50. A sender who meant to pay once paid twice.

Nothing in the consensus path checked it. `Engine.Submit` dedups on
sender+nonce+**signature**, so two different transfers at one nonce are two
different keys; `verifyBlockForHeightLocked` checked that same key;
`commitAndApply` checks affordability and nothing else. `token.Chain.Append`
does enforce a strict nonce, but signed transfers settle through consensus now
and never reach it.

Fixed with a `sender:nonce` uniqueness set, tracked exactly the way
`committedTxs` already is - in memory, rehydrated from the committed chain at
startup, so it stays a function of committed state and every node computes the
same one. `Submit` now refuses a *different* transfer at a spent or pending
nonce with `ErrNonceAlreadyUsed` (an identical re-submission stays idempotent),
and block verification refuses a block that reuses one, which closes the
malicious-leader path. Reserved-recipient consensus operations are exempt on
purpose: the engine mints them from three counters (`providerOfferNonce`,
`setChangeNonce`, `bondNonce`) that all start at zero for the same sender, so
their nonces collide by construction and mean nothing.

Re-verified on the NATted pair: with B stopped so nothing can reach quorum, the
first submission sits pending and the second is refused by name; bringing B back
commits exactly one transfer and credits exactly one recipient. The three new
tests in `internal/consensus/nonce_test.go` all fail if the check is removed.

**Three defects the two-process run found that a green test suite did not.** All
three were invisible to a single node, which is the point:

1. **Nothing printed the node's peer id or listen addresses,** so a second node
   was impossible to configure: `bootstrap_peers` wants
   `/ip4/<host>/tcp/<port>/p2p/<peer id>` and there was no way to learn the id.
   Fixed by `Node.printPeerAddresses`.

2. **The libp2p peer id changed on every start.** `libp2p.New` was called with
   no `Identity()` option, so it minted a fresh key each time. The consensus
   identity was already persisted, so the effect was specific and confusing: a
   node kept its validator identity across a restart and lost its network
   identity, which staled every other node's `bootstrap_peers` entry the moment
   it bounced. libp2p verifies the id it dialed and correctly refuses a host
   presenting a different one, so the only symptom is `all dials failed` with no
   hint the id is merely out of date. Fixed by `p2p.LoadOrCreatePeerKey`, with a
   test that fails if the persisted key is not the one the host presents.

3. **`FundAccount` forks a multi-validator network.** It moves reward-pool
   MATRIX on the receiving node's ledger only and is not a consensus
   transaction, so funding on A left B reporting zero. That is not merely a
   confusing read: `distributeEmissionLocked` clamps its per-block budget to the
   reward-pool balance it reads while applying a block, so two nodes with
   different pools credit providers different amounts from the same block. It is
   latent only while `rewards.approved_providers` is empty. It is now refused
   when the validator set has more than one member; use a signed transfer, or
   set the allocation in every node's `genesis` config.

   The gate reads the live validator **set**, not `consensus.validators`. The
   set is self plus the configured ids, so the two-node network above - each
   config naming only its peer - has a config list of one and a set of two, and
   a count taken from the config would have let the call through. The set also
   changes at epoch boundaries, so a number captured at startup goes stale.

## Done since 2e6325e

The five items that stood open last session all landed. They are recorded here
rather than as TODOs so nothing done still reads as pending.

**1. The signed-transfer path routes through consensus** (`2db328e`).
`matrix wallet transfer` and `SubmitSignedTransfer` no longer touch
`token.SettledLedger`; a `node.TransferSettlementCoordinator` submits the signed
transfer into the engine and waits for commit-and-apply, so nodes agree on
balances and the transfer pays the fee. `tx list` / `GetTransaction` read the
committed-block history by a stable index. The proto `Transaction` shape changed
(wire-breaking; retired field names reserved in `fa96324`), and CLI, SDK and
web/docs copy were updated to match.

**2. The join procedure is documented on the site** (`faba75f`).
`/docs/guides/network-setup` now covers the two requirements above (genesis
validator set, matching genesis config) alongside set changes, stake, the fee
and the emission.

**3. `DeployAgent` is exposed over gRPC** (`f026af4`). A new
`matrix.agent.v1.AgentService` (`DeployAgent` / `ListAgents` / `GetAgent`) with
an `internal/agentapi` server (default `:9094`, same `enable_acls` auth,
reachable over connectapi HTTP JSON). Deployments persist in the Pebble store
under an `agent/*` namespace and survive a restart; each run is metered by
settling a per-run charge through consensus. The metering price defaults to 0
(unmetered) and is operator config; a metered deploy that can't pay is refused,
not run for free. Adds a `matrix agent deploy/list/get` CLI group.

**4. A configurable, secure-by-default send policy for the agent runtime**
(`4d5fa9d`). `agent.SendPolicy` (an allowlist of on-node deployment ids) with the
refuse-all default preserved: opt in via `agent.allow_send` /
`agent.send_allowlist`. There is no wildcard and nothing off-node is
addressable; a permitted target is delivered into that agent's inbox.

**5. Bridge mint cap and a reconciliation endpoint** (`44e5816`, from
`docs/proposals/token-and-bridge-policy.md`). `WrappedMatrix.sol` gained an
immutable total-supply mint cap (a constructor param; over-cap `mint` reverts
`MintCapExceeded`) with new hardhat tests. The cap is deliberately immutable with
no raising authority - raising it is a redeploy - consistent with the operator's
solo/no-raise posture; the proposal's "raisable-after-a-clean-period" authority
was intentionally not built. `bridge.Reconcile()` is exposed as a node endpoint
`GetBridgeReconciliation` (gRPC + connectapi HTTP), backed by the node's own
bridge over its ledger.

## Decisions the CTO has made

The monetary-policy numbers are decided and wired into the generated config
(`ffc6320`; the engine's zero-value defaults stay neutral, so the protocol still
forces nothing):

- **Fee rate: 100 basis points,** the code cap.
- **Emission: the recommended schedule** - `per_block` 280,000,000,000,
  `half_life` 1,000,000, about `1.44 x per_block x half_life` ≈ 4e17 base units,
  ~40% of the 1e18 native cap, matching the proposal's provider allocation.
- **Stake off** - permissioned for the first release.
- **Fundraising:** solo, no raise, which is why the bridge mint cap is immutable
  rather than raisable.

## Still the operator's to turn

Runtime monetary policy that is armed in config but inert until the operator
acts:

- **The emission pays nobody yet.** `rewards.approved_providers` is empty;
  registering a provider needs an operator quorum. Until then the emission is
  armed and pays nothing.
- **Agent metering price is 0** (unmetered) until set.
- **The HTTP rate limit is armed at 600/minute, burst 120** in a generated
  config (`connect.rate_limit_per_minute` / `rate_limit_burst`). Raise it for an
  endpoint serving many callers; zero turns it off.
- **`MATRIX_WALLET_PASSPHRASE`** is read before prompting, and a
  non-interactive caller must set it: the tool refuses to read a passphrase off
  a pipe, where it would land in a log.
- **An API key spends from no account until one is set.** `account` under
  `security.api_keys` is what makes a key usable on `/v1/chat/completions`, and
  the node must hold that account's signing key to settle for it. Leaving it
  empty is the safe default and means that key cannot buy inference.
- **`connect.public_reads` and `connect.signed_writes` are both off.** A node
  serving a browser needs both; a node its own operator drives needs neither.
- **Permissioned vs. open launch is a config switch** - currently permissioned,
  stake off. Turning on stake means choosing `min_bond` / `unbonding_period`.

Also still open from the proposal: headcount and vesting, jurisdiction for the
issuing entity, and the audit budget. A real mainnet/testnet bridge deploy still
needs the operator's funded key, an RPC endpoint, and an external audit; the
contracts are undeployed by design (45 hardhat tests pass locally).

## The product surface: what landed

All four items that were pending here are done, on
`claude/handoff-md-checklist-s0ptw1`. Each was verified against a running node
and, where a protocol was involved, against a real third-party client.

**a. A provider joins from config** (`c8b7def`). `market.Provider.Models`
(normalized: trimmed, lowercased, de-duplicated, sorted) plus
`market.ProvidersForModel`, which returns providers advertising a model that
still have capacity, cheapest first with ties on ID. And `inference.backends`, a
config list (id, kind, base_url, api_key_env, models, capacity, price_per_unit)
that registers each backend twice at start: in the inference registry as what
fulfills the job, and on the order book as a provider that can reserve capacity
and be paid. A `model` filter on ListProviders, `--models` on `matrix provider
register`, and `models` on the SDK's Provider. The model list is signed with the
rest of a ProviderAnnouncement, entry count length-prefixed alongside the
entries.

**b. The OpenAI protocol is served** (`59fb383`). `internal/openaiapi` serves
`POST /v1/chat/completions` and `GET /v1/models` on the node's existing HTTP
endpoint, sharing its listener, origin list and key policy. The three things the
protocol does not carry: who pays comes from `admin.APIKey.Account` (a key with
no account is refused rather than having its buyer guessed), which provider
comes from the model, and the reservation is a generous estimate because the
settled charge is clamped to it. `inference.InferenceJob.Usage` now retains the
reported token counts alongside `Units` - work done vs. what was paid - and is
on the inference proto too. Streaming is refused, not faked. Verified with the
real `openai` Python SDK: `models.list()`, a completion that billed and settled
(13 tokens x price 2 = 26 base units moved), and an unknown model surfacing as a
typed `NotFoundError`.

**c. A buyer can pay with their own key** (`f3511c9`). `RunInferenceJob` /
`SettleInferenceJob`. Pre-signing cannot work - `token.Transaction` signs over an
exact amount and the price of an inference is not knowable until the work is
done - so the primitive is sign-the-invoice: run the model, return the exact
transfer to sign, withhold the completion until it is signed. Withholding IS the
enforcement; the provider's exposure is one job per defecting buyer, bounded by
the affordability check at reservation time. Every signable field must match the
invoice, because a transfer of one base unit to an account the buyer controls
verifies perfectly. `ExpireUnpaid` releases the reservation of a job nobody
signs for, swept every 30s. SDK: `paymentSigningBytes` produces the canonical
bytes and leaves signing to the caller's wallet, held to the node's encoding by
a golden vector. Verified on a node holding no key for the buyer, including the
contrast: the hosted path fails "no signing account" for that same buyer.

**d. The HTTP endpoint is rate limited** (`0d93243`). A token bucket per caller,
keyed on the credential where one is presented and the remote address otherwise
(the credential is fingerprinted, not stored). Idle buckets are evicted. A
generated config arms 600/minute with a burst of 120; zero disables it. CORS is
outermost so a 429 still carries headers a browser can read, the refusal uses
the caller's own envelope, and preflights are not counted. It also fixed a CORS
bug (b) introduced: `Access-Control-Allow-Methods` was POST-only, so a browser
preflight for the GET `/v1/models` route failed.

**A correction to what this file said before.** It claimed `connectapi`'s
`AllowedOrigins` "default is `"*"`". That was a misreading of the package's doc
comment: `matrixd -init` writes two localhost dev origins, and an empty list
means no browser may call at all. The origin policy was already deny-by-default;
only the missing rate limit was real.

## The rest of the product surface: what landed

The four items this file listed as still pending are done too.

**e. Opt-in public reads** (`8b9bbd7`). `connect.public_reads` opens Get*/List*
to callers with no key; writes are untouched. Reads are Get* and List* by the
convention every service here already follows, and
`TestReadClassificationIsPinned` enumerates the served surface so a mutating
`GetSomething` breaks the build rather than quietly becoming world-callable. Off
by default, because turning it on by default would widen what an
unauthenticated caller can see on every node that already exists.

**f. `matrix inference submit --client-signed`** (`8b9bbd7`). Run, sign the
invoice with the local wallet, settle. It checks the wallet is the buyer's own
account up front rather than letting the node answer with a payment mismatch
after the provider has already worked.

**g. Streaming** (`fa81716`). Four layers: an optional `StreamingBackend` (a
backend that cannot stream is wrapped into one chunk, and says so via
`StreamedOneShot`); real upstream streaming in `OpenAIBackend`; the
`StreamInferenceJob` server-stream RPC; and SSE on `/v1/chat/completions`.
`connectapi` now serves server-streaming methods with the Connect framing - it
used to refuse to build if a bound service declared one, so this had to land
with the RPC. `FulfillJob`'s settlement half is extracted as `settleRun` so both
paths share it, and a test pins that streaming and fulfilling settle the same
amount.

Streaming is the hosted path only, by construction. On the client-signed path
the completion is withheld until the buyer signs, and withholding is the only
enforcement because the provider has already worked.

Two things worth carrying forward from this one. First, usage: a streamed
chat-completions response carries none unless the vendor honours
`stream_options.include_usage`, so when it never arrives it is derived locally
from the prompt and the assembled completion - deterministic, and the same basis
a buyer can recompute, which is what keeps an inflated bill detectable. Second,
the flush: the first version of the framing test passed with the flush REMOVED,
because the handler finished before the client read. It now blocks mid-stream,
so it times out without it. Any future test of a stream has to hold the handler
open or it proves nothing.

**h. A browser wallet and `/chat`** (`935effa`). An ed25519 keypair generated
with `extractable: false`, kept as a CryptoKey in IndexedDB: script on the
origin can ask it to sign and cannot read the key. Verified in Chromium -
`exportKey` throws. Passkeys are deliberately NOT used, and the note in the
earlier version of this file suggesting them was wrong: a passkey signs
WebAuthn's own challenge structure with its own key, so it cannot produce the
ed25519 signature the chain verifies.

Building it surfaced a real hole and closed it. A page cannot hold an API key,
so the three write methods it needs had to be openable. Two were already
self-authorising; `RunInferenceJob` was not - `buyer` was just a string, so
opening it would have let anyone name someone else's funded account, have a
provider work, and never sign. `inference.RunAuthorization` fixes that: a buyer
signature bound to one provider, one prompt and one moment, with the account
derived from the signing key. `connect.signed_writes` opens the three AND makes
that authorization mandatory, from one field, because the two cannot be allowed
to drift apart.

## Wallets

**A recovery phrase and encryption at rest** (`5c5b1ad`). A wallet was the
private key in hex at mode 0600, with no derivation of any kind: anyone who
could read the file owned the account, and losing it lost the account. Now
BIP-39 + SLIP-0010 at `m/44'/9004'/0'/0'` (the path Solana, Near, Aptos and Sui
use for ed25519, so a phrase restores in those tools) and a scrypt/AES-256-GCM
keystore. `wallet create` shows the phrase once, `wallet import` restores it,
and `wallet show` / `wallet balance` do NOT ask for a passphrase because the id
is public.

The derivation is pinned to all 12 of SLIP-0010's published ed25519 vectors, and
that check earned its keep immediately: a value I transcribed was wrong and the
spec agreed with the code.

Legacy plaintext wallets are still read, with a warning on every command that
SIGNS with one. There is no migration a tool can do unasked, because it cannot
invent a passphrase.

**MetaMask can be the wallet** (`484925d`, `4c7eb0d`). Two account kinds now:

| id | signs | verified with |
| --- | --- | --- |
| `<64 hex>` | the canonical bytes | ed25519 |
| `eth:0x<40 hex>` | EIP-712 typed data | ecrecover |

The kinds are told apart by the ACCOUNT ID, not by a field in the signed
payload. Adding a scheme byte to `Transaction.SigningBytes` would have
invalidated every ed25519 signature ever produced and re-hashed every committed
block; a test pins those bytes to the same golden vector the SDK and web app
use.

`internal/ethsig` owns keccak256, the address type and ecrecover. The bridge
delegates to it, because it imports `internal/token` and so a second copy of
ecrecover would otherwise have lived there.

**The thing to know before touching any of this**: only agreeing with a real
Ethereum library proves a wallet will produce a signature the node accepts.
Every self-consistency test passes just as happily with a wrong digest. The
digests are pinned against ethers v6's `TypedDataEncoder`, and one of its real
signatures is verified end to end - and both times I wrote one of these
constants from memory it was wrong. A drift here surfaces as "invalid
signature", which reads as a key problem and sends you to the wrong place
entirely.

Also: a bytes32 is exactly 32 bytes or empty, never padded. Ethereum tooling
right-pads a short bytes32 while every integer is left-padded, so a 4-byte value
would encode one way in our code and the other way in the wallet.

## Still pending

**A USDC on-ramp.** The path is connected end to end in code: lock (now
consensus-ordered), gather a threshold of attestations, `WrappedMatrix.mint`,
then a DEX purchase and `WrappedMatrix.burn` back to native through the
quorum-attested unlock. See "The on-ramp, end to end" for how each half works and
why.

What remains is not code: a mainnet deploy, an audit, the liquidity capital, and
putting the validators' attestor addresses in `ATTESTORS` at deploy time.

This entry previously said the remaining work was "a mainnet contract deploy, an
audit, and DEX liquidity, none of which is code", and that was wrong twice over -
first because nothing could mint wMATRIX at all, then because nothing could sign
the authorization. Both are now built. The correction is left recorded rather
than quietly overwritten, because the failure mode it names is the one this
document is most prone to: an entry that describes the state it was written in
and is never revisited.

**Separate hosts and a NAT are now verified; two separate machines are not.**
Two containers with distinct network namespaces, IPs and routing tables - one of
them behind a NAT that blocks all inbound - agreed on the chain in both
directions, and that run turned up five defects in total (see "What two running
nodes verified" and the NAT section above). Two separate physical machines, real
WAN latency and packet loss remain untested; latency and loss cannot be injected
in this kernel at all, which has no `sch_netem`.

**Copies of byte layouts: down from three to two, and the last one is pinned
properly.** The node owns four canonical payloads (the ed25519 transfer, the
ed25519 run authorization, and the two EIP-712 digests).

`packages/protocol` is now the one TypeScript implementation of the two ed25519
layouts. `apps/web/src/lib/wallet/signing.ts` is a re-export of it and has no
implementation left. The blocker was real: a plain relative import across the app
root type-checks and then fails the build, because Turbopack refuses to resolve
a module outside its inferred root. What fixes it is a package boundary plus two
lines of `next.config.js` (`transpilePackages` and `turbopack.root`).

`packages/sdk` keeps its own implementation on purpose - it is PUBLISHED, so it
cannot depend at runtime on a private workspace package, and it must stay
dependency-free. Its copy is held byte-identical by a differential test over
hundreds of randomized inputs (`src/layout-parity.test.ts`), including the edges
a golden vector says nothing about: an empty recipient, a zero-length prevHash,
a negative timestamp, multi-byte characters, and values above 2^53. Verified to
fail on an injected one-bit drift.

The EIP-712 digests in `apps/web/src/lib/wallet/eip712.ts` are still a separate
copy; they depend on ethers, which the dependency-free shared package will not
take. Moving them needs a keccak implementation in `packages/protocol` or a
second package that may depend on ethers.

**CI now runs the SDK and the contracts.** It ran neither. The SDK had tests and
nothing executed them - which matters now that the layout-parity guard lives
there - and the Solidity tests covering the mint threshold, the timelock and the
bridge attestation threshold were never run either. A guard CI never runs is not
a guard.

**Client-streaming** is refused by `connectapi` by design; nothing needs it.

**No vesting contract, and no vesting of any kind.** Still open, and worth
stating precisely because a model built on this repo got it wrong: a grep for
vest/cliff/lockup across the Go and Solidity trees returns nothing.
`GenesisAllocation` is `{Account, Amount}` and `ApplyGenesis` credits balances
once at first start - that is the whole mechanism. The "12-month cliff, 36 months
linear" in `docs/proposals/token-and-bridge-policy.md` is a document, not code.

Also worth knowing before anyone plans a cap table against that proposal: a
genesis allocation is applied from each node's OWN config at first start, and the
chain carries transactions rather than the balances they started from. So an
allocation exists only if every operator writes the identical genesis into their
own file. A node with a different one agrees on history while disagreeing about
money.

The multisig half is done. `MatrixToken.mint` was `onlyOwner`: one address could
issue up to the 1e27 cap in a single transaction, sitting next to
`WrappedMatrix.sol`, which already required a threshold of attestor signatures
for exactly that reason. `Ownable` is removed rather than pointed at a Safe,
because an owner pointed at a Safe is a deploy-time convention nothing enforces
- the next deploy script or a later `transferOwnership` puts an EOA back in
charge and the contract cannot tell.

Every mint now needs a threshold of distinct registered minter signatures (the
same construction and signature rules as `WrappedMatrix`) AND a 2-day timelock:
`proposeMint` starts the clock, the proposal is on-chain and visible,
`executeMint` lands it, and the same threshold can `cancelMint` during the
delay. A proposal expires 7 days after becoming executable. The threshold,
minter set, cap and delay are all immutable with no changing authority, so
altering any of them means redeploying. `scripts/deploy.ts` requires `MINTERS` on
mainnet or sepolia and refuses a threshold below 2 or a set smaller than 2: a
1-of-1 set satisfies the m-of-n code and is exactly the key this replaced.

## Product decisions made this session

Policy, not engineering. Recorded so they are not re-litigated.

**Two servers, two names, deliberately.**

| Host | What it is | Nature |
| --- | --- | --- |
| `rpc.ecirlabs.com` | a `matrixd` node (market + inference over connectapi) | replaceable infrastructure, holds no keys |
| `seed.ecirlabs.com` | a libp2p bootstrap peer | replaceable |
| `api.ecirlabs.com` | the OpenAI-compatible gateway, keys and quotas | a company product |

Do not merge them. "A user can bypass us" has to be checkable, and it is only
checkable if the bypass has its own address. Every chain runs servers - MetaMask
defaults to Infura, Ethereum ships hardcoded bootnodes - so the property to
protect is not their absence but that they are verifiable and substitutable. The
SDK's `DEFAULT_ENDPOINT` is `http://127.0.0.1:9093`, the user's own node, and
that default stays.

**No fiat, therefore no custody.** There is no card processor and no prepaid
balance. An API key is a proof of account ownership; the balance lives on-chain
and each request settles from it. That removes the custodian problem from the
gateway, which is also why (c) matters: the node has to stop signing for users.

**Reselling spare API credits is supply we want.** A provider pointing
`OpenAIBackend` at any OpenAI-compatible vendor brings that whole catalogue onto
the network. It costs one hop more than going direct, it inherits the upstream's
content policy, and the provider carries the currency and ToS risk - all of which
are the provider's judgement, not ours to prevent. Do not gate it with
`approved_providers`. It is how a marketplace launches with a catalogue instead
of an empty list, and real GPU providers then undercut the proxies on the models
they can serve.

Quality selects itself here in a way it cannot for GPU work, because the output
is checkable: the buyer holds the prompt and the completion and can recount the
tokens with the model's tokeniser, so an inflated `usage` is detectable. Keep
trying cheap - do not add a minimum job size.

**Self-dealing is not blocked, because the emission already prices it.**
`distributeEmissionLocked` splits the per-block budget pro rata by
`credited[id]`, the amount that provider was actually paid in that block, not by
headcount. With self-dealt volume V, honest volume H, per-block emission E and
fee rate f, a farmer pays `f*V` and collects `E*V/(V+H)`:

- the optimum is finite (`V = sqrt(E*H/f) - H`), so looping does not scale;
- it pays at all only while `H < E/f`, so at the 100bp cap it loses money at any
  size once honest volume per block passes 100x the per-block emission;
- the worst case is bounded by the emission budget itself, and the farmer paid
  the protocol fee to reach it. That is an advertising spend, and it is fine.

So the exposure is a number, not a missing feature: `per_block` sets the size of
the farmable window. 280,000,000,000 was chosen against the token allocation, not
against expected launch volume, so it is worth a second look - or arm it at 0 and
raise it once real volume exists. No reputation system is needed; repeat purchase
from distinct buyers is already visible in committed settlements if we ever want
to weight by it.

**Permissioned now, permissionless as a stated destination.** Stake is off and
`approved_changes` gates admission, so this is permissioned with capital at risk.
That is the standard launch posture (Ethereum's beacon genesis set, Solana, Sui,
Aptos and the Cosmos hub all started this way) and it is where DePIN compute
networks tend to stay: an open settlement layer with a gated service layer. What
blocks opening the set is technical rather than political - only equivocation is
provable from committed state, so a bond is a deposit rather than collateral
against the offences that matter in an open set (censoring, withholding, lying
about off-chain work). The order to open it: make job settlement prove provider
fraud, broaden slashing past equivocation, size `min_bond` against value at risk
per block, then drop operator approval for admission while keeping it for
removal. Until then the site must not claim permissionless.

**What privacy we can honestly claim.** A dApp is a static page and need not
touch our servers, so "we do not store your chats" is true. Two things are not:
the settlement is on-chain forever (who paid whom, how much, when), and the
provider sees the prompt, because the model runs on their hardware. There is no
confidential computing here. Say "we do not store this", never "nobody sees
this".

## Things to know before changing consensus

Each of these cost real time this session.

- **Anything that decides balances must depend only on committed state.** The
  fee is paid to the validator set *in force*, not to the validators whose
  precommits carried the block, because the certificate a node observes is
  node-specific: one node may have seen three precommits and another four, so a
  split that depended on it gives two honest nodes different balances. That is a
  fork, not a rounding difference.

- **Voting power is persisted with the set.** A node that reloaded a set at
  equal power while its peers held it at stake weights would compute a different
  quorum from the same votes.

- **Weighting a partly-bonded set is the dangerous case.** An unbonded member
  counts 1, so the first validator to bond anything holds almost all the power
  and cannot even be slashed. The set stays at headcount until every member has
  bonded.

- **An epoch boundary is a HEIGHT, and an idle chain produces none.** A committed
  set change or bond would sit forever on a quiet network, so the leader produces
  empty blocks while either is pending. Bounded: the boundary clears them.

- **A consensus subsidy cannot be a percentage of a transfer.** Consensus cannot
  see work, so a percentage is a money pump: send coins to an account you also
  control, collect, send them back. That is why the emission is a fixed per-block
  budget.

- **A four-member set that loses one member has quorum 3 of 3 and tolerates no
  lag.** Four tests failed under `-race` for this reason and were fixed by
  starting from five nodes, not by lengthening timeouts. Similarly, a fixed batch
  of transfers is not a way to produce a fixed number of *heights*, because one
  block can carry any number of them; use the open-ended traffic helper.

- **Run the binary.** The stranded set change on an idle chain, the demo
  inference provider that could not take a job, the stale-binary confusion, and
  the `/download` rate limit were all invisible to a green test suite.

## Adversarial pass: what an attacker could take

Five findings, all fixed, all with a test that fails without the fix. Each was
reachable by an account with no privileges.

**1. A permanent chain halt from one signed transaction.** Critical.
`Engine.Submit` accepted anything with a valid signature and a non-empty
recipient; block validation is far stricter. Anything in that gap reached the
mempool and could never be in a valid block, and the mempool only drops what
COMMITS - so one transfer of value to `market/provider/add/anything` made every
leader propose a block every validator refused, forever. The root cause was a
reserved-namespace list kept separately at four sites; block building already
had this guard, for two namespaces only, with a comment that literally said
"rather than poison a proposal with it". `verifyReservedRecipientLocked` is now
the one list, used by block validation, block building and `Submit`.

**2. A buyer could make a provider work for one unit's pay.** High.
`unitsEstimate` is the client's own number and is what the affordability check
runs against; `FulfillJob` then clamps the charge DOWN to it. Correct as buyer
protection, unguarded on the other side: a 90,000-byte prompt against a 1-unit
reservation, from an account holding the price of one unit. `MinUnitsFor` bounds
the work against the reservation from what is knowable before running.

**3. An account with no balance could exhaust every node's memory.** High.
`Submit` cannot check affordability (that is decided at apply time), and the
mempool had no cap. Nonces 0, 1, 2, ... all passed. Now capped, with an explicit
`ErrMempoolFull` so an honest sender can tell back-pressure from rejection.

**4. The Go and Solidity verifiers disagreed about the same signature.**
`WrappedMatrix.sol` rejects high-S (EIP-2); `ethsig.RecoverAddress` did not. So
a signature Solidity refuses was accepted on the Go side - in the one package
that exists so there is a single implementation of this check.

**5. The run-authorization replay set keyed on signature bytes, not content.**
A re-encoded signature over the same authorization read as a new one. Now keyed
on the signed content, which does not depend on every signature scheme having
exactly one canonical encoding.

### Checked and found sound

- **The provider emission**: saturating weights, a `big.Int` share, the budget
  clamped to the pool, truncation leaving the remainder behind. Deterministic
  and overflow-safe.
- **Provider registry changes**: amount must be zero, sender must be a
  validator, and each voter checks its own `approved_providers` before voting.
  A stranger cannot register themselves and start drawing the emission.
- **`FeeFor`**: split into quotient and remainder so it cannot overflow, and
  asserts the fee never exceeds the amount.
- **`public_reads`**: the `Get*`/`List*` rule looked like the "derived from a
  name" hazard the file's own comment warns about - but
  `TestReadClassificationIsPinned` enumerates the real service descriptors and
  pins the exact split, so a mutating `GetFoo` fails the build rather than
  becoming world-callable. A change here was started and reverted: the design is
  already defended, and reporting it as a finding would have been wrong.

## Adversarial pass: the WASM sandbox and agent resource accounting

Four more findings, all fixed, each MEASURED with a purpose-built guest module
rather than argued from the code. The fixtures are committed
(`internal/agent/testdata/{flood,spam,escape}.rs`) with their numbers in the
README.

**1. The log budget counted lines, not bytes.** `maxLogLines` capped how many
lines a guest may write and its comment claimed that stopped "a guest in a loop
[filling] the node's disk". A line may be up to `maxHostCallBytes`, so the real
ceiling was lines x bytes-per-line, nearly 10 GiB. Measured: **586 MiB from one
run** with the line cap working the whole time. Now bounded in bytes too, with
the notice emitted once so it cannot itself become the flood.

**2. The secure default was a node OOM.** Every `send()` attempt was retained in
`sendLog`, refused ones included - a good reason to record something, not a
reason to record everything. Measured with NO send policy, the default where
every send is refused: **1172 MiB held, nothing delivered anywhere**. The
attempt is now always counted (`SendsDropped`) and only the retained copy is
dropped, so a bounded log can still say how many there were.

**3. A deployer chose its own resource ceiling.** `MaxMemoryPages` and
`MaxRunTimeMS` arrive in the deployer's request; `normalizeLimits` filled in a
default when a field was zero and never bounded one from above. `Validate`
allows 65536 pages (4 GiB) and no ceiling on run time at all. The metering
charge did not compensate - it is a FLAT price per run, so one payment bought a
trivial call or the whole ceiling the caller picked. Pricing by consumption
would need an instruction meter wazero does not have, so the fix is a ceiling on
what one payment buys: `MaxAllowedMemoryPages` 1024 and `MaxAllowedRunTime` 30s,
clamped rather than refused. Clamped in `Deployment.Limits` as well, since that
is the path every run after the first takes.

**4. An inbox grew without bound and outlived the run.** Worse than the sender's
own log, because the bytes stay in the Manager until the recipient deployment is
removed. Bounding it exposed that `Inbox` only PEEKS, so a full inbox would have
been full for good - an unbounded-memory bug traded for a permanent-refusal one.
`DrainInbox` is the consumer's half.

Also: `Stop` closed the module and returned early on error, **skipping the
runtime close**. The module is exactly what fails to close after a guest was
interrupted for exceeding its deadline, so a module that reliably times out
leaked one wazero runtime per run - and `Manager.run` defers `Stop` discarding
the error, so nothing upstream would notice.

### The sandbox boundary itself holds

`escape.rs` asks all four host functions for ranges outside its own linear
memory - past the end, wrapping u32, over the per-call cap - and every one is
refused, the guest keeps running (the ABI has no way to hand it an error), the
host buffer stays untouched, and each refusal is reported. The import table
holds only the four host functions, so there is no file or socket to reach for.
The run-time deadline is real (`WithCloseOnContextDone`, proven by `spin.rs`),
and `MaxFuel` was already replaced by it - a counter nothing could spend.

## Adversarial pass: the p2p layer

Two amplification findings, both fixed, both measured.

**1. gossipsub relayed anything, and decoding it was 204x amplification.**
Critical.

`pubsub.NewGossipSub` was called with **no topic validator**, so gossipsub
accepted any message on a subscribed topic from any connected peer and forwarded
it to the mesh BEFORE the application looked at it. Every check the engine makes
- is the proposer a validator, does the signature verify, is the height current
- runs in a handler that executes after the relay.

The decode was worse than the bandwidth. Measured: a **1 MiB** message of
`{"block":{"txs":[{},{},...]}}` declares **349,503** transactions (`maxBlockTxs`
is 512) and allocates **204 MiB of heap** - and it decodes with **no error**, so
nothing rejected it early. From a peer with no key, no stake and no place in the
validator set, repeatable at line rate.

Fixed with `consensus.NewGossipGuard`, registered through a new
`transport.Config.Validator` hook. It bounds payload size, JSON token count and
nesting depth with a token walk that costs **3 KB** where the decode costs
204 MiB - five orders of magnitude - and it bounds STRUCTURE rather than any
particular field, so it covers every message type and any future one. Rejection
(not Ignore) is what also lowers the sender's gossipsub score, which is what
eventually prunes a peer that keeps sending garbage.

The guard deliberately does **not** verify signatures: it runs on every message
from every peer before any is known to be worth anything, and a signature check
there is work an attacker chooses for the node. The handlers keep that job. That
choice is pinned by a test so it is not "fixed" later by accident.

Its own tests found two gaps in the first version: `{"block":` was accepted
(a truncated value leaves depth above zero, and `Token()` reports plain `EOF`
for it), and 5,000 nested arrays was accepted (10,002 tokens, under the token
bound - depth is a separate axis). `dec.More()` also could not catch two
top-level values, because the loop consumes to EOF first; the count is now kept
during the walk.

**2. A 38-byte sync request made every node broadcast a megabyte.** High.

`lastSyncRequest` rate-limited how often a node ASKS for blocks. Nothing limited
how often it ANSWERS, and a response is up to `MaxSyncResponseBytes` (1 MiB)
**broadcast to the whole topic**. So one 38-byte `BlockSyncRequest{Height:0}`
had every node holding the chain read from disk, marshal a megabyte, and publish
it, which gossipsub then fanned out to each node's mesh peers. At ten nodes that
is roughly 60 MiB of mesh traffic from 38 bytes, and the responses are
themselves 1 MiB gossip messages every node then decodes.

Answering is now rate-limited too, at half the round timeout - deliberately
shorter than the request side's own limit, so an honest lagging peer asking at
its natural cadence is never held up and the only traffic dropped is a flood.
This costs nothing in correctness because responses are broadcast rather than
addressed: one response serves every peer at that height, whether one asked or a
thousand did. Verified: 500 requests in a tight loop were answered 500 times
before the fix, at most twice after.

### `internal/transport` had no tests at all

Which mattered the moment it gained a security-relevant hook: a validator that
is registered but never consulted is indistinguishable in behaviour from one
that accepts everything. There are now three, over real libp2p hosts and real
gossipsub: a rejected message is not delivered, the validator sees every
message, and the hook stays optional.

### Checked and found sound

- **The sync responder's size bounds.** `MaxSyncBatch` (8 blocks) and
  `MaxSyncResponseBytes` (1 MiB) were already there; the missing piece was the
  rate, not the size.
- **libp2p's own limits.** v0.41 installs a default resource manager and
  connection manager, so per-peer connection and stream counts are bounded
  without configuration here.
- **Synced blocks are not trusted.** A `BlockSyncResponse` carries the commit
  votes and the receiver verifies each independently through the same tally as
  ordinary vote gossip, so a synced block commits under the same quorum rule as
  a proposed one.

### Peer scoring, configured

The guard decides one message; the score decides the PEER. Without scoring, a
peer sending nothing but garbage is refused a million times and stays a full
mesh member, so the work never ends.

Measured on two real hosts with real gossipsub: **60 messages published, only 6
reached validation, worst score -1800** against a graylist threshold of -1500.
That is the whole point - the node did six units of work instead of sixty, and
the peer was then ignored.

The measurement corrected the design. The first version put the graylist at
-2500, computed from P4's squared counter. A live run showed the validator being
called only **6 times for 60 published messages**: once a score crosses a
threshold gossipsub stops delivering that peer's messages for validation, so the
counter stops climbing and the score plateaus and decays back up. -2500 was
therefore unreachable by P4 - decoration. It is -1500, crossed by the sixth
rejection.

What is deliberately OFF, and why: **P3 (mesh message deliveries)**. P3
penalizes a peer for delivering too FEW messages, which is how a busy network
detects a freeloader. This network is not busy: a validator set of two or four
on an idle chain delivers almost nothing for long stretches, so every honest
peer would accumulate the deficit-squared penalty and be graylisted - and a
graylisted validator's votes stop being processed. That is a self-inflicted
partition, worse than the freeloading it would catch. Enabling it needs a
measured per-topic message rate from a live network first, and until that number
exists zero is the honest setting. A test fails if someone turns it on.

**P5 (application-specific)** is zero for a different reason: it could give known
validators a positive baseline, but a peer id is a libp2p identity and a
validator is an ed25519 account, and this node holds no mapping between them.
Deriving one from the connection would mean trusting the thing being scored.

**IP colocation** has a threshold of 4, not 1. Legitimate deployments share an
address - two nodes behind one NAT is a topology this project verified working
earlier - and a strict threshold would have penalized exactly that. Four allows a
small cluster and still charges a sybil farm, whose penalty is the square of the
excess: twenty peers on one address scores -12800.

Per-topic parameters are attached at JOIN time (`Topic.SetScoreParams`) rather
than listed in `PeerScoreParams.Topics`. A topic list would be a second place to
keep in sync, and a topic missing from it is scored by nothing at all - silently,
because an unscored topic still works, it just stops charging anyone.

Several of the tests assert that an HONEST peer is never punished, because that
is the direction a misconfiguration breaks: a fresh peer starts at exactly zero,
so every negative threshold has to be strictly below it or a new peer is
graylisted on arrival.

## Timing side channels, verified

Measured, then fixed one real defect and corrected two comments.

**The defect: a constant-time compare that could not fail.** `admin/auth.go`
looked up a credential as `a.keys[apiKey]` - the raw secret as the map key -
then ran `subtle.ConstantTimeCompare(apiKey, key.Key)` under a comment saying it
prevented timing attacks. `AddKey` had stored the record under `key.Key`, so a
map hit already proved the two strings equal; the compare returned 1 every time
it ran, for the life of the process. A no-op with a reassuring comment is worse
than no defense, because it stops anyone looking.

**Probed before changing anything.** 60000 samples per class, 1001 keys loaded,
credentials sharing 0, 32, 63 and 64 bytes of prefix with the real one. Medians:
264, 274, 267, 313 ns - no ordering by prefix length. So the old code was not
exploitable, but not for the reason it gave: Go seeds each map's string hash from
process-random state, which destroys the byte-by-byte oracle a naive compare
would expose. The safety rested on a runtime implementation detail nobody had
written down.

**The fix keys the map by `sha256(credential)`.** The secret now never reaches a
comparison at all - what the map compares is a digest, so a perfect oracle on the
lookup yields digest bits and turning those into the credential is the preimage
problem. That is a property of SHA-256 rather than of Go's map internals:
statable, and true whatever the runtime does next. No constant-time compare was
added back, because comparing the presented digest against the stored digest is
the same no-op one indirection later. Re-probed after the change: 447, 446, 434,
454 ns, still no prefix ordering, ~180 ns dearer.

The stored record also **drops the plaintext**. Nothing downstream read
`APIKey.Key` - it existed only to feed the no-op compare - and
`AuthenticateKey` hands the record to callers its own doc invites to log the
key's name. A struct that travels toward logs must not carry a working
credential. Six tests pin it, and they fail against the old plaintext-keyed map.

One imprecision of my own, corrected in the comment: a digest takes the
credential's length out of the LOOKUP, but hashing still costs in proportion to
the input, so a 16-byte credential resolves faster than a 64-byte one (363 vs
447 ns). That is the length of what the caller presented, which the caller
already knows.

**A WASM guest cannot read a clock, and that is now pinned.** Every other
sandbox limit bounds what a guest can DO. A clock bounds what it can MEASURE: a
guest runs in the node's own process, alongside the validator's signing key and
the ledger, so a nanosecond timer would let it time its own host calls and turn
any data-dependent branch in the host into a side channel without breaking a
single other limit. The runtime denies it by omission - no WASI module is
instantiated, and all four host functions (`log`, `send`, `get_memory`,
`set_memory`) return nothing, so there is no reply channel to build a timer
from.

Omission is fragile, so `testdata/clock.rs` asks for
`wasi_snapshot_preview1.clock_time_get` and must be refused at instantiation.
**Measured that the guard is needed**: adding the ordinary one-liner
`wasi.MustInstantiate(ctx, r)` leaves every pre-existing test in the package
passing and fails only the new one. A second test reads `guest.wasm`'s import
table and asserts all four host functions return nothing - authoritative rather
than a second copy, because wazero refuses to instantiate a module whose
declared signatures disagree with the host's.

**Checked and found clean, no change:**

- **ed25519 signing and SLIP-10 derivation.** `ed25519.Sign` is constant-time in
  Go, and `accountFromSeed` walks the path unconditionally with HMAC-SHA512 - no
  secret-dependent branch or retry loop.
- **Keystore unlock.** scrypt with the file's own parameters, then AES-GCM,
  whose tag comparison is constant-time in the standard library. The `subtle`
  compare that survives there is a real one: it checks the decrypted public key
  against the advertised one, two independently derived values.
- **No secp256k1 private key exists in the node.** `ethsig.SignDigest` is called
  only from tests, so there is no signing side channel on that curve.
- **No MAC is verified against user input anywhere.** Authentication is by
  signature, a public-key operation on public data, so the classic
  forgery-oracle compare has no site here. Every `bytes.Equal` on a digest
  compares block hashes, which are broadcast.
- **The rate limiter's FNV-1a fingerprint of a credential.** Its comment is
  accurate: collisions would share a bucket, but targeting one needs the
  victim's token, and colliding with any of a handful of active tokens is ~2^57
  work. Left alone.
- **One credential surface only** (`Authorization`), read in two places. No
  credential is ever accepted in a query parameter.

Second comment corrected: `isHighS` says it compares bytewise so it is "constant
in shape", which sits close enough to "constant-time" to mislead. It returns
early on the first differing byte, and does not need to be constant-time: both
operands are public - `s` arrives inside a submitted signature and the constant is in the
Solidity source.

### Still not covered

Nothing on the list. Remote existence oracles (whether a response reveals that
an account or provider exists) were treated as out of scope here: they are
answered by the response itself, not by its timing.

## Money: how the operator gets paid

Everything in this section is OFF by default. Every price and every share is
monetary policy, so none of it is a default the engine picks on an operator's
behalf.

### The maintainer share: how the person running the network gets paid

The protocol fee pays validators for validating. It paid nothing for
MAINTENANCE, and the two alternatives available to a founder both fail at
exactly the wrong moment: a validator share **dilutes as the set grows**, so it
shrinks precisely as the project succeeds, and a genesis allocation funds a
moment rather than an ongoing obligation.

`consensus.maintainer_account` is now paid a fixed cut of the fee, taken before
the rest is split pro rata. Zero by default.

- **It is a share OF THE FEE, not of the transfer**, so it inherits the fee's
  1% ceiling. At the maximum fee and this share's own ceiling, a maintainer
  takes 0.5% of transferred value and can never take more, whatever anyone
  configures. A cut quoted against the transfer would need its own ceiling and a
  second worst case to reason about.
- **Capped at half the fee** (`MaxMaintainerShareBasisPoints = 5000`), in code
  rather than config, for the reason the fee itself is capped: the fee is what
  makes a bond worth posting by a third party, and a maintainer funding
  themselves out of most of it would be spending the budget that buys the
  network its security.
- **An operator cannot quietly zero it.** It is read from config like the fee
  rate, and like the fee rate every node must agree: a node using a different
  share computes different balances from the same block and forks itself off.
  Nobody built that as enforcement, it falls out of the fee being consensus
  arithmetic - but it is stronger than a promise, because the cost of not paying
  is leaving the network.
- **It is a tax on users, and it is disclosed rather than hidden.** Default
  zero, bounded in code, printed at startup with both the share of the fee and
  the share of transferred value, and readable through `Engine.MaintainerShare`.
- **A misconfiguration refuses to start.** A share over the ceiling, a share
  with no account, a reserved id, or anything that is not 64 lowercase hex.
  That last one matters most: a mistyped account is a fee paid every block into
  something nobody holds a key for, forever, and it looks exactly like it is
  working.

The arithmetic divides before multiplying, as `FeeFor` does, because the accrual
can reach the supply cap and `accrued * 5000` wraps a uint64 - a wrapped share
is a wrong balance on every node rather than an error anywhere. Pinned at seven
scales from one base unit to the whole cap.

What is still only the CTO's to decide is the number. The mechanism defaults to
paying nobody.

### Storage rent, built

Stored module bytes are now charged for. The manager charged for a RUN and
nothing for STORAGE, so a deployed module was the one resource a deployer took
for free and kept indefinitely - `agent/module/<id>`, raw wasm up to 32 MiB each,
on a service served to the network.

`MeterConfig.StoragePrice` is credits per MiB per day, zero by default, on the
same reasoning as the per-run price: what storage costs is monetary policy. When
set, an hourly sweep charges each deployment's owner through the same consensus
settler the run charge uses, and evicts what goes unpaid past a grace period
(72h default). Eviction is what makes rent a bound on disk rather than an
unpayable debt that grows.

**The rounding is the whole problem, and a naive version silently charges
nothing.** Rent for one hour on a small module is a fraction of a credit.
Truncating per sweep charges zero forever, and gets WORSE the more often the
sweep runs - the opposite of what a shorter interval should mean. So the
watermark advances only by the time the paid credits actually cover, and the
sub-credit remainder keeps accruing until it crosses one credit. Measured
against the naive version: **24 hourly sweeps charged 0 where one daily sweep
charged 10**. Two tests pin it, and both fail against the truncating
implementation. The accrual is `big.Int` because bytes x price x nanoseconds
passes 2^64 well before any of the three is unreasonable, and a wrapped multiply
produces a wrong bill rather than an error.

**A second defect surfaced while building it: the payer was whoever the client
said.** `deployer` was a request field, and the auth interceptor only checked
that the caller held a valid key with the deploy permission - not that the named
account was theirs. Any caller who could deploy could bill any account the node
holds a signing key for. Survivable when every key was the operator's and the
only charge was one run; not survivable with rent, which re-bills the named
account every hour for as long as the bytes sit there. The payer is now the
account on the caller's own key, the same rule the OpenAI route already uses,
and naming a different account is refused rather than silently redirected. Four
tests; the refusal test fails against the old behaviour.

Other properties pinned: the deployer is persisted and survives a restart (rent
has to find the payer an hour later); re-deploying the same id does NOT reset the
rent clock (or the bill is avoidable by re-pushing before every sweep); a record
written before rent existed starts its clock at the sweep rather than
back-charging to CreatedAt; a record with no owner is skipped, never evicted, so
an upgrade cannot delete an operator's own agents; and a node configured to
charge rent it cannot collect refuses to start.

Not done: `Deployment.Deployer` is not surfaced in the proto response, so a
client cannot read back who it is billed as. The record has it and the CLI does
not show it.

### Superseded: the per-account deployment cap

Nothing bounds how much wasm one deployer accumulates on a node.
`agentapi.Manager` persists `agent/module/<id>` as raw bytes, up to
`DefaultMaxModuleBytes` (32 MiB) each, and `matrix.agent.v1.AgentService` is
served on the network (default `0.0.0.0:9094`). Today the deployer is
operator-authenticated, so this is an operator's own disk. It stops being that
the moment keys are issued to paying deployers, which is what the metering
exists for.

**This was previously written up as "no cap on deployments per account", asking
the CTO for a number. That framing was wrong on three counts, the question
should not have been asked in that shape, and rent above is what was built
instead.**

1. **Count is the wrong unit.** Disk is the resource and `Deployment.ModuleSize`
   already records it. A cap of 10 permits 320 MiB and a cap of 1000 permits
   32 GiB, so a count only bounds disk if every module is assumed to be maximal.
2. **There is nothing to count against.** `Deployment` carries ID, status,
   module hash and size, limits, last output, last error, last charge and
   timestamps - and no owner. The deployer IS known at deploy time
   (`Deploy(ctx, id, module, limits, deployer string)`, used to charge) and is
   then dropped. So no per-account accounting exists at all, and a per-account
   cap cannot be expressed, let alone enforced across a restart.
3. **The metering charges the wrong thing.** `LastCharge` is the credits settled
   through consensus for a RUN. Storage is charged nothing, so it is the one
   resource a deployer takes for free and keeps indefinitely. In a market the
   answer to heavy resource use is to price it, not to forbid it: a cap turns a
   paying customer into a refused one and bounds revenue at the same time.

The answer was therefore **storage rent through the metering path that already
existed**, not a number, and it is built - see above. A hard byte quota, if one
is ever wanted as a backstop, has its prerequisite in place now: the deployer is
on the record.

### Rotating the maintainer account

`maintainer_account` was startup config and nothing else, so moving to a fresh
key meant editing YAML on every validator and restarting them - and because the
share is consensus arithmetic, a network part-way through that edit computes
different balances from the same block. A fork. The operation most likely to be
needed was the one most likely to break the chain.

A rotation is now a transaction to `consensus/maintainer/rotate/<successor>`,
applied inside the same critical section as the block's transfers, so every node
switches at the same height with no coordinated restart.

Only the CURRENT maintainer may rotate, proved by the transaction's own
signature. **A validator-quorum override was deliberately not added**: it would
let a validator majority redirect the maintainer's income to themselves, turning
a standing share into something held at the set's pleasure. That may be right for
some network; it is not a default for the engine.

No new persisted key. The engine already replays every committed block at startup
to rebuild the dedup set and the burn tallies, so rotations replay too and the
CHAIN stays the single authority - last committed rotation wins. The account is
an `atomic.Pointer` because the block-apply path writes it while the ledger
critical section reads it, and taking `e.mu` under the ledger lock would
establish a second lock order; the validator set is atomic for the same reason.

What it does NOT fix, and the file says so: a lost key cannot sign, so it cannot
rotate. This is key hygiene, not key recovery. Nothing in a chain can be.

### A deployment's rent rate is pinned, and prices have a ceiling

Rent RECURS, which makes it different from the per-run price: a deployer agrees
to a rate once, and then the operator can change it while their bytes are already
on the disk. Billing at the live config value let an operator raise the price on
modules already stored and either drain the deployer or evict them - a bill
nobody agreed to, on data already handed over. **Measured against the old
behaviour: raising the price a thousandfold billed 21,486 credits where 21 were
agreed.**

`Deployment.RentRate` fixes the rate when the clock starts and the sweep bills
from the record. Same shape as the keystore reading the FILE's own KDF
parameters. A price change applies only to deployments made after it;
re-deploying an id keeps the agreed rate, because updating a module is not a new
tenancy.

`MaxRunPrice` and `MaxStoragePrice` exist too, and the comment is honest about
what they are not. They are not the fee's ceiling, which exists because a fee is
taken from a transfer the payer did not choose. An agent price is opted into -
too high and nobody deploys, a refusal rather than a theft - and the real
protection against re-pricing is the pinned rate. What the ceilings catch is a
supply-cap-sized number pasted into a price field.

### What the numbers actually are

The CTO asked what it would take to get rich off this. Worth recording, because
the answer is counterintuitive and the arithmetic is fixed in code.

The composite maintainer take is `fee_bps x maintainer_bps`, both compile-time
capped, so **0.5% of transferred value is the hard ceiling** and 0.2% is a
realistic setting. Calibrated against the category (searched, not estimated):
Akash did **$3.15M of lease revenue in all of 2025**; io.net peaked around $20M
annualized and fell back; Render takes a 5% service fee - ten times this
protocol's ceiling - and that yields about $110k/yr.

**Capture 100% of the settled volume of the largest decentralised compute network
that has ever existed, at the maximum rate the binary permits, and the maintainer
earns roughly $100,000/year.** The fee path funds a maintainer. It does not make
one rich, which is exactly what it was asked to do.

Two corrections to what was said in the moment, kept because both were wrong in
instructive ways. "Token price does not affect fee income" is false HERE: it
holds only where prices are quoted in fiat and converted at settlement, and
nothing in this code reprices a job - `price_per_unit` is quoted in MATRIX and
static until a provider re-registers. And "stock beats flow by two orders of
magnitude" was a units error, comparing a capitalized quantity (FDV) against one
year of income; the token price cancels out of the comparison entirely.

## The on-ramp, end to end

It is connected now. It was not before, in a way the handoff had recorded
incorrectly as "none of which is code".

**Locking is consensus-ordered.** A lock is a signed transfer to
`bridge/lock/<eth address>`, applied by every node from the committed block. This
is simpler than the burn unlock, which needed a quorum because observing an
Ethereum burn requires an Ethereum endpoint and consensus may only depend on
committed state; a lock needs no outside observation, because the user's signed
intent IS the transaction.

The lock id is derived, not counted. `bridge.Lock` numbered locks with a per-node
sequence - exactly the kind of state two nodes disagree about. The id now comes
from the transaction (nonce, sender, recipient, amount), and a test pins it
byte-identical to the bridge's own derivation, because an id that differed
between the chain and the bridge would attest to a lock the contract could not
match.

It is the one reserved recipient besides a stake bond that carries value, and it
pays no fee. **Load-bearing, not incidental**: escrow must receive the full
amount, because the wrapped supply minted against it is computed from what the
user locked, so a fee would mint more wrapped than the escrow holds and break the
1:1 backing by exactly the fee. The amount is also kept out of `credited[]`,
since escrow is collateral rather than earnings.

A node with no bridge still escrows - it would otherwise diverge from every node
that has one - and simply cannot attest afterwards.

**The attestor key lives in the encrypted keystore.** See the money section above
for the format guard; operationally: `matrix bridge attestor-new --out <path>`
generates one and prints the address to register in the contract's attestor set,
`bridge.attestor_keystore` points at it, and `MATRIX_ATTESTOR_PASSPHRASE` unlocks
it. A configured key that will not unlock is a startup FAILURE, because a
validator that looks like it is attesting and is not means mints silently stop
reaching quorum with nothing pointing at the node responsible.

**`GetLockAttestation` returns one signature.** Each validator holds its own key
and the contract counts the threshold, so a client gathers m of them. It is a
public read: it signs nothing new, and the mint it authorizes goes to the address
the LOCKER chose, so an open endpoint lets anyone gather an authorization and
nobody redirect one.

**And it has been shown to mint.** `BridgeE2E.test.ts` used to drive only the
legacy `bridge.Lock` - a direct ledger write with a per-node counter and no
production caller - so the derivation and the attestation a real node produces
had never been fed to `WrappedMatrix.mint`. The on-ramp was built and never
demonstrated. A third e2e case now drives the consensus path end to end:
consensus-derived lock id, real signatures, real mint, and a replay of the same
id reverted. `cmd/bridge-attest -consensus -nonce N` exercises it.

That e2e proved the derivation and the attestation and **proved nothing about
the locking**, because it did the escrow move and the record in two separate
`Atomically` calls where the engine does both in one. The section below is what
happened when a real node did it the engine's way.

`matrix bridge lock --to <address> --amount N` is the front door and prints the
lock id the attestation step needs; `matrix bridge attestor-new` generates a
validator's key.

The recipient format now lives in `packages/protocol` (`bridgeLockRecipient`),
transcribed into the published SDK and held identical by the differential test,
with a Go test pinning the same literal. That closes a drift surface of the worst
kind: the recipient IS the instruction, so a client that builds the string
wrongly gets no error - it makes a transfer to a different reserved namespace or
to an account id nobody holds, and the money goes somewhere quiet.

So: lock -> gather a threshold -> `WrappedMatrix.mint` -> wMATRIX exists -> a pool
can be seeded. What remains is genuinely not code: a mainnet deploy, an audit,
the liquidity capital, and putting the attestor addresses in `ATTESTORS` at
deploy time.

### The first lock on a real node deadlocked it

Driven against a running `matrixd` with a hardhat chain behind it, an ordinary
transfer committed and `matrix bridge lock` reported "did not commit within
1m0s". A `SIGQUIT` stack dump named it in one goroutine: `commitAndApply` ->
`Ledger.Atomically` -> `applyBridgeLock` -> `Bridge.RecordLock` ->
`Ledger.Atomically`. A non-reentrant `RWMutex`, re-entered by the goroutine
already holding it. `RecordLock` also took `b.mu` while holding the ledger, the
inverse of the order `Reconcile` and `ProcessBurn` use, so it was an AB-BA
deadlock as well.

**The consensus driver goroutine never came back.** The node kept answering
`health` (SERVING) and `tx list`, and hung on anything that reads a balance. It
produced no further blocks. Nothing in the log said so, because a parked
goroutine writes nothing - the last line was an ordinary pubsub validation, six
minutes before.

Two things about how it got there are worth keeping:

- **The rule was already written down.** `ApplyAttestedUnlock` is the same shape
  and takes the caller's `LedgerTx`; the `consensusOrdered` field doc spells out
  that mixing the two lock orders is a deadlock. `RecordLock` was written next to
  both and did neither.
- **Every test passed the whole time**, including the four-validator cluster
  test, because all of them drive a `fakeLocker` that takes no locks. A test
  double that omits the only behaviour that matters tests the harness.

The fix is `ApplyAttestedUnlock`'s: `RecordLock` takes the caller's
`market.LedgerTx` and no lock of its own. The parameter is deliberately unused -
it exists so the contract is checked by the compiler instead of by a comment. It
is deliberately NOT gated on `consensusOrdered`: the unlock half has a mode (a
solo node may release escrow itself, a set may not), the lock half does not, and
gating it would leave a solo node escrowing value it never recorded.

`Reconcile` had a smaller version of the same mistake: it read the counters
outside the ledger lock and the escrow balance inside it, so a lock landing
between the two reads reported a backing mismatch that never existed. It now
takes one snapshot in one section.

`TestARealBridgeDoesNotWedgeTheNode` wires a genuine `*bridge.Bridge` into a
running cluster and, after the lock, commits an ordinary transfer to prove the
node is still a node. Every read in it is watchdogged, because the failure mode
is a hang and an unbounded poll loop turns a one-sentence failure into a
1500-line stack dump. Both new tests were run against the deadlock restored.

**Then the whole round trip, on a live node:** `matrix bridge lock` committed,
escrow held 4,000,000,000, a transfer after it committed at block 1,
`GetLockAttestation` returned a real signature from the node's keystore attestor,
`WrappedMatrix.mint` minted 4e18 wMATRIX from it on chain, and the replay
reverted with `LockAlreadyMinted`. That is the on-ramp, demonstrated rather than
argued.

A **public** testnet run (Sepolia) is still not possible from here: it needs a
funded key and an RPC endpoint, which is operator capital, not code.

## Docs and website, brought in line

The consensus-ordered burn unlock landed in code but not in prose, and the prose
said the opposite. **Four code comments contradicted the code they headed** -
`node/bridge_watch.go`, `node/node.go`, `bridge/watcher.go` and `bridge/doc.go`
all still said the unlock was "still applied by whichever node runs the watcher"
and that making it consensus-ordered was "a separate, larger design change".
`consensus/burnunlock.go` even quotes one of them as the problem it was written
to solve, while the quoted comment stood unamended. A comment that contradicts
its code is worse than none, because it is what a reader trusts instead of
reading on. That is the same failure as the no-op constant-time compare, one
layer up.

Fixed in code and in five doc surfaces: `contracts/README.md`, the marketplace
and configuration doc pages, the token product page, the `TokenBridge` diagram
description, the introduction, and the token-and-bridge policy proposal.

Three things the docs never covered and operators will hit:

- **The libp2p peer id is persisted.** The network-setup guide documented the
  consensus identity's persistence and not this one, and it attributed the
  symptom of an ephemeral id - `all dials failed` - to a typo. Now stated, along
  with the 10-second bootstrap re-dialer.
- **`matrix fund` refuses on a validator set.** Documented in the CLI page and
  the genesis config section, with the reason (it is not consensus-ordered, so
  it moves value on one node and forks the provider emission) and the two
  alternatives.
- **What a guest cannot exhaust.** The agent guide documented the memory ceiling
  and the deadline, which bound one call and say nothing about what a guest
  accumulates inside it. Now carries the measured 586 MiB and 1172 MiB, the byte
  budgets, the clamping of requested limits, and the no-clock boundary.

Added to the architecture page: gossip validation before the relay, and peer
scoring. Added to the README layout: `packages/protocol` and `packages/sdk`,
neither of which was listed, plus the SDK build section.

### Screenshots

Re-captured, all at the 1280x577 the originals used so they drop into the README
layout without reflowing it, and every one checked by eye rather than by exit
code.

**The console shot was stale in a way that mattered.** It showed MarketService
and InferenceService on separate ports 9091 and 9092; the console now points both
at one matrixd HTTP endpoint on 9093. It also showed the Connection tab,
disconnected - an empty form under a caption claiming "the whole thing running".
It is now the Providers tab connected to the built-in demo backend, which exists
so the console can be driven with no daemon: all six tabs, and local versus
remote providers with the peer ids they were discovered from.

**Four captures were deleted.** `web-introduction`, `web-cli`,
`web-network-setup` and `web-agent-dev` were doc-page shots that nothing
embedded, and the screenshots README claimed five files while nine existed.
`web-cli` had already been removed once on the reasoning that a doc page is
navigational rather than a claim, then came back unreferenced - so deleting
again without a rule would just repeat. The README now carries the rule: every
file in the directory is embedded in the root README, and a capture is added
only together with the change that embeds it. A screenshot of prose goes stale
every time the prose changes and nothing points at it to notice, which is
exactly how those four came to show a site that had moved on. Verified before
deleting that the only remaining mention was an untracked `.agents` scratch file
listing images themselves deleted long ago.

Two capture failures are recorded in that README, because the file is the
instructions for doing this again:

- **Wait for the entry animation to finish, and only for the FINITE ones.**
  Awaiting every animation hangs forever on a looping background one - the first
  script burned 600s that way. The capture now asserts no visible element is
  below full opacity, so a mid-fade shot fails loudly instead of shipping an
  almost-invisible headline.
- **Chromium in a container needs `--no-sandbox --disable-dev-shm-usage`.**
  Without them the renderer dies partway through and every later wait hangs
  against a closed target.

## Non-blocking notes

Known and accepted, not blocking:

- **A two-machine libp2p join is still unverified.** Separate network
  namespaces with distinct IPs, one behind a NAT, now agree on the chain in both
  directions - which is what caught the peer-identity, `FundAccount`, bootstrap
  re-dial and nonce-reuse defects. Two separate physical machines, real WAN
  latency and packet loss are still untested; `sch_netem` is absent from this
  kernel, so latency and loss cannot be injected here at all.
- **`apps/console`'s native Tauri bundle still won't build in this sandbox**
  (missing `webkit2gtk`/`gtk`). The frontend itself builds.
- **Wallet/agent metering nonces are non-monotonic across restarts** but kept
  collision-safe by the signed timestamp.
- **Committed-transfer history read is O(history) per settlement.** Fine at the
  current scale.
- **CI needed no changes.** The existing gofmt check and four test-group gates
  cover the new code.
