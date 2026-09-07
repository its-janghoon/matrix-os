# Proposal: initial MATRIX distribution, and how wMATRIX reaches a market

**Status:** proposal, for a decision. Nothing here is implemented.
**Author:** written against the code as it stands, commit `f6c6c76`.

Two questions block the network from being usable by strangers:

1. How does a buyer get MATRIX when nobody has any yet?
2. How does a provider turn earnings into money they can spend?

Both are policy, not engineering. But policy that ignores what the code enforces
is a press release, so this starts from the code.

## What the code already decides

These are constraints, not opinions. Changing any of them is a code change.

| Fact | Where |
| --- | --- |
| Hard cap: 1,000,000,000 whole MATRIX = 1e18 base units, 9 decimals | `internal/token/supply.go` |
| The cap is enforced on every issuance, inside the ledger's critical section | `Treasury.Issue`, `internal/token/genesis.go` |
| Genesis is idempotent and one-shot per store: named allocations plus one reserved reward pool | `Treasury.ApplyGenesis` |
| The reward pool is a real account, `native/reward-pool`, not an abstraction | `RewardPoolAccount` |
| Funding an account from the pool MOVES coins; it does not mint | `Treasury.FundFromRewardPool` |
| The only runtime faucet is `FundAccount`, and it is admin-authenticated | `matrix.market.v1.MarketService/FundAccount` |
| The bridge cannot create native MATRIX. Burning wMATRIX only releases native that was locked earlier | `internal/bridge`, `WrappedMatrix.sol` |
| Wrapped supply always equals locked native, by construction | mint requires an attestation per lock id; burn releases per event |
| **There is no emission to VALIDATORS.** They are paid from the fee, which is usage, not from an emission | by design |
| The validator set is chain state: a change rides in a committed block and takes effect at an epoch boundary, and it needs a quorum of operators to have approved it | `internal/consensus/setchange.go` |
| A validator proven to have equivocated is ejected the same way, with no config entry, because the evidence proves itself | `internal/consensus/evidence.go`, `Engine.reportEquivocation` |
| Bonded stake exists and is OFF by default. With it on, voting power is bonded MATRIX, admission requires a minimum bond, and equivocation moves the offender's whole bond to the reward pool | `internal/consensus/stake.go` |
| A protocol fee, capped at 1% in code, takes a cut of every value transfer a committed block carries and pays the validator set pro rata by voting power. OFF by default | `internal/consensus/fees.go` |
| A provider emission pays a fixed, halving per-block budget out of the genesis pool to the registered providers a block paid. OFF by default | `internal/consensus/rewards.go` |
| **The fee reaches consensus-settled value only.** Marketplace settlement pays it; `matrix wallet transfer` appends to the signed-transfer chain instead and pays nothing. That path is also not agreed by any quorum, so it needs to move to consensus for correctness before revenue | `internal/marketapi`, `token.SettledLedger` |
| **A bond is necessary to be admitted, never sufficient.** A quorum of operators still has to approve the change, so this is permissioned-with-skin-in-the-game rather than permissionless | `internal/consensus/setchange.go` |

Three consequences follow, and they drive everything below.

**The first buyer cannot use the bridge.** Burn-to-unlock releases escrowed
native. At genesis the escrow is empty, so the bridge is an exit before it is an
entrance. Somebody has to be given native MATRIX first, by us, at genesis. There
is no market-based alternative.

**Validators currently earn nothing.** Consensus pays no one. Every node happens
to run both a marketplace and a validator today, so validator revenue is job
revenue - which means an honest validator that has no compute to sell earns
nothing for securing the chain. That is fine for a testnet and untenable for a
network whose security is supposed to be worth defending.

**"Provider rewards" is not a thing that exists.** The reward pool is a pot with
an admin RPC pointed at it. Calling that an emission schedule would be a lie of
exactly the kind we spent this week removing from the website.

## Part 1: initial distribution

### What I am optimising for

1. A stranger can buy MATRIX without asking us. Otherwise we are a store, not a
   network, and every job settles at our discretion.
2. A provider can leave. An earner who cannot exit is not being paid, and word
   travels.
3. We are not the counterparty to every trade. Market making by hand is a
   business with a balance sheet, and it is the thing that kills small networks
   when the treasury runs dry.
4. Nothing we allocate can be worth more than the network's honesty. Team and
   investor coins that unlock before providers can sell is the single most
   reliable way to destroy the thing.

### Proposed allocation

| Bucket | Share | MATRIX | What it is for | Unlock |
| --- | --- | --- | --- | --- |
| Provider rewards | 40% | 400,000,000 | Paid per committed block to the providers that served work, on a fixed decaying schedule | Programmatic only, ~8 years, never by hand |
| Ecosystem and liquidity | 15% | 150,000,000 | DEX pool depth, bridge float, integration incentives | 20% at listing, remainder linear over 24 months |
| Team and contributors | 18% | 180,000,000 | | 12-month cliff, then linear over 36 months |
| Investors, if we raise | 15% | 150,000,000 | | 12-month cliff, then linear over 24 months |
| Treasury and grants | 10% | 100,000,000 | Audits, bounties, integrations, legal | Spend requires a published decision |
| Launch float | 2% | 20,000,000 | The coins that actually exist and trade on day one | At launch |

The judgment calls, stated plainly so they can be argued with:

- **40% to providers is the number that matters.** They are the side that has to
  show up first; a compute market with no compute is nothing. If any bucket
  should grow, it is this one, and the honest place to take it from is team and
  investors.
- **2% launch float is deliberately small.** It is enough to make a real market
  and small enough that we are not selling the network to fund it.
- **Team unlocks last.** A 12-month cliff behind a working bridge is the only
  credible signal available to us.
- **No allocation to validators as such.** See below - they should be paid by
  the protocol out of the provider-reward stream, not given a pre-allocation.

### How provider rewards must actually work

Not `FundAccount`. That RPC is an operator with a keyboard, and a schedule that
depends on us running it is not a schedule.

Concretely: consensus already commits a block per height and already applies its
transactions deterministically. A reward is one more deterministic step in the
same apply function - pay `R(height)` from `native/reward-pool` to the providers
that settled work in that block, pro rata to units served, where `R` halves
every ~2 years and stops when the pool is empty. Every node computes the same
number from the same committed block, so it needs no new trust and no new
message. When the pool is empty, emission ends: the cap enforces itself because
the pool was allocated once at genesis and never refilled.

That is roughly a day of work on top of what exists. It should ship before any
public claim about provider rewards is made.

### Who pays validators

Three options, and I would take the third:

1. **Nothing (today).** Validators are providers and earn from jobs. Breaks as
   soon as a validator has nothing to sell.
2. **Emission to validators.** Simple, but it pays for existence rather than for
   work, and it competes with the provider bucket for the same coins.
3. **A settlement fee, once stake exists.** Take a small fee - I would start at
   1% and cap it in code - from each settled job, and split it among the
   validators that committed the block. It scales with real usage, costs nothing
   when the network is idle, and gives stake something to earn, which is what
   makes slashing a deterrent rather than a threat.

Option 3 is built. The fee is capped at one percent in code, comes out of the
transferred amount, and is split across the validator set in force pro rata by
voting power. It is off by default, because the rate is policy and I am not
setting it for you: my recommendation is still to start at 1% (100 basis
points), which is what the cap allows.

One thing to decide with it. The fee is paid to the set in force rather than to
the validators whose precommits carried the block, and that is not a preference
- the certificate a node observes is node-specific, so a split that depended on
it would give two honest nodes different balances, which is a fork. The cost is
that the fee pays for stake rather than for participation: a validator that
never votes still earns. The remedy is the one we already have, which is to
remove it.

### What I would refuse to do

- Mint outside the cap. The code will not let us, and that is a feature.
- Unlock team or investor coins before a provider can sell.
- Promise a reward schedule that runs through an admin RPC.
- Call the reward pool "staking rewards". It is neither.

### What I need from you to finish this

1. Are we raising? If not, the investor 15% goes to providers and treasury.
2. Headcount to vest against, and over what period.
3. Jurisdiction for the issuing entity. A launch float that trades is a
   regulated event in most places, and the answer changes the sale mechanics
   (and whether there is a sale at all). This needs a lawyer, not me.
4. Audit budget. It sets the bridge timeline in part 2.
5. Is the validator set permissioned at launch, or open with stake? I still
   recommend permissioned for the first release, but the reason has narrowed
   again. A third-party validator can be admitted and, if it misbehaves
   provably, slashed and ejected while the network runs; misbehaving now costs
   the offender its bond. Two things are left before "open" is honest: a bond
   earns nothing, so nobody outside the operator group has a reason to post
   one; and admission is still a quorum of operators approving you, which is
   the right default while the fee does not exist. Turn on the fee, and
   "permissionless" becomes a policy switch rather than an engineering one.

## Part 2: getting wMATRIX to a market

Today: the contracts are written and tested, the deploy scripts exist for
mainnet and Sepolia, and **nothing is deployed anywhere**. There is no wMATRIX
to buy, so there is no way to cash out. The site says this; it should keep
saying it until it is false.

The bridge is also where the money will be, which makes it the most attractive
thing we will ever ship. The sequence below is ordered by that risk, not by
convenience.

### Gate 1: audit the attestation path

Scope: `WrappedMatrix.sol`, the digest it signs
(`keccak256(recipient || amount || lockId || chainId || this)`), the Go signer,
and the native escrow accounting. An external audit, not a review by us.

Gate: no critical or high findings open. Publish the report, including what we
chose not to fix and why.

### Gate 2: the attestor set is an operational thing, not a config value

Validators sign attestations with secp256k1 keys because the EVM cannot verify
ed25519. That means a second key per validator, and those keys are the mint
authority.

- Threshold: more than two thirds of the registered attestors, matching the
  consensus quorum. One key must never be enough, ever, including ours.
- Custody: hardware, and no two keys in one place or one cloud account.
- A documented, reproducible key ceremony, and a published rotation procedure
  that has been rehearsed once before mainnet.

Gate: the ceremony is written down, run once on Sepolia, and someone other than
the author can follow it.

### Gate 3: a cap on the bridge, in code

Nothing in the contract limits how much can be locked. A bug in a bridge with
no cap loses everything at once, which is how essentially every large bridge
loss has happened.

Add a mint cap - I would start at 5% of supply - raisable only after a defined
clean period. This is a contract change and belongs before deployment, not
after.

### Gate 4: Sepolia, in public, for 30 days

A full round trip: lock, attest, mint, trade, burn, unlock. Then a
reconciliation report anyone can reproduce: escrowed native equals wrapped
supply, at a stated block, from public data.

Gate: 30 days, no unexplained discrepancy.

### Gate 5: mainnet, then a pool, in that order

- Deploy with the cap and the audited attestor set.
- Seed one Uniswap v3 pool, MATRIX/USDC, from the ecosystem bucket. Publish the
  pool address and the amount. Do not "support the price" - a treasury that
  defends a number runs out of treasury.
- Publish the reconciliation on a schedule, automatically, from the node.

### Gate 6: a listing, only after the bridge has been boring for 90 days

A centralised listing before that is borrowing credibility we have not earned
yet, and it makes the bridge a target while it is still new.

### So: what does a provider's cash-out actually look like

Once gates 1-5 are done, and not before:

```
earn native MATRIX (settled by consensus)
  -> lock into bridge escrow on the L1
  -> validators attest the lock
  -> mint wMATRIX on Ethereum
  -> swap wMATRIX -> USDC in the pool
  -> withdraw
```

Every arrow except the last two exists in code today. The last two are gates 5
and 6, and they are the whole difference between "a token" and "money".

## Engineering this policy implies

In the order I would do them:

1. ~~**Programmatic provider rewards**~~ - DONE, and the shape was forced by one
   fact I did not appreciate when I wrote this: consensus cannot see work. A job
   lives in the marketplace, whose provider list and job records are per-node
   state no quorum ordered, so the chain knows only that MATRIX moved between
   two accounts.

   That rules out the obvious mechanism, a percentage of what a provider was
   paid, because a percentage of a transfer is a MONEY PUMP: send coins to an
   account you also control, collect the percentage, send them back, repeat. It
   would have drained the pool to whoever looped fastest and nothing about it
   would have looked irregular.

   So the emission is a FIXED per-block budget, halving on a schedule, shared
   among the registered providers a block paid, pro rata by how much. Faked
   volume can move a share of the budget and cannot increase it, so the pool
   empties on schedule and not faster. Eligibility is a registry a quorum of
   operators changes, the same as admitting a validator, because otherwise the
   emission would pay whoever happened to receive a transfer.

   What that means for the 40%: it now has a mechanism, and the mechanism is
   permissioned. A provider earns from the pool because operators approved it,
   not because the chain verified it served anything. Making it permissionless
   needs jobs to be consensus state, which is a substantially larger piece of
   work than this was.
2. ~~**Dynamic validator set**, then **stake**~~ - DONE. A change is a
   transaction to a reserved recipient, it needs a quorum of operators to have
   approved it, and it takes effect at an epoch boundary so every node switches
   at the same height. Voting power is bonded MATRIX, so a quorum costs two
   thirds of what is bonded rather than two thirds of the identities; admission
   requires a minimum bond; and proven equivocation moves the offender's whole
   bond to the reward pool. A bond is withdrawable only after the validator has
   left the set and served an unbonding period, so it cannot equivocate and
   withdraw ahead of the evidence.
3. ~~**A settlement fee**, capped in code~~ - DONE, split across the validator
   set in force rather than the validators of the committing block, for the
   determinism reason above.
4. **A bridge mint cap** in the contract, plus the reconciliation report as a
   node endpoint rather than a manual query.

Item 4 is what is left of this list, and one thing that was not on it: the
signed-transfer path has to move to consensus. Two paths move value today and
only one is agreed by a quorum, which was already a correctness problem and is
now also the one way to move MATRIX without paying the fee.

What I need from you on the numbers: the fee rate (I recommend 100 basis
points, the cap), and the emission's per-block amount and half-life. The
arithmetic is that total spend is about 1.44 x per_block x half_life, so
targeting the 40% provider allocation of 4e17 base units over a half-life of a
million blocks wants a per-block figure near 2.8e11. I would rather you set
those than inherit a default I invented.
