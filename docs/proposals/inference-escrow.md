# Proposal: escrow and refund, so a self-custody buyer can watch the answer arrive

**Status: decided, being built.** It changes how state is APPLIED and it moves
money, so the open questions below are answered before any code rather than
during it. The answers are in **Decisions** and the section that set each one is
marked where it happens.

---

## The constraint this exists to get around

`services/core/internal/inference/settle.go` states it as the thing that shapes the whole client-signed path:

> `token.Transaction` signs over an EXACT Amount, and the amount of an inference is not knowable until the work is done: it comes from the tokens the backend reported. A buyer cannot sign for a number nobody can compute yet.

So the path is sign-the-invoice: run first, invoice second, and withhold the completion until the buyer signs. The withholding is the only enforcement there is, because by then the provider has already spent the GPU time.

That has one consequence nobody chose: **the answer cannot stream.** Streaming hands over the goods before payment, so `StreamJob` refuses a job awaiting payment by construction rather than by oversight. A buyer on the self-custody path waits in silence for however long the model takes - over a minute for a reasoning model - and gets everything at once at the end.

The hosted path streams fine, because the node holds the buyer's key and settles for itself. That is the trade as it stands: **hold your own key, or watch the answer arrive. Not both.**

## What makes it solvable

The amount is unknowable. The RESERVATION is not.

`SubmitInferenceJob` already computes `units_reserved x price_per_unit` and already runs the affordability check against it, before any provider does any work. That number exists before the model starts, and a buyer can sign for it.

So: pay the maximum up front, do the work in the open, and return what was not used.

## The shape

```
1. SubmitInferenceJob      reserve capacity. R = units_reserved x price is known here.
2. buyer signs ONE transfer of R, buyer -> inference escrow, bound to this job
3. node submits it; once it commits AND applies, the node has been paid the most
   this job can cost
4. node runs the model and STREAMS, because there is nothing left to withhold
5. settlement: consensus pays `actual` to the provider and returns `R - actual`
   to the buyer
```

Step 5 is the part that is not a library change.

## Why the refund must be applied by consensus

The tempting shortcut is to escrow into the provider's own account and have the provider send the remainder back. It would work for an honest provider and it is wrong.

The charge ceiling (`MaxUnitsFor`) exists precisely because a provider's self-reported number is not trusted - it bounds a bill by the bytes the node actually holds, so a seller cannot answer "hello" to "hi" and charge for a thousand tokens. A refund the provider chooses to make reintroduces exactly the trust that ceiling removes, one layer down. The buyer would be relying on the seller's goodwill for the difference between the cap and the bill, which on a generous reservation is most of the money.

So the split is a rule consensus applies, deterministically, from a committed block - the same shape the bridge already uses to release escrow on a burn (`internal/consensus/burnunlock.go`): every node reaches the same balances from the block alone.

## What it costs

This is a change to how state is APPLIED, which the validator upgrade runbook singles out as the category needing more than a rolling restart. The pieces:

- a reserved escrow account for inference, alongside `bridge/escrow`
- a settlement rule consensus applies: pay `actual`, refund the rest, refuse `actual > R`
- an EXPIRY rule, because escrow that cannot be returned is worse than the problem being solved (see the open questions)
- a protocol version gate. `verifyBlockVersionLocked` already refuses a block whose version does not match what a node expects at that height, so a node that has not upgraded stops voting rather than diverging - the loud failure, which is what this needs
- an activation height far enough out that all four nodes are upgraded before it
- 3/3 quorum today, so every validator must be on the new build before the height or the chain stalls. A fourth validator does not remove that requirement, but it does mean the upgrade itself no longer pauses block production

## What the buyer gives up, stated plainly

Today the buyer signs the EXACT amount. Under this, they sign a CAP and the provider picks the actual within it, bounded by the ceiling.

That is strictly less control, and it is the model every metered API already uses: you authorise a limit, they bill what they used. It should be said out loud rather than discovered, because the current design's selling point is that the buyer signs the real number.

## Decisions

Four questions had to be settled before this could be written, and one of them
turned out to decide the shape of everything else.

### Who signs the settlement: the BUYER's side

The obvious reading of "the provider has been paid up front" is that the
provider then tells consensus what the job actually cost. That is wrong, and
reading the code is what shows it.

There is no check on the buyer's side today. The browser signs whatever invoice
the node returns - it does not compute a ceiling, it does not compare the bill to
the text it received, it submits the number it was given. `MaxUnitsFor` runs on
the SELLER's node, bounding the seller's own model server. That is worth having
and it is not a buyer's protection: an operator who wants to overcharge controls
the node running the check.

So the only thing that has ever stood between a buyer and an inflated bill is
their power to WITHHOLD THE SIGNATURE. Hand settlement to the provider and that
disappears, and what remains bounding the bill is the reservation - which is
generous by design, which is the exact hole `MaxUnitsFor` was written to close.

The settlement is therefore signed by the buyer's side, which under a spend
budget is the delegate and needs no prompt. Streaming still works, because what
frees the text is the money already sitting in escrow rather than a signature
still to come.

**This makes a second change non-optional: the CLIENT must compute the ceiling
before signing.** A buyer-signed settlement that rubber-stamps the node's number
is the same design with extra steps.

### What expiry does: pays the PROVIDER, in full

Answers open question 1, and 2 with it.

An expiry that refunds the buyer makes "receive the answer and never settle"
free, which is worse than today - today the seller at least still holds the text.
By the time expiry arrives the provider has spent the GPU time and delivered, so
expiry pays them the whole reservation.

The buyer is then strictly better off settling honestly, because the actual is by
construction at most the reservation. Hanging up mid-stream costs the buyer the
difference rather than the provider their work, which is the honest allocation:
the bytes were delivered.

The timeout has to clear a slow reasoning run by a wide margin. Refunding a
provider's fee out from under them while they are still working would be the same
mistake pointed the other way.

### What parks the money: the BUDGET, not the wallet

Answers open question 3. The reservation moves real money for the length of a
run, and on a raw wallet that is felt. Funded from a spend budget it is not: the
escrow is opened from the budget account, inside an amount the buyer has already
approved and bounded. The two proposals compose here rather than merely shipping
together.

### Is it worth it

Answers open question 4, and the live network answered it. A buyer can now hold
their own key and pay without a prompt per message, and the one thing they still
cannot do is watch the answer arrive. That is the last item on the list that
made this work worth doing.

## The questions these answered

1. **What happens to escrow when the run never finishes?** A node that dies mid-run leaves `R` sitting in escrow. An expiry that refunds the buyer is the obvious answer, but it has to be a consensus rule too, and its timeout is a policy: too short and a slow legitimate run is refunded out from under a provider that is still working; too long and a buyer's money is parked.

2. **Who pays when the buyer hangs up mid-stream?** Today the reservation is released and nobody is billed, so the provider eats the partial work. Under escrow the money is already committed, so the provider could be paid for what it produced. That is more honest to the provider and it is a change in behaviour, not a detail.

3. **Does the reservation become the quote?** A generous reservation parks a lot of a buyer's balance for the length of a run. That is already true of the affordability check, but it becomes real money moved rather than a check, so buyers will feel it.

4. **Is it worth it, given the hosted path already streams?** The honest case for yes: the hosted path requires handing the seller's node a key that can move everything in that account - worse than a card on file, which can only be charged. This is the design that removes that trade rather than documenting it.

## What it does not solve

Nothing about the provider being trusted to report token counts. The ceiling still does that work, and it still bounds the bill by the text the buyer receives.

## The half this does not cover

It makes the answer stream. It does not change how OFTEN a buyer signs: the
transfer at step 2 is still one wallet prompt per job, on top of the run
authorization. [`spending-authorization.md`](spending-authorization.md) is the
other half - one signature that delegates a bounded budget to a subordinate key,
so the per-job signature is made by that key and not by the wallet. Shipped
alone, each proposal leaves one of the complaints in place; they should go out
under one activation height.

## Recommendation

Build it, but not next. In order:

1. The hosted path now works end to end, so a buyer who accepts custody can stream today. Fund a dedicated account for it rather than a main one, and say why in the provider runbook.
2. Progress reporting makes the self-custody wait legible without changing any rule. Shipped.
3. A fourth validator first, so the activation-height upgrade does not have to be done against a set with no fault tolerance.
4. Then this.
