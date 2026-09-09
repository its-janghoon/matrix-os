# `matrix` CLI

`matrix` is the command-line operations tool for a Matrix OS node. It drives a
running node over its gRPC market API
(`matrix.market.v1.MarketService`, served on the node's market port, default
`127.0.0.1:9091`): check node health, manage compute providers and jobs, read
balances and committed consensus transfer history, operate the Base bridge, and
manage an ed25519 wallet that signs native MATRIX transfers locally.

## Install / build

From `services/core`:

```sh
go build -o matrix ./cmd/matrix
# optionally install onto your PATH
go install ./cmd/matrix
```

Run `matrix --help` to see the full command tree.

## Quickstart (zero to first job)

Go from nothing to a funded account and a first completed compute job with a
single copy-paste block. From `services/core`:

```sh
# 1. build the daemon and CLI
go build -o matrixd ./cmd/matrixd
go build -o matrix  ./cmd/matrix

# 2. start a local dev node. -init writes a secure baseline, not a production
#    launch profile: ACLs stay on, public browser access is off, no maintainer is
#    invented, provider emissions are zero, and the bridge is disabled.
#    For this local-only demo, disable ACLs; exposed nodes must keep them on.
./matrixd --init
sed -i 's/enable_acls: true/enable_acls: false/' config.yaml   # dev only
./matrixd &

# 3. in the same or another shell, run the one-command demo loop
./matrix quickstart
```

`matrix quickstart` drives the whole economic loop against the node and prints
each step and the resulting balances:

1. creates a local wallet at `~/.matrix/wallet.json` if you do not have one (the
   buyer account);
2. funds that wallet from the node's genesis reward pool via `FundAccount` (no
   coins are minted; reward-pool MATRIX is moved through the treasury);
3. registers a demo provider on the order book;
4. submits a demo job (buyer buys compute units at the provider's price);
5. completes/settles the job, moving native MATRIX buyer -> provider.

The submitted job's returned total is the reservation snapshot; quickstart prints
that server-returned value rather than recomputing it from the CLI flags.

Re-running is safe: the wallet is reused and the provider re-registered. Tune the
demo with `--fund`, `--provider`, `--price`, `--capacity`, and `--units`, or point
at a non-default node with `--addr host:9091`.

Under the hood `quickstart` calls the same RPCs as the individual commands, so
you can also run the loop by hand:

```sh
matrix fund --account <account-id> --amount 1000000
matrix provider register --id quickstart-provider --capacity 100 --price 5
matrix job submit --buyer <account-id> --provider quickstart-provider --units 10
matrix job complete --id <job-id>
matrix balance --account <account-id>
```

## Global flags

These persistent flags apply to every subcommand:

| Flag | Default | Description |
| --- | --- | --- |
| `--addr` | `127.0.0.1:9091` | Node market gRPC endpoint (`host:port`). This is the node's `Market.Addr`. |
| `--inference-addr` | `127.0.0.1:9092` | Node inference gRPC endpoint. A separate server on a separate port, so `--addr` does not cover it. Registered on the `inference` commands only. |
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
# Advertise local compute capacity with a final manual quote in native MATRIX
# base units per compute unit. This is not a USD/stablecoin price.
matrix provider register --id provider-1 --capacity 100 --price 5 \
  --models llama-3.3-70b

# Refresh before expiry without resetting capacity or active reservations.
matrix provider quote-update --id provider-1 --price 6

# List providers with fresh quotes (add --include-remote for P2P-discovered providers).
matrix provider list
matrix provider list --include-remote --json
```

A provider registration receives quote identity, observation and expiry metadata
inside the marketplace. Use `provider quote-update`—not `provider register`—as
the refresh path for an existing provider. It atomically preserves total and
available capacity, active reservations, and advertised models while advancing
the quote for future reservations. Run or schedule it before the quote expires;
expired local offers are omitted from buyer-facing lists until refreshed.
Provider commands print quote metadata (and include it in `--json`). Job output
prints the immutable unit price, quote ID/version, observed time, and valid-until
time snapshotted at reservation.

### Jobs

```sh
# Reserve provider capacity for a paid compute job (no MATRIX moves yet).
matrix job submit --buyer <account-id> --provider provider-1 --units 10

matrix job get --id <job-id>
matrix job list                     # all jobs
matrix job list --buyer <account-id>

# Settle a job (moves native MATRIX buyer -> provider).
matrix job complete --id <job-id>

# Cancel a job (returns reserved capacity).
matrix job cancel --id <job-id>
```

`job complete` settles through consensus: the payment is a transfer SIGNED BY
THE BUYER that a quorum commits and every node applies. So the node needs the
buyer's signing key, which it resolves from the wallet files under `~/.matrix`.
A job whose buyer the node holds no key for is refused, with the reservation
left intact so it can be cancelled:

```
Error: FailedPrecondition: settle job "98a1df95-...": node: no signing account
for buyer: "b8cba113..."
```

That refusal is the point. Settling with a direct ledger write instead would
charge an account without its owner's signature, and would move MATRIX on this
node's copy of the ledger only, where no other node would ever see them.

### Funding

```sh
# Move native MATRIX from the node's genesis reward pool into an account so it
# has spendable balance for paid jobs. No coins are minted; the supply cap is
# respected. Prints the account's new balance.
matrix fund --account <account-id> --amount 1000000
```

### Balances

```sh
matrix balance --account <account-id>
matrix --json balance --account <account-id>
```

### Transactions (consensus transfer history)

Value transfers settle through consensus, so `tx list`/`tx get` read the
consensus transaction history: the ordered sequence of committed value transfers
(the same on every node). A transfer is addressed by its stable `--index`, not a
per-node chain height.

```sh
matrix tx get --index 0
matrix tx list                      # all committed transfers + total
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

`matrix wallet transfer` performs a fully client-side signed transfer that
settles through consensus:

1. Derives the sender's next transfer nonce by inspecting the consensus
   transaction history over gRPC (`ListTransactions`). The nonce is a per-sender
   uniquifier; consensus provides replay protection through its
   committed-transaction dedup set, so an empty history uses nonce `0`.
2. Builds the canonical transaction payload
   (`from_public_key`, `to`, `amount`, `nonce`, `timestamp`, `prev_hash`) and
   signs it with the wallet's ed25519 private key. `prev_hash` is a zero seed
   (it is unused for consensus ledger linkage). The signing is identical to the
   node's `internal/token` verification, so the signature is accepted by
   `SubmitSignedTransfer`.
3. Calls `SubmitSignedTransfer`, which submits the signed transfer into
   consensus; a quorum orders it into a committed block and every node applies it
   to the shared ledger, so all nodes agree on the resulting balances and the
   transfer pays the protocol fee (when configured) like every other committed
   transfer. The CLI prints the settled transfer once it has committed.

The private key stays in the local wallet file the entire time; only the public
key, signature, and transfer fields are sent to the node.

## Base bridge

The bridge CLI covers setup, native lock, fixed-committee attestations, and
reconciliation. Base Sepolia rehearsal uses chain ID `84532`; Base production
uses `8453`. These commands do not claim a public deployment exists: verify the
exact WrappedMatrix address and code first.

```sh
# One-time, only for an operator assigned to the fixed EVM attestor committee.
matrix bridge attestor-new --out ~/.matrix/bridge-attestor.json

# Lock the minimum 100 MATRIX (100000000000 native base units) or more.
matrix bridge lock --wallet ~/.matrix/wallet.json \
  --to 0x<base-recipient> --amount 100000000000

# Query endpoints serving distinct registered contract attestors. JSON goes to
# stdout; progress goes to stderr, so redirection remains machine-readable.
matrix bridge attestation --lock-id 0x<lock-id> \
  --validator validator-1.example.org:9091 \
  --validator validator-2.example.org:9091 > attestations.json

# Compare node escrow accounting with WrappedMatrix.totalSupply().
matrix bridge reconcile
```

`attestor-new` creates a secp256k1 key for the immutable committee chosen when
WrappedMatrix was deployed. That EVM committee is separate from the dynamic
native bonded-open validator set; native admission or exit never updates the
contract. The user submits the Base mint/burn/approval/swap and pays Base gas
from their wallet. There is no gas relayer.

Burn release is always ordered through native consensus and is applied exactly
once. The production WrappedMatrix mint ceiling is 6% of native maximum supply,
but each minted unit still requires matching escrow. The 1:1 relationship is
MATRIX-to-wMATRIX backing, not a USD, USDC, or stablecoin peg.

## Authentication

When a node is started with ACLs enabled, pass the API key:

```sh
matrix --api-key "$MATRIX_API_KEY" provider list
```

The key is attached as the `authorization` gRPC metadata header. The gRPC health
check is always unauthenticated.

## Inference

`matrixd` serves `matrix.inference.v1.InferenceService` on its own port
(`inference.addr`, default `0.0.0.0:9092`), and the CLI drives it:

```sh
matrix inference submit \
  --buyer <account-id> \
  --provider demo-inference-provider \
  --model demo \
  --prompt "one sentence about peer-to-peer compute"

matrix inference get --id <job-id>
```

```
id:         13ac1c62-5013-4c31-8cbd-c5f3b7848d23
buyer:      c5b097824f2e2222a278af3dbe4b519fa653a96e44ff08891668b3a2a708d5d9
provider:   demo-inference-provider
model:      demo
status:     completed
units:      5
completion: echo: user: one sentence about peer-to-peer compute
```

`--fulfill` defaults to true, so the job is reserved, run and settled in one
call; `--fulfill=false` reserves only. `demo-inference-provider` is the GPU-free
echo backend a freshly initialized node registers (`inference.echo_provider`),
both in the inference registry and on the market, so this works before you have
a model server.

Two things follow from an inference job being a compute job with a prompt
attached:

- It reserves capacity from a **registered market provider** and settles at that
  provider's price. `--provider` must name one; `matrix provider list` shows
  which.
- Settlement goes through consensus and needs the **buyer's signing key**. The
  node resolves one from the wallet files under `~/.matrix`, so a wallet created
  with `matrix wallet create` works and an arbitrary account id does not. A
  multi-tenant deployment supplies its own custodial resolver instead.

These commands talk to the inference port, not the market port, so `--addr`
alone is not enough for a remote node - use `--inference-addr` as well.

## Exit codes

`matrix` exits non-zero on error. gRPC failures are mapped to concise messages:
connection failures include a hint that the node may not be running or `--addr`
may be wrong, and authentication failures suggest setting `--api-key`.
