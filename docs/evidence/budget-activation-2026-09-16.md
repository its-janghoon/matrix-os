# Evidence: v0.4.0 on the live network, spend budgets scheduled for height 1497

Chain `8170`, four validators and one seller, 2026-09-16. Produced by
[`scripts/rollout.sh`](../../scripts/rollout.sh); every line below is its output
rather than a description of it.

## Before

| box | version | schedule |
|---|---|---|
| validator-1 | v0.3.9 | `protocol_upgrades: []` |
| validator-2 | v0.3.9 | `protocol_upgrades: []` |
| validator-3 | v0.3.9 | `protocol_upgrades: []` |
| validator-4 | v0.4.0 | `protocol_upgrades: []` |
| gpu (seller) | v0.4.0 | `protocol_upgrades: []` |

All four validators at height 1296, head `0x910938f04aae`, state root
`0x7b76963cc422`. Identical on all four, which is the gate: a rule change
scheduled onto a network whose nodes hold different balances is a fork with a
date on it.

## After

All five on v0.4.0, all five carrying `height: 1497, version: 2`, all four
validators at height 1297 on head `0x3249f066a3f8` and state root
`0x7b76963cc422` - still identical.

Config backups the script wrote, one per box, are `<config>.pre-budgets.<stamp>`
next to each config. Before the height they are a complete undo; after it they
are not, because blocks will have committed under the new rules.

## The block rate, which is why the height is 1497 and not 3255

The chain produced **one block in ninety seconds** while idle. That is the
number the lead has to come from:

- the first attempt at this used "current height + 2000", read off a runbook
  line about a 3s round timeout. At this chain's actual rate that is **two days
  out**, not the hour it reads as.
- 200 blocks, the script's floor, is about **five hours** here. The floor exists
  so a sudden burst of traffic cannot overtake the rollout itself; at the fastest
  cadence this chain could reach it is still ten minutes.

An idle chain produces no blocks at all - `Engine.buildBlock` returns nil rather
than minting an empty one - so this rate is a property of the traffic, not of the
software. Sending transactions brings the activation forward; doing nothing
leaves it about five hours away.

## The boundary, crossed

At height 1504, past the scheduled 1497, all four validators reported the same
head `0x60f7f61d9f47` and the same state root `0x7b76963cc422`. Nobody stopped
voting and nobody diverged, which is the thing the version gate exists to make
loud: a node without the new rules would have refused every block from 1497 on
and been visibly stuck while the others advanced.

**What that proves and what it does not.** It proves the activation did not split
the network. It does not prove a budget works, and the state root says why: it is
byte for byte the value it held at height 1297, two hundred blocks earlier. The
ledger has not moved. Every block since has been empty, so consensus agreeing
here is consensus agreeing about nothing having happened.

The functional test - open a budget, read it on every validator, close it - is
what closes that gap, and it is still outstanding: the wallet that can afford it
is an encrypted keystore whose passphrase is not in the box's
`/etc/matrix/matrixd.env`.

## Still open

At 07:48:43, before any of this, validator-1 logged:

```
consensus: peer 8b333a1ba8b96f224b3bed3e6d39676b7b617897aff67d1e926e83afb348ca6c
is at height 1105 on the same chain but a DIFFERENT ledger
(its state d0ffcb860916, ours 1e7e502571c0);
the two applied the same blocks and reached different balances
```

That message only fires when both nodes are at the SAME height
(`headDisagreementLocked` returns early otherwise), so it is not a peer lagging.
Two nodes applied the same blocks and got different balances.

It is not true now - all four agree at 1297 - and that is why this rollout was
allowed to proceed. But nothing here explains how it healed, and a divergence
that resolves itself without anyone knowing why is the same defect waiting for a
worse moment. It needs its own investigation, starting from whether 1105 is near
any of the pre-payment settlement changes.
