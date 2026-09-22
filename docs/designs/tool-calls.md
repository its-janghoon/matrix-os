# Design: tool calls

The handoff asked one question before anything could be built: **is a tool call a
protocol-level field on the inference job, or something the seller's model server
handles behind the door?** That answer decides whether this needs a protocol
version at all.

**It is seller-side, and it needs no protocol version.** But it is not free, and
the cost is in a place the question did not point at: the buyer's signature.

---

## What is actually true today

Worth stating, because the handoff described the current state as "tool calls do
not exist in the protocol, only in the UI's ambition", and the second half is no
longer accurate.

- **The protocol carries nothing.** No `tools` field anywhere in
  `proto/matrix/inference/v1`, and none in `inference.InferenceRequest`, which is
  `Model`, `Prompt`, `Messages`, `MaxTokens`, `Temperature` and nothing else.
- **The `/v1` door already refuses them honestly.** `mapRole` answers
  `role "tool" is not supported on this network yet` for `tool`, `function` and
  `developer`. A client sending an OpenAI tool transcript gets a clear refusal,
  not a silent drop.
- **Nothing in the web app claims otherwise.** A search of the whole tree for a
  tool-call claim finds only the handoff line itself.

So this is a feature to add, not a false claim to correct. That matters for
sequencing: nothing is broken while it does not exist.

---

## Why consensus does not need to change

Consensus sees three things about an inference job: the reservation, the funding,
and the settlement. All three are denominated in **units**, and units are already
metered by the seller and clamped to the reservation.

A tool call changes what the model does between those events. It does not change:

- **how state is applied** — a settlement is a transfer, whatever produced the
  completion;
- **what makes a block valid** — the request content is never in a block;
- **what a node must agree with its peers about** — no node but the seller ever
  sees the transcript.

Those are the only three grounds on which this repository has ever introduced a
protocol version (see `ProtocolVersionSpendBudgets`,
`ProtocolVersionInferenceEscrow`, `ProtocolVersionCanonicalEthRecipient`). None of
them applies. **No protocol version, no activation height, no rollout gate.**

---

## What DOES have to change, and why it is the real cost

### The buyer's signature does not currently cover the tools

`RunAuthorization` is EIP-712 over
`(buyer, provider, model, promptDigest, timestamp)`, and `promptDigest` is
`requestDigest`, which hashes **only the count of messages and each message's role
and content**, length-prefixed.

A tool definition is an instruction to the model about what it may do. If it rides
in the request but outside `promptDigest`, then a node can add, remove or rewrite
the tools on a request the buyer signed, and the signature still verifies. The
buyer authorised one question and the model is asked a different one — with the
buyer paying for it.

So **tools must be inside `requestDigest`.** That is the whole engineering cost of
this feature, and it is a compatibility break rather than a chain one:

- `requestDigest` gains the tool definitions, canonically ordered and
  length-prefixed like the messages already are.
- **Every client that computes the digest must change in the same commit.** There
  are two: `services/core/internal/inference/runauth.go` and the browser signer in
  `apps/web/src/lib/wallet/`. A request signed by the old rule does not verify
  under the new one.
- `Go len() counts bytes; JS .length counts UTF-16 code units.` A tool schema is
  JSON with user-chosen names, so this is exactly the trap the handoff already
  records: length-prefix over **bytes** on both sides, and add a cross-language
  vector to the tests that has a non-ASCII tool name in it.

Note what this exposes: `MaxTokens` and `Temperature` are **also** outside the
digest today, so a node can already change them on a signed request. The loss is
bounded — the charge is clamped to the reservation the buyer chose — but it is the
same class of hole, and it should be closed in the same change rather than left as
the one field a reader has to know about.

### A tool loop is several jobs, and nothing bounds the count

A tool call is not one request. The model returns a call, the CLIENT executes it,
and the result comes back as a new message. Each round trip is a **separate job**
with its own reservation and its own settlement, which the existing machinery
handles without change.

What it does not handle is the **number of iterations**. A budget's `perJobCap`
bounds one job; the budget's total amount is the only backstop on a loop. So an
agent that loops — a model that keeps calling a tool that keeps failing — drains a
budget through many individually legal jobs, and the buyer's only protection is the
number they typed into `/chat`.

That is a real gap, and it is the buyer's to close, not the seller's. Two options,
and the first is enough to start:

1. **The client bounds the loop.** It already executes the tools, so it already
   decides whether to continue. A maximum-iterations argument, defaulted, is one
   field and no protocol surface.
2. **The budget bounds it.** A `maxJobs` term inside the budget's name, checked at
   draw time. This is a consensus rule and a new term in the account id, so it is a
   protocol version — which is the one thing this design otherwise avoids
   entirely. Not worth it for the first version.

---

## Shape of the implementation

In dependency order. Each step is landable on its own.

1. **Close the digest hole.** Fold `MaxTokens` and `Temperature` into
   `requestDigest`, in both implementations, with a cross-language test vector.
   Ships alone and is worth having regardless of tool calls.
2. **Carry the tools.** `Tools []ToolDefinition` on `InferenceRequest` and the
   proto message; fold into `requestDigest` the same way; `mapRole` starts
   accepting `tool`.
3. **Return the calls.** `ToolCalls` on the completion, and the SSE path learns to
   stream a call rather than content. This is where the work is.
4. **Bound the loop client-side**, with a default, before anything is advertised.
5. **Only then say so in the UI.** A claim added before step 4 is a claim about an
   unbounded spend.

---

## What this design refuses

**A node executing tools on the buyer's behalf.** The client executes and the
seller only says what to call. A seller that ran the tool would need the buyer's
credentials for whatever the tool reaches, which is a custody story this network
has already declined once — see the decision not to broker purchases in
`handoff.md` task 3. Same answer, same reason.

**Tool definitions outside the signature.** Covered above. It would make the
cheapest attack on this network "add a tool to somebody else's signed request".
