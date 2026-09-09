# Historical proposal: token and bridge policy

**Status: superseded.** This document originally proposed an Ethereum-first distribution and bridge policy against commit `f6c6c76`. It is retained as design history, not current launch guidance. The authoritative policy and ordered operator ceremony are in [`docs/runbooks/base-launch.md`](../runbooks/base-launch.md).

Do not use the old 40% provider emission, 18% team allocation, 2% launch float, 5%-raisable bridge cap, permissioned/stake-off launch, Ethereum-primary chain, or “nothing is deployed anywhere” assertions from earlier revisions. They were proposals or point-in-time observations and are not the approved Base launch policy. Repository state alone also cannot prove whether an external deployment is public; require published on-chain evidence.

## Current approved launch policy

- Primary EVM: Base production, chain ID `8453`; rehearsal: Base Sepolia, chain ID `84532`.
- Native MATRIX remains canonical at 9 decimals. wMATRIX is an 18-decimal 1:1 escrow-backed mirror, not the native settlement asset.
- Minimum lock: 100 MATRIX = `100000000000` native base units.
- Immutable wMATRIX cap: `60000000000000000000000000` wrapped base units = 60,000,000 wMATRIX = 6% of the 1B native maximum.
- Founder allocation: 50,000,000 wMATRIX = 5%, held in an immutable vault. Vesting is linear from the start over 5 years total with a 1-year cliff; 20% is vested at the cliff.
- Founder wMATRIX may be minted only after exactly 50,000,000 native MATRIX (`50000000000000000` native base units) is committed to bridge escrow for the vault. The wrapped mint is exactly `50000000000000000000000000` base units.
- Protocol fee: 100 basis points. An explicit maintainer account receives 5000 basis points **of the fee**, so its maximum charge is 0.5% of transferred value.
- Provider emission at launch: zero.
- Validator membership: bonded-open with stake enabled.
- Users pay their own Base ETH gas. There is no relayer or protocol gas sponsorship.
- A DEX price is market-discovered. wMATRIX is not pegged to a stablecoin, USD, or fiat.

## Security-domain correction

Dynamic native validators and immutable per-deployment EVM mint attestors are separate security domains. Native validator join, exit, or ejection does not add, remove, or rotate a `WrappedMatrix` attestor. A removed validator may retain EVM mint authority on an old deployment. Changing attestors or threshold requires a new `WrappedMatrix` deployment and a planned user/liquidity migration.

Base production requires at least 2 attestors and a signature threshold strictly greater than two thirds (`3 * threshold > 2 * attestor_count`); the deployment script enforces it. A 2-of-3 committee is therefore invalid. Every bridge configured in `matrixd`, including a singleton, uses consensus-ordered burn unlock; singleton does not mean a direct watcher release.

## Historical rationale retained

The original proposal correctly emphasized several enduring constraints:

1. Native supply is capped at 1,000,000,000 MATRIX and bridge burns cannot create native funds.
2. Wrapped supply must never exceed outstanding native escrow.
3. Attestor custody and reproducible reconciliation are operational security requirements, not mere config choices.
4. Liquidity does not create a peg. A treasury should not promise to defend a DEX price.
5. Claims about deployment, backing, or market availability require published evidence at stated chain heights.

The approved implementation differs from the proposal by using an immutable 6% cap, zero launch emission, a 5% immutable founder vesting vault, bonded-open stake, and Base as the primary EVM. For exact commands, evidence gates, and the required Base Sepolia-before-Base ordering, use the Base launch runbook.
