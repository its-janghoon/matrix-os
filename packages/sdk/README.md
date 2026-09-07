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
