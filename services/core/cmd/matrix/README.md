# `matrix` CLI

`matrix` is the command-line operations tool for a Matrix OS node. It drives a
running node over its gRPC market API
(`matrix.market.v1.MarketService`, served on the node's market port, default
`127.0.0.1:9091`): check node health, manage compute providers and jobs, read
balances and the token chain, and manage an ed25519 wallet to sign and submit
native MATRIX transfers.

## Install / build

From `services/core`:

```sh
go build -o matrix ./cmd/matrix
# optionally install onto your PATH
go install ./cmd/matrix
```

Run `matrix --help` to see the full command tree.

## Global flags

These persistent flags apply to every subcommand:

| Flag | Default | Description |
| --- | --- | --- |
| `--addr` | `127.0.0.1:9091` | Node market gRPC endpoint (`host:port`). This is the node's `Market.Addr`. |
| `--api-key` | _(empty)_ | API key for nodes running with ACLs. Sent as the `authorization` gRPC metadata header, which is exactly what the node's authenticator reads. Only needed when the node is started with ACLs enabled; otherwise the market API is open. |
| `--timeout` | `10s` | Per-RPC timeout. |
| `--json` | `false` | Emit machine-readable JSON instead of human-friendly tables. |

The transport is insecure (plaintext gRPC) to match the node's local market
server. Point `matrix` at a remote node with `--addr host:9091`.

## Commands

### Node status / health

```sh
matrix status          # dial the node and report serving status + endpoint
matrix health          # gRPC health Check -> SERVING / NOT_SERVING
matrix --json health
```

### Providers

```sh
# Advertise local compute capacity on the order book.
matrix provider register --id provider-1 --capacity 100 --price 5

# List providers (add --include-remote for P2P-discovered providers).
matrix provider list
matrix provider list --include-remote --json
```

### Jobs

```sh
# Reserve provider capacity for a paid compute job (no credits move yet).
matrix job submit --buyer <account-id> --provider provider-1 --units 10

matrix job get --id <job-id>
matrix job list                     # all jobs
matrix job list --buyer <account-id>

# Settle a job (transfers credits buyer -> provider).
matrix job complete --id <job-id>

# Cancel a job (returns reserved capacity).
matrix job cancel --id <job-id>
```

### Balances

```sh
matrix balance --account <account-id>
matrix --json balance --account <account-id>
```

### Token chain / transactions

```sh
matrix tx get --height 0
matrix tx list                      # all transactions + chain length
matrix tx list --start 10 --limit 20 --json
```

### Wallet

The wallet is an ed25519 keypair stored at `~/.matrix/wallet.json` (overridable
with `--wallet <path>`) with `0600` permissions. **The private key is never
printed or logged.** An account ID is the lowercase hex encoding of the public
key.

```sh
# Generate a new wallet (refuses to overwrite an existing file).
matrix wallet create
matrix wallet create --wallet /path/to/wallet.json

# Show the account ID / public key (never the private key).
matrix wallet show

# Read the wallet's balance (or an explicit account with --account).
matrix wallet balance
matrix wallet balance --account <account-id>

# Sign and submit a native MATRIX transfer.
matrix wallet transfer --to <recipient-account-id> --amount 200
```

#### Transfer flow

`matrix wallet transfer` performs a fully client-side signed transfer:

1. Reads the current chain head hash and derives the sender's next nonce by
   inspecting the chain over gRPC (`ListTransactions`). An empty chain uses the
   genesis prev-hash (32 zero bytes) and nonce `0`.
2. Builds the canonical transaction payload
   (`from_public_key`, `to`, `amount`, `nonce`, `timestamp`, `prev_hash`) and
   signs it with the wallet's ed25519 private key. The signing is identical to
   the node's `internal/token` verification, so the signature is accepted by
   `SubmitSignedTransfer`.
3. Calls `SubmitSignedTransfer` and prints the settled transaction record.

The private key stays in the local wallet file the entire time; only the public
key, signature, and transfer fields are sent to the node.

## Authentication

When a node is started with ACLs enabled, pass the API key:

```sh
matrix --api-key "$MATRIX_API_KEY" provider list
```

The key is attached as the `authorization` gRPC metadata header. The gRPC health
check is always unauthenticated.

## Inference

The `matrix.inference.v1` InferenceService is **not yet wired into the node**
(it is not started by `cmd/matrixd`), so this CLI does not expose an `inference`
command. Inference operations remain internal until the service is served over
gRPC.

## Exit codes

`matrix` exits non-zero on error. gRPC failures are mapped to concise messages:
connection failures include a hint that the node may not be running or `--addr`
may be wrong, and authentication failures suggest setting `--api-key`.
