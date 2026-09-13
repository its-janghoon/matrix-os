# Upgrading the validator set for a consensus-affecting change

This is the procedure for the bridge fixes in `7a3efee`, and the shape of it
applies to any change that alters what a node considers a valid block.

It is short because the risky parts are few, and most of what follows is
establishing that they are few.

---

## What changed, and which half is dangerous

Two fixes, and they are **not** equally risky.

**The burn recipient fix is liveness only.** A node normalizes the recipient in
its WATCHER, before it builds a consensus message, so what reaches the ledger is
already canonical and an un-upgraded node validates it perfectly. The only
difference is which nodes are willing to submit an attestation at all: an old
node's watcher refuses the burn locally and stays silent. Fewer attestations
means an unlock may not reach quorum. Nothing forks.

**The lock nonce fix changes block validity.** `nonceKey` now covers bridge
locks, and it is consulted in `verifyBlockForHeightLocked` - so a block carrying
a second lock at a spent nonce is **accepted by an old node and rejected by a new
one**. That is the only disagreement in this upgrade, and everything below exists
to keep it from mattering.

---

## Three validators means every node is required

Quorum is `(2 * total_power) / 3 + 1`. With three equally bonded validators:

```
total  = 3P
quorum = 2P + 1     ->  two validators (2P) is NOT enough
```

So **all three must vote to commit anything.** A three-node set has no fault
tolerance; four is the first size where one node can be down (quorum needs 3 of
4).

Two consequences, and the second is the reassuring one:

- **Block production pauses while any node is restarting.** This is expected, not
  a failure. Plan the window for it.
- **A mixed set cannot fork.** A fork needs two quorums, and at 3-of-3 there is
  only one possible quorum. If the versions disagree about a block, it simply
  does not commit and the chain stalls until they agree - visible, and
  recoverable by finishing the upgrade.

---

## Before you touch anything

**1. Confirm no duplicate-nonce lock is already in history.**

This is the one check that cannot be skipped. A restart replays the persisted
chain WITHOUT re-verifying it, so an upgraded node keeps its own history
regardless. But `acceptSyncedBlock` runs the same validation a proposal gets, so
a node that ever has to **block-sync** - a rebuilt host, a store lost, a node
far enough behind - would reject a historical block containing a second lock at
a spent nonce, and could never catch up.

On any validator:

```sh
matrix bridge reconcile --api-key <key>
```

- `escrow balance` equals `outstanding` → no lock was ever overwritten. Proceed.
- A `reconciliation mismatch` error → a duplicate lock is already committed.
  **Stop.** The upgrade is still correct, but that block becomes unsyncable, so
  decide what to do about it before going further, not after.

**2. Freeze bridge locks for the window.** The disagreement above is only
reachable through a bridge lock. No locks in flight and none submitted means the
two versions cannot disagree about anything, and the upgrade becomes an ordinary
restart. Announce it; it is minutes, not hours.

Burns need no freeze. An un-upgraded node simply does not attest to one, and it
applies once the set is upgraded.

**3. Record the starting state**, so "did this change anything" is answerable:

```sh
matrix bridge reconcile --api-key <key>        # escrow, outstanding
matrix tx list --api-key <key> | tail -1       # transfer count
```

and the state root each node reports. All three must agree before you start. If
they do not, you have a different problem and this is not the time.

---

## The upgrade

Build once and ship the same binary to all three. Do not build per host: three
binaries from three checkouts is three chances to ship a different one.

```sh
cd services/core && go build -o matrixd ./cmd/matrixd && sha256sum matrixd
```

Then, **one node at a time**:

1. Stop it. The chain stops committing here - expected, see above.
2. Replace the binary. Keep the old one beside it, named, for the rollback.
3. Start it with the attestor passphrase in the process environment:
   `MATRIX_ATTESTOR_PASSPHRASE=... matrixd -config /etc/matrix/config.yaml`
4. Wait for it to rejoin and for the chain to commit again before touching the
   next one. It has rejoined when its height is advancing and it reports
   `Bridge: attesting as 0x...` with the address registered in the contract.

Do not stop two at once. There is nothing to gain - the chain is already paused
during each restart - and a set with two nodes down has no way to tell you it is
healthy again.

---

## After each node, and after the last

**After each:** the chain commits again, and all running nodes agree on the state
root. If it commits with two upgraded and one not, the versions have not
disagreed about anything, which is what the lock freeze bought you.

**After the last:**

```sh
matrix bridge reconcile --api-key <key>
```

Escrow and outstanding must be unchanged from step 3 and must still close. Then
lift the lock freeze and prove both fixes on the real chain:

```sh
# Two locks of the SAME amount to the SAME recipient. Distinct ids is the fix.
matrix bridge lock --to 0x<addr> --amount 100000000000 --api-key <key>
matrix bridge lock --to 0x<addr> --amount 100000000000 --api-key <key>
```

Two different lock ids, and reconcile still closing with escrow equal to
outstanding, is the whole of bug 2 demonstrated.

For bug 1, a burn naming a `0x`-prefixed account id should now release. The node
log says so in both spellings:

```
Bridge watcher: unlocked N native base units to <account>
  (burn <id>; the contract emitted "0x<account>", normalised)
```

---

## Rolling back

Safe, because nothing in this change alters stored state: the normalization
happens before a consensus message is built, and the nonce set is an in-memory
index rebuilt from the chain on every start. Put the old binary back and restart,
one node at a time, the same way.

The one thing a rollback cannot undo is a burn that the new code released and the
old code would have refused. That escrow has moved and the ledger records it.
Which is the correct outcome - it went to the account the burner named.

---

## What this procedure does not cover

- **Anything that changes how state is APPLIED.** These two do not: one runs
  before consensus sees the message, the other decides validity rather than
  effect. A change to apply logic needs an activation height, which this chain
  has no mechanism for, and that is a different and much longer document.
- **A validator set larger than three**, where a genuine rolling upgrade with no
  pause is possible because quorum survives one node being down.
