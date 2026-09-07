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

Still not verified: separate hosts, NAT traversal, and real latency or packet
loss. Two processes on one machine is not two machines, and this note should not
be read as if it were.

**Three defects this found that a green test suite did not.** All three were
invisible to a single node, which is the point:

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

**A USDC on-ramp.** This is now the only thing between a MetaMask user and using
the network with money they already hold. The path exists in full - a DEX
purchase of wMATRIX, `WrappedMatrix.burn`, and the burn watcher in
`node/bridge_watch.go` releasing native from escrow - and every piece is built.
What is missing is a mainnet contract deploy, an audit, and DEX liquidity, none
of which is code. Note also what that watcher's own comment says: the unlock is
a per-node relayer and is NOT consensus-ordered, which is correct for a solo
operator and not for a validator set.

**A two-process libp2p join is now verified; a two-*machine* one is not.** Two
processes with separate data directories agreed on the chain in both directions
over real libp2p TCP, and that run turned up three defects (see "What two
running nodes verified" above). Separate hosts, NAT and real latency remain
untested.

**Copies of byte layouts.** The node owns four canonical payloads now (the
ed25519 transfer, the ed25519 run authorization, and the two EIP-712 digests).
`packages/sdk` and `apps/web` each carry TypeScript copies of what they need,
because there is no workspace linkage. Every copy is pinned to a shared golden
vector, so a drift fails a test - but a workspace, or publishing the SDK, would
remove a copy rather than guard it.

**Client-streaming** is refused by `connectapi` by design; nothing needs it.

**No multisig, and no vesting contract.** Unchanged from the proposal, and worth
restating next to the wallet work: `MatrixToken.mint` is still `onlyOwner` with
a single address, and that key can issue to the cap. Moving it to a Safe belongs
before any mainnet deploy.

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

## Non-blocking notes

Known and accepted, not blocking:

- **A two-machine libp2p join is still unverified.** Two separate processes on
  one machine now agree on the chain in both directions over real libp2p TCP,
  which is what caught the peer-identity and `FundAccount` defects. Separate
  hosts, NAT traversal and real latency are still untested.
- **`apps/console`'s native Tauri bundle still won't build in this sandbox**
  (missing `webkit2gtk`/`gtk`). The frontend itself builds.
- **Wallet/agent metering nonces are non-monotonic across restarts** but kept
  collision-safe by the signed timestamp.
- **Committed-transfer history read is O(history) per settlement.** Fine at the
  current scale.
- **CI needed no changes.** The existing gofmt check and four test-group gates
  cover the new code.
