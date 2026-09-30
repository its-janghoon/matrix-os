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

## What was actually built, and where it departed from the above

Steps 1 through 5 are done. Two things went differently, and both are worth
knowing before reading the code.

### The tool block in the digest is CONDITIONAL

The plan above says tools go into `requestDigest`, and they do — but appending a
zero tool count unconditionally would have changed the digest of every request
that uses no tools, which is nearly all of them. That is a second
compatibility-break three weeks after the first, for nothing.

So the block is emitted only when the request has a **tool surface**: some tool
offered, or some turn carrying tool metadata. A request without one hashes
byte-for-byte as it did in v0.5.8, so every signature made before this change is
still valid and the legacy arm can still be deleted on its original schedule.

That is not a hole in either direction. Signing without tools and running with
them is a different digest, so the added tools fail; signing with tools and
running without them is also different. Both are refused.

It did open one gap that had to be closed with it: the **legacy arm** verifies
against the pre-v0.5.8 digest, which covers the transcript and nothing else. Left
alone it would have accepted a legacy-signed request carrying any tools a node
liked. The legacy arm is therefore closed to tool requests — which costs nothing
real, since no client predating tools can be sending them.

### Two things the plan did not mention had to move with it

**The overbilling ceiling.** A tool schema is prompt tokens the seller really
spent — several times the size of the question — and a call is completion tokens.
The ceiling counted neither, so an honest tool-calling bill was clamped DOWN,
underpaying the seller for work the buyer received. Same failure the reasoning
text caused before it was counted, same direction. `MaxUnitsForResponse` counts
both, and the browser's `maxUnitsFor` had to move in step: the node uses it to
clamp and the browser uses it to REFUSE, so the two disagreeing means a reader is
told their seller is overcharging, after funding, and forfeits the reservation.

**The idempotency fingerprint.** Two requests identical but for their tools ask
the model to do different things. Left uncovered, a retry with one tool removed
would have been served the other request's answer.

### The loop bound is a ceiling, not a suggestion

`runWithTools` defaults to 4 round trips and caps any caller at 12. The cap is
applied to what the caller asks for rather than trusted from it, because the
caller is a page and a page can be wrong. A loop that runs out reports
`exhausted` with the units it spent rather than throwing — the reader paid for
those jobs and is owed both the partial work and the fact that it was cut off.

### The tool path does not fall back

The runtime falls back from the escrowed path to `chat()` when a node has not
activated the escrow rules. `chat()` carries no tools, so falling back on a tool
run would answer the question with a model that cannot search and no sign that
anything was lost — which is the exact failure this feature exists to remove. The
tool path surfaces the error instead.

### There is no keyless general web search, so there is no web_search tool

The first attempt held a `BRAVE_SEARCH_API_KEY` on a route in this app. The second
moved the key into the reader's own browser. Both worked; neither shipped, because
a key is a key — either one person pays for everyone's searches through an
unauthenticated route, or every reader has to go and get a credential before
`/chat` can look anything up.

So the question became whether it can be done with **no key at all**, and the
answer was measured rather than assumed:

| candidate | keyless | CORS | usable |
|---|---|---|---|
| Brave | no | `405`, no headers | no |
| Serper, Tavily | no | yes | key required |
| DuckDuckGo Instant Answer | yes | `*` | **no** — returns an empty object for an ordinary query and for a bare entity alike; it answers a narrow set of canned questions, not searches |
| public SearXNG instances | yes | none | **no** — `403`/`429`, or `200` with an HTML page because `format=json` is disabled, and no CORS header |

General web search therefore does not exist on these terms, and no `web_search`
tool is offered. A tool that cannot work is worse than an absent one: the model
calls it, the call fails, and the reader pays for a round trip that could never
have succeeded.

### What IS offered, keyless: weather and wikipedia

Two APIs are keyless, send `access-control-allow-origin: *`, and between them
cover the questions that sent a reader looking for search in the first place —
what the weather is, and what a thing is. `apps/web/src/lib/wallet/keylessTools.ts`.

Nothing to configure, nothing that can be missing, so both are offered on every
send. Their descriptions say what they do **not** cover — news, prices, who holds
a role now, a specific page — so the model answers from its own knowledge there
rather than calling something that cannot help.

**The trap worth recording.** Open-Meteo's geocoder resolves `대구` to a village
in **North Korea** before the city of 2.4 million, and returns nothing at all for
`서울` while `Seoul` works. A tool that reported only a temperature would have
answered the wrong country's weather with nothing anywhere to notice. So the
resolved place, its region, its country and its timezone are part of the answer,
the rejected candidates are listed, and the tool's own description tells the model
to romanize the name. The failure is made visible rather than made unlikely.

---

## What this design refuses

**A node executing tools on the buyer's behalf.** The client executes and the
seller only says what to call. A seller that ran the tool would need the buyer's
credentials for whatever the tool reaches, which is a custody story this network
has already declined once — see the decision not to broker purchases in
`handoff.md` task 3. Same answer, same reason.

**Tool definitions outside the signature.** Covered above. It would make the
cheapest attack on this network "add a tool to somebody else's signed request".
