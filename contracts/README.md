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

- **`WrappedMatrix.sol` (wMATRIX)** — the bridge-backed wrapped token. Its
  supply is minted/burned **only** through the lock-and-mint bridge, so it is
  always backed 1:1 by locked native MATRIX. This is the real bridge token.
- **`MatrixToken.sol` (MATRIX)** — the original standalone ERC-20 mirror kept
  for its existing deploy/test surface and EIP-2612 `permit` support. It is an
  owner-minted mirror; prefer `WrappedMatrix` for the bridge.

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
  nativeRecipient)`, which burns the wMATRIX and emits `Burned`. The Go bridge
  applies that event exactly once (replay-protected) and releases the escrowed
  native MATRIX to `nativeRecipient`.

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
   feeds the resulting signatures into `WrappedMatrix.mint` — proving the Go
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

## Toolchain notes

- Hardhat v2 (`hardhat@^2.22`) with `@nomicfoundation/hardhat-toolbox@^5` and
  `@openzeppelin/contracts@^5`.
- Solidity `0.8.24`, optimizer enabled (200 runs), EVM target `cancun`
  (OpenZeppelin v5.x uses the `mcopy` opcode). `solc` is downloaded by Hardhat.
