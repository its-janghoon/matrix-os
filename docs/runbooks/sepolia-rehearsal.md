# Legacy Ethereum Sepolia rehearsal

> **Legacy compatibility guide.** The authoritative launch policy is Base Sepolia
> (`84532`) followed by Base production (`8453`), documented in
> [`base-launch.md`](base-launch.md). Current cap, founder vesting, fee,
> maintainer, emission, bonded-open stake, public endpoint, and evidence policy
> come only from that runbook. This file remains for operators maintaining the
> legacy Ethereum Sepolia path.

This is an Ethereum Sepolia rehearsal for the lock-and-mint bridge on a chain you do not
control. It goes from nothing to: native MATRIX escrowed on your own L1, wMATRIX
minted on Sepolia against it, the two reconciled, and the wrapped tokens burned
back.

Everything here has been run locally against a real `matrixd` and a real EVM
chain. What has NOT been run is Sepolia itself, because that needs a funded key
and an RPC endpoint, which are yours and not the repository's.

---

## "Can I just use my MetaMask account that has test ETH?"

Yes for one job, and no for the other two. The distinction is worth getting
right, because it is the difference between a key you can throw away and a key
you cannot.

**Use MetaMask as the wMATRIX holder.** Name your MetaMask address as `--to` on
`matrix bridge lock`. The wrapped tokens mint straight into it, you can watch
them arrive in the wallet UI, and you burn them from there at the end. This is
the one role that genuinely wants a wallet you control interactively.

**Do not export your MetaMask private key into `PRIVATE_KEY`.** Two steps need a
raw key in an environment variable - deploying the contract, and broadcasting
the mint - and a secp256k1 key is not testnet-scoped. The same key controls the
same address on Sepolia, on mainnet, and on every EVM L2. Putting it in an env
var puts it in `/proc/<pid>/environ`, in the environment of every child process
this repo spawns, and inside the dependency tree `npx hardhat` executes. There
is no rotation: the only remedy is abandoning the address.

**And you gain nothing by using a real key, because the deployer has no
authority.** `WrappedMatrix` has no `Ownable`, no owner, no admin, and an
immutable mint cap. `mint` is `external` with no access control at all, and the
recipient is fixed inside the signed digest, so whoever broadcasts it cannot
redirect a single token. The deploy key pays gas and then matters to nobody.

So: generate a throwaway, fund it from MetaMask, and keep MetaMask as the
recipient.

```sh
cd contracts
node -e 'console.log(require("ethers").Wallet.createRandom().privateKey)'
```

Faucet Sepolia ETH into your MetaMask address first - many faucets gate on the
receiving address having some mainnet history, so a brand-new throwaway is more
likely to be refused than an address you have used - then send the throwaway
~0.05 sepETH from MetaMask.

---

## Which keys exist, and which need ETH

| Key | What it is | Needs Sepolia ETH? |
| --- | --- | --- |
| Deployer | `PRIVATE_KEY`, secp256k1 EOA | **Yes** - about 1.5M gas for the deploy |
| Mint broadcaster | the same `PRIVATE_KEY` unless you swap it | **Yes** - about 102k gas for the first mint, ~85k after |
| wMATRIX holder / burner | the `--to` address; **your MetaMask** | **Yes** - about 37k gas to burn |
| Attestor (per validator) | secp256k1, encrypted keystore on the node | **No. Never.** It only signs digests off-chain |
| Matrix native wallet | ed25519, `~/.matrix/wallet.json` | **No** - it never touches Ethereum |
| Node admin API key | `--api-key` on the CLI | **No** - not a chain key at all |

The attestor row is the one that surprises people. Grep the Go tree for
`SendTransaction`, `bind.NewKeyedTransactor` or an `ethclient` and you will find
nothing: the node's only Ethereum client speaks `eth_blockNumber` and
`eth_getLogs`, both reads. The attestor key's entire job is
`SignDigest` over 32 bytes. It is also, on a threshold bridge, the key that
actually matters - so it lives in a scrypt + AES-256-GCM keystore, while the
deploy key lives in an env var for an afternoon.

---

## Before you start

- Node 20+ and Go 1.25 (`npx hardhat --version`, `go version`).
- A Sepolia RPC endpoint (Alchemy, Infura, or your own node).
- An Etherscan API key, if you want source verification.
- `matrixd` and `matrix` built: `cd services/core && go build ./cmd/...`

---

## 1. Configure the environment

`contracts/hardhat.config.ts` loads `.env`, so put your values there rather than
inline on the command line - an inline `PRIVATE_KEY=0x... npx hardhat run ...`
lands in your shell history verbatim.

```sh
cd contracts
cp .env.example .env
$EDITOR .env
```

Set at minimum:

```
SEPOLIA_RPC_URL=https://eth-sepolia.g.alchemy.com/v2/<your key>
PRIVATE_KEY=0x<the throwaway key, 64 hex characters>
ETHERSCAN_API_KEY=<optional, for verification>
```

`.env` is gitignored. Check it: `git check-ignore contracts/.env` should
succeed.

The `sepolia` network declares `chainId: 11155111`, so if `SEPOLIA_RPC_URL`
actually points at another chain, hardhat refuses before signing anything. That
is the guard against a mainnet URL in the wrong variable.

## 2. Generate the attestor keys

One per validator. Each node holds exactly one and never shares it.

```sh
export MATRIX_ATTESTOR_PASSPHRASE='<a real passphrase>'
matrix bridge attestor-new --out ~/.matrix/attestor-1.json
```

It prints an ADDRESS. Collect one per validator; that list is `ATTESTORS` at
deploy time, and it is **immutable** - changing the set later is a redeploy.

`--out` is required and the command refuses to overwrite an existing file, which
is deliberate: silently replacing an attestor key strands every lock that
validator already attested to.

This immutable EVM committee is separate from dynamic native validator
membership. A native validator join, exit, or ejection does not add, remove, or
rotate an attestor; a removed validator can remain an attestor for this contract.
Changing the committee requires a new `WrappedMatrix` deployment and migration.

## 3. Deploy WrappedMatrix

```sh
cd contracts
ATTESTORS=0xAttestor1,0xAttestor2,0xAttestor3 THRESHOLD=3 \
  MINT_CAP=60000000000000000000000000 npm run deploy:bridge:sepolia
```

The script refuses, on a real network, to invent an attestor set from local
accounts, and refuses a single-attestor deployment unless you say
`ALLOW_SINGLE_ATTESTOR=1` - which is a legitimate thing to say for a throwaway
rehearsal, and a terrible thing to say for anything else. A 1-of-1 bridge is one
key able to mint the entire cap.

It writes a deployment record under `contracts/deployments/` and prints the
verify command. Note the address.

## 4. Verify on Etherscan (optional)

```sh
npm run verify:sepolia
```

It reads the deployment record and submits the CONSTRUCTOR ARGUMENTS, which are
not the same as the contract's resolved values: an unset `MINT_CAP` deploys with
`0` and the contract substitutes its default internally. Submitting the resolved
default for a deploy that passed `0` fails every time.

## 5. Point the node at it

In the node's config:

```yaml
bridge:
  contract: "0x<the WrappedMatrix address>"
  chain_id: 11155111
  attestor_keystore: "/home/you/.matrix/attestor-1.json"
  watch:
    enabled: true
    rpc_url: "https://eth-sepolia.g.alchemy.com/v2/<your key>"
    start_block: <the deploy block>
    confirmations: 12
    poll_interval: 12s
    max_block_span: 2000
```

`poll_interval` must be a duration STRING (`12s`). A bare `12` fails to parse
and the node will not start.

`start_block` at the deploy block, not 0: from genesis the watcher scans the
whole chain before it sees anything.

Then start it with the passphrase in the environment:

```sh
MATRIX_ATTESTOR_PASSPHRASE='<the same passphrase>' matrixd -config ./node.yaml
```

A configured key that will not unlock is a startup FAILURE, deliberately: a
validator that looks like it is attesting and is not means mints silently stop
reaching quorum, with nothing pointing at the node responsible.

The node prints `Bridge: attesting as 0x...`. That address must be in the set
you deployed with. It also prints the endpoint it polls with the credential
redacted, so the node log is not a place your RPC key ends up.

## 6. Get native MATRIX into an account

On a **single-node** rehearsal:

```sh
matrix fund --account <account id> --amount 100000000000 --api-key <key>
```

On a **multi-validator** rehearsal this is refused, and correctly: reward-pool
funding is not consensus-ordered, so it would diverge the validators' pools.
Put the allocation in every node's `genesis.allocations` instead, before first
start.

## 7. Lock

```sh
matrix bridge lock --to 0x<your MetaMask address> --amount 100000000000 \
  --api-key <key>
```

Amounts are native base units, 9 decimals. The example locks the current
minimum: `100000000000` base units = 100 MATRIX. It prints the LOCK ID.

Escrow receives the full amount - a lock pays no protocol fee, because the
wrapped supply minted against it is computed from what was locked, and a fee
would mint more wrapped than the escrow holds.

## 8. Collect the attestations

```sh
matrix bridge attestation --lock-id 0x<lock id> \
  --validator node1:9091 --validator node2:9091 --validator node3:9091 \
  --api-key <key> > atts.json
```

One signature per validator; each holds its own key and the contract counts the
threshold. There is no gossip of partial signatures, deliberately - that would
be a second consensus for something the contract already checks.

The command refuses a set that cannot mint: validators disagreeing about the
lock (they are not on the same chain), or the same attestor answering twice
(the contract counts DISTINCT signers, so that is one signature dressed as two).

## 9. Mint

```sh
cd contracts
CONTRACT=0x<WrappedMatrix> ATTESTATIONS=../atts.json npm run mint:sepolia
```

It checks every recovered signer against the contract's registered set, sorts
the signatures by ascending signer - the contract requires that ordering to
count distinct signers, so an m-of-n mint submitted in the order the nodes
answered reverts `InvalidSignature` - and simulates the call before
broadcasting. A revert therefore costs no gas.

Check MetaMask. Add the token by its contract address if it does not appear.

## 10. Reconcile

```sh
matrix bridge reconcile --api-key <key>
```

Compare the `Outstanding (erc20)` figure against `totalSupply()` on Etherscan.
They must be equal. Escrow may lead the supply by a lock whose mint has not been
broadcast; it must never lag it.

## 11. Burn back

From MetaMask, or Etherscan's Write Contract tab, call
`burn(amount, nativeRecipient)` where `nativeRecipient` is your Matrix account
id. The amount must be an exact multiple of 1e9.

The node's watcher sees the `Burned` event after `confirmations` blocks and
releases the escrow. Re-run `matrix bridge reconcile`: outstanding should be
back to zero.

---

## What a testnet cannot rehearse

- **A singleton still exercises consensus ordering.** Every bridge configured
  inside `matrixd`, including a singleton, submits burn observations through
  consensus. A singleton has a one-validator quorum; it does not use the direct
  watcher release path. Run multiple validators to rehearse multi-party quorum
  behavior, dynamic membership, and disagreement handling.
- **Multi-signature ordering,** with one attestor. The sort and the distinct
  signer rule are trivially satisfied by a set of one.
- **Mainnet gas prices.** Gas UNITS transfer exactly; the ETH cost does not.
- **Adversaries.** Nothing on Sepolia is worth stealing, so nobody probes the
  attestor set or your escrow accounting. Thirty quiet testnet days are weak
  evidence about a chain with money on it.
- **The discipline of irreversibility.** Attestors, threshold and cap cannot be
  changed after deploy. On Sepolia a redeploy is free, so the rehearsal does not
  test getting them right the first time. Write them down and review them as if
  it were the real one.
- **A key ceremony.** One person generating n attestor keys on one laptop is not
  a ceremony, it is one machine holding the whole threshold. The rehearsal
  proves the mechanics, not the custody.

## When it goes wrong

| Symptom | Cause |
| --- | --- |
| `refusing to deploy ... ATTESTORS is not set` | Set it. There is no local fallback on a real network: it would register the deploy key as the sole minting authority. |
| `refusing to deploy ... with a single attestor` | Give two or more, or set `ALLOW_SINGLE_ATTESTOR=1` and treat the deployment as disposable. |
| Node refuses to start: `wrong passphrase, or the keystore is corrupt` | `MATRIX_ATTESTOR_PASSPHRASE` does not match what the keystore was created with. The key cannot be recovered; generate a new one and redeploy. |
| `signer 0x... is not in the contract's attestor set` | Either that address was never registered, or the node's `chain_id` / `contract` does not match this deployment - the digest binds both, so a mismatch recovers to a different address entirely. |
| Mint reverts `ThresholdNotMet` | Fewer distinct registered signers than the threshold. Collect more, and check no two nodes share a keystore. |
| Mint reverts `LockAlreadyMinted` | That lock already minted. Each lock mints exactly once. |
| Verification fails on constructor args | You passed the resolved cap instead of the constructor argument. `npm run verify:sepolia` gets this right; a hand-typed `hardhat verify` may not. |
| `matrix fund` returns FailedPrecondition | You have more than one validator. Use genesis allocations. |
| The node will not parse `poll_interval` | It must be a duration string, `12s`, not `12`. |
