# Proposal: one signature that lasts, so holding your own key is not a tax per message

**Status: proposed, not built.** It is a new consensus rule over money, so it is written down and argued before any code.

This is the second half of [`inference-escrow.md`](inference-escrow.md). That proposal makes the answer stream by paying the reservation up front and having consensus refund the difference. It does not reduce how OFTEN a buyer signs, and after using the chat page for ten minutes that is the thing people complain about first.

---

## Three symptoms, one cause

1. **"It asks me to sign every message."** One message costs two wallet prompts: a run authorization before the seller spends GPU time, and a payment after, because the amount does not exist until the tokens are counted.
2. **"Does it stream?"** No, on the self-custody path. The node holds the completion until the payment is signed, because that withholding is the only thing holding the buyer to the bargain.
3. **"How does someone who just swaps `base_url` use us?"** Today, only by letting a node hold their private key. `openaiapi` says so in its own package comment: *"this is the hosted-wallet door, and it should be called that."* The OpenAI protocol carries an API key and no signature, so somebody has to sign, and that somebody has to hold the key.

The cause is the same in all three: **a signature authorises exactly one payment of an exact amount, and nothing else.** There is no way in this protocol to say "I allow this much, for this long, for this purpose."

## What the industry did about the same problem

Worth writing down, because we are not the first to hit it and two of the answers are directly useful.

**Session keys, ERC-4337 and EIP-7702.** The EVM world converged on delegating bounded authority to a short-lived subordinate key: it may spend up to a set amount, only to certain targets, only for certain calls, only until expiry. The usual description is a valet key - it starts the car, it does not open the trunk or exceed a speed limit. ERC-4337 needed a new smart-contract account at a new address; EIP-7702 (Ethereum's Pectra upgrade, May 2025) let an ordinary EOA delegate to a contract implementation without moving funds or changing address, which is why it is the one that stuck. **This is the pattern we want and we need no contract for it, because we own the consensus rules that a contract would otherwise have to emulate.**

**x402** (Coinbase and Cloudflare). A wire protocol rather than a custody model: a server answers `402 Payment Required` with structured payment metadata, the client returns a signed payment authorization, a facilitator settles it. Its stated design goal is the sentence that matters here - the client proves intent to pay **without exposing private keys**. Reported volume as of March 2026 was ~119M transactions on Base and ~35M on Solana. It is orthogonal to this proposal and worth speaking later, so that an agent which has never heard of our chain can still pay us.

**Payment channels.** Deposit once, then exchange signed cumulative balances off-chain and settle the last one on-chain. Strictly more efficient than per-request settlement, and strictly more complex: dispute windows, timeouts, both sides tracking state. The decentralised-inference literature that proposes them (SAKSHI and others) says the same thing about operational complexity. We settle per job on-chain today and our volume is small, so this optimises a cost we are not paying yet.

**Custody**, which is what we have. Best possible UX, worst possible risk: other people's private keys on a disk that also runs a model server, a security blast radius that is not our money, a balance-sheet liability rather than revenue, an operational duty to process withdrawals, and in Korea a plausible 특금법 가상자산사업자 (보관·관리업자) question - which needs a lawyer and not an engineer's reading. Every system above exists to avoid it.

## The proposal

**A spending authorization: one EIP-712 message, signed once by the wallet, that lets a subordinate key spend a bounded amount for a bounded time on inference and nothing else.**

```
SpendingAuthorization(
  address buyer,           // who is delegating
  bytes32 delegate,        // the ed25519 public key allowed to act
  uint256 cap,             // total base units spendable under this authorization
  uint256 perJobCap,       // most that any single job may draw
  uint256 maxPricePerUnit, // refuse a seller dearer than this
  int64   expiry,          // unix seconds, after which it is dead
  uint256 nonce            // per buyer; a higher nonce supersedes
)
```

The delegate is an **ed25519 public key, not an address**, which is deliberate: the browser wallet this page already builds - non-extractable, generated in the page, unreadable by any script - becomes the delegate. It stops being a separate account that needs its own funding and becomes a bounded hand on the account the buyer already has.

### What consensus has to enforce

There are three operations and no new state beyond one account:

- **Open.** A transfer from the buyer into the escrow account named below. The amount deposited is the budget.
- **Draw.** A transfer out of that escrow to a provider, signed by the DELEGATE rather than by the buyer. Consensus refuses it unless the delegate is the one the account names, the block's time is before the expiry, and the amount is within `perJobCap` and within the balance.
- **Close.** The remaining balance returns to the buyer. After expiry anyone may trigger it, because by then it is not a decision; before expiry only the buyer, and that is what revocation is.

Every check reads the block and the ledger, so four nodes reach the same verdict. In particular the clock is the BLOCK's, never `time.Now()`: four wall clocks are four answers, and an authorization that expires on one node and not another is a fork. Expiry is the moment it is dead rather than the last moment it lives, for the same reason.

A refusal is never a clamp. A partly-paid job is worse than a refused one, because the buyer has paid for something they did not get.

### What `maxPricePerUnit` does and does not bound

**Corrected after starting the implementation, because the first version of this
section claimed a protection that does not exist.** It said the price ceiling is
what stops a thief with the delegate key from registering as a provider, quoting
an absurd price and settling against themselves.

Consensus cannot enforce it. A draw is an amount moving to a provider; there is
no unit count in it to divide by. Carrying one would not help either, because in
the case being defended against the unit count is asserted by the same person
who holds the stolen key.

**What actually bounds a stolen delegate key is the escrow balance and the
expiry**, and those are exactly the two consensus can check from the block
alone. That is what a budget means, and it is enough - but it has to be said
plainly rather than dressed up with a third bound that sounds stronger.

`maxPricePerUnit` stays in the signed message, doing a smaller and real job: the
buyer's own client refuses a seller dearer than this when it picks one. It
bounds an honest client against an expensive market. It travels with the grant
rather than living in one page's settings, which is why it is signed.

A consensus-side version is possible later and is a different check: compare the
ceiling against the price in the PROVIDER REGISTRY, which is consensus state and
not the caller's to assert. It is not in this proposal because the cap already
bounds the loss, and coupling settlement to a registry lookup is real complexity
for a second lock on the same door.

### How the terms reach consensus, which turned out to need no new wire format

The first sketch had each draw carry the signed authorization so consensus could
read its terms, which meant a new field on the transaction and therefore a
protocol message change. Writing it showed that is not necessary, because this
chain already addresses consensus operations by naming them in the recipient
string - a bond is a transfer to `consensus/stake/bond/<id>`, a burn release to
`bridge/unlock/<hash>/<account>/<amount>`.

So the escrow account's NAME carries the terms:

```
spend/escrow/<buyer>/<delegate>/<perJobCap>/<maxPricePerUnit>/<expiry>/<nonce>
```

Three things fall out of that, and together they are why this is the shape to
build:

- **The buyer's ordinary transfer signature already covers the terms**, because
  `to` is part of what a transfer signs. Opening an escrow is a transfer into
  that account and nothing else.
- **The remaining budget is that account's balance**, so there is no separate
  spend record to keep in step, and it is inside the state root already - the
  root is a fold over balances. A separate record would have been new state that
  the root does not cover, which is the shape of a silent divergence.
- **A draw needs no extra bytes at all.** It is a transfer out of the escrow
  signed by the delegate; consensus parses the terms from the account it is
  drawing from.

The cost is honest and worth naming: the wallet prompt shows the terms as a long
recipient string rather than as labelled fields. Everything the buyer is
agreeing to is visible, but it reads like a path and not like a budget. The
EIP-712 `SpendingAuthorization` type exists for the version that shows them
properly, and buying that costs carrying its signature on every draw. Ship the
plain one first and see whether the string is actually the problem it looks like.

### How it composes with the escrow proposal

They are two halves and both are needed:

| | answers | without the other |
|---|---|---|
| Escrow (already proposed) | where the money sits while the model runs, and who returns the unused part | streaming works, the wallet still prompts per message |
| Spending authorization (this) | who may sign each draw without the wallet | prompts go away, the answer still cannot stream |

Together the flow is: the wallet signs **once**; the delegate signs the reservation before each run; the node streams because it has been paid the maximum the job can cost; consensus pays the actual and refunds the rest.

## What it changes, surface by surface

**The chat page.** The wallet chooser stops being "durable but two prompts a message" against "no prompts but you lose it when you clear your browser". It becomes: connect MetaMask, approve a budget once, then type. The browser key is still generated and still non-extractable - it is now the delegate rather than a second account, so clearing site data costs a re-authorization and not the money.

**The OpenAI-compatible door.** The API key becomes a handle for a delegate key, and that is the whole difference between the two deployments we might want:

- the developer holds the delegate key in their own process and signs there - fully non-custodial, needs our SDK rather than the stock openai one;
- the developer lets our gateway hold a delegate key - **scoped** custody: capped, expiring, revocable by them, and unable to move their money anywhere but inference at a price they set. That is a different risk class from holding a wallet key, and probably a different regulatory class, though that is again a lawyer's call.

**The protocol does not care which.** That is the point of proposing it now: we do not have to choose today, and whichever we ship first does not foreclose the other.

**The CLI.** A long-running script can hold a delegate key with a day's budget instead of a wallet passphrase in the environment.

## What this deliberately does not do

- **It does not remove trust in the seller's token counts.** The charge ceiling still does that work, bounded by the bytes the buyer actually received.
- **It does not stop a delegate spending the whole cap on useless inference.** Nothing can; that is what a cap is for. The buyer is authorising a budget, exactly as they do with any metered API key.
- **It is not a payment channel.** Every draw still commits on-chain. Revisit when that cost is one we are actually paying.
- **It is not x402.** That is a wire protocol and this is a ledger rule. Speaking x402 later is easier once this exists, not harder.

## What it costs

Same category as the escrow proposal, and they should ship in one activation:

- a new EIP-712 type, which is a signature format and therefore permanent once used
- a consensus rule verifying a delegated signature and maintaining the spend record
- a protocol version gate and an activation height, so a node that has not upgraded stops voting rather than diverging
- every validator on the new build before that height

Four validators means the upgrade itself no longer pauses block production, which is why this was sequenced after the fourth.

## Open decisions

1. **Where does the gateway's delegate key live, if we offer one at all?** Scoped custody is much better than wallet custody and it is still custody. Worth pricing the legal answer before building the convenience.
2. **Does a delegate authorise a counterparty, or anyone?** Naming a seller is safer and breaks the moment a gateway routes to whoever is cheapest. `maxPricePerUnit` is the weaker but composable alternative, which is why it is in the message above.
3. **What does the wallet prompt show?** Once per-draw signing is gone, the cap, the expiry and the price ceiling are the only protection a buyer has. They have to be the largest things in the MetaMask dialog, not a hash.
4. **Does an authorization survive a chain relaunch?** Genesis snapshots carry balances. An authorization is a promise about future spending and probably should not be carried over - but that has to be decided rather than discovered.
5. **Per-job or per-message?** A chat turn is a job. An agent loop is many. `perJobCap` bounds one; nothing here bounds the rate.

## Recommendation

Build it with the escrow proposal, as one activation, in this order:

1. Fix the live hosted door so it settles from an account **we** own, and use it for internal testing only. Our own money is not custody.
2. Ship escrow and spending authorization together behind one activation height. Separately, each leaves one of the three symptoms in place.
3. Then self-serve keys, which now need no custody decision.
4. Then x402, so an agent that has never heard of us can pay.
