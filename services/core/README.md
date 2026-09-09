# Matrix Core

Matrix Core is the Go peer-to-peer daemon (`matrixd`) for Matrix OS. It hosts the native MATRIX ledger, dynamic validator consensus, compute and inference settlement, the native side of the wMATRIX bridge, and operator APIs.

For the Base launch, follow [`docs/runbooks/base-launch.md`](../../docs/runbooks/base-launch.md). The merge-only config example is [`configs/base-launch.overlay.yaml.example`](configs/base-launch.overlay.yaml.example).

## Architecture

| Component | Package | Responsibility |
| --- | --- | --- |
| Node lifecycle/config | `internal/node` | Configuration, startup, shutdown, APIs, and subsystem wiring |
| Global consensus | `internal/consensus` | Fast leader-based BFT, dynamic membership, bonded stake, fees, and deterministic settlement |
| Native token | `internal/token` | Accounts, supply/genesis rules, signing, and transaction history |
| Marketplace | `internal/market` | Provider capacity, jobs, and native MATRIX balances |
| Bridge | `internal/bridge` | Native escrow, lock attestations, burn decoding, and reconciliation |
| Inference | `internal/inference` | Local or OpenAI-compatible backends and consensus-settled usage |
| P2P | `internal/p2p`, `internal/transport` | libp2p identity, discovery, gossip, and peer scoring |
| Storage | `internal/kv` | Pebble persistence |
| Browser/CLI APIs | `internal/connectapi`, `internal/marketapi`, `internal/inferenceapi` | Authenticated and self-authorized public interfaces |

## Consensus and membership

Native MATRIX settlement is always consensus-ordered, including on a singleton. A singleton is a one-validator consensus set, not a direct-ledger bypass. Leaders rotate by height and round; blocks commit after voting power strictly exceeds two thirds. The committed hash-linked log is applied deterministically to every node's ledger.

The generated launch policy uses `bonded-open` membership. Validator membership is dynamic chain state: self-signed admission/exit and proven-evidence ejection take effect at epoch boundaries. Stake determines voting power and an admitted validator must meet the configured minimum bond.

### Native validators are not EVM attestors

The native validator set and a `WrappedMatrix` mint-attestor committee are separate security domains:

- Native consensus identities are ed25519 and membership changes over time.
- Each EVM attestor uses a separate secp256k1 keystore. Its address is fixed in one `WrappedMatrix` constructor.
- Validator join/ejection does not rotate EVM attestors. A removed native validator can remain an authorized EVM attestor.
- Changing attestors or threshold requires a new contract deployment and migration.

Production `WrappedMatrix` requires at least 2 attestors and threshold strictly greater than two thirds; the contract deployment script enforces `3 * threshold > 2 * attestor_count`.

The in-node burn watcher never directly releases escrow. Every configured `matrixd` bridge, singleton included, submits burn observations into native consensus. Run the watcher on every validator. It reads Base logs but is not an EVM transaction relayer.

## Safe configuration workflow

Generate the baseline first:

```sh
cd services/core
go build ./cmd/...
./matrixd -init -config /absolute/path/to/config.yaml
```

`matrixd -init` writes a `0600` file and generates a random admin API key. Its current safe/launch-aware defaults include:

- local-only P2P listen address and exact local Console origins;
- ACLs enabled, public reads and signed writes disabled until intentionally exposed;
- positive HTTP limits: 600 requests/minute and burst 120;
- bonded-open membership, participation enabled, stake enabled, minimum/target bond `1000000000000000`, and a 1000-block unbonding period;
- 100-basis-point protocol fee;
- no invented maintainer account and no maintainer share;
- provider emission `0` with an empty approved-provider list;
- full native cap in the genesis reward pool with no named allocations;
- bridge disabled because no deployment facts can be generated safely.

For Base launch, merge the leaf values in [`configs/base-launch.overlay.yaml.example`](configs/base-launch.overlay.yaml.example) into that generated config. The example is **not standalone**, and `matrixd` does not merge it automatically. Preserve the generated API key and unrelated sections; resolve every placeholder; use one exact production origin rather than `*`; and review the complete resulting config on every node.

The launch overlay sets Base chain ID `8453`, exact origin, `public_reads: true`, `signed_writes: true`, positive rate limits, bonded-open stake, fee `100`, an unresolved explicit maintainer account, maintainer share `5000` basis points of the fee, and emission `0`. A 5000-basis-point share is 50% of the 1% fee, so the maintainer receives at most 0.5% of ordinary transferred value.

For Base Sepolia rehearsal, use a separately resolved copy with chain ID `84532`, rehearsal contract/RPC/deployment block, and rehearsal keystores. Never copy rehearsal addresses or secrets into production.

### Genesis caution

Genesis is applied once and persisted. Every node must begin with the same allocations and reward pool. If a 50,000,000-MATRIX founder-backing account is funded at genesis, add exactly `50000000000000000` native base units to the real account and reduce the generated reward pool by the same amount, to `950000000000000000`. Do this before first start. On an existing network use a consensus-settled transfer; editing genesis YAML afterward has no effect.

## Base bridge operator facts

- Base Sepolia chain ID: `84532`; Base production: `8453`.
- Minimum lock: 100 MATRIX = `100000000000` native base units.
- Launch cap: `60000000000000000000000000` wrapped base units = 60M wMATRIX = 6%.
- Founder backing: exactly `50000000000000000` native base units before minting `50000000000000000000000000` wrapped base units to the immutable vault.
- Every user pays their own Base ETH gas; there is no relayer.
- A DEX price is a market price, not a stablecoin peg.

Generate one attestor keystore per validator:

```sh
matrix bridge attestor-new --out /absolute/path/to/attestor.json
```

Point `bridge.attestor_keystore` at the file and provide `MATRIX_ATTESTOR_PASSPHRASE` in the environment. The passphrase must not be stored in YAML. The node refuses to start if a configured keystore cannot be unlocked.

## Getting started

Generated Protocol Buffer stubs are gitignored and must be generated before building:

```sh
cd ../../proto
buf generate
cd ../services/core
go build ./cmd/...
go test ./...
```

Initialize and run:

```sh
./matrixd -init -config ./config.yaml
./matrixd -config ./config.yaml
```

The daemon prints its stable native consensus identity and libp2p addresses. In a multi-node network, `consensus.validators` must contain the identical **genesis** validator IDs on every node; a joining node replays membership changes from genesis. `genesis` must also match every node.

## Public HTTP surface

A public browser deployment deliberately opts into:

```yaml
connect:
  allowed_origins:
    - "https://<exact-production-origin-host>"
  public_reads: true
  signed_writes: true
  rate_limit_per_minute: 600
  rate_limit_burst: 120
```

Never use `*` for a production origin. Public reads open only read methods. Signed writes require client signatures and make the run-authorization checks mandatory. Positive rate limits bound each caller. Keep ACL-protected administrative methods separate.

## Build and test

From the repository root:

```sh
make proto
make build
make test
```

For focused core work:

```sh
cd services/core
go test ./...
```

## License

MIT — see the repository [`LICENSE`](../../LICENSE).
