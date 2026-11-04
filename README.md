# Matrix OS

Matrix OS is a peer-to-peer marketplace for compute and LLM inference that you run yourself.
Machines announce spare capacity over libp2p, buyers pay for jobs and model responses with
signed transfers, and every balance is settled in native MATRIX on a leader-based BFT
Layer 1.

There is no public network. Start one on your own machine in one command, then add machines
when you want more than one.

- Website and docs: [its-janghoon.github.io/matrix-os](https://its-janghoon.github.io/matrix-os/)
- Releases: [GitHub Releases](https://github.com/its-janghoon/matrix-os/releases)

![Matrix OS home](docs/screenshots/web-home.png)

## Start a network

You need Go, [`buf`](https://buf.build/docs/installation) and `make`.

```sh
git clone https://github.com/its-janghoon/matrix-os
cd matrix-os
make devnet-keep
```

`make devnet-keep` starts three validators on this machine and leaves them running, so a
wallet, the console or the website's `/market` page can point at them. Three is the smallest
quorum that means anything. `make devnet` does the same, checks leader rotation, quorum, the
state root and a wallet round trip, prints a verdict, and stops.

To run real nodes on separate machines:

```sh
cd services/core
go build ./cmd/...
./matrixd -init -config ./config.yaml   # writes a 0600 config and an admin API key
./matrixd -config ./config.yaml
```

Every node must start from the same `genesis` and the same `consensus.validators` list. The
[network-setup guide](https://its-janghoon.github.io/matrix-os/docs/guides/network-setup/)
walks through it, and [`services/core/README.md`](services/core/README.md) covers every
config key.

## Runbooks

| Task | Runbook |
|---|---|
| Sell GPU capacity on a running network | [gpu-provider.md](docs/runbooks/gpu-provider.md) |
| Buy compute or inference | [consumer.md](docs/runbooks/consumer.md) |
| Join the validator set with a bond | [validator-join.md](docs/runbooks/validator-join.md) |
| Upgrade validators | [validator-upgrade.md](docs/runbooks/validator-upgrade.md) |
| Turn on spend budgets | [budget-activation.md](docs/runbooks/budget-activation.md) |
| Turn on inference escrow | [escrow-activation.md](docs/runbooks/escrow-activation.md) |
| Bridge to Base as wMATRIX | [base-launch.md](docs/runbooks/base-launch.md), rehearsed first with [sepolia-rehearsal.md](docs/runbooks/sepolia-rehearsal.md) |

## What is in this repository

| Path | What it is |
|---|---|
| [`services/core`](services/core/README.md) | `matrixd`, the Go node: P2P, consensus, ledger, market, inference |
| [`proto`](proto/README.md) | Protocol Buffers shared across the network |
| [`apps/console`](apps/console/README.md) | Matrix Console, a Tauri desktop app for a local node |
| [`apps/web`](apps/web) | The website, a static Next.js export on GitHub Pages |
| [`contracts`](contracts/README.md) | wMATRIX, the escrow-backed ERC-20 mirror on Base |
| [`packages/protocol`](packages/protocol) | The one TypeScript copy of the byte layouts a client signs |
| [`packages/sdk`](packages/sdk) | TypeScript client SDK |

A few facts worth knowing before you change anything:

- Native MATRIX is capped at 1,000,000,000 whole tokens at 9 decimals. wMATRIX on Base is a
  mirror for exchanges, not the settlement token.
- Ethereum wallets are first-class clients: secp256k1 keys, EIP-155 and EIP-1559 envelopes,
  EIP-55 addresses and the JSON-RPC a wallet needs. The chain runs no EVM, and contract calls
  are refused rather than ignored.
- Every block commits to a Merkle root over all balances, so a node with different apply logic
  refuses to vote, and one balance can be proven to someone holding nothing else.
- An idle chain produces no blocks. Check that validators agree on the head hash and state root
  at a named height, not that the height moved.

## Build

```sh
make build       # proto + Go
make test
cd apps/web && corepack yarn install --frozen-lockfile && corepack yarn build
cd apps/console && corepack yarn install && corepack yarn build
cd contracts && npm install && npx hardhat test
```

## Contributing

Branches are `<type>/<slug>`, pull requests fill in the four-section template, and the author
reviews their own pull request before asking for review. The rules are in
[CONTRIBUTING.md](https://github.com/its-janghoon/.github/blob/main/CONTRIBUTING.md). Report
vulnerabilities privately as described in
[SECURITY.md](https://github.com/its-janghoon/.github/blob/main/SECURITY.md).

## License

[MIT](LICENSE).
