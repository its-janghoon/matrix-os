# Adding a validator to a running network

This is the procedure for growing a `bonded-open` set, written from the fourth
validator being added to a live three-node network. It is the first size change
that buys anything: three validators tolerate zero failures, four tolerate one.

Most of what follows exists because a step looked obvious and was wrong.

---

## What the new validator buys, and what it does not

Quorum is `(2 * total_power) / 3 + 1`, and power is bonded stake. With three
equally bonded validators every one of them is required:

```
total  = 3P
quorum = 2P + 1     ->  two validators (2P) is NOT enough
```

Add a fourth bonded at `P/10` and one node may be down:

```
total  = 3.1P
quorum = 2.0666...P + 1

big + big + big    3.0P   OK
big + big + small  2.1P   OK      <- any one of the big three may be down
big + big          2.0P   NO      <- two is still not enough, which is correct
```

**The fourth bond does not have to match the others.** Making it larger buys
nothing: the two rows that decide fault tolerance do not change until the set
reaches seven. Size it so the candidate can afford it and so a slash still
stings.

What it does not buy is the EVM bridge's mint direction. `WrappedMatrix` fixes
`attestorCount` and `threshold` in its constructor as `immutable`, so the
attestor set cannot grow without redeploying the contract and migrating every
wrapped token. A new validator can never sign a mint.

It does help the other direction. Releasing escrow for a burn is gated by
**consensus voting power**, not by the contract's attestor list: each validator
watches the EVM chain itself and attests into consensus, and
`burnunlock.go` counts that power against the same quorum as everything else. A
fourth validator with a working EVM endpoint makes burn releases survive one
node being down, exactly as block production does.

---

## Before you touch anything

**1. Can the candidate's bond actually be funded?** The money has to come from an
account whose key someone holds. `scripts/wallet-sweep.py` answers that across a
machine without decrypting anything or asking for a passphrase; it also names
which wanted accounts are *absent*, which is the half that unblocks a decision.
Settle this first. Everything below is wasted if the answer is no.

**2. Is `min_bond` low enough that the candidate can meet it?** If not, lowering
it is a change to every existing node - see the next section, because it is not
an ordinary config edit.

**3. Do all existing nodes agree right now?** Compare state roots at the same
height. If they do not agree, this is not the time.

---

## Lowering `min_bond` is a block-validity change

`min_bond` decides whether a `SetChangeAdd` is a valid transaction
(`engine.go`, `verifySetChangeLocked`). A node with a lower value accepts an
admission that a node with a higher value rejects. That is a fork, not a
preference.

It is also the *only* rule that differs, and it is only reachable through an
admission. So the rule is simple: **no admission may be in flight while the set
disagrees.** Roll the change one node at a time, with the candidate either not
yet provisioned or running with `participate_in_open_set: false`, and the
disagreement is unreachable.

`min_bond` is not in the state root, not in any digest, and not in the genesis
hash. Nothing else reads it. A node restarted with a new value keeps its history
unchanged.

Roll it with `validator-upgrade.md`'s restart procedure, and fold in a binary
upgrade if one is pending - both need the same window, and on a three-node set
that window is a pause in block production either way.

---

## The transfer trap: the fee comes out of the amount

This one has cost a whole round-trip more than once.

```go
fee := FeeFor(tx.Amount, e.feeBasisPoints)
net := tx.Amount - fee
```

The protocol fee is **deducted from what you sent**, not added on top. Sending
exactly `min_bond` delivers `min_bond` minus the fee, the candidate is below the
floor, and the node retries a bond it can never complete. The symptom is a quiet
log line, not an error.

At the 1% cap, send at least `min_bond / 0.99`. A worked example at
`min_bond = 100,000 MATRIX`:

| | base units | MATRIX |
| --- | --- | --- |
| sent | 102,000,000,000,000 | 102,000 |
| fee (1%) | 1,020,000,000,000 | 1,020 |
| **received** | **100,980,000,000,000** | **100,980** |
| floor | 100,000,000,000,000 | 100,000 |

Bonding itself is free: `paysFee` exempts reserved recipients, so moving your own
coins into your own bond is not taxed.

Prove the whole path with a trivial transfer first - a thousand base units to any
account. It exercises the keystore, the passphrase, the endpoint, the API key and
the fee arithmetic in one shot, and it is far cheaper to debug than the real one.

---

## Provisioning the candidate

**Give it a static address.** Every existing validator's firewall will name the
candidate's address explicitly, so an address that changes on stop/start breaks
every one of those rules at once. The symptom is "the node is up but no peers
connect", and nothing in the node's own logs points at the cause.

**Open the P2P port from the new address on every existing node**, not just one.
Where two nodes share a private network, allow the private address too: traffic
between them may present it instead of the public one, and that failure looks
identical to the one above.

**Match the architecture.** Check `uname -m` on a node you are copying from
rather than assuming; a release archive for the wrong architecture extracts fine
and fails only when something tries to execute it.

Build the candidate's config from a working node's config rather than from the
example, and make exactly these changes:

| Change | Why |
| --- | --- |
| `participate_in_open_set: false` | Sync first. Its consensus account does not exist until its store does, and there is nothing to bond until the account is funded. |
| remove `bridge.attestor_keystore` | It cannot be in the contract's immutable attestor set, and `loadAttestor` treats an absent keystore as "no attestor" without erroring. |
| keep every other `bridge` field | A node with **no** bridge configured has a nil `burnUnlocker`: it tallies attestations and releases nothing, so its balances diverge from its peers' the first time a burn reaches quorum. |
| `consensus.stake.bond` = what it can afford | Copied from a node holding ten times as much, the candidate bonds its entire balance and retries the shortfall forever. |
| `bootstrap_peers` = **all** existing nodes | An existing node's config lists only the *others*, so copying it leaves out the node you copied from. |
| a fresh API key | Do not reuse another node's credential. |

Everything else - `validators`, the genesis allocations, `chain_id`,
`epoch_length`, `fee_basis_points`, `maintainer_account`, `min_bond` - must stay
byte-identical. They define the chain.

Run `matrixd -preflight-production -config <path>` before starting anything. It
parses the config and validates the consensus-critical fields without touching
the store, so it is safe to run against a live node, and it catches a
mis-edited file before a restart turns it into an outage.

---

## Joining

Fund the candidate's consensus account, then flip `participate_in_open_set` to
`true` and restart. Nothing else is manual: the node bonds the shortfall from its
own key, and the code refuses to ask for admission until the bond is committed.

```go
// maybeMaintainOpenMembership: a candidate joins only after its minimum bond is
// visible in committed state.
bonded, err := e.stake.Bonded(e.selfID)
if err != nil || bonded < e.minBond {
    return
}
```

A set change commits at one height and **takes effect at the next epoch
boundary**, so with `epoch_length: 100` an admission committed at 900 is in force
at 1000. The chain produces blocks to reach that boundary even with no traffic,
which is why height climbs quickly for a moment. The log says both halves:

```
consensus: committed set change at height 900: add:<account> (takes effect at the next epoch)
consensus: validator set change in force at height 1000: add:<account>
consensus: validator set is now 4 members, total power ..., quorum ...
consensus: this node is now a validator and will propose and vote
```

---

## Checking it worked

**An idle chain produces no blocks.** `engine.go` returns no proposal when there
are no transactions, no epoch boundary to reach and no stall to escape - "an idle
chain with nothing to say stays silent". So a height that does not move is not
evidence of anything, in either direction, and watching it for thirty seconds
answers no question at all. A genuinely stuck height escapes on its own after
`stallRecoveryFactor` round timeouts.

Check these instead:

1. **Every node reports the same state root at the same height.** Read it from
   each node's `eth_rpc` endpoint with `matrix_getChainInfo`, which needs no API
   key. Compare roots only at equal heights; different heights make a healthy
   chain look forked.
2. **Every node reports the same set size and quorum.** The candidate believing
   it joined is not the same as the network agreeing.
3. **A transaction commits.** On an idle chain this is the only real liveness
   test.

Then do the one that matters:

**Stop a validator and submit a transaction.** This is the acceptance test for
the entire exercise, and it is the only step that distinguishes "the arithmetic
says one node can be down" from "one node can be down". Stop it, watch a transfer
commit, start it again, and confirm all nodes reconverge on one root once the
stopped node has synced. Anything short of that is a config review.

---

## What this does not cover

- **Removing a validator.** Voluntary exit is the same machinery in reverse
  (`participate_in_open_set: false` gossips a self-signed removal), but the
  unbonding period and what it means for a set that is already at its minimum
  size are a separate decision.
- **Growing past four.** The arithmetic generalizes; the operational cost does
  not. Every added node is another host to upgrade in a window where the chain
  pauses, until the set is large enough for a genuine rolling upgrade.
- **Any EVM endpoint allowlist** the new node's address needs. The node syncs and
  validates fine without one - only its burn attestations are affected - so this
  fails quietly and is easy to leave broken.
