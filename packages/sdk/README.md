# matrix-os-sdk

A TypeScript client for a Matrix OS node: the compute marketplace and the LLM
inference API, over HTTP.

**Not published to npm yet.** Use it from this repository:

```sh
# from your project
yarn add file:/path/to/matrix-os/packages/sdk
```

Nothing here claims a package name on a registry we do not control. When it is
published, this line changes and the import stays the same.

## What it talks to

A node serves its market and inference services over the Connect protocol's
unary JSON form on port `9093` - a plain `POST` to
`/{package}.{Service}/{Method}` (see
[`internal/connectapi`](../../services/core/internal/connectapi)). Raw gRPC
rides on HTTP/2 trailers no browser can produce, which is why that endpoint
exists and why this client works from a browser, a dApp front end, Node, Deno,
Bun or a WebView. It has no dependencies.

### If all you want is chat completions

The same node also serves the OpenAI protocol at `/v1/chat/completions` and
`/v1/models`, so for LLM inference alone you do not need this SDK at all - point
the `openai` package at the node and change nothing else:

```ts
import OpenAI from 'openai';

const client = new OpenAI({
  baseURL: 'http://127.0.0.1:9093/v1', // your node, or any node
  apiKey: process.env.MATRIX_API_KEY,
});

const r = await client.chat.completions.create({
  model: 'llama-3.3-70b',
  messages: [{ role: 'user', content: 'hello' }],
});
```

Streaming works there too (`stream: true`), and this SDK also wraps the native
streaming RPC:

```ts
for await (const chunk of client.streamInferenceJob({
  buyer: myAccountId, provider: 'gpu-1', model: 'llama-3.3-70b', prompt: 'hello',
})) {
  if (chunk.job) console.log('settled', chunk.job.units);
  else process.stdout.write(chunk.delta);
}
```

Breaking out of that loop cancels the request, which aborts the run so the
provider stops generating tokens nobody will read. The last chunk carries the
settled job - do not treat a stream as paid for until you have it - and
`streamedOneShot` tells you when the provider's backend could not really stream
and the whole answer arrived as one delta.

Streaming is the hosted path only. On the client-signed path the completion is
withheld until you sign the invoice, and streaming it out first would give away
the only thing holding you to the bargain.

That route needs an API key whose `account` is set (under
`security.api_keys`), because the OpenAI protocol carries no buyer field and the
key is the only thing that can say whose on-chain balance to charge. The request
names a model, not a provider: it is routed to the cheapest provider advertising
that model with capacity to spare. Streaming is not supported yet and a
`stream: true` request is refused rather than answered with one whole body.

Use this SDK when you need the marketplace itself - registering providers,
submitting and inspecting jobs, balances, signed transfers, agents.

### If the node must not hold your key

The route above and `submitInferenceJob` / `fulfillInferenceJob` both settle with
a key the node holds for you. That is right for a node you run and wrong for a
public endpoint, where it makes the operator a custodian of your balance, or a
dApp, where it makes wallet login decorative.

`runInferenceJob` / `settleInferenceJob` need no key on the node. Pre-signing the
transfer cannot work - a transaction signs over an exact amount, and the price of
an inference is not known until the work is done - so the node runs the model,
hands back the exact transfer to sign, and withholds the completion until you
have signed it:

```ts
const { payment } = await client.runInferenceJob({
  buyer: myAccountId,
  provider: 'gpu-1',
  model: 'llama-3.3-70b',
  messages: [{ role: 'CHAT_ROLE_USER', content: 'hello' }],
});

// payment.amount and payment.to are what you are authorising. Show them.
const signature = await sign(paymentSigningBytes(payment, publicKey));

const job = await client.settleInferenceJob({ payment, fromPublicKey: publicKey, signature });
job.completion; // handed over only now
```

`paymentSigningBytes` produces the canonical bytes and nothing else - signing is
left to your wallet, passkey-derived key, WebCrypto Ed25519 or hardware signer,
because pinning one of them would make this package care about key custody. The
bytes are pinned against the node's own encoding by a golden vector in the tests;
a one-byte drift would make every signature fail and look like a bad key.

Every signable field must match the invoice. A transfer of one base unit to an
account you control verifies perfectly and is refused.

## Use

```ts
import { MatrixClient } from 'matrix-os-sdk';

const matrix = new MatrixClient({ endpoint: 'http://127.0.0.1:9093' });

// Throws a MatrixError if no node answers or it is not serving; returns nothing
// when it is.
await matrix.ping();

const providers = await matrix.listProviders({ includeRemote: true });
for (const p of providers) {
  console.log(`${p.id}: ${p.available}/${p.capacity} units at ${p.pricePerUnit} each`);
}

const job = await matrix.submitJob({ buyer: myAccountId, provider: 'gpu-1', units: 3n });
const done = await matrix.completeJob(job.id);
console.log(done.status, done.price); // JOB_STATUS_COMPLETED 21n
```

`completeJob` settles through consensus: the payment is a transfer signed by
the buyer that a quorum commits. The node therefore needs the buyer's signing
key, which it resolves from the wallet files under `~/.matrix`, and refuses a
job whose buyer it holds no key for with a `failed_precondition` `MatrixError`,
leaving the reservation intact so you can cancel it. Paying out of an account
without its owner's signature is what that refusal exists to prevent.

Inference:

```ts
const inference = await matrix.submitInferenceJob({
  // A freshly initialized node registers this GPU-free echo provider on the
  // market, so the path works before you have a model server. Point it at your
  // own provider once you do.
  provider: 'demo-inference-provider',
  buyer: myAccountId,
  model: 'demo',
  prompt: 'Explain a lock-and-mint bridge in two sentences.',
  maxTokens: 256,
});
const finished = await matrix.fulfillInferenceJob(inference.id);
console.log(finished.completion);
```

Inference settles the same way `completeJob` does, so the same signing-key
requirement applies to the buyer.

## Bridge to Base

`submitBridgeLock` submits a caller-signed native transfer to the canonical
`bridge/lock/<address>` recipient, requires that consensus both committed and
applied it, and returns the lock id used to collect validator attestations. The
SDK never creates, imports, or holds the private key: pass the sender account id,
its 32-byte ed25519 public key or raw 20-byte EVM address, and the signature you
obtained from your own signer. `collectLockAttestations` polls only the explicit
validator clients/endpoints you provide, retries only a not-yet-visible
`not_found`, and validates field agreement, amount conversion, and claimed
attestor uniqueness. It does not claim cryptographic verification; the Base
contract adapter performs that check.

The native lock does not pay Base gas. The user (or a relayer they arrange) pays
the Base transaction gas when submitting the wrapped-token mint. The attestor
committee and threshold are fixed for each bridge contract deployment; changing
the validator committee requires a new deployment/migration rather than an SDK
configuration update.

Before an irreversible lock, call `bridgeReadiness` on every configured endpoint
with the same fresh random 32-byte challenge. Each response signs its chain ID,
normalized contract, attestor address, protocol minimum, and echoed challenge
under the readiness-only domain `MATRIX_BRIDGE_READINESS_V1`:

```ts
const challenge = crypto.getRandomValues(new Uint8Array(32));
const proof = await matrix.bridgeReadiness(challenge);
```

The SDK only transports this proof. A browser or contract adapter must recover
the signer, reject duplicate or unregistered addresses, compare every deployment
field, and require the live on-chain threshold before asking a wallet to sign a
native payment. The readiness digest is deliberately distinct from the mint
attestation digest and cannot authorize a mint.

## Amounts are `bigint`

Native MATRIX has 9 decimals and a cap of 1,000,000,000 whole coins, so a
balance reaches 1e18 base units - two orders of magnitude past
`Number.MAX_SAFE_INTEGER`. Every 64-bit field is therefore a `bigint`:

```ts
const { balance } = await matrix.getBalance(account);
balance;            // 1000000000000000001n, exactly
Number(balance);     // 1000000000000000000  <- silently wrong
```

The wire format already carries these fields as strings, which is what makes the
exact conversion possible. Requests accept `bigint` or `number` and are encoded
as strings.

## Errors

Every failure is a `MatrixError` with a `code` you can branch on, rather than a
message to pattern-match:

```ts
import { MatrixError } from 'matrix-os-sdk';

try {
  await matrix.submitJob({ buyer, provider: 'nope', units: 1n });
} catch (err) {
  if (err instanceof MatrixError) {
    if (err.code === 'not_found') { /* no such provider */ }
    if (err.code === 'unauthenticated') { /* the node has ACLs on; pass apiKey */ }
    if (err.code === 'unreachable') { /* nothing answered at that endpoint */ }
  }
}
```

## Authentication

A node started with ACLs enabled requires an API key on every call:

```ts
const matrix = new MatrixClient({ endpoint, apiKey: process.env.MATRIX_API_KEY });
```

It travels as `Authorization: Bearer <key>`, the same credential the gRPC
surface takes, checked by the same authenticator.

## Reaching a method this version does not wrap

`call` is public for exactly that:

```ts
const raw = await matrix.call('matrix.market.v1.MarketService', 'ListJobs', { buyer: '' });
```

## How this stays honest

`src/rpc-manifest.json` is generated from the node's own gRPC service
descriptors:

```sh
cd services/core && go run ./cmd/rpcmanifest > ../../packages/sdk/src/rpc-manifest.json
```

Two tests use it. A Go test fails when the checked-in copy is stale, and an SDK
test fails when a served method has no wrapper here - or when a wrapper points
at a method the node does not serve. That pair exists because this repository
already shipped a client for a protocol its daemon did not serve; a mismatch
should be a red test, not a support ticket.

## Develop

```sh
corepack yarn install
corepack yarn test
corepack yarn typecheck
corepack yarn build      # -> dist/
```
