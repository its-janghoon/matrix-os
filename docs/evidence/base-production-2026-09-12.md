# Base production launch evidence

Recorded 2026-09-12 KST. This record contains no private keys, API keys, or
attestor keystores.

## Source and validator set

- Consensus source revision:
  `f9f17ba96c6b42b8d4b94a157bb0872a7f9b6a05`
- Validator binary SHA-256 on Seoul, Virginia, and Frankfurt:
  `d902efb8a165c81319c97eaea1c83c1387459f3111981c1ed17d74ae727696e5`
- Full `services/core/internal/consensus` test suite passed in 70.864 seconds.
- Full `services/core/internal/node` test suite passed in 10.742 seconds.
- The active set became 3 validators at native height 400, with total power
  `3000000000000000` and quorum `2000000000000001`.
- All three validators resumed the same active set and reported no new
  equivocation or slash after the Virginia restart recovery.

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
