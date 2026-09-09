# Matrix contracts: Base-primary wrapped MATRIX

`contracts/` contains the EVM contracts and Hardhat operator harness for wMATRIX. The primary launch target is **Base** (chain ID `8453`), rehearsed first on **Base Sepolia** (chain ID `84532`). Ethereum mainnet and Sepolia remain legacy-compatible networks.

MATRIX is native-first: the canonical coin lives on the Matrix consensus L1 with 9 decimals and is the source of truth for balances and supply. Marketplace compute and inference settle in native MATRIX through consensus. `WrappedMatrix` is an 18-decimal EVM mirror that can be minted only against native MATRIX locked 1:1 in L1 escrow; it is not a second settlement currency and it has no stablecoin or fiat peg.

Follow the authoritative ordered ceremony in [`docs/runbooks/base-launch.md`](../docs/runbooks/base-launch.md). Commands in this README are a reference, not a substitute for that review and evidence process. Nothing in this repository proves that a public deployment exists; operators must publish deployment and reconciliation evidence.

## Contracts

- `WrappedMatrix.sol`: bridge-backed wMATRIX. Its attestor list, signature threshold, and mint cap are immutable constructor values. Mint is permissionless to submit but requires the deployed threshold of valid lock attestations; the signed recipient cannot be redirected.
- `FounderVestingVault.sol`: immutable founder vault with no owner/admin/upgrade/recovery authority. The launch allocation is 50,000,000 wMATRIX (5%), vested linearly from the start timestamp over 5 years total with a 1-year cliff. At the cliff, 20% has vested.
- `MatrixToken.sol`: standalone legacy ERC-20 test/deploy surface with immutable threshold minters and a mint timelock. It is not the bridge-backed launch asset; use `WrappedMatrix`.

## Exact units and launch limits

| Quantity | Exact value |
| --- | --- |
| Native decimals | 9 |
| Wrapped decimals | 18 |
| Wrapped units per native base unit | `1000000000` |
| Native maximum | `1000000000000000000` base units = 1,000,000,000 MATRIX |
| Wrapped full-scale equivalent | `1000000000000000000000000000` base units |
| Base launch mint cap | `60000000000000000000000000` wrapped base units = 60,000,000 wMATRIX = 6% |
| Founder allocation | `50000000000000000000000000` wrapped base units = 50,000,000 wMATRIX = 5% |
| Native founder backing | `50000000000000000` native base units = 50,000,000 MATRIX |
| Minimum lock | `100000000000` native base units = 100 MATRIX |

The cap is immutable. Increasing it or changing attestors/threshold requires a new `WrappedMatrix` deployment and a published migration.

## Security domains: validators are not attestors

The native validator set and the EVM mint-attestor committee are separate:

- Native validators are dynamic chain state. In bonded-open mode, validators can join/exit and proven equivocators can be ejected at epoch boundaries.
- EVM attestors are secp256k1 addresses fixed forever in one `WrappedMatrix` constructor. Native consensus identities use ed25519.
- A native validator join or ejection does not rotate the EVM committee. A removed validator can remain an authorized attestor on the old contract.
- Rotation means deploying a new contract and migrating users/liquidity; no config edit can mutate the old contract.

Production requires at least 2 attestors and a threshold strictly greater than two thirds (`3 * threshold > 2 * attestor_count`). `scripts/deploy-mainnet.ts` enforces both. For example, 3 attestors require threshold 3; 2-of-3 is rejected. The strict `>2/3` rule is also enforced on Base Sepolia because it is a real network to the harness.

The burn path is native consensus-ordered in every configured `matrixd`, including a singleton. A singleton has a one-validator quorum; the watcher never directly releases escrow. Run the watcher on every validator. This native burn quorum still does not modify the immutable EVM mint committee.

## Native-to-wrapped flow

```text
native lock -> EVM-attestor signatures -> user-paid Base mint
Base burn -> per-validator log observation -> native consensus-ordered unlock
```

The bridge lock and unlock are ordered by native consensus. `matrix bridge lock` refuses less than 100 MATRIX. The mint caller pays their own Base ETH gas; there is no relayer or protocol gas sponsorship. The holder pays their own Base gas to burn. A DEX pool, if one is created, discovers a market price; wMATRIX is not pegged to USDC or any other asset.

## Environment

Copy `.env.example` to the gitignored `.env` and resolve real values outside version control. Base-primary inputs are:

| Variable | Purpose |
| --- | --- |
| `BASE_SEPOLIA_RPC_URL` | Base Sepolia RPC, chain `84532` |
| `BASE_RPC_URL` | Base production RPC, chain `8453` |
| `BASESCAN_API_KEY` | BaseScan source verification |
| `PRIVATE_KEY` | Funded deploy/mint broadcaster key; never hardcode or put in shell history |
| `ATTESTORS` | Comma-separated reviewed secp256k1 addresses |
| `THRESHOLD` | Strict `>2/3` signature threshold |
| `MINT_CAP` | Exact immutable cap; launch value `60000000000000000000000000` |
| `WMATRIX_ADDRESS` | Reviewed deployed wMATRIX address for founder-vault deployment |
| `FOUNDER_BENEFICIARY` | Reviewed founder beneficiary, preferably a Safe |
| `VESTING_START` | Explicit Unix start; Base production accepts only latest block time ±5 minutes, inclusively |
| `FOUNDER_ALLOCATION` | Whole-token allocation; launch value `50000000` |
| `FOUNDER_CEREMONY` | Absolute path to reviewed non-secret JSON frozen before deployment |
| `VESTING_ADDRESS` | Vault address for guarded founder mint and read-only status |

Ethereum `MAINNET_RPC_URL`, `SEPOLIA_RPC_URL`, and `ETHERSCAN_API_KEY` remain for legacy compatibility.

## Build and local checks

```sh
cd contracts
npm install
npm run compile
npm test
```

An optional `MAINNET_FORK_RPC_URL` can fork Ethereum mainnet for legacy/local simulation. It is not required for the Base Sepolia rehearsal.

## Base Sepolia rehearsal commands

Use the production-shaped committee and exact cap; do not qualify a rehearsal with `ALLOW_SINGLE_ATTESTOR=1`.

```sh
npm run deploy:bridge:base-sepolia
npm run verify:base-sepolia
CONTRACT=<address> ATTESTATIONS=<absolute-path> npm run mint:base-sepolia

npm run deploy:founder-vesting:base-sepolia
CONTRACT=<address> VESTING_ADDRESS=<vault> FOUNDER_CEREMONY=<absolute-path> \
  ATTESTATIONS=<absolute-path> npm run mint:founder-vesting:base-sepolia
VESTING_ADDRESS=<vault> npm run founder-vesting:status:base-sepolia
```

The bridge deploy writes `deployments/wrapped-matrix.baseSepolia.json`; the founder deploy writes `deployments/founder-vesting.baseSepolia.json`. Both are gitignored ceremony artifacts, not public evidence by themselves. The founder deploy prints the exact direct `npx hardhat verify` command for `FounderVestingVault`; there is no separate package alias for that verification.

## Base production commands

Run only after the Base Sepolia exit gate in the authoritative runbook:

```sh
npm run deploy:bridge:base
npm run verify:base
CONTRACT=<address> ATTESTATIONS=<absolute-path> npm run mint:base

FOUNDER_CEREMONY=<absolute-path> npm run deploy:founder-vesting:base
CONTRACT=<address> VESTING_ADDRESS=<vault> FOUNDER_CEREMONY=<absolute-path> \
  ATTESTATIONS=<absolute-path> npm run mint:founder-vesting:base
VESTING_ADDRESS=<vault> npm run founder-vesting:status:base
```

Production deploy guards require chain ID `8453`, `PRIVATE_KEY`, explicit `ATTESTORS`, a nonzero `MINT_CAP`, at least 2 attestors, and strict `>2/3`. Founder deployment additionally reads time from the latest Base block, accepts the reviewed start only within an inclusive five-minute past clock-skew/five-minute future ceremony window, and requires the frozen ceremony JSON to match every constructor input. The vault constructor enforces the same window against the block that actually mines the deployment, so a delayed or pending transaction cannot make an accepted start stale. The verification script reads the recorded raw constructor arguments rather than guessing resolved values.

## Founder backing ceremony

Before deploying, freeze and hash/sign a non-secret JSON file in this exact shape (decimal strings avoid JSON integer ambiguity):

```json
{
  "network": "base",
  "chainId": "8453",
  "token": "0x...",
  "beneficiary": "0x...",
  "start": 1800000000,
  "cliffDuration": 31536000,
  "duration": 157680000,
  "allocation": "50000000000000000000000000"
}
```

Use the same `FOUNDER_CEREMONY` file for deployment and `mint:founder-vesting:*`; do not regenerate it from the deployment record. The founder-only mint command compares the on-chain start and every other immutable getter with this frozen input, binds the attestations to the reviewed vault and allocation, and refuses to mint after the cliff or into a nonempty/already-releasing vault before invoking the generic mint simulation/broadcast path.

Deploying `FounderVestingVault` creates no tokens. Founder minting is allowed only after exact 1:1 native backing exists:

1. Verify the vault source and immutable constructor values: token, beneficiary, start, `31536000`-second cliff, `157680000`-second total duration, and `50000000000000000000000000` allocation.
2. Lock exactly `50000000000000000` native base units to the verified vault address.
3. Confirm the committed lock before gathering the immutable committee's threshold signatures.
4. Have a user run the guarded `mint:founder-vesting:*` command with the frozen file and their own Base gas; never use the generic `mint:*` alias for the founder allocation.
5. Confirm the vault holds exactly `50000000000000000000000000` wrapped base units.
6. Run `founder-vesting:status:*` and `matrix bridge reconcile`; status `fullyFunded` alone does not prove native escrow backing.

Never mint founder wMATRIX first and attempt to back it later.

## Node integration

Start from `matrixd -init`; do not replace its generated config with a snippet. Merge the launch leaf values from [`services/core/configs/base-launch.overlay.yaml.example`](../services/core/configs/base-launch.overlay.yaml.example), resolve every placeholder, and preserve generated secrets and unrelated safe defaults. For production set `bridge.chain_id: 8453`; for rehearsal use `84532` with the separate rehearsal address/RPC/block.

Launch node policy is fee `100` basis points, an explicit real maintainer account, `5000` basis points of the fee to that maintainer (at most 0.5% of transferred value), zero provider emission, bonded-open membership with stake, exact CORS origins, public reads/signed writes, and positive rate limits.

Each validator generates its own attestor keystore:

```sh
matrix bridge attestor-new --out /absolute/path/to/attestor.json
```

Set `bridge.attestor_keystore` to that file and provide `MATRIX_ATTESTOR_PASSPHRASE` in the process environment. The passphrase is never a YAML field. Configure and run the burn watcher on every validator. Every configured bridge, singleton included, is consensus-ordered.

## Legacy Ethereum compatibility

The following aliases remain available but are not the primary launch ceremony:

```sh
npm run deploy:bridge:sepolia
npm run verify:sepolia
npm run mint:sepolia
npm run deploy:bridge:mainnet
npm run verify:mainnet
```

The historical Ethereum Sepolia guide is [`docs/runbooks/sepolia-rehearsal.md`](../docs/runbooks/sepolia-rehearsal.md). It defers current launch policy to the Base runbook.

## Toolchain

- Hardhat v2 with `@nomicfoundation/hardhat-toolbox` and OpenZeppelin Contracts v5.
- Solidity `0.8.24`, optimizer 200 runs, Cancun EVM target.
- Deployment records under `contracts/deployments/` and `.env` are gitignored; publish reviewed non-secret evidence separately.
