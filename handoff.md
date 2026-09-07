# Handoff

State of `main` as of `cb4db0c`, plus the product-surface work on
`claude/handoff-md-checklist-s0ptw1` through `f3511c9`. Everything below was run, not inferred; where
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

Not verified: a genuine two-machine join over real libp2p. The join tests use
the in-process bus, and the earlier two-node work was manual. Worth doing.

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
- **An API key spends from no account until one is set.** `account` under
  `security.api_keys` is what makes a key usable on `/v1/chat/completions`, and
  the node must hold that account's signing key to settle for it. Leaving it
  empty is the safe default and means that key cannot buy inference.
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

## Still pending on the product surface

**A `matrix inference` flag for the client-signed path.** The CLI still drives
`SubmitInferenceJob` + `FulfillInferenceJob`, which is fine there - it holds the
wallet key locally, so the hosted path is the honest one for it. A
`--client-signed` flag would mostly exercise the new path by hand.

**Streaming.** `connectapi` rejects streaming methods by design and the protos
declare none, so token-by-token output is its own piece of work: a server-stream
RPC plus Connect streaming framing, and on the OpenAI route the SSE `data:`
frames a client expects. Right now `"stream": true` is refused.

**A browser wallet and a top-up relayer.** `token.Account` is a single ed25519
keypair, so generating one in the browser and sealing it with a passkey is
natural, and (c) means the node no longer needs that key. Nothing does it yet. A
top-up flow also needs a relayer turning USDC into native MATRIX (DEX buy, then
`WrappedMatrix.burn`) so the word "wMATRIX" never reaches a user.

**Open reads on the public endpoint.** `connectapi` still requires a valid key on
every method, reads included. A browser cannot hold a secret, so a public RPC
wants reads open and writes signature-authorised. Deliberately not changed here:
opening reads by default would widen what an unauthenticated caller can see on
every existing node, so it should arrive as config an operator turns on.

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

- **A genuine two-machine libp2p join is still unverified.** The join tests use
  the in-process bus; the earlier two-node work was manual. Worth doing on real
  hardware.
- **`apps/console`'s native Tauri bundle still won't build in this sandbox**
  (missing `webkit2gtk`/`gtk`). The frontend itself builds.
- **Wallet/agent metering nonces are non-monotonic across restarts** but kept
  collision-safe by the signed timestamp.
- **Committed-transfer history read is O(history) per settlement.** Fine at the
  current scale.
- **CI needed no changes.** The existing gofmt check and four test-group gates
  cover the new code.
