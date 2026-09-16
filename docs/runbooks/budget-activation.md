# Runbook: turning on spend budgets, at a height everyone agrees on

This is a **rule change over money**, not a bug fix. It changes what a node DOES
with a transaction rather than only what it refuses, so a node running the old
rules and a node running the new ones apply the same block and reach different
balances. That is why it activates at a height instead of on a restart, and why
the order below is not negotiable.

Written from the upgrade rather than from the feature: what a budget is, and why
it is shaped the way it is, is in
[`spending-authorization.md`](../proposals/spending-authorization.md).

---

## What actually changes at the height

Three recipients start meaning something:

```
spend/escrow/<terms>   the account that HOLDS a budget
spend/draw/<payee>/<terms>   a payment out of one, signed by the delegate
spend/close/<terms>    the return of whatever is left
```

An un-upgraded node reads `spend/escrow/...` as an ordinary account name. It
charges the protocol fee that opening a budget is exempt from, credits the string
as if it were a seller, and never lets a delegate draw. Its ledger and an
upgraded node's diverge from the first budget anyone opens.

The version gate is what turns that from a silent divergence into a loud refusal:
past the activation height, a node without the new rules stops voting rather than
disagreeing. **A node that stops voting is a node the quorum has to survive
without.**

## Before you start

- [ ] **Four validators, all healthy.** Three tolerates zero failures, so the
      upgrade itself would pause block production. With four, one node lagging is
      survivable. Check with the validator-join runbook's liveness gate, not with
      "the height is going up" - an idle chain produces no blocks.
- [ ] **Every validator on the new binary**, verified individually. Not "the
      release is out".
- [ ] **A height chosen with room.** Current height plus enough blocks that the
      slowest operator has hours, not minutes. At a 3s round timeout a thousand
      blocks is about fifty minutes of chain time, and rather more of wall clock
      on an idle network.

## Order, and why it is this order

**1. Roll the binary everywhere first, with no schedule.**

The new rules are dormant without a `protocol_upgrades` entry: `protocolVersionAt`
returns the genesis version for a chain with no schedule, and every budget
recipient is refused as "not yet". So a fully upgraded network behaves exactly
like the old one, which is what makes this step safe to do slowly and one node at
a time.

```
sudo systemctl stop matrixd
# install the new matrixd and matrix, per the validator-upgrade runbook
sudo systemctl start matrixd
```

Verify on each node before moving to the next:

```
matrixd -version
```

**2. Only once every node is on the new binary, add the schedule.**

The same stanza on **every** node, byte for byte. The version is part of block
validity, so two nodes with different schedules disagree about the same block -
that is the failure this whole mechanism exists to avoid, reintroduced by a
typo.

```yaml
consensus:
  protocol_upgrades:
    - height: <CHOSEN HEIGHT>
      version: 2
```

Gate each edit before restarting:

```
sudo matrixd -preflight-production -config /path/to/config.yaml
sudo systemctl restart matrixd
```

**3. Wait for the height. Do nothing.**

There is nothing to do at the boundary. Every node switches because the height is
agreed, not because anyone acts.

## Verifying it took

After the height, open a small budget from a wallet that can afford to lose it:

```
matrix budget open --amount 100000 --delegate <a 64-hex account id> \
  --per-job-cap 10000 --max-price-per-unit 10000 --ttl 1h
```

Then, on **every** validator, confirm the balance is the same:

```
matrix budget show --account <the account it printed>
```

Four identical answers is the check that matters. One different answer means the
nodes are applying different rules, and the state root disagreement in the logs
will say so in the same breath.

Then close it and confirm the money comes back:

```
matrix budget close --account <the account>
matrix wallet balance
```

## Backing out

**Before the height:** remove the `protocol_upgrades` entry everywhere and
restart. Nothing has happened yet.

**After the height: you cannot.** Blocks have committed under the new rules and
budgets may hold balances. Rolling a node back makes it refuse the chain's own
history. The way out of a problem after activation is forward - a fix, a new
version, a new height - which is the same shape as this document.

This is the reason for the room in step 1. The cheap moment to find out a
validator is not ready is before the schedule goes in, not after.

## What can go wrong, and what it looks like

**A node did not get the binary.** At the activation height it refuses every
block and stops voting. With four validators the other three still make quorum
(`(2*4)/3 + 1 = 3`), so the chain continues and the odd node is visibly stuck.
With three validators it would have stalled the chain outright, which is why the
fourth is a precondition and not a nicety.

**A node has a different schedule.** It disagrees about block validity from the
earlier of the two heights. Same symptom, different cause - so check the stanza
on every node before blaming the binary.

**Someone opened a budget before the height.** It is refused as "not yet" and
stays in the mempool, then lands by itself once the height arrives. That is
deliberate: the refusal before activation is "not yet", never "never".

**A budget account with money and no name.** The terms ARE the account's name, so
whoever opened it needs that string to close it. It is recoverable - budget
operations appear in the owner's own transaction history, unlike every other
reserved recipient, precisely for this - but it is a search. Tell people to keep
the line `budget open` prints.
