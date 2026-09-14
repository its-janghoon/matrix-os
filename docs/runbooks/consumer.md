# Buying inference on Matrix OS

This runbook is for the side that spends rather than earns: a developer putting
a model behind an app, a backend integrating a chat endpoint, or anyone who wants
a completion and is willing to pay for it in native MATRIX.

It assumes a network that is already running and at least one provider selling
the model you want. The provider side is [`gpu-provider.md`](gpu-provider.md).

## What you are buying, and what it costs

One unit is one token, prompt and completion summed, as reported by the model
server that did the work. That number multiplies the provider's `price_per_unit`,
quoted in native base units at 9 decimals. There is no per-request fee and no
per-second charge.

On top of that the network takes `consensus.fee_basis_points` out of the
settlement transfer. It is a percentage of value moved, not gas, so it does not
vary with how busy the chain is and there is nothing to bid up.

Two things follow that are worth knowing before you write any code:

- **A wallet will show gas as free, and your transfers are not free.** There is
  no EVM here to meter, so the wallet's zero is honest about gas and silent
  about the protocol fee. Read the fee rate from the network, not from MetaMask.
- **You pay for the prompt too.** A long system prompt resent on every turn is
  billed on every turn. That is ordinary for token pricing and it is the first
  thing to look at when a bill surprises you.

## Step 1: get MATRIX into an account you control

Buying needs a funded native account. Three ways in, in rough order of how most
people will arrive:

**From wMATRIX you already hold.** Burn it on Base naming your native recipient
account. The burn event is the authority the native bridge uses to release the
escrowed native MATRIX to you. The amount must be an exact multiple of
`ERC20_PER_NATIVE_UNIT`, because 18 decimals do not divide evenly into 9 and the
contract refuses a value that would round.

**From someone who already has it.** An ordinary transfer. Now that the chain
speaks EIP-155, that can be a MetaMask send to your address, and it settles in
one confirmation.

**From a faucet or an allocation**, on a test network or at launch.

Check it landed:

```sh
matrix --api-key <key> balance --account eth:0x<your-address>
```

or, from anything that speaks Ethereum JSON-RPC, `eth_getBalance` against the
node's `eth_rpc.addr`.

## Step 2: pick a door

Two ways to buy, and the difference is who holds your key. This is the decision
that shapes your integration, so make it before writing code rather than after.

| | Hosted wallet | Self-custody |
| --- | --- | --- |
| Protocol | OpenAI-compatible HTTP: `POST /v1/chat/completions` | `RunInferenceJob` over Connect |
| Your credential | An API key the node operator issued you | Your own signing key |
| Who can settle for you | The node, using a key it holds for your account | Only you |
| Client libraries | Every OpenAI SDK, unchanged | The Matrix client or generated Connect stubs |
| Streaming | Yes, SSE, as the SDKs expect | Yes |
| Right for | An app whose operator also runs the node | A dApp, or any buyer who is not the operator |

**The OpenAI-compatible route is custodial, and there is no signed variant of
it.** That protocol identifies a caller by the API key alone and carries no
buyer field, so the key is what says whose balance to charge - and the node has
to hold that account's signing key to settle for it. A key with no account
attached can drive every other surface and cannot buy inference.

So the comfortable door is only open to you if the node operator custodies an
account for you. If you are buying from a GPU owner you have never met, that is
not a reasonable thing to ask of either of you, and the second door is the one
you want.

## Path A: the OpenAI SDK, unchanged

Point `base_url` at the provider's node and use the key they issued. Nothing else
about your code changes.

```python
from openai import OpenAI

client = OpenAI(
    base_url="https://<provider-node-host>:9093/v1",
    api_key="<the key the operator issued you>",
)

resp = client.chat.completions.create(
    model="<the model the provider advertises>",
    messages=[{"role": "user", "content": "hello"}],
)
```

Ask the endpoint what it sells rather than guessing the model string:

```sh
curl -s https://<provider-node-host>:9093/v1/models \
  -H "Authorization: Bearer <key>" | jq '.data[].id'
```

Model names are matched case-insensitively, and everything else about the name
has to be exact.

### Set an idempotency key. This is the part backends get wrong.

**Every OpenAI SDK retries on a connection error or a 5xx by default, and so does
any proxy you put in front of this.** Without a key, a retry is a second job and
a second charge, and the buyer whose network blinked has no way to tell.

```python
resp = client.chat.completions.create(
    model="...",
    messages=[...],
    extra_headers={"Idempotency-Key": str(uuid.uuid4())},
)
```

It is the header name Stripe established, so if you already set one somewhere in
your stack this is not a new concept. Rules worth knowing:

- **Generate the key once per logical request, not per attempt.** A key generated
  inside the retry loop deduplicates nothing.
- **A replay is told the original job id and refused. It is not handed a cached
  completion.** Storing prompts and completions on a provider's disk for a day is
  a retention decision a provider should make deliberately, so nothing here makes
  it for them. Your retry gets an answer about what happened, not a second copy
  of the text.
- **Reusing a key with a different request is an error, not a silent wrong
  answer.** The key is bound to the request it was first used with.
- **A failed job releases its key**, because a failed job charged nobody and a
  retry is what you actually want.
- Keys are scoped per buyer, so yours cannot collide with anyone else's.

Requests are deliberately **not** deduplicated by content. Two identical prompts
are an ordinary thing to send - a regenerate, a second sample at temperature -
and refusing the second to fix an accidental retry would break a legitimate use.
Only an explicit key deduplicates.

### Streaming

`"stream": true` works and arrives as SSE, the way the SDKs expect, when the
provider runs a backend that can stream (vLLM can; Ollama does not, and the node
falls back to a single response rather than failing).

Token counts still come from the server that did the work. When an upstream does
not report usage on a streamed response the node derives the count locally, which
is a documented approximation of somebody else's tokeniser - honest and
deterministic, and still not the number the real tokeniser produced.

### Timeouts

The provider's `request_timeout` bounds one upstream request end to end. Ask what
theirs is if you intend to request long completions, and set your client's
timeout above it rather than below: a client that gives up first turns a
completion you are about to be charged for into one you never see.

## Path B: self-custody, where nobody holds your key

Used when the node running the model is not yours and you are not willing to make
its operator a custodian of your balance. The provider has to have
`connect.signed_writes: true` on; ask, because with it off this door is shut.

It is two signatures, and they answer different questions at different moments:

1. **A `RunAuthorization`**, signed before the work: *I am the buyer and I am
   asking for this work.* It covers your public key, the provider, the model, a
   digest of the exact prompt, and a timestamp - so it authorises exactly one run
   and cannot be lifted onto a different provider or a different prompt. Its
   timestamp has to be within two minutes of the node's clock.
2. **The settlement transfer**, signed after: *I accept this bill.* The node runs
   the model, returns the exact transfer to sign, and withholds the completion
   until you sign it.

Without the first one, `buyer` would be just a string: anyone could name your
funded account, have a provider do the work, and never sign for it. Your balance
would be untouched and the provider would have worked for free with its capacity
held until the request expired. An API key does not fix that, because in a
browser the key is in the page for anyone to read.

Both signatures accept an Ethereum key as well as an ed25519 one - the key length
decides which - so a MetaMask user is a first-class buyer here.

The CLI does the whole exchange, and is the quickest way to see the shape of it
before you implement it:

```sh
matrix inference submit \
  --buyer eth:0x<your-address> \
  --provider <the provider's id> \
  --model <model> \
  --prompt "hello" \
  --client-signed \
  --wallet <path to your wallet file>
```

Drop `--client-signed` and the node signs the payment with a key it holds for
you, which is Path A's trust model wearing a different interface. If you are
implementing Path B, `--client-signed` is the flag you are reproducing.

## Step 3: check it before you ship it

- **Send one real request and read the settled units**, rather than trusting the
  estimate. The reservation is an estimate; the charge comes from the tokens the
  backend reported.
- **Confirm the balance moved by what you expected**, net of the protocol fee.
  Both numbers are readable: the transfer and the fee rate.
- **Kill the connection mid-request and retry with the same idempotency key.**
  You should be told about the original job, not charged twice. If you are
  charged twice, your key is being generated in the wrong place.
- **Ask what happens when the provider goes down.** A node suspends a backend
  from the order book on the first failed health probe, so a crashed runner stops
  winning routing decisions. That protects you from reservations nobody can
  serve; it does not give you a second provider unless one exists.
- **Verify the chain is what you were told**, if you are moving enough money to
  care: `matrix_getChainInfo` reports the chain id, height, head and state root,
  and `matrix_getAccountProof` proves your own balance against that root without
  anyone handing you the whole ledger.

## What this chain is not

It speaks Ethereum's account, signature and transaction formats. It runs **no
EVM**: contract creation and calldata are refused rather than ignored, and
`eth_call` says so. If your integration plan has a contract in it, that plan does
not work here. "EIP-155 compatible accounts and transactions" is true; "an EVM
chain" is not.
