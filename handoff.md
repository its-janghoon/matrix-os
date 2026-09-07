# Handoff

State of `main` as of `2e6325e`. Everything below was run, not inferred; where
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

Four things landed this session that change how the layer behaves:

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

**A fee and an emission** (`consensus.fee_basis_points`, `consensus.rewards`,
both off by default). The fee is a cut of each committed value transfer paid to
the validator set pro rata by power, capped at 100 basis points *in code*. The
emission is a fixed halving per-block budget from the genesis pool, shared among
registered providers a block paid.

## Joining a running network

This is the operational path and it has two requirements that are easy to get
wrong. Both are covered by `internal/consensus/join_test.go`, including a test
for the failure mode.

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

## Pending engineering

In the order I would take them.

**1. Route the signed-transfer path through consensus.** `matrix wallet transfer`
and `SubmitSignedTransfer` append to `token.Chain` via `token.SettledLedger`,
which moves credits on one node and is ordered by no quorum. Two consequences:
balances can diverge between nodes, and it is the one way to move MATRIX without
paying the protocol fee. This was already a correctness problem before the fee
existed. Expect it to change `matrix tx list` (which reads that chain and would
go empty) and the RPC's response shape, so it needs a decision about what
replaces them.

**2. Document the join procedure on the site.** `/docs/guides/network-setup`
covers set changes, stake, the fee and the emission, but not the two
requirements above. I was writing this when the session ended.

**3. Expose `DeployAgent` over gRPC.** It loads and runs a wasm module now, but
nothing outside the process can ask it to, so a module has to come from inside
the node. Deployments also do not survive a restart, and running an agent costs
nobody anything because the runtime is not metered against the marketplace.

**4. A `send` policy for the agent runtime.** The host function is real and
refuses everything without a `SendFunc`, which is the safe default and not a
useful one. It needs a decision about who may be named and what a name means.

**5. Bridge mint cap and a reconciliation endpoint** (from
`docs/proposals/token-and-bridge-policy.md`).

## Decisions waiting on the CTO

Numbers, not engineering. All three knobs are off in a generated config on
purpose: they are monetary policy and a default would be making it on the
operator's behalf.

- **Fee rate.** Recommend 100 basis points, which is the code cap.
- **Emission size.** Total ever paid is about `1.44 x per_block x half_life`. The
  proposal's 40% provider allocation is 4e17 base units; over a half-life of
  1,000,000 blocks that wants `per_block` near 2.8e11.
- **Whether to run stake at all,** and `min_bond` / `unbonding_period` if so.

Also still open from the proposal: whether we raise, headcount and vesting,
jurisdiction for the issuing entity, and the audit budget.

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
