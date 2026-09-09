# Base launch: rehearsal and production ceremony

This is the authoritative operator runbook for launching `WrappedMatrix` (wMATRIX) on Base. Follow it in order: complete and publish a Base Sepolia rehearsal first, then repeat the reviewed ceremony on Base production. Ethereum mainnet and Sepolia remain legacy-compatible targets, not the primary launch path.

This runbook documents a procedure; it is not evidence that any deployment is public. A deployment is public only after operators publish the addresses, transactions, verified source, immutable constructor values, ceremony record, and reconciliation snapshots listed below.

## Launch policy (do not improvise)

| Item | Base Sepolia rehearsal | Base production |
| --- | --- | --- |
| EVM chain | Base Sepolia, chain ID `84532` | Base, chain ID `8453` |
| Minimum bridge lock | `100 MATRIX` = `100000000000` native base units | Same |
| Immutable wMATRIX cap | `60000000000000000000000000` wrapped base units = 60,000,000 wMATRIX = 6% of the 1B native maximum | Same |
| Founder allocation | 50,000,000 wMATRIX = 5%, minted to the immutable vesting vault only after exact 1:1 native escrow | Same |
| Vesting | Linear from the start timestamp over 5 years total, with a 1-year cliff; 20% has vested at the cliff | Same |
| Protocol fee | `100` basis points = 1% of ordinary consensus-settled value transfers | Same |
| Maintainer policy | An explicit real Matrix account receives `5000` basis points of the protocol fee | Same |
| Maximum maintainer charge | 50% of the 1% fee = at most 0.5% of transferred value | Same |
| Provider emission | `0` native base units per block | Same |
| Membership | `bonded-open`, with bonded stake enabled | Same |
| EVM gas | Each deployer, mint caller, holder, or burner pays their own Base ETH gas | Same |
| Relayer | None. The protocol does not sponsor or relay user EVM transactions. | Same |
| Market price | A DEX price is the market price. wMATRIX has no stablecoin or fiat peg. | Same |

A bridge lock receives the full native amount and does not pay the protocol transfer fee. The 100-basis-point fee applies to ordinary consensus-settled value transfers; its 5000-basis-point maintainer share is a share **of that fee**, not an additional 50% fee.

## Two security domains

Native validators and EVM mint attestors are deliberately separate security domains:

- The native Matrix validator set is dynamic chain state. With `bonded-open`, validators can join and exit and proven equivocators can be ejected at epoch boundaries.
- The `WrappedMatrix` attestor addresses and threshold are immutable constructor values for one EVM deployment. Each validator may operate a separate secp256k1 attestor key, but validator membership does not control the contract's immutable list.
- Joining the native validator set does **not** add or rotate an EVM attestor. Leaving or being ejected does **not** remove one. A removed validator can remain an authorized mint attestor for that existing `WrappedMatrix` deployment.
- Changing the attestor list or threshold requires a new `WrappedMatrix` deployment and an explicitly planned liquidity/user migration. Do not describe a validator-set update as attestor rotation.

For Base production, use at least 2 attestors and a threshold strictly greater than two thirds: `3 * threshold > 2 * attestor_count`. The deployment script enforces both rules. This means 3 attestors require 3 signatures, 4 require 3, and 5 require 4; 2-of-3 is not valid. Base Sepolia is also a real network to the script and receives the strict `>2/3` check. Rehearse the production-sized committee; do not use `ALLOW_SINGLE_ATTESTOR=1` for launch qualification.

Native burn unlocks are a different consensus decision again. Every bridge configured inside `matrixd` is consensus-ordered, including a singleton network. A singleton has a one-validator quorum; it does **not** bypass consensus or directly mutate escrow from the watcher. Dynamic membership later changing that singleton still does not rotate the EVM attestors.

## 0. Freeze inputs and prepare generated node configs

Record reviewers and the exact git revision. Resolve these inputs before spending gas:

- Base Sepolia and Base RPC URLs and explorer API access. Verification uses one
  etherscan.io API key for every chain through the Etherscan V2 API; the
  per-explorer V1 endpoints (`api.basescan.org`, `api-sepolia.basescan.org`) are
  retired and reject every request, and a stale key setup fails at the
  verification step - after the deploy is already broadcast and paid for.
- Funded, disposable Base Sepolia deploy/mint/burn accounts and production custody accounts.
- The ordered attestor addresses, independent custody owners, and threshold.
- A real 64-lowercase-hex Matrix `maintainer_account`.
- The production browser origin, native genesis validator IDs, bootstrap peers, stake funding source, and each node's absolute attestor-keystore path.
- The founder beneficiary, preferably a reviewed Safe; the policy that Base `VESTING_START` must be within the inclusive interval from five minutes before to five minutes after the latest Base block at deployment; the frozen ceremony-file evidence location; and the native account that can escrow exactly 50,000,000 MATRIX. Select and freeze the exact timestamp immediately before the vault deployment, not at the beginning of a long ceremony.
- Base watcher deployment blocks, confirmation policy, monitoring owner, rollback/migration owner, and evidence publication location.

Build and test from the exact revision:

```sh
cd contracts
npm install
npm run compile
npm test
```

Generate each node's safe baseline rather than constructing a config from the overlay:

```sh
cd services/core
go build ./cmd/...
./matrixd -init -config /absolute/path/to/config.yaml
```

`matrixd -init` generates a `0600` config with a random admin key, local-safe origins, positive rate limits, a full-cap reward pool, a 100-basis-point fee, zero emission, bonded-open membership, stake enabled, and the bridge disabled. Preserve those safe defaults. The launch overlay at [`services/core/configs/base-launch.overlay.yaml.example`](../../services/core/configs/base-launch.overlay.yaml.example) is a **merge-only example**, not a standalone config. Merge its leaf values into each generated config, retain generated secrets and unrelated sections, resolve every placeholder, and review the final full config on every node.

If a founder-backing account must be funded at genesis, add exactly `50000000000000000` native base units to its named allocation and reduce `genesis.reward_pool` by exactly the same amount (from `1000000000000000000` to `950000000000000000`). Do this identically on every node **before first start**. Genesis is one-shot; changing YAML after genesis is applied does not move funds. On an existing network, fund the backing account with a consensus-settled transfer instead. Never use a fake account in a launch config.

## 1. Base Sepolia rehearsal (chain 84532)

### 1.1 Generate and review attestor keys

Each attestor custodian independently runs:

```sh
export MATRIX_ATTESTOR_PASSPHRASE='<secret supplied outside config and logs>'
matrix bridge attestor-new --out /absolute/path/to/attestor.json
```

Publish only the printed address and custody acknowledgement. Do not copy keystores between validators. The passphrase remains environment-only. Freeze the ordered `ATTESTORS` list and select `THRESHOLD` satisfying strict `>2/3`.

This committee is immutable for the resulting Base Sepolia contract. A native validator join, exit, or ejection during the rehearsal does not change it.

### 1.2 Configure the contract environment

Copy `contracts/.env.example` to the gitignored `contracts/.env` and resolve at least:

```dotenv
BASE_SEPOLIA_RPC_URL=<credential-bearing Base Sepolia HTTPS RPC URL>
ETHERSCAN_API_KEY=<one etherscan.io key; covers Base Sepolia via Etherscan V2>
PRIVATE_KEY=<funded throwaway rehearsal key, 0x-prefixed>
ATTESTORS=<comma-separated reviewed addresses>
THRESHOLD=<integer satisfying 3*m > 2*n>
MINT_CAP=60000000000000000000000000
WMATRIX_ADDRESS=<set after WrappedMatrix deployment>
FOUNDER_BENEFICIARY=<reviewed rehearsal beneficiary>
VESTING_START=<reviewed Unix timestamp>
FOUNDER_ALLOCATION=50000000
FOUNDER_CEREMONY=<absolute path to reviewed frozen ceremony JSON>
VESTING_ADDRESS=<set after vault deployment>
```

Do not put secrets on a command line. Confirm `.env` is ignored and confirm the RPC reports chain ID `84532`.

### 1.3 Deploy and verify wMATRIX

```sh
cd contracts
npm run deploy:bridge:base-sepolia
npm run verify:base-sepolia
```

The deployment writes the gitignored `deployments/wrapped-matrix.baseSepolia.json`. Independently compare its chain ID, deployment block, address, constructor attestors, threshold, raw cap argument, and resolved on-chain cap with the reviewed ceremony sheet and BaseScan. Confirm `totalSupply()` is zero.

The repository does not track that deployment record, so the command alone is not a public deployment claim.

### 1.4 Merge the node configuration and start all watchers

Merge the example overlay into each generated node config. For this rehearsal, change its Base production values to the reviewed Base Sepolia values:

```yaml
bridge:
  contract: "<deployed Base Sepolia WrappedMatrix address>"
  chain_id: 84532
  attestor_keystore: "/absolute/path/to/this-node-attestor.json"
  watch:
    enabled: true
    rpc_url: "<credential-bearing Base Sepolia HTTPS RPC URL>"
    start_block: <WrappedMatrix deployment block>
    confirmations: 12
    poll_interval: 12s
    max_block_span: 2000
```

Resolve the exact browser origin (never `*`), maintainer account, peers, validator IDs, and all other placeholders. Start each node with `MATRIX_ATTESTOR_PASSPHRASE` in its environment. Confirm the logged attestor address belongs to the immutable contract set.

Run the watcher on every validator. It reads Base logs and submits native consensus attestations; it is not a user-gas relayer. Every configured `matrixd`, singleton included, releases native escrow only through consensus ordering.

Before any lock, run the browser pre-lock readiness gate against **every** configured validator endpoint. The browser first confirms the wallet chain and deployed bytecode, then reads `threshold`, `attestorCount`, `mintCap`, `totalSupply`, and `ERC20_PER_NATIVE_UNIT` from the exact WrappedMatrix address. It sends one fresh random 32-byte challenge to every endpoint's public, rate-limited `GetBridgeReadiness` RPC and requires the responses to agree on chain, normalized contract, and `100000000000` minimum. Recover each readiness-only signature, reject duplicates, require every recovered address to satisfy on-chain `isAttestor`, and require at least the live contract threshold. A transport failure is tolerable only when the remaining valid registered responders still meet threshold; any semantic or signature mismatch stops the ceremony. Preserve the endpoint list and recovered signer addresses as rehearsal evidence. This gate proves current deployment/key availability; it does not replace source verification or post-lock reconciliation.

### 1.5 Rehearse lock, attest, mint, burn, and reconciliation

Fund a native account through the reviewed genesis allocation or a consensus-settled transfer. The minimum lock is exactly 100 MATRIX:

```sh
matrix bridge lock --to <Base-wallet-address> --amount 100000000000 --api-key <key>
```

Collect at least the deployed threshold of independent contract-attestor signatures:

```sh
matrix bridge attestation --lock-id <lock-id> \
  --validator <validator-1:9091> --validator <validator-2:9091> \
  --validator <additional-validator-endpoints> --api-key <key> > atts.json
```

The dynamic validator set and immutable EVM attestor list can differ. Count signatures by registered EVM attestor address, not merely by current validator membership.

The user who submits the mint pays their own Base Sepolia ETH gas; there is no relayer:

```sh
cd contracts
CONTRACT=<WrappedMatrix-address> ATTESTATIONS=<absolute-path-to-atts.json> \
  npm run mint:base-sepolia
```

The script validates agreement, registered distinct signers, signature order, threshold, replay status, cap, and a static simulation before broadcasting. Reconcile native escrow against on-chain `totalSupply()`, then burn an exact multiple of `1000000000` wrapped base units from the holder's wallet and wait for the configured confirmations. Verify the burn unlock is consensus-ordered even if the rehearsal currently has one validator. Reconcile again.

### 1.6 Rehearse founder vesting and exact backing

Immediately before deployment, freeze and hash/sign the non-secret `FOUNDER_CEREMONY` JSON described in `contracts/README.md` with the exact network, chain ID, token, beneficiary, start, cliff, duration, and wrapped-base-unit allocation. Set the rehearsal `WMATRIX_ADDRESS`, beneficiary, start, 50M allocation, and ceremony path, then deploy the immutable vault:

```sh
FOUNDER_CEREMONY=<absolute-path> npm run deploy:founder-vesting:base-sepolia
```

The vault has a 1-year cliff and a 5-year total linear schedule measured from `VESTING_START`; therefore 20% is vested at the cliff. The deployment script prints the exact direct `npx hardhat verify` command. Run that command and preserve the verification URL.

Deployment creates no founder tokens. Complete the backing ceremony in this exact order:

1. Lock exactly `50000000000000000` native base units (50,000,000 MATRIX) into L1 bridge escrow with the new vault address as the EVM recipient.
2. Confirm the committed native lock and its exact amount before requesting signatures.
3. Gather the immutable contract committee's threshold attestations.
4. Have a user run the guarded founder mint with the frozen input and their own Base Sepolia gas:

   ```sh
   CONTRACT=<WrappedMatrix-address> VESTING_ADDRESS=<vault-address> \
     FOUNDER_CEREMONY=<absolute-path> ATTESTATIONS=<reviewed-json> \
     npm run mint:founder-vesting:base-sepolia
   ```

   This compares every on-chain immutable, including `start`, with the pre-deployment file before invoking the generic mint simulation/broadcast workflow.
5. Confirm the vault received exactly `50000000000000000000000000` wrapped base units (50,000,000 wMATRIX).
6. Run the status and bridge reconciliation checks and publish both snapshots:

```sh
VESTING_ADDRESS=<vault-address> npm run founder-vesting:status:base-sepolia
matrix bridge reconcile --api-key <key>
```

`fullyFunded` in the vesting status checks the vault's wrapped balance plus released amount; it does not independently prove native backing. The reconciliation evidence is mandatory. Never mint any founder unit before the exact 1:1 native escrow exists.

### 1.7 Rehearsal exit gate

Do not proceed until reviewers sign off on:

- Chain ID `84532`, exact 6% cap, zero initial supply, source verification, and deployment records.
- At least 2 attestors and strict `>2/3`; no single-attestor override.
- A demonstrated native membership change that does not falsely claim to rotate the immutable EVM committee.
- Minimum 100-MATRIX lock, threshold mint, user-paid gas, burn, consensus-ordered unlock, and two reconciliation snapshots.
- The 50M exact-backing founder ceremony, verified immutable vault, status output, 1-year cliff, 5-year total schedule, and 20% vested at cliff.
- Final full node configs showing fee `100`, explicit maintainer account, maintainer fee share `5000`, emission `0`, bonded-open stake, exact origin, public reads/signed writes, and positive rate limits.
- A written migration procedure for the fact that changing EVM attestors requires a new deployment.

## 2. Base production ceremony (chain 8453)

### 2.1 Re-freeze production inputs

Do not copy rehearsal secrets, addresses, blocks, or RPC credentials. Reconfirm production custody and resolve every input listed in section 0. The production attestor committee is a separate immutable deployment security domain. The production deploy script refuses fewer than 2 attestors and refuses any threshold not strictly greater than two thirds.

Set the Base values in the gitignored environment:

```dotenv
BASE_RPC_URL=<credential-bearing Base HTTPS RPC URL>
ETHERSCAN_API_KEY=<one etherscan.io key; covers Base via Etherscan V2>
PRIVATE_KEY=<funded production deployer key>
ATTESTORS=<comma-separated production attestor addresses>
THRESHOLD=<integer satisfying 3*m > 2*n>
MINT_CAP=60000000000000000000000000
FOUNDER_BENEFICIARY=<reviewed production beneficiary>
VESTING_START=<reviewed production Unix timestamp selected immediately before vault deployment>
FOUNDER_ALLOCATION=50000000
FOUNDER_CEREMONY=<absolute path to reviewed frozen ceremony JSON>
```

Confirm the endpoint reports chain ID `8453`; confirm each transaction in a hardware wallet or equivalent production custody flow; record Base ETH funding and current gas policy. Users pay their own Base gas. No relayer or gas sponsorship is part of launch.

### 2.2 Deploy, verify, and publish WrappedMatrix

```sh
cd contracts
npm run deploy:bridge:base
npm run verify:base
```

Before enabling locks, have independent reviewers verify on BaseScan:

- Chain ID `8453`, contract bytecode/source, deployment transaction/block, and deployer.
- Ordered attestors, attestor count, and threshold with `3*m > 2*n` and `n >= 2`.
- Immutable cap `60000000000000000000000000` wrapped base units (60M, 6%).
- `ERC20_PER_NATIVE_UNIT = 1000000000` and `totalSupply() = 0`.
- No owner/admin path that can rotate the committee or increase this deployment's cap.

Publish those facts and the ceremony signatures. If any immutable value is wrong, abandon the deployment; do not fund it.

### 2.3 Merge production node configs

Merge [`base-launch.overlay.yaml.example`](../../services/core/configs/base-launch.overlay.yaml.example) into every separately generated config. Resolve the exact production origin, bootstrap peers, genesis validator IDs, real maintainer account, contract address, chain `8453`, per-node attestor-keystore path, RPC URL, and deployment block. Preserve generated API keys and all unrelated generated sections. Compare consensus-critical values across nodes before first start.

The launch economics are:

```yaml
consensus:
  membership_mode: bonded-open
  participate_in_open_set: true
  fee_basis_points: 100
  maintainer_account: "<resolved real 64-lowercase-hex Matrix account>"
  maintainer_fee_share_basis_points: 5000
  rewards:
    per_block: 0
```

The maintainer receives half of the 1% protocol fee, at most 0.5% of an ordinary transferred amount. Emission is zero. Run every watcher and confirm every node's attestor identity against the immutable EVM list. Again: native join/ejection does not rotate that list; a removed validator may retain mint authority until migration to a newly deployed contract.

### 2.4 Deploy and verify the founder vault

After setting `WMATRIX_ADDRESS` to the reviewed production token, read the latest Base block timestamp and choose `VESTING_START` in the inclusive interval from 300 seconds before through 300 seconds after it. Freeze the exact constructor inputs in the non-secret JSON shape documented in `contracts/README.md`, hash/sign that file, and do not edit or regenerate it from deployment output. Then run:

```sh
cd contracts
FOUNDER_CEREMONY=<absolute-path> npm run deploy:founder-vesting:base
```

The script reads the latest Base block itself and rejects a start even one second outside that window (including any already-elapsed or stale cliff). The vault constructor rechecks the same inclusive bounds against the block that actually mines the deployment, so custody or mempool delay fails the transaction instead of creating a stale immutable schedule. The script also compares every deployment input with the frozen file before broadcasting.

Run the exact direct verification command printed by the script. Independently verify the token, beneficiary, Unix start, `31536000`-second cliff, `157680000`-second total duration, and `50000000000000000000000000` wrapped-base-unit allocation. The schedule is linear from start over five years; nothing is releasable before the one-year cliff, and 20% is vested at that cliff. Publish the verified vault address and constructor values.

### 2.5 Execute the production founder backing ceremony

The vault deployment does not mint or back tokens. Perform the irreversible steps in order, with two-person checks at each boundary:

1. Confirm the native funding source owns at least `50000000000000000` native base units plus any unrelated operational needs.
2. Re-run the complete browser readiness gate from section 1.4 against every production endpoint for the exact founder amount. Record the fresh challenge, deployment fields, recovered unique registered signers, live threshold, conversion, and cap headroom. Do not sign the native payment if this fails.
3. Submit and commit a lock of exactly `50000000000000000` native base units to the verified Base founder-vault address. This is 50,000,000 MATRIX at 9 decimals.
4. Query the committed lock independently on multiple nodes and record its lock ID, recipient, native amount, wrapped amount, Base chain ID, and contract.
5. Collect the required signatures from the immutable production attestors. Confirm distinct recovered addresses and the deployed strict `>2/3` threshold.
6. A designated user broadcasts the mint and pays their own Base ETH gas:

   ```sh
   cd contracts
   CONTRACT=<production-WrappedMatrix> VESTING_ADDRESS=<production-vault> \
     FOUNDER_CEREMONY=<absolute-path> ATTESTATIONS=<reviewed-json> \
     npm run mint:founder-vesting:base
   ```

   This founder-only entry point must pass before any transaction is sent: it compares the deployed vault's on-chain `start` and every other immutable with the frozen ceremony file, checks the attested recipient/allocation, and refuses a nonempty, already-releasing, or cliff-elapsed vault. Do not use the generic `mint:base` alias for this allocation.

7. Confirm the immutable vault balance is exactly `50000000000000000000000000` wrapped base units and no founder amount was minted elsewhere.
8. Run and publish:

   ```sh
   VESTING_ADDRESS=<production-vault> npm run founder-vesting:status:base
   matrix bridge reconcile --api-key <key>
   ```

9. Compare native outstanding escrow with Base `totalSupply()` at stated native and Base heights. Founder status alone is insufficient proof of backing.

Any mismatch stops launch. Do not top up with an unbacked mint; investigate or migrate.

### 2.6 Open user operations and market access

Only after the founder and general bridge reconciliation evidence is public:

- Open the exact configured browser origin with `public_reads: true`, `signed_writes: true`, and positive per-caller rate limits. Confirm `GetBridgeReadiness` is classified as a public read and shares that rate limiter; the browser must pass its full readiness gate again before every payment signature.
- Announce the 100 MATRIX (`100000000000` native base-unit) minimum lock and that every EVM transaction requires the user's own Base ETH.
- Do not advertise a relayer; none exists.
- If liquidity is provided on a DEX, publish the pool address and assets. The resulting DEX price is market-discovered and may move freely. wMATRIX has no stablecoin, USD, or fiat peg.
- Monitor watcher health on every validator, pending locks, mints, burns, native escrow, Base supply, cap headroom, and vesting-vault funding.

## 3. Evidence and migration checklist

Publish and retain:

- Exact source revision and test output.
- Both chain IDs and RPC chain-ID checks.
- wMATRIX and vault addresses, deployment transactions/blocks, verified-source URLs, and raw constructor arguments.
- Attestor ceremony participants, ordered addresses, threshold calculation, and custody attestations (never secrets).
- Full reviewed policy values: 60M/6% cap; 50M/5% founder allocation; 1-year cliff; 5-year total linear vesting; 20% at cliff; fee 100 bp; explicit maintainer; 5000 bp share of fee; emission 0; bonded-open stake.
- Pre-lock readiness evidence: fresh challenge, each endpoint's signed chain/contract/attestor/minimum, recovered unique registered signer set, live threshold, conversion, and cap-headroom result.
- Lock IDs, attestation signer addresses, mint/burn transaction hashes, vesting status, and reconciliation snapshots at stated heights.
- The exact origin and public endpoint/rate-limit policy.

If an EVM attestor is compromised, removed from native consensus, or simply needs rotation, native membership operations are not remediation. Pause new bridge use operationally, deploy a new `WrappedMatrix` with a new immutable committee and strict production threshold, verify it, establish a reconciliation cutover, and publish a holder/liquidity migration plan. A removed native validator can remain an attestor on the old contract until users migrate away from it.
