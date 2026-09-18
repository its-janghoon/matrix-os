# Evidence: inference escrow live on chain 8170, and one answer bought through it

Protocol version 3 activated at height **1831**. One real inference was then
bought down the escrowed path against the live GPU seller: the reservation was
funded before the model started, the answer **streamed**, and consensus returned
the change when the buyer signed for what it actually cost.

Every number below is output from
[`scripts/rollout.sh`](../../scripts/rollout.sh),
[`scripts/advance-height.sh`](../../scripts/advance-height.sh) and
[`scripts/escrow-smoke.sh`](../../scripts/escrow-smoke.sh), not a description of
it.

## The activation

Five boxes on v0.5.1, schedule `1497=2,1831=3` on every one of them, written by
`rollout.sh` with an automatic revert if it had landed on some and not others.

At height 1880, past the boundary:

| box | version | schedule | root |
|---|---|---|---|
| validator-1 | v0.5.3 | `1497=2,1831=3` | `0x381352cd310e` |
| validator-2 | v0.5.3 | `1497=2,1831=3` | `0x381352cd310e` |
| validator-3 | v0.5.3 | `1497=2,1831=3` | `0x381352cd310e` |
| validator-4 | v0.5.3 | `1497=2,1831=3` | `0x381352cd310e` |
| gpu (seller) | v0.5.3 | `1497=2,1831=3` | n/a (no eth_rpc) |

Block 1879 is `0x4157ff4fa36826e21b493a4d515d748e8629f659e3a0981b7b2a0b8d1550c5c5`
on all four validators - the same height asked of every node, which is the only
comparison that means anything.

## Reaching the height cost 175 transfers

The activation was scheduled 200 blocks out, from `rollout.sh`'s floor, because
the chain produced **zero blocks in a 90-second sample**. That is not a fault: it
mints a block when there is a transaction to put in one, and escapes otherwise
only through stall recovery at `60 x round_timeout` - 3s on these nodes, so one
empty block every three minutes and **570 minutes** to cover 190 blocks.

`advance-height.sh` sent 1 base unit at a time from the seller's wallet to an
account named on the command line, 175 times, and the chain reached 1833. Cost:
175 base units.

## The sale

```
reserving 4000 units at 1000 each = 4000000, claimable by the provider after 2026-09-17T14:11:06Z
```

The answer arrived as it was produced. Then the working - 1235 bytes of it -
because the buyer is **billed** for it:

> 1. **Analyze User Input:** Question: "what is a marketplace for?" Constraint:
> "Answer in one short sentence" [...] 5. **Final Check against Constraints:**
> One sentence? Yes. Short? Yes (18 words).

The receipt the seller's node signed:

```json
{"job_id":"5e665a07-a8da-41f5-a92e-2e9e2b12fcef",
 "model":"qwen3.6-27b","prompt_tokens":22,"completion_tokens":315,
 "total_tokens":337,"units":"337","price_per_unit":"1000","total":"337000",
 "exchange_digest":"nzJ1P8DppV+zJ8tXr/oJVgDm/bOLrWRStTV6z8YpVIM=", ...}
```

And the money:

```
wallet 912837825 -> 912500825, cost 337000 base units against a reservation of 4000000

PASS. One answer bought down the escrowed path:
     4000000 reserved, 337000 paid, 3663000 returned.
```

**3,663,000 returned is the whole point.** The provider held the entire 4,000,000
before the first token - which is what made streaming safe to offer - and gave
back 91% of it when the buyer signed for the actual.

Note the ratio the receipt records: **315 completion tokens for one sentence of
answer.** Almost all of it is working. A path that did not deliver that text
would be charging for something the buyer cannot check.

## What the live chain found that the tests did not

Four bugs, and every one of them surfaced because the buyer's own ceiling check
refused to sign. Without that check they would all have passed silently.

1. **ssh does not carry argument boundaries.** It joins its command words with
   spaces and the remote shell splits them again, so the prompt arrived as
   `$1="Answer"` and the model was asked one word. It replied asking what the
   question was, and both sides billed 200 units for the exchange.
2. **The verdict compared base units to a unit count** - 200000 against 200 - and
   reported a settled sale, receipt and all, as the catastrophic failure this
   test exists to catch.
3. **The node billed for working it refused to show.** `GetJob` blanks the text
   while a job awaits payment, which IS the enforcement on the client-signed
   path; the escrowed path inherited the rule, so the buyer's ceiling came out
   tighter than the node's and refused an honest invoice - 479 asked, 193
   accountable. Read from the buyer's chair, that says the seller is cheating.
4. **`rollout.sh` could not roll a binary without scheduling a version**, so
   there was no way to ship the fix for (3) to the boxes it was failing on.

## What is left behind

Two refused runs each funded a 4,000,000 reservation that was never settled -
8,000,000 base units in escrow accounts, which the provider claims at their
expiry. Both accounts are the same operator's, so nothing leaves; what it shows
is a real gap: **a refused settlement has no way back.**
`RecoverEscrowedInferenceJob` existed on the node and nothing called it from a
terminal.

**Since fixed**: `matrix inference recover --id <job>` reads the settlement and
`--settle` pays it. The two reservations above can be collected with it. The
proof it presents is a signature made now over the job id, because a later
process does not hold the reservation's own authorization and should not be
keeping one on disk so that it can.
