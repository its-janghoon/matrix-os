# Relaunching the native chain on the wallet-compatible format

This runbook restarts the native L1 from a new genesis so it speaks the formats an
Ethereum wallet, explorer and exchange already understand. It is written for the
situation it was written in: a chain days old, three validators, and wrapped
tokens that exist but have barely moved.

**The wrapped token is not touched.** `WrappedMatrix` on Base has no idea the
native chain exists. Its immutable values are `mintCap`, `threshold`,
`attestorCount` and the attestor addresses, all of them EVM-side, and its mint
digest is `keccak256(recipient, amount, lockId, block.chainid, address(this))` -
Base's chain id, its own address, and an opaque `lockId`. Nothing in it names a
native genesis, height, or chain. So the contract, every wMATRIX balance, every
DEX pool and LP position, and the vesting vault all survive a native relaunch
untouched. Read that paragraph again before anyone proposes redeploying the
token: there is no reason to.

What a relaunch actually costs is the native chain's own history, the validators'
bonds, and one careful restoration of the escrow.

## Why now rather than later

Every change below breaks something that cannot be un-broken later:

| Change | What it invalidates |
| --- | --- |
| Ethereum transaction format, EIP-155 | Every signature made for the old layout |
| Addresses as an account kind | Nothing. 64-hex ids keep working alongside them |
| `chain_id` in the signature | Cross-network replay, which had nothing stopping it |
| State root in the block | Every block hash from genesis |
| Block timestamp and protocol version | Every block hash from genesis |

With few holders and little history this is a config exercise. With an exchange
listing and a year of transfers it is a migration nobody wants to run. The cost
of doing it is lowest on the first day it can be done and rises every day after.

## What has to be decided before anything is typed

**The chain id.** Any number nobody else is using. It is not earned or
registered anywhere before you use it - chainlist.org is a directory people
publish to, not an authority that grants ids - so the whole decision is "check it
is free, then never change it".

Checking is one request per candidate, against the list's own source file. 404
means nobody has registered it:

```sh
# 404 = free. 200 = taken, pick another.
curl -s -o /dev/null -w '%{http_code}\n' \
  https://raw.githubusercontent.com/ethereum-lists/chains/master/_data/chains/eip155-<candidate>.json
```

Confirm any candidate a second way before committing to it, because a check that
is silently broken looks exactly like a free id:

```sh
# Prints the name if it is taken, nothing if it is free.
curl -s https://chainid.network/chains_mini.json \
  | python3 -c 'import json,sys; print(next((c["name"] for c in json.load(sys.stdin) if c["chainId"]==<candidate>), "free"))'
```

The registry is a directory, not a gate. It lists ids people have published and
knows nothing about private or unpublished networks, so a 404 means "unclaimed
here", not "provably unused anywhere". That is the right bar for picking one, and
it is the reason to publish yours once it is live.

It goes inside every signature a wallet makes and must be identical on every
node, and changing it later invalidates every signature already made for the old
value. That is the only reason it is worth two minutes of care.

**The escrow figure.** The node already computes this - do not read two numbers
and compare them by hand:

```sh
matrix --api-key <key> bridge reconcile
```

It prints what the escrow holds and what `totalSupply()` on the Base contract
must equal if the backing is intact, and it REFUSES to return a snapshot at all
when the node's own accounting and its escrow balance disagree. An error there
is not a reporting problem: it means the two halves have diverged, and nothing
below is safe until that is understood.

Take the `Outstanding (native)` figure into the genesis file, and confirm it
against the contract once:

```sh
# Must equal the erc20 figure reconcile printed.
cast call <WrappedMatrix> "totalSupply()(uint256)" --rpc-url <base-rpc>
```

The reason to run this rather than copy the launch record is that the record is a
snapshot: every native lock since raised the escrow and minted more wMATRIX. If
nothing has been locked since launch these are the numbers already published, and
this is a minute's confirmation rather than an afternoon's arithmetic.

**Who holds what.** Old 64-hex ed25519 account ids keep working: that account
kind was never removed, its transactions still verify, and `genesis.allocations`
still accepts one. So carrying a balance over is copying the id and the amount,
not remapping anyone to anything.

A holder who WANTS a wallet can be allocated to an `eth:0x...` address instead,
and that is the only case needing a decision - it is per holder, optional, and
not a precondition for the relaunch. An address a holder cannot sign for is a
balance nobody can ever move, so take those from the holder rather than deriving
them.

## The genesis file

```yaml
consensus:
  chain_id: <the-id-you-picked>
  # Empty until a rule change is actually planned. It is the mechanism that makes
  # the NEXT change a release rather than a second relaunch: schedule an
  # activation height, roll the binary over days, and every node switches at the
  # same height because the height is agreed rather than the moment.
  protocol_upgrades: []
  validators: [<the genesis validator ids, identical on every node>]
  epoch_length: 100
  round_timeout: <the same value on every node>
  fee_basis_points: <the network's fee>

genesis:
  allocations:
    # The escrow FIRST, and matched to wrapped supply. Everything else is a
    # policy decision; this one is an accounting fact.
    - account: "bridge/escrow"
      amount: <exactly the native escrow figure read above>
    # Then the carried-over balances, each to an address its holder controls.
    - account: "eth:0x<holder-address-lowercase>"
      amount: <native base units>
  reward_pool: <the remainder, so allocations + reward_pool is unchanged>
```

`allocations` accepts an address, the escrow account, or a 64-hex id. It does not
accept any other reserved name: a bond account or a validator admission is a
consensus OPERATION, and an allocation to one would be value sent into an
operation rather than into an account.

Every node's genesis must be byte-identical in effect. A node that differs
reaches different balances from the same blocks, and the state root in the first
block refuses its vote and names both digests - which is the check that makes
this verifiable rather than a matter of trust. That is the intended failure: fix
the config, do not restart around it.

## Order of operations

1. **Freeze.** Stop accepting native transfers. Announce a window. Nothing below
   is safe while balances are still moving.
2. **Run `matrix bridge reconcile`** and record its output in the evidence file
   with the Base block `totalSupply()` was read at. An error here stops the
   relaunch until it is understood.
3. **Read the old ledger back as a genesis file.** Do this FIRST, while the
   old data directory is still in place, with the node stopped:

   ```sh
   matrixd -genesis-snapshot -config <the node's OLD config> > genesis-block.yaml
   ```

   It prints the `genesis:` block to paste, with a report above it of what it
   decided. Copying accounts by hand is the one irreversible step of a relaunch
   and the one nobody can review, so do not do it by hand.

   What it decides, and why each would otherwise be a quiet loss:

   - **The reward pool** becomes `reward_pool`, not an allocation. As an
     allocation it would be credited to the account AND counted again as the
     pool, and the supply would not close.
   - **Bonds go back to the accounts that posted them.** A bond lives in a
     reserved account genesis does not accept, so it cannot carry as itself.
     Dropped, every validator loses its stake in a file that still looks
     complete. It is returned to its owner and re-bonded in step 7.
   - **Undistributed fees go back to the reward pool.** That account holds the
     remainder of an uneven split, owed to the validator set collectively and to
     no member in particular - which is why no block has paid it out. Carrying it
     to an account would hand it to whoever was named first.
   - **The escrow is emitted first** and flagged, because it is an accounting
     fact rather than a policy choice and must match wrapped supply.

   It refuses to guess. A reserved account it does not recognise is reported
   under `NOT READY`, left out of the total, and the command exits non-zero, so
   a script cannot mistake an unfinished file for a finished one. Decide where
   that value goes and add it by hand.

   Check the supply line says it closes exactly at the cap. Short or over means a
   balance was dropped or counted twice, and production preflight refuses either.
4. **Give every node a fresh data directory and read its new identity.** This
   comes after the snapshot above, which needs the old store, and before the
   genesis file, which names these ids.

   The hosts do not need reinstalling and nothing needs uninstalling: a relaunch
   is a new binary, a new config, and an empty store. But the store is also
   where the validator keypair lives (`consensus/identity/validator_key`, beside
   the committed chain), so emptying it for the new chain discards the identity
   with it. A node started on a fresh directory generates a NEW keypair, and a
   genesis carrying the old ids would then name three accounts that never
   validate - the network comes up holding nothing, several layers from its
   cause.

   So move the old directory aside rather than deleting it (it is the archive),
   point `storage.path` at the new one, and then read each node's identity:

   ```sh
   # storage.path must already be the NEW directory. Run this against the old
   # config and the id you get back is the one you are trying to leave behind;
   # run it with no config at all and the identity is created under ./data and
   # then abandoned, because the node makes another one against its real path.
   matrixd -init-identities -config <the node's new config>
   ```

   It prints a `consensus_id` and a `peer_id`. The three `consensus_id` values
   are what go in `consensus.validators`, identical on every node; the `peer_id`
   values are what the other nodes dial. Reading it twice returns the same pair -
   that is the check that it was stored where the node will look for it.

   Keeping the OLD identity instead is possible but is not the simple path: the
   key is a record inside a pebble store, so preserving it means preserving the
   old chain's store, which is the thing being replaced. Identities carry nothing
   across a relaunch - bonds do not carry over either - so generating three new
   ones costs nothing.
5. **Write and review the genesis**, on every node: the snapshot's block, the
   chain id, the validator ids from step 4, and the frozen escrow figure
   confirmed against the contract. Two reviewers, and a diff between the three
   files that shows only the node's own identity differing.

   ```sh
   # The same validation the launch runs. Do this before anything starts.
   matrixd -preflight-production -config <the node's new config>
   ```
6. **Set the watcher's start block to the CURRENT Base head**, not to the
   contract's deployment block. This is where money leaks if it is going to: the
   new chain has no record of past burns, so a watcher rescanning from the
   deployment block would release escrow for burns the old chain already
   released. It is harmless only while nothing has been burned - check
   `released` on the vault and the `Burned` events before relying on that.
7. **Start the validators** on the new genesis, and confirm they commit past the
   first epoch boundary.
8. **Re-bond.** Bonds do not carry over: they were balances on a chain that no
   longer exists. Each validator funds its account and bonds, either with
   `stake.bond` in config or `matrix stake bond` from a wallet. `matrix stake
   status` shows both numbers.
9. **Verify the escrow** before announcing anything: run `matrix bridge
   reconcile` against the NEW chain and check it against the contract again. Also
   check that anyone can: `matrix_getAccountProof` on `bridge/escrow` returns a
   proof against the state root, and that proof is the evidence an exchange or an
   auditor checks without trusting the node that served it. If the figures do not
   line up, stop - the mirror is unbacked and every later step compounds it.
10. **Point a wallet at it.** Add the network, read a balance, send a transfer to
   yourself, watch the receipt land. What passes here is what a buyer will see.
   `make devnet` does exactly this against three throwaway nodes, so run it first
   and see it pass there before doing it against the real one.
11. **Publish the evidence**: genesis hash, chain id, escrow figure and the Base
   block it was read at, the validator set, and the binary's revision and SHA-256
   on each host.

## After it is running

**The old chain is not a fallback.** Once the escrow is a genesis allocation on
the new chain, both chains claim to back the same wrapped supply. Shut the old
validators down and keep their data only as an archive that cannot start.

**A wallet will show gas as free, and transfers are not free.** There is no EVM
to meter, so the gas figure is honest; the protocol fee is a percentage of the
value moved, and Ethereum's fee model has nowhere to express that. Tell buyers
the fee rate. Do not let the wallet's zero be the only number they see.

**Contracts still do not exist.** This chain speaks Ethereum's account, signature
and transaction formats. It runs no EVM: contract creation and calldata are
refused rather than ignored, and `eth_call` says so. "EIP-155 compatible accounts
and transactions" is true; "an EVM chain" is not, and the difference will be
asked about in any listing review.
