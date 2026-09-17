# Runbook: turning on inference escrow (protocol version 3)

This is the second rule change over money on this chain, and the procedure is the
one that worked for the first. **Read
[`budget-activation.md`](budget-activation.md) for the procedure**: the
preconditions, the order, the agreement checks, why a height is compared by name
rather than by head, and why there is no way back after the boundary are all the
same and are not repeated here.

What follows is only what is different about version 3.

```
export MATRIX_ROLLOUT_BOXES="validator-1|host|keyfile|validator
validator-2|host|keyfile|validator
validator-3|host|keyfile|validator
validator-4|host|keyfile|validator
gpu|host|keyfile|seller"
./scripts/rollout.sh v0.5.1 3
```

`rollout.sh` appends to an existing schedule rather than replacing it, so the
version 2 entry the chain already ran stays where it is. It refuses a schedule
that goes backwards in height or in version, and refuses a second entry at a
height already named.

**v0.5.1, not v0.5.0.** v0.5.0 was cut before the cut-short fixes and bills a
buyer who disconnects for the whole reservation. It is on the releases page and
should not be rolled anywhere.

---

## What actually changes at the height

Three more recipients start meaning something:

```
infer/escrow/<jobID>/<reserved>.<expiry>/<provider>/<payer>   OPEN   the payer funds a reservation
infer/settle/<same terms>                                     SETTLE the payer's side names the actual
infer/claim/<same terms>                                      CLAIM  the provider takes the rest, at the expiry
```

The terms live in the account NAME, exactly as a budget's do, which is what lets
one ordinary transfer signature cover all of them - `to` is signed.

The asymmetry between the last two is the whole design, and it is worth
understanding before you turn it on:

- **SETTLE is the payer's signature.** The bill is only ever bounded from the
  buyer's side, so consensus will not let a provider name it.
- **CLAIM is the provider's, and only at or after the expiry.** This is what
  stops a buyer reading a streamed answer and never settling. It is also what
  makes streaming safe to offer at all: the provider already holds the cap before
  the first token, so the text is not leverage any more.

Together they mean a buyer can only ever pay LESS by settling honestly, and a
provider is never worse off than the reservation.

## Reaching the height, on a chain nobody is using

`rollout.sh` measures the block rate and, on a chain producing none, falls back to
a floor of 200 blocks. That floor is honest and it is not a duration. **This chain
mints a block when there is a transaction to put in one.** With no traffic it
still escapes, through stall recovery - but that timer is
`60 x consensus.round_timeout` (see `heightIsStalledLocked` in `engine.go`), so a
node on the production 3s produces one empty block every three minutes and a
200-block lead is ten hours away.

An activation is a height rather than a time precisely so that every node
switches together, so the height has to be reached. Nothing else moves it:

```
read -s -p "passphrase: " MATRIX_WALLET_PASSPHRASE; export MATRIX_WALLET_PASSPHRASE
./scripts/advance-height.sh                        # aims at the scheduled activation
./scripts/advance-height.sh 1831                   # or a height you name
./scripts/advance-height.sh 1831 <64-hex-account>  # and a recipient you name
```

It sends one base unit at a time from a funded wallet to one other account, and
reads the height rather than counting transfers - transfers that arrive together
share a block, so a count overshoots and stops short of the one number that has
to be right. A couple of hundred blocks costs a couple of hundred base units plus
fees, so it does not bother sending them back.

It needs a **recipient that is not the sender**: the ledger refuses a
self-transfer, which moves nothing while consuming a nonce, so the free block is
not on offer. It looks for one among the other boxes' wallets and then among the
validator account IDs in the config - those are this operator's own nodes, so a
base unit sent there has not left the building - and you can name one instead.

The wallet has to be able to SIGN. An encrypted one needs its passphrase in your
own shell, as above: it travels over ssh's stdin, never in argv where `ps` on the
box would show it, and it is never printed.

Check where it has got to at any point:

```
curl -s -X POST http://127.0.0.1:9095 -H "content-type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"matrix_getChainInfo","params":[]}'
```

## What is different about verifying it

A budget could be proved with `budget open` and four `budget show` calls, because
a budget is a balance. An escrow is a **sequence**, and the thing that can be
wrong is the money at the end of it. So the check is a real inference:

```
./scripts/escrow-smoke.sh
```

It buys one answer down the escrowed path against the live seller, streams it,
settles it, and reports what the wallet actually paid against what was reserved.
**A cost equal to the reservation is a failure even if every command succeeded** -
it means the settlement did not apply and the provider is holding the cap.

It needs ONE box that both holds a funded wallet and serves a provider. The
escrowed path signs with a local key, and inference is served by the node that
owns the provider, so those two have to be the same machine for a script driving
it over ssh - a key that travelled to a second box to sign there would be the
custody this whole path exists to avoid. The script says which boxes have which
when none has both.

By hand, the same thing:

```
matrix inference submit --escrowed \
  --inference-addr <the SELLER's node>:9092 \
  --buyer <your account> --provider <the provider id> \
  --prompt "one sentence about the sea" --units 200 --wallet ~/.matrix/wallet.json
matrix wallet balance
```

Inference is served by the node that OWNS the provider. A reserve sent anywhere
else is refused by a node that has never heard of the job, which reads as a
configuration error and is not one.

## Two things that will look like bugs and are not

**A deposit submitted before the height sits in the mempool.** It is refused as
"not yet" rather than rejected, and lands by itself once the height arrives -
deliberate, and the same behaviour budgets have. What is new is that a CLIENT
waiting on it gives up first: `FundEscrow` waits 5 seconds for the deposit to
commit AND apply, and on a chain that produces no blocks except when there are
transactions, a refused transaction is not a transaction. So an escrowed request
before the boundary fails with a timeout rather than with "not yet", and the
honest reading of that timeout is "not yet". After the height it commits in one
block like anything else.

**A job that streams and then reports `stopped early`.** That is the recovery
path working. The settlement rides on the stream's last frame, so a buyer who
cancels, reloads, or loses their connection never receives it - and with nothing
to sign, the reservation goes to the provider's claim in full. A cut-short run is
therefore FINALISED rather than failed: the node bills for the text that actually
reached the buyer, and `RecoverEscrowedInferenceJob` hands the settlement to
whoever comes back for it. Seeing it means someone stopped an answer and paid for
what they read, which is the intended outcome and not an error to chase.

## What can go wrong that is specific to this version

**A provider is streaming against an unfunded reservation.** It cannot: the node
refuses to stream a job whose deposit has not committed AND applied, which is the
check the whole path rests on. If you see a completion arriving on a job in
`AWAITING_DEPOSIT`, stop and report it - that is the one failure here that gives
an answer away for nothing.

**A reservation nobody settles.** It sits until its expiry (30 minutes by
default) and the provider claims it. The buyer overpaid; nobody is stuck. The
usual cause is a client that crashed between funding and settling, and the fix is
for that client to call `RecoverEscrowedInferenceJob` on restart rather than for
anyone to intervene on the chain.

**A settlement larger than the answer can account for.** Both the browser and
`--escrowed` compute `MaxUnitsFor` on their own side and refuse before signing,
so this surfaces as a client refusing to pay rather than as an overcharge. If a
seller's node is doing it consistently, that is a seller to name - the refusal
message carries the arithmetic.
