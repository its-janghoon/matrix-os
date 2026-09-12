# Base production launch evidence

Recorded 2026-09-12 KST. This record contains no private keys, API keys, or
attestor keystores.

## Source and validator set

- Consensus source revision:
  `751673d09885d954062e9be6ef569c80d61e110d`
- Validator binary SHA-256 on Seoul, Virginia, and Frankfurt:
  `8eae436b1d17d36d1e4b963dd3998aa39f9b2efda57610f3f70ce310e73e6353`
- Full `services/core` test suite passed, including
  `internal/consensus` in 80.312 seconds and `internal/node` in 13.287
  seconds. The consensus package was additionally run three consecutive
  times with no failure to rule out flakiness before deployment.
- The prior revision of this record was
  `f9f17ba96c6b42b8d4b94a157bb0872a7f9b6a05` with binary
  `d902efb8a165c81319c97eaea1c83c1387459f3111981c1ed17d74ae727696e5`.
  It is superseded because that binary halts at an epoch boundary; see
  the liveness incident section. Reverting to it is unsafe for a second
  reason recorded there.
- The active set became 3 validators at native height 400, with total power
  `3000000000000000` and quorum `2000000000000001`.
- All three validators resumed the same active set and reported no new
  equivocation or slash after the Virginia restart recovery, and again
  after the height-400 liveness recovery described below.

Native validator IDs:

1. Seoul:
   `92678c1743c945d375c0c09f594f74d25a5c185b9d34c268d21aad31888031e2`
2. Virginia:
   `86c3d2ec2a1378a1a647512112002c1a92e9e781e3fbcfb3e9e2c999967431fb`
3. Frankfurt:
   `82c55f1ecfcb3a90b006cc950eab78ad89e438af31a3440b6e6e5985b463adac`

## Base contracts

Network: Base, chain ID `8453`.

### WrappedMatrix

- Address:
  [`0x0ec1C40829B3A5C2349Afa8C4Da6dC8B727fC87c`](https://basescan.org/address/0x0ec1C40829B3A5C2349Afa8C4Da6dC8B727fC87c#code)
- Deployment transaction:
  [`0x341df3e3d2e2f2f01cb7c8b0bd8b571b13e31c9a4aefd6e300721136aac211a3`](https://basescan.org/tx/0x341df3e3d2e2f2f01cb7c8b0bd8b571b13e31c9a4aefd6e300721136aac211a3)
- Deployment block: `51165783`
- Source: BaseScan exact-match verified, Solidity `0.8.24`, optimizer 200 runs.
- Immutable mint cap: `60000000000000000000000000` wMATRIX base
  units, equal to 60,000,000 wMATRIX.
- Conversion: `1000000000` ERC-20 base units per native base unit.
- Committee threshold: 3-of-3.

Ordered EVM attestors:

1. `0x14C311C413D08aAC038EaAc9d0382742F28B2696`
2. `0x9eb8806E2665e80b7c09F44ba2941004C2D0d473`
3. `0x08dAB841369B6FDa564cdE92455Df2415414e427`

### FounderVestingVault

- Address:
  [`0x7A23b7748162e7BcA02b16C4d236E4A9D122038d`](https://basescan.org/address/0x7A23b7748162e7BcA02b16C4d236E4A9D122038d#code)
- Deployment transaction:
  [`0x4c1d0ab55ac37a7046a8b143c1a0b8bb2ba6fd5b4af32e63ed5488acfa726906`](https://basescan.org/tx/0x4c1d0ab55ac37a7046a8b143c1a0b8bb2ba6fd5b4af32e63ed5488acfa726906)
- Deployment block: `51167374`
- Source: BaseScan exact-match verified, Solidity `0.8.24`, optimizer 200 runs.
- Beneficiary:
  `0x4A857C9022C6D152DF524174CDF9776E45b09001`
- Start: `1789124081`
- Cliff: `31536000` seconds.
- Total duration: `157680000` seconds.
- Allocation: `50000000000000000000000000` wMATRIX base units.
- The schedule is linear from start for five years, with no release before the
  one-year cliff. Twenty percent is vested at the cliff.

## Founder backing ceremony

- Native lock ID:
  `0x49047d03b7b591e12285e4d2bfeb426e2ac00d21a8aacbe930f654ee1dd747ac`
- Locked native amount: `50000000000000000` native base units.
- Mint transaction:
  [`0xaba866d6c810a4343e8467dd22a2a9988581a7193bc5c7bf8bd2d40b56f2b145`](https://basescan.org/tx/0xaba866d6c810a4343e8467dd22a2a9988581a7193bc5c7bf8bd2d40b56f2b145)
- Mint block: `51177355`
- Mint recipient: the immutable founder vault.
- Mint amount: `50000000000000000000000000` wMATRIX base units.
- All three registered EVM attestors signed the same lock, recipient, and
  amount.
- Vault status after mint: `fullyFunded: true`, `released: 0`,
  `releasable: 0`.

Native reconciliation at native height 400:

```text
lockedNative:     50000000000000000
unlockedNative:   0
outstandingNative: 50000000000000000
escrowBalance:    50000000000000000
outstandingErc20: 50000000000000000000000000
```

The Base `totalSupply()` and vault balance both reported
`50000000000000000000000000`. Native escrow and wrapped supply therefore
reconciled exactly at the stated heights.

## Public readiness gate

Fresh challenge:
`0xac2c75d4b7e283d71cc797added7970cb2fdb36e9c5813030f106f6450da0198`.

Each public endpoint echoed the challenge and signed the same deployment:

1. `https://validator-1.ecirlabs.com` recovered
   `0x14C311C413D08aAC038EaAc9d0382742F28B2696`.
2. `https://validator-2.ecirlabs.com` recovered
   `0x9eb8806E2665e80b7c09F44ba2941004C2D0d473`.
3. `https://validator-3.ecirlabs.com` recovered
   `0x08dAB841369B6FDa564cdE92455Df2415414e427`.

All recovered signers were distinct and registered on the deployed contract.
The responses agreed on chain `8453`, contract
`0x0ec1C40829B3A5C2349Afa8C4Da6dC8B727fC87c`, and minimum lock
`100000000000` native base units. At the gate, supply was 50,000,000
wMATRIX and cap headroom was 10,000,000 wMATRIX.

## Public operating policy

- Browser origin: `https://www.ecirlabs.com`
- Public reads: enabled.
- Caller-signed writes: enabled.
- Rate limit: 600 requests per minute, burst 120, on every validator.
- Watchers: enabled on all validators from Base block `51165783`, with 12
  confirmations and a 12-second poll interval.
- Minimum bridge lock: 100 MATRIX, equal to `100000000000` native base
  units.
- Users pay their own Base ETH for EVM transactions.
- There is no relayer or gas sponsorship.
- wMATRIX has no USD, fiat, or stablecoin peg.
- No production DEX pool is announced in this record.

Consensus policy:

- Fee: 100 basis points on ordinary native transfers.
- Maintainer:
  `35198460be7c999d9ef4009f4b9ad1d51700d10f92db93323c4eb5a105eef595`
- Maintainer share: 5000 basis points of the protocol fee.
- Provider emission: zero.
- Membership: `bonded-open`.
- Validator target bond: `1000000000000000` native base units.
- Epoch length: 100 blocks.
- Equivocator ejection: enabled.

## Public float allocation

A single, consensus-gated transfer of existing native units out of the
genesis reward pool. It is not a mint: issued supply is unchanged and
`NativeMaxSupply` is untouched.

- Recipient operation: `consensus/treasury-allocation/public-float-v1`
- Destination account:
  `945871e41d6116218c5b9dde5f384843396e12949ab2232dc5d6d57a5be2853b`
  (the same account used for the founder backing ceremony, so the
  holding is disclosed rather than split across identities)
- Amount: `1000000000000000` native base units, equal to 1,000,000
  MATRIX.
- Reward pool before: `946999999999999900`
- Reward pool after: `945999999999999900`
- Conservation: pool after plus allocation equals pool before, verified
  independently on all three validators.
- Single-use guard: the operation requires the reward pool to hold
  exactly the pre-transfer balance. The pool is monotonically
  non-increasing, so the precondition can never hold again regardless of
  what the recipient later does with the funds. Re-verified as refused
  after the transfer landed.
- Remaining wrapped mint headroom is unchanged by this: 50,000,000 of
  the immutable 60,000,000 cap is held by the founder vault, so
  10,000,000 MATRIX is the maximum that can ever reach Base.

## Consensus liveness incident, height 400

Recorded because it stopped the chain and because the second defect was
a regression from the fix for an earlier one.

The chain halted at height 400, an exact epoch boundary, and stayed
there for roughly 13 hours across restarts. No funds were at risk and no
validator was slashed. Two independent defects:

1. No node would build a block. A leader refused to propose with an
   empty mempool unless a pending set change or unweighted bond was
   outstanding, and the epoch boundary clears both in the same critical
   section that sets the new height. Because the preceding epoch's
   traffic was validator-set churn rather than user transactions, that
   flag was the only thing driving block production, so crossing the
   boundary switched production off with nothing left to switch it back
   on.

2. Restart replayed rounds the node had already voted in. The
   persistent own-vote store, added earlier to stop self-slashing across
   restarts, correctly refuses a second conflicting vote at a position
   already voted in - but resume reset the round to 0 while the store
   held records for rounds 0 to N. The node therefore could not vote on
   any proposal until it burned N rounds again, and each of those rounds
   timed out into two further nil votes, so the occupied range grew at
   least as fast as the node walked it. Measured at 1,418 rounds at
   height 400 with no vote ever cast for a real block.

Diagnosis was confirmed before any change by reading the persisted own
votes at height 400 out of a copy of the store: 1,418 nil prevotes and
1,418 nil precommits, and zero votes for any real block.

Fixes: a leader now proposes an empty block once a height has gone
unproduced for a duration threshold, excluding the case where the node
holds a quorum of precommits for a body it lacks (that height is
settled and block sync owns the recovery); and resume now starts past
the highest recorded round. The threshold is a duration rather than a
round count because rounds rotate on a short backed-off timeout, so a
high round is the ordinary signature of a slow network and treating it
as a stall would produce empty blocks without bound.

Both fixes carry regression tests. The full service test suite passes,
and the three previously flaky block-sync tests were confirmed stable
across repeated runs before deployment.

Recovery: rolling restart of all three validators onto the fixed binary.
The chain resumed from 400 and has committed continuously since. No
equivocation and no slash occurred during or after the restarts, which
also re-confirms the earlier self-slash fix. The public float allocation
above was the transaction pending in the mempool throughout, and it
committed on recovery.

Do not revert to the prior binary
`d902efb8a165c81319c97eaea1c83c1387459f3111981c1ed17d74ae727696e5`. It
cannot help, because the chain was already halted at 400 for 13 hours
before the current revision was deployed, so the current code did not
cause the halt and removing it removes nothing that did. Reverting is
also actively unsafe: it removes the pardon for the Virginia
restart-induced equivocation, so that stored evidence counts again, a
slash of Virginia is re-approved from it, and the restored bond of
`1000000000000000` native base units is taken at the next epoch
boundary. That would turn a liveness incident into a loss of funds and
drop the set back to two members.
