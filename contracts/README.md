# Matrix Ethereum contracts (wrapped MATRIX)

`contracts/` is the Solidity project for the **Ethereum-side, wrapped
representation of native MATRIX**.

MATRIX is **native-first**: the canonical coin lives on the Matrix OS consensus
L1 (see `services/core/internal/{token,market}`) with 9 decimals and is the
single source of truth for balances and supply. All marketplace compute and LLM
inference settlement happens in **native MATRIX** on the L1 via consensus. The
ERC-20 contracts here are a **wrapped mirror** so native MATRIX can be
represented on Ethereum (e.g. for a future exchange listing) while staying
backed 1:1 by native MATRIX locked on the L1.

Two contracts:

- **`WrappedMatrix.sol` (wMATRIX)** - the bridge-backed wrapped token. Its
  supply is minted/burned **only** through the lock-and-mint bridge, so it is
  always backed 1:1 by locked native MATRIX. This is the real bridge token.
- **`MatrixToken.sol` (MATRIX)** - the standalone ERC-20 mirror kept for its
  existing deploy/test surface and EIP-2612 `permit` support. Prefer
  `WrappedMatrix` for the bridge.

  Its minting used to be `onlyOwner`: one key could issue up to the 1e27 cap in
  a single transaction, with no warning to holders, sitting next to a bridge
  token that already required m-of-n. `Ownable` is now gone entirely - not
  merely pointed at a multisig, because that is a deploy-time convention nothing
  enforces. Every mint needs a threshold of distinct registered minter
  signatures AND a 2-day timelock: `proposeMint` starts the clock, the proposal
  is publicly visible, `executeMint` lands it, and the same threshold can
  `cancelMint` during the delay. The threshold, minter set and delay are
  immutable, so changing any of them means redeploying. `scripts/deploy.ts`
  refuses a threshold below 2 on a real network.

## Native <-> wrapped scale (single source of truth)

The conversion is defined once in `services/core/internal/token/supply.go` and
mirrored in Solidity:

| Unit                          | Value                                    |
| ----------------------------- | ---------------------------------------- |
| Whole MATRIX (human)          | 1                                        |
| Native base units / whole     | `1e9` (native has 9 decimals)            |
| ERC-20 base units / whole     | `1e18` (wrapped has 18 decimals)         |
| **ERC-20 base units / native**| **`1e9`** (`ERC20_PER_NATIVE_UNIT`)      |
| Native max supply             | `1e18` base units (fits uint64)          |
| Wrapped max supply            | `1e27` (`1,000,000,000 * 1e18`)          |

`1` native base unit maps to exactly `1e9` wrapped base units, and the caps are
the same money at both scales.

## Lock-and-mint bridge

```
native lock  -> attest -> wrapped mint      (native MATRIX -> wMATRIX)
wrapped burn -> unlock native               (wMATRIX -> native MATRIX)
```

- **Lock (native -> wrapped):** the Go bridge (`services/core/internal/bridge`)
  moves a user's native MATRIX into an on-L1 escrow account and records a
  `LockEvent`. The validator set signs a **secp256k1 attestation** over the
  canonical digest
  `keccak256(recipient || amount || lockId || chainId || contract)`.
  `WrappedMatrix.mint` verifies a **threshold** (m-of-n) of signatures from
  distinct registered attestor addresses, mints `amount` wMATRIX to `recipient`,
  and marks the `lockId` minted so it can never be replayed.
- **Burn (wrapped -> native):** a holder calls `WrappedMatrix.burn(amount,
  nativeRecipient)`, which burns the wMATRIX and emits
  `Burned(address indexed burner, string nativeRecipient, uint256 amount)`. The
  Go bridge decodes that log with `bridge.DecodeBurnedLog` (a dependency-free ABI
  decoder that recovers the burner, native recipient, and amount, and derives a
  stable id from `txHash:logIndex`) into a `BurnEvent`, then applies it exactly
  once (replay-protected on that id), releasing the escrowed native MATRIX to
  `nativeRecipient` - directly on a single node, and by quorum attestation on a
  validator set, as below. The `BridgeE2E` test cross-checks the raw
  emitted log against the exact bytes the Go decoder parses. Feeding logs into
  the decoder is automated by `bridge.Watcher`, an `eth_getLogs` poller that runs
  as a `matrixd` subsystem against the node's own ledger - see
  [Running the burn watcher in `matrixd`](#running-the-burn-watcher-in-matrixd).

  **The unlock is consensus-ordered on a validator set.** The watcher is still
  the thing with an Ethereum endpoint, so what it observes is per-node - but what
  that observation is allowed to do depends on the size of the set, and the node
  decides that itself rather than trusting the operator to. On a single-node
  network the watcher releases escrow directly: one ledger, one authority. On a
  set it may not, because the release would land on one node's ledger and nowhere
  else, splitting the escrow balance and the 1:1 backing invariant across nodes.
  There the watcher instead submits an **attestation** to a reserved recipient
  that encodes the burn, and the engine releases escrow on the block where
  attesting voting power crosses quorum. The recipient is a pure function of the
  burn, so two validators attesting the same burn produce byte-identical
  transactions and the tally counts them as being about one thing; the tally is
  keyed by the whole recipient, so attestations that disagree about the account
  or the amount are separate tallies and neither borrows the other's power. A
  quorum has to agree on where the money goes, not merely that something was
  burned. A node with no Ethereum endpoint still tallies from the committed
  blocks and reaches the same release at the same height.

  This mirrors the lock half, which always required a threshold of validator
  signatures before the contract would mint. Both directions are now
  quorum-gated. What it does not do is verify that the burn happened: it
  verifies that a quorum of the set says so, which is the same trust assumption
  the mint half has always had.

### Locking, and getting the attestations to mint with

A lock is a signed transfer to the reserved recipient
`bridge/lock/<ethereum address>`, so it is ordered by consensus and every node
applies the same escrow move from the same committed block. Build it with:

```sh
matrix bridge lock --to 0xYourEthAddress --amount 4000000000
```

It prints the LOCK ID, derived from the transaction (nonce, sender, recipient,
amount). Then ask each validator for its signature:

```
POST /matrix.market.v1.MarketService/GetLockAttestation  {"lockId": "0x..."}
```

Each node returns ONE signature, because each holds its own attestor key and the
contract counts the threshold. Collect m of them and call
`WrappedMatrix.mint(recipient, amount, lockId, signatures)`.

Each validator generates its attestor key with:

```sh
matrix bridge attestor-new --out ~/.matrix/attestor.json
```

That prints the address to put in `ATTESTORS` at deploy time. Point
`bridge.attestor_keystore` at the file and export
`MATRIX_ATTESTOR_PASSPHRASE`; a node that cannot unlock a configured key refuses
to start, because a validator that looks like it is attesting and is not means
mints silently stop reaching quorum.

`test/BridgeE2E.test.ts` drives this exact path - consensus-derived lock id,
real signatures, real mint - alongside the legacy one.

### Running the burn watcher in `matrixd`

Add a `bridge` section to the node config (`~/.matrix/config.yaml`) with the
deployment facts, then restart the node:

```yaml
bridge:
  contract: "0xYourWrappedMatrixAddress"   # required to enable the bridge at all
  chain_id: 1                              # EVM chain id the contract is on
  watch:
    enabled: true
    rpc_url: "https://your-eth-endpoint"
    start_block: 18000000                  # the contract's deployment block
    confirmations: 12                      # null -> 12; set 0 only on a local chain
    poll_interval: 12s                     # empty/0 -> 2s
    max_block_span: 2000                   # blocks per eth_getLogs call
```

On start the node logs the enabled bridge and the block the watcher resumes
from. Notes that matter in operation:

- **The bridge is opt-in.** With no `contract` the subsystem stays off and the
  node behaves as it did before. A config that sets `watch.enabled` without a
  `contract`, `chain_id`, or `rpc_url` **fails startup** rather than coming up
  quietly not relaying.
- **`chain_id` and `contract` are bound into every attestation digest.** Wrong
  values produce signatures `WrappedMatrix.mint` rejects, so they are validated
  at startup, not on first use.
- **The scan cursor is persisted** in the node's KV store (namespaced per
  contract address), so a restart resumes at the next unscanned block instead of
  re-scanning from `start_block`. Correctness never depends on it: the cursor is
  written after a span's burns are applied, and `ProcessBurn` dedups by burn id,
  so a crash re-scans harmlessly rather than double-unlocking.
- **`confirmations` left unset defaults to 12,** not 0 - a node on a real
  endpoint must not release escrow on a block that can still be reorged away.
  Set it to `0` only for an instant-finality dev chain such as Hardhat.
- **Run the watcher on every validator, not on one relayer.** On a set, escrow
  is released by quorum, so a burn stays unreleased until more than two thirds of
  the voting power has attested it. Pointing only one node at Ethereum leaves
  every burn pending forever. A node whose bridge is consensus-ordered also
  refuses a direct release, so `cmd/bridge-watch` cannot be used to force one
  through.
- **Because the node holds both halves on one ledger,** `Bridge.Reconcile` is a
  real invariant check there (escrow balance == locked - unlocked).
  `cmd/bridge-watch` applies burns to a throwaway ledger it seeds, so use it to
  inspect a range or verify an endpoint and address, not to run a bridge.

### Why secp256k1 attestor keys

L1 consensus uses ed25519 keys, which the EVM cannot verify (its only signature
precompile is `ecrecover`, i.e. secp256k1/ECDSA). Each validator therefore also
holds a **secp256k1 attestor key** whose Ethereum address is registered in
`WrappedMatrix`. The Go signer (`internal/bridge.Attestor.SignDigest`) produces
`ecrecover`-compatible `{r,s,v}` signatures over the identical digest, so a
Go-produced attestation verifies on-chain unchanged. Native locking/unlocking
still uses the ed25519 consensus accounts. `SignCompact` already yields a
canonical low-S signature, satisfying EIP-2.

## Commands

```sh
cd contracts
npm install            # Node 22
npx hardhat compile
npx hardhat test       # MatrixToken + WrappedMatrix + Go<->Solidity e2e
```

### Local deploy

Start a local node and deploy in the same shell session:

```sh
npx hardhat node &                                        # background the node
sleep 4                                                   # wait for readiness
npx hardhat run scripts/deploy.ts        --network localhost   # MatrixToken
npx hardhat run scripts/deploy-bridge.ts --network localhost   # WrappedMatrix
```

`scripts/deploy.ts` reads the minter set from `MINTERS` (comma-separated `0x`
addresses), the threshold from `THRESHOLD`, the initial-supply holder from
`INITIAL_HOLDER` (defaults to the deployer), and the initial supply from
`INITIAL_SUPPLY` in whole MATRIX.

**`INITIAL_SUPPLY` defaults to zero and it used to be a hardcoded
100,000,000 minted to the deploying key.** `MatrixToken` is the standalone
mirror and is backed by NOTHING - only `WrappedMatrix` is backed by escrowed
native - while this project describes its asset as 1:1 backed. A default that
hands the deployer a hundred million unbacked tokens named MATRIX is a thing
nobody asked for and everybody would ask about. Zero is also the honest genesis
for a mirror: the only supply that should exist is the supply something was
locked for. Minting a real float on mainnet or sepolia still works, but it has
to be typed by someone who meant it, and the script prints a warning saying the
tokens exist against nothing. On a local chain, with none set, it
uses a published 2-of-3 development key set and says so. On `mainnet` or
`sepolia` it **requires** `MINTERS` and refuses any threshold below 2 or a set
smaller than 2: a 1-of-1 set satisfies the contract's m-of-n code and is exactly
the single minting key the design removed.

`scripts/deploy-bridge.ts` reads the attestor set from the `ATTESTORS` env var
(comma-separated `0x` addresses) and the mint threshold from `THRESHOLD`
(default `2`); with neither set it uses the first local hardhat accounts. It
reads an optional deployer key from `PRIVATE_KEY` (wired in `hardhat.config.ts`)
for real networks and falls back to hardhat's funded local accounts. **No real
private keys or secrets are committed to this repository.**

## End-to-end flow (Go attestation -> Solidity mint)

`test/BridgeE2E.test.ts` drives the full flow and is part of `npx hardhat test`:

1. It shells out to the Go command `services/core/cmd/bridge-attest`, which
   locks native MATRIX on a throwaway ledger, produces a real validator-signed
   attestation, and prints it as JSON (attestor addresses, `lockId`, wrapped
   amount, ordered signatures).
2. It deploys `WrappedMatrix` registering those attestor addresses.
3. It re-runs the Go command bound to the **deployed** contract address and
   feeds the resulting signatures into `WrappedMatrix.mint` - proving the Go
   signatures verify on-chain unchanged.
4. It burns part of the minted supply and asserts the `Burned` event carries the
   native recipient, and that wrapped supply tracks net locked 1:1.

You can reproduce the Go side manually (local, deterministic test keys only, no
secrets):

```sh
cd services/core
go run ./cmd/bridge-attest \
  -recipient 0x70997970C51812dc3A010C7d01b50e0d17dc79C8 \
  -native 4000000000 \
  -chain-id 31337 \
  -contract 0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512 \
  -threshold 2 -validators 3
```

The `-seed` flag derives the local test attestor keys deterministically via
sha256; it is **not** a secret and must never be used for real funds.

## Mainnet Release Harness

This section is the **operator runbook** for releasing the Ethereum-side wrapped
token + bridge. **This repository never performs the real mainnet deploy and
holds no keys.** It ships a complete, runnable harness so **you** run the deploy
yourself with **your** funded key. Everything is keys-from-env; `.env` is
gitignored and only `.env.example` (placeholders) is committed.

### What actually gets deployed (native-vs-wrapped)

MATRIX is **native-first**: the canonical coin lives on the Matrix OS consensus
L1 and is the single source of truth for balances and supply. What this harness
deploys to Ethereum is the **WRAPPED ERC-20 (`WrappedMatrix` / wMATRIX) + the
validator-attestation bridge** - a 1:1 mirror backed by native MATRIX locked in
L1 escrow. You are **not** deploying "the" token; you are deploying its wrapped
Ethereum representation.

### Environment variables

Copy `.env.example` to `.env` and fill in your values (see that file for the
full annotated list):

| Var                   | Purpose                                              | Required for            |
| --------------------- | ---------------------------------------------------- | ----------------------- |
| `MAINNET_RPC_URL`     | Ethereum mainnet RPC endpoint                        | mainnet deploy/verify   |
| `SEPOLIA_RPC_URL`     | Sepolia testnet RPC endpoint                         | sepolia dress rehearsal |
| `MAINNET_FORK_RPC_URL`| Mainnet RPC used to fork state for the local sim     | fork simulation         |
| `PRIVATE_KEY`         | Deployer key (hex, `0x`). Env only - never hardcoded | mainnet/sepolia deploy  |
| `ETHERSCAN_API_KEY`   | Etherscan source verification                        | verify                  |
| `ATTESTORS`           | Comma-separated secp256k1 attestor addresses (`n`)   | mainnet/sepolia deploy  |
| `THRESHOLD`           | Distinct signatures required to mint (`m`), 1..n     | deploy                  |

Real-network RPC URLs and the deployer key are read from env only and default to
undefined/empty when unset. Nothing credential-bearing is hardcoded in
`hardhat.config.ts`.

### 1. Local dry-run (no secrets, always runnable)

The dry-run simulation lives in `test/DeployHarness.test.ts`. It imports and
runs the **real** deploy function (`deployWrappedMatrix` from
`scripts/deploy-mainnet.ts`), asserts the deployed token's
name/symbol/decimals/attestorCount/threshold/`ERC20_PER_NATIVE_UNIT`/initial
supply, drives a representative attestation-mint path, and reports the
deployment gas.

```sh
cd contracts
npx hardhat compile
npx hardhat test            # includes the DeployHarness dry-run (must be green)
```

### 2. Mainnet-fork simulation (optional, against real chain state)

Set `MAINNET_FORK_RPC_URL` to a mainnet RPC; `hardhat.config.ts` then forks
mainnet state for the in-process `hardhat` network, so the same harness runs
against real chain state **without broadcasting anything**:

```sh
cd contracts
MAINNET_FORK_RPC_URL="https://your-mainnet-rpc" npx hardhat test
```

### 3. Pre-deploy SAFETY + GAS checklist

Do **all** of these before a real deploy:

- [ ] **Fund the deployer.** The `PRIVATE_KEY` account has enough ETH for the
      deploy gas (see the gas the dry-run reports) plus margin.
- [ ] **Confirm the attestor set.** `ATTESTORS` exactly matches the secp256k1
      attestor keys the Go validators sign with (`internal/bridge`). A mismatch
      means Go-produced attestations will never verify on-chain.
- [ ] **Confirm the threshold.** `THRESHOLD` is the intended m-of-n (1..n).
- [ ] **Verify gas price.** Check current mainnet base fee; do not deploy into a
      fee spike unless intended.
- [ ] **Double-check the network.** `--network mainnet` vs `sepolia` is correct.
- [ ] **Dry-run passed.** `npx hardhat test` (and, ideally, the mainnet-fork
      simulation) is green on the exact commit you are deploying.
- [ ] **No secrets committed.** `git check-ignore .env` succeeds; no key or
      credential-bearing URL is in tracked files.

### 4. Go-live (run by YOU, with YOUR key)

The harness refuses unsafe real-network runs: it errors if `PRIVATE_KEY` is
unset or if `ATTESTORS` is not explicitly provided on mainnet/sepolia (it will
**not** silently fall back to local hardhat accounts on a real chain).

Dress-rehearse on Sepolia first:

```sh
cd contracts
PRIVATE_KEY=... ATTESTORS=0x...,0x...,0x... THRESHOLD=2 \
  npx hardhat run scripts/deploy-mainnet.ts --network sepolia
```

Then mainnet:

```sh
cd contracts
PRIVATE_KEY=... ATTESTORS=0x...,0x...,0x... THRESHOLD=2 \
  npx hardhat run scripts/deploy-mainnet.ts --network mainnet
```

`deploy-mainnet.ts` prints the deployed address, constructor args, attestor set,
threshold, chainId, block, and gas used, and writes a JSON deployment record to
the gitignored `deployments/wrapped-matrix.<network>.json`.

### 5. Verify on Etherscan

Using the recorded deployment (reads `ETHERSCAN_API_KEY` from env):

```sh
cd contracts
ETHERSCAN_API_KEY=... npx hardhat run scripts/verify-mainnet.ts --network mainnet
```

Or directly with the recorded constructor args:

```sh
npx hardhat verify --network mainnet <address> '["0x..","0x.."]' <threshold>
```

### 6. Post-deploy

Register the deployed contract address with the Go bridge (the `bridge.contract`
and `bridge.chain_id` node config keys) and confirm the attestor addresses match,
so Go-produced attestations mint on-chain. Then enable `bridge.watch` with the
deployment block as `start_block` so burns unlock automatically - on **every
validator**, not on one relayer node, because on a set the release needs a quorum
of attestations and a single watching node produces one. See
[Running the burn watcher in `matrixd`](#running-the-burn-watcher-in-matrixd).
The wrapped supply must always equal the outstanding native locked in L1 escrow;
`Bridge.Reconcile` on the node checks that on the native side.

> **Reminder:** this repository holds no private keys and never executes a real
> mainnet deploy. The commands above are run by the operator with their own key.

## Toolchain notes

- Hardhat v2 (`hardhat@^2.22`) with `@nomicfoundation/hardhat-toolbox@^5` and
  `@openzeppelin/contracts@^5`.
- Solidity `0.8.24`, optimizer enabled (200 runs), EVM target `cancun`
  (OpenZeppelin v5.x uses the `mcopy` opcode). `solc` is downloaded by Hardhat.
