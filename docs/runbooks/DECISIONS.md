# What only you can decide

Everything else on the launch path is now a script. This is the short list that
is not, because each item is a judgement, a secret, or a piece of hardware - and
a script that made these choices for you would be a script nobody should run.

Work top to bottom. Items 1-3 block the relaunch; items 4-6 do not.

---

## 1. The chain id

**Why it is yours.** It goes inside every signature a wallet makes. Changing it
later invalidates every signature already made for the old value, which makes it
the one irreversible decision here.

**What to do.** Pick a number. Then:

```sh
scripts/launch-plan.sh your-launch.yaml --out ./launch
```

The script checks it against two independent sources and refuses a taken id
before it writes anything. It has been tested against chain id 1 and correctly
refuses it - worth knowing, because the check that used to be in the runbook
returned "free" for Ethereum mainnet.

The registry is a directory, not a gate: a 404 means "unclaimed here", not
"provably unused anywhere". That is the right bar for picking one, and it is the
reason to publish yours once it is live.

---

## 2. The three validators

**Why it is yours.** Which three machines, in which accounts, run the network.
Nothing can infer that.

**What to do.** On each host, with `storage.path` already pointing at that
host's real data directory:

```sh
matrixd -init -config /etc/matrix/config.yaml
matrixd -init-identities -config /etc/matrix/config.yaml
```

Paste the three `consensus_id` and `peer_id` values into your launch file.

**Use an absolute `storage.path`.** A node's identity lives in its store, so a
relative path makes that identity depend on the directory the command ran from.
Reading three nodes' identities from a parent directory resolves all three to
one store and returns the SAME id three times - a validator list naming one
account instead of three, and a quorum smaller than it looks. The script catches
this, and it caught it on our own test harness first.

---

## 3. The genesis allocations

**Why it is yours.** Who holds what on the new chain. The maintainer balance,
the founder balance, the escrow - these are the ledger, and nobody should be
guessing at them.

**What to do.** Do not type them. With the old node **stopped** and its old data
directory still in place:

```sh
matrixd -genesis-snapshot -config <the node's OLD config> > genesis-block.yaml
```

Check the supply line closes exactly at the cap. Short or over means a balance
was dropped or counted twice, and the production preflight refuses either.

Then point your launch file's `genesis_file` at it. The launch script preflights
every config it writes and proves the three differ only where they should.

---

## 4. A GPU box for the first-party provider

**Why it is yours.** It is hardware, and which machine and which payout wallet
is a business decision.

**What to do.** Point it at a config from a node already on the network:

```sh
scripts/first-party-provider.sh your-provider.yaml --out ./gpu-1
```

It writes the config, issues the maintainer attestation bound to that node and
that payout account, and checks the badge verifies. What is left for you is
starting vLLM, putting `MATRIX_VLLM_API_KEY` in the node's process environment,
and terminating TLS.

**Verify from another node**, which is the only check that counts - a node
vouching for itself proves nothing:

```sh
matrix provider directory --addr <another node's market addr, default port 9091>
```

**Diary the expiry.** The protocol refuses an attestation dated more than 30
days out. When it lapses the badge simply disappears and the seller keeps
working, unbadged.

---

## 5. Upgrading the three validators for the bridge fixes

**Why it is yours.** It is three production hosts and a maintenance window.

**What to do.** [`validator-upgrade.md`](validator-upgrade.md) is the procedure.
The short version:

- Run `matrix bridge reconcile` FIRST. If it does not close, stop - a duplicate
  lock is already in history and that block becomes unsyncable after the upgrade.
- Freeze bridge locks for the window. That is the only transaction type the two
  versions disagree about; with none in flight the upgrade is an ordinary restart.
- Three validators means quorum needs all three, so block production pauses while
  each one restarts. Expected. It also means a mixed set cannot fork - only stall.
- One node at a time. Wait for the chain to commit again before the next.

Nothing on Base changes, and no burn has ever happened there, so there is no
backlog to replay.

---

## 6. Who gets an API key

**Why it is yours.** Who may spend, on whose account, is a product decision. The
node deliberately has no self-serve signup: there is no RPC that mints
credentials, because quotas, identity and payment are not a node's call.

**What to do.** Add the key under `security.api_keys`, then:

```sh
kill -HUP $(pidof matrixd)
```

The node re-reads its credentials without restarting. A config that does not
parse leaves the running keys exactly as they were and says so - it will not
lock you out of the node you were administering.

A key needs an `account` to buy over `/v1/chat/completions`: that protocol
carries no buyer field, so the key says whose balance to charge. A key without
one drives every other surface and cannot buy inference.

Buyers using a wallet need none of this. The browser path signs with a key the
node never holds.

---

## What is NOT on this list, and why

**Multi-host.** `make devnet` runs a real three-validator quorum, and the
first-party provider has been proven joining a running network as a separate
process with its own store. Neither says anything about NAT, firewalls, clock
drift between regions, or disks filling up. The first real GPU box (item 4) is
what answers those, which is a reason to do it before the relaunch rather than
after.

**The bridge rehearsal.** It has now been run - against a local EVM, since
Sepolia itself needs a funded key and an RPC endpoint. It found two ways to lose
money, both fixed in the node rather than the contract, and both re-proven end to
end afterwards. What it still does not cover is Sepolia's own network: real
confirmations, a real RPC's failure modes, and a real funded key.
