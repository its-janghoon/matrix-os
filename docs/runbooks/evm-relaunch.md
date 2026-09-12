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
| Addresses as account ids | Every 64-hex account id, and anything holding one |
| `chain_id` in the signature | Cross-network replay, which had nothing stopping it |
| State root in the block | Every block hash from genesis |
| Block timestamp and protocol version | Every block hash from genesis |

With few holders and little history this is a config exercise. With an exchange
listing and a year of transfers it is a migration nobody wants to run. The cost
of doing it is lowest on the first day it can be done and rises every day after.

## What has to be decided before anything is typed

**The chain id.** Pick one nobody else uses; chainlist.org is the registry people
check. It goes inside every signature a wallet makes, it must be identical on
every node, and it can never change on a running chain - a change invalidates
every signature already made for the old value. Pick it once.

**The escrow figure.** Native escrow must equal wrapped supply, exactly. Read
both, at the same moment, before writing a genesis file:

```sh
# Native: what the old chain holds in escrow.
matrix --api-key <key> balance --account bridge/escrow

# Wrapped: what exists on Base. Both numbers are in their own scale - native base
# units are 9 decimals and wMATRIX is 18 - so convert before comparing: one native
# base unit is 1e9 wrapped base units.
cast call <WrappedMatrix> "totalSupply()(uint256)" --rpc-url <base-rpc>
```

If the launch evidence is still current these are 50,000,000 MATRIX escrowed
against 50,000,000 wMATRIX minted to the vesting vault. Do not assume it: if
anyone has locked native since, the escrow is larger, and that larger number is
what goes in the genesis file.

**Who holds what.** Old ed25519 accounts do not exist on the new chain. Every
balance worth carrying over needs a wallet address to carry it to, and that
mapping is a decision with no technical answer - collect it before the ceremony,
publish it as part of the genesis, and have the holders confirm their own
addresses. An address a holder cannot sign for is a balance nobody can ever
move.

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
2. **Read the escrow and the wrapped supply**, at the same moment, and record
   both in the evidence file with the Base block they were read at.
3. **Write and review the genesis**, on every node, against the frozen figures.
   Two reviewers, and a diff between the three files that shows only the node's
   own identity differing.
4. **Set the watcher's start block to the CURRENT Base head**, not to the
   contract's deployment block. This is where money leaks if it is going to: the
   new chain has no record of past burns, so a watcher rescanning from the
   deployment block would release escrow for burns the old chain already
   released. It is harmless only while nothing has been burned - check
   `released` on the vault and the `Burned` events before relying on that.
5. **Start the validators** on the new genesis, and confirm they commit past the
   first epoch boundary.
6. **Re-bond.** Bonds do not carry over: they were balances on a chain that no
   longer exists. Each validator funds its account and bonds, either with
   `stake.bond` in config or `matrix stake bond` from a wallet.
7. **Verify the escrow** before announcing anything: native `bridge/escrow` must
   equal wrapped supply converted to native base units. If it does not, stop -
   the mirror is unbacked and every later step compounds it.
8. **Point a wallet at it.** Add the network, read a balance, send a transfer to
   yourself, watch the receipt land. What passes here is what a buyer will see.
9. **Publish the evidence**: genesis hash, chain id, escrow figure and the Base
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
