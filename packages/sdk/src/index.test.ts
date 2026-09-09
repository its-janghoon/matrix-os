import { describe, expect, it, vi } from 'vitest';

import manifest from './rpc-manifest.json';
import {
  DEFAULT_ENDPOINT,
  MatrixClient,
  MatrixError,
  fromBase64,
  paymentSigningBytes,
  runAuthorizationSigningBytes,
  toBase64,
} from './index';

/** A fetch stub that records calls and replays canned responses. */
function stubFetch(responses: Array<{ status?: number; body: unknown }>) {
  const calls: Array<{ url: string; init: RequestInit }> = [];
  let i = 0;
  const fetchMock = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
    calls.push({ url: String(url), init: init ?? {} });
    const next = responses[Math.min(i, responses.length - 1)];
    i += 1;
    return {
      ok: (next?.status ?? 200) < 400,
      status: next?.status ?? 200,
      text: async () => (typeof next?.body === 'string' ? next.body : JSON.stringify(next?.body ?? {})),
    } as unknown as Response;
  });
  return { fetchMock: fetchMock as unknown as typeof globalThis.fetch, calls };
}

function client(responses: Array<{ status?: number; body: unknown }>, apiKey?: string) {
  const { fetchMock, calls } = stubFetch(responses);
  return {
    calls,
    client: new MatrixClient({
      endpoint: 'http://node.test:9093',
      ...(apiKey ? { apiKey } : {}),
      fetch: fetchMock,
    }),
  };
}

describe('transport', () => {
  it('posts Connect-protocol JSON to the service-qualified path', async () => {
    const { client: c, calls } = client([{ body: { providers: [] } }]);

    await c.listProviders({ includeRemote: true });

    expect(calls[0]!.url).toBe('http://node.test:9093/matrix.market.v1.MarketService/ListProviders');
    expect(calls[0]!.init.method).toBe('POST');
    const headers = calls[0]!.init.headers as Record<string, string>;
    expect(headers['Content-Type']).toBe('application/json');
    expect(headers['Connect-Protocol-Version']).toBe('1');
    expect(JSON.parse(String(calls[0]!.init.body))).toEqual({ includeRemote: true });
  });

  it('sends the api key as a bearer token when one is configured', async () => {
    const { client: c, calls } = client([{ body: { account: 'a', balance: '1' } }], 'k3y');

    await c.fundAccount({ account: 'a', amount: 1 });

    const headers = calls[0]!.init.headers as Record<string, string>;
    expect(headers.Authorization).toBe('Bearer k3y');
  });

  it('sends no Authorization header when no key is configured', async () => {
    const { client: c, calls } = client([{ body: { account: 'a', balance: '0' } }]);

    await c.getBalance('a');

    const headers = calls[0]!.init.headers as Record<string, string>;
    expect(headers.Authorization).toBeUndefined();
  });

  it('defaults to the local node', () => {
    expect(new MatrixClient({ fetch: (() => {}) as unknown as typeof fetch }).endpoint).toBe(DEFAULT_ENDPOINT);
  });

  it('strips a trailing slash from the endpoint so paths never double up', async () => {
    const { fetchMock, calls } = stubFetch([{ body: { providers: [] } }]);
    const c = new MatrixClient({ endpoint: 'http://node.test:9093/', fetch: fetchMock });

    await c.listProviders();

    expect(calls[0]!.url).toBe('http://node.test:9093/matrix.market.v1.MarketService/ListProviders');
  });
});

describe('64-bit amounts', () => {
  it('decodes to bigint, exactly, past Number.MAX_SAFE_INTEGER', async () => {
    // The supply cap is 1e18 base units. Rounding that through a double would
    // lose real money, which is why these fields are bigint.
    const huge = '1000000000000000001';
    const { client: c } = client([{ body: { account: 'whale', balance: huge } }]);

    const balance = await c.getBalance('whale');

    expect(balance.balance).toBe(1000000000000000001n);
    expect(balance.balance.toString()).toBe(huge);
    // What a double would have done - and note the literal `1000000000000000001`
    // written in source is itself rounded, which is the whole hazard.
    expect(String(Number(huge))).not.toBe(huge);
    expect(String(Number(huge))).toBe('1000000000000000000');
  });

  it('encodes bigint requests as strings, which is what proto JSON expects', async () => {
    const { client: c, calls } = client([{ body: { provider: {} } }]);

    await c.registerProvider({ id: 'gpu', capacity: 10n, pricePerUnit: 12345678901234567890n });

    expect(JSON.parse(String(calls[0]!.init.body))).toEqual({
      id: 'gpu',
      capacity: '10',
      pricePerUnit: '12345678901234567890',
    });
  });

  it('treats a missing 64-bit field as zero rather than NaN', async () => {
    const { client: c } = client([{ body: { account: 'ghost' } }]);
    const balance = await c.getBalance('ghost');
    expect(balance.balance).toBe(0n);
  });

  it('encodes updateProviderQuote metadata and decodes the refreshed quote', async () => {
    const { client: c, calls } = client([{ body: { provider: {
      id: 'gpu', pricePerUnit: '12', costPerUnit: '8', markupBasisPoints: 125,
      quoteId: 'quote-2', quoteVersion: '9', observedAt: '2026-02-01T00:00:00Z',
      validUntil: '2026-02-02T00:00:00Z',
    } } }]);

    const provider = await c.updateProviderQuote({
      id: 'gpu',
      pricePerUnit: 12n,
      costPerUnit: 8n,
      markupBasisPoints: 125,
      quoteId: 'quote-2',
      quoteVersion: 9n,
      observedAt: '2026-02-01T00:00:00Z',
      validUntil: '2026-02-02T00:00:00Z',
    });

    expect(calls[0]!.url).toBe('http://node.test:9093/matrix.market.v1.MarketService/UpdateProviderQuote');
    expect(JSON.parse(String(calls[0]!.init.body))).toEqual({
      id: 'gpu',
      pricePerUnit: '12',
      costPerUnit: '8',
      markupBasisPoints: 125,
      quoteId: 'quote-2',
      quoteVersion: '9',
      observedAt: '2026-02-01T00:00:00Z',
      validUntil: '2026-02-02T00:00:00Z',
    });
    expect(provider.costPerUnit).toBe(8n);
    expect(provider.quoteVersion).toBe(9n);
  });
});

describe('decoding', () => {
  it('maps a provider from proto JSON', async () => {
    const { client: c } = client([
      {
        body: {
          providers: [
            {
              id: 'gpu-1',
              capacity: '20',
              pricePerUnit: '5',
              costPerUnit: '4',
              markupBasisPoints: 250,
              quoteId: 'quote-1',
              quoteVersion: '7',
              observedAt: '2026-01-01T00:00:00Z',
              validUntil: '2026-01-02T00:00:00Z',
              available: '17',
              origin: 'PROVIDER_ORIGIN_LOCAL',
              peerId: '',
              models: ['llama-3.3-70b', 'qwen-2.5-72b'],
            },
          ],
        },
      },
    ]);

    const [provider] = await c.listProviders();

    expect(provider).toEqual({
      id: 'gpu-1',
      capacity: 20n,
      pricePerUnit: 5n,
      costPerUnit: 4n,
      markupBasisPoints: 250,
      quoteId: 'quote-1',
      quoteVersion: 7n,
      observedAt: '2026-01-01T00:00:00Z',
      validUntil: '2026-01-02T00:00:00Z',
      available: 17n,
      origin: 'PROVIDER_ORIGIN_LOCAL',
      peerId: '',
      models: ['llama-3.3-70b', 'qwen-2.5-72b'],
    });
  });

  it('reads a compute-only provider, which advertises no models, as an empty list', async () => {
    const { client: c } = client([
      {
        body: {
          providers: [
            { id: 'cpu-1', capacity: '4', pricePerUnit: '1', available: '4', origin: 'PROVIDER_ORIGIN_LOCAL', peerId: '' },
          ],
        },
      },
    ]);

    const [provider] = await c.listProviders();

    expect(provider!.models).toEqual([]);
  });

  it('sends a model filter only when one is given, so an unfiltered call is unchanged', async () => {
    const { client: c, calls } = client([{ body: { providers: [] } }, { body: { providers: [] } }]);

    await c.listProviders();
    await c.listProviders({ model: 'Llama-3.3-70B' });

    expect(JSON.parse(String(calls[0]!.init.body))).toEqual({ includeRemote: false });
    expect(JSON.parse(String(calls[1]!.init.body))).toEqual({ includeRemote: false, model: 'Llama-3.3-70B' });
  });

  it('maps a job, including the fixed price', async () => {
    const { client: c } = client([
      {
        body: {
          job: {
            id: 'job-1',
            buyer: 'b',
            provider: 'p',
            units: '3',
            price: '21',
            pricePerUnit: '7',
            quoteId: 'quote-1',
            quoteVersion: '4',
            quoteObservedAt: '2026-01-01T00:00:00Z',
            quoteValidUntil: '2026-01-02T00:00:00Z',
            status: 'JOB_STATUS_COMPLETED',
            createdAt: '2026-01-01T00:00:00Z',
            updatedAt: '2026-01-01T00:00:01Z',
          },
        },
      },
    ]);

    const job = await c.completeJob('job-1');

    expect(job.price).toBe(21n);
    expect(job.pricePerUnit).toBe(7n);
    expect(job.quoteId).toBe('quote-1');
    expect(job.quoteVersion).toBe(4n);
    expect(job.quoteObservedAt).toBe('2026-01-01T00:00:00Z');
    expect(job.quoteValidUntil).toBe('2026-01-02T00:00:00Z');
    expect(job.status).toBe('JOB_STATUS_COMPLETED');
    expect(job.createdAt).toBe('2026-01-01T00:00:00Z');
  });

  it('returns an empty list for a response with no items', async () => {
    const { client: c } = client([{ body: {} }]);
    expect(await c.listJobs()).toEqual([]);
  });
});

describe('errors', () => {
  it('surfaces the endpoint code so a caller can branch on it', async () => {
    const { client: c } = client([{ status: 404, body: { code: 'not_found', message: 'provider not found' } }]);

    await expect(c.submitJob({ buyer: 'b', provider: 'nope', units: 1 })).rejects.toMatchObject({
      name: 'MatrixError',
      code: 'not_found',
      message: 'provider not found',
      method: 'matrix.market.v1.MarketService/SubmitJob',
    });
  });

  it('reports unauthenticated distinctly, so a caller knows to supply a key', async () => {
    const { client: c } = client([{ status: 401, body: { code: 'unauthenticated', message: 'authentication required' } }]);

    await expect(c.getBalance('a')).rejects.toMatchObject({ code: 'unauthenticated' });
  });

  it('reports unreachable when nothing answers, rather than a protocol error', async () => {
    const fetchMock = vi.fn(async () => {
      throw new TypeError('fetch failed');
    }) as unknown as typeof globalThis.fetch;
    const c = new MatrixClient({ endpoint: 'http://dead.test:9093', fetch: fetchMock });

    const err = await c.listProviders().catch((e: unknown) => e);

    expect(err).toBeInstanceOf(MatrixError);
    expect((err as MatrixError).code).toBe('unreachable');
  });

  it('keeps the HTTP status as the story when the body is not from the node', async () => {
    const { client: c } = client([{ status: 502, body: '<html>bad gateway</html>' }]);

    const err = await c.getBalance('a').catch((e: unknown) => e as MatrixError);

    expect((err as MatrixError).code).toBe('internal');
    expect((err as MatrixError).message).toContain('502');
  });
});

describe('base64 helper', () => {
  it('encodes bytes the way a proto bytes field expects', () => {
    expect(toBase64(new Uint8Array([77, 97, 116, 114, 105, 120]))).toBe('TWF0cml4');
    expect(toBase64(new Uint8Array())).toBe('');
  });

  it('pads partial groups, and agrees with the platform encoder on every length', () => {
    expect(toBase64(new Uint8Array([1]))).toBe('AQ==');
    expect(toBase64(new Uint8Array([1, 2]))).toBe('AQI=');
    expect(toBase64(new Uint8Array([1, 2, 3]))).toBe('AQID');
    // Exhaustive against a known-good implementation, since a base64 bug would
    // corrupt a signature and be reported as "the node rejects my transfer".
    for (let len = 0; len <= 64; len++) {
      const bytes = new Uint8Array(len);
      for (let i = 0; i < len; i++) bytes[i] = (i * 37 + len) % 256;
      const expected = Buffer.from(bytes).toString('base64');
      expect(toBase64(bytes), `length ${len}`).toBe(expected);
    }
  });
});

describe('coverage against the served surface', () => {
  // The manifest is generated from the node's gRPC service descriptors
  // (services/core/cmd/rpcmanifest). Checking the client against it is what
  // stops this SDK from drifting away from the protos - the failure that left
  // the console speaking a protocol the daemon did not serve.
  const wrapped: Record<string, string[]> = {
    'matrix.market.v1.MarketService': [
      'RegisterProvider',
      'UpdateProviderQuote',
      'ListProviders',
      'SubmitJob',
      'GetJob',
      'ListJobs',
      'CompleteJob',
      'CancelJob',
      'GetBalance',
      'GetTransaction',
      'ListTransactions',
      'SubmitSignedTransfer',
      'FundAccount',
      'GetBridgeReadiness',
      'GetBridgeReconciliation',
      'GetLockAttestation',
    ],
    'matrix.inference.v1.InferenceService': [
      'SubmitInferenceJob',
      'FulfillInferenceJob',
      'GetInferenceJob',
      'RunInferenceJob',
      'SettleInferenceJob',
      'StreamInferenceJob',
    ],
    'matrix.agent.v1.AgentService': ['DeployAgent', 'ListAgents', 'GetAgent'],
  };

  it('wraps every method the node serves', () => {
    for (const service of manifest.services) {
      const have = wrapped[service.name] ?? [];
      const missing = service.methods.filter((m) => !have.includes(m));
      expect(missing, `${service.name} has unwrapped methods; add them to MatrixClient`).toEqual([]);
    }
  });

  it('wraps nothing the node does not serve', () => {
    for (const [service, methods] of Object.entries(wrapped)) {
      const served = manifest.services.find((s) => s.name === service);
      expect(served, `${service} is not in the manifest`).toBeDefined();
      const extra = methods.filter((m) => !served!.methods.includes(m));
      expect(extra, `${service} wraps methods the node does not serve`).toEqual([]);
    }
  });
});

describe('payment signing bytes', () => {
  // The golden vector is the output of token.Transaction.SigningBytes on the Go
  // side for the same input. If these ever disagree by one byte, every signature
  // a client produces is rejected by the node, and the failure looks like "bad
  // key" rather than "bad encoding" - which is exactly why this is pinned.
  const GOLDEN =
    'AAAAIAABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4fAAAACnByb3ZpZGVyLTEAAAAAB1vNFQAAAAAAAAAHGNL8IrtyxRUAAAAE3q2+7w==';

  it('matches the node byte for byte', () => {
    const fromPublicKey = new Uint8Array(32);
    for (let i = 0; i < 32; i++) fromPublicKey[i] = i;

    const bytes = paymentSigningBytes(
      {
        to: 'provider-1',
        amount: 123456789n,
        nonce: 7n,
        timestamp: 1788769228123456789n,
        prevHash: new Uint8Array([0xde, 0xad, 0xbe, 0xef]),
      },
      fromPublicKey,
    );

    expect(toBase64(bytes)).toBe(GOLDEN);
  });

  it('separates fields so no two distinct payments share a payload', () => {
    const key = new Uint8Array(32);
    const base = { amount: 1n, nonce: 0n, timestamp: 0n, prevHash: new Uint8Array() };

    // Without the length prefixes, "ab" + "c" and "a" + "bc" would collide, and
    // one signature would authorise paying either recipient.
    const a = toBase64(paymentSigningBytes({ ...base, to: 'ab' }, key));
    const b = toBase64(paymentSigningBytes({ ...base, to: 'a' }, key));
    expect(a).not.toBe(b);

    // And the amount is covered, which is the field a buyer most cares about.
    const dear = toBase64(paymentSigningBytes({ ...base, to: 'p', amount: 999n }, key));
    const cheap = toBase64(paymentSigningBytes({ ...base, to: 'p', amount: 1n }, key));
    expect(dear).not.toBe(cheap);
  });

  it('round-trips prevHash through base64', () => {
    const bytes = new Uint8Array([0x00, 0xff, 0x10, 0x7f, 0x80]);
    expect(Array.from(fromBase64(toBase64(bytes)))).toEqual(Array.from(bytes));
  });

  it('decodes a payment request from proto JSON', async () => {
    const { client: c } = client([
      {
        body: {
          payment: {
            jobId: 'job-1',
            from: 'buyer',
            to: 'gpu-1',
            amount: '36',
            nonce: '4',
            timestamp: '1788769228123456789',
            prevHash: toBase64(new Uint8Array([0xde, 0xad])),
            usage: { promptTokens: 7, completionTokens: 5, totalTokens: 12 },
            model: 'llama-3.3-70b',
            expiresAt: '2026-01-01T00:02:00Z',
          },
          job: { id: 'job-1', status: 'INFERENCE_JOB_STATUS_AWAITING_PAYMENT', completion: '' },
        },
      },
    ]);

    const { payment, job } = await c.runInferenceJob({
      buyer: 'buyer',
      provider: 'gpu-1',
      model: 'llama-3.3-70b',
      prompt: 'hi',
    });

    expect(payment.amount).toBe(36n);
    expect(payment.timestamp).toBe(1788769228123456789n);
    expect(Array.from(payment.prevHash)).toEqual([0xde, 0xad]);
    expect(payment.usage.totalTokens).toBe(12);
    // The completion is withheld at this point by design.
    expect(job.completion).toBe('');
    expect(job.status).toBe('INFERENCE_JOB_STATUS_AWAITING_PAYMENT');
  });

  it('sends the payment fields back verbatim when settling', async () => {
    const { client: c, calls } = client([{ body: { job: { id: 'job-1', completion: 'done' } } }]);

    const payment = {
      jobId: 'job-1',
      from: 'buyer',
      to: 'gpu-1',
      amount: 36n,
      nonce: 4n,
      timestamp: 1788769228123456789n,
      prevHash: new Uint8Array([0xde, 0xad]),
      usage: { promptTokens: 7, completionTokens: 5, totalTokens: 12 },
      model: 'llama-3.3-70b',
      expiresAt: '2026-01-01T00:02:00Z',
    };

    await c.settleInferenceJob({
      payment,
      fromPublicKey: new Uint8Array(32),
      signature: new Uint8Array(64),
    });

    // Any drift here is refused by the node as a payment mismatch, so the body
    // has to carry the invoice's own numbers unchanged.
    const body = JSON.parse(String(calls[0]!.init.body));
    expect(body.id).toBe('job-1');
    expect(body.amount).toBe('36');
    expect(body.nonce).toBe('4');
    expect(body.timestamp).toBe('1788769228123456789');
    expect(body.to).toBe('gpu-1');
  });
});

describe('run authorization signing bytes', () => {
  // Taken from inference.RunAuthorization.SigningBytes on the node. Same reason
  // as the payment vector: a one-byte drift rejects every signature and looks
  // like a bad key rather than a bad encoding.
  const GOLDEN =
    'AAAAJW1hdHJpeC9pbmZlcmVuY2UvcnVuLWF1dGhvcml6YXRpb24vdjEAAAAgAAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8AAAAFZ3B1LTEAAAANbGxhbWEtMy4zLTcwYgAAACC5plUi18KqH5VVzrz5BlHYN9SQ1bcVKQ66LcPWFJUKBxjS/CK7csUV';

  const key = (() => {
    const k = new Uint8Array(32);
    for (let i = 0; i < 32; i++) k[i] = i;
    return k;
  })();

  it('matches the node byte for byte', async () => {
    const bytes = await runAuthorizationSigningBytes({
      fromPublicKey: key,
      provider: 'gpu-1',
      model: 'llama-3.3-70b',
      timestamp: 1788769228123456789n,
      messages: [{ role: 'CHAT_ROLE_USER', content: 'hello' }],
    });
    expect(toBase64(bytes)).toBe(GOLDEN);
  });

  it('digests a bare prompt the same as the equivalent transcript', async () => {
    // An honest client that picked the other field must not get a mysterious
    // refusal, so both have to sign to the same bytes.
    const fromPrompt = await runAuthorizationSigningBytes({
      fromPublicKey: key,
      provider: 'gpu-1',
      model: 'llama-3.3-70b',
      timestamp: 1788769228123456789n,
      prompt: 'hello',
    });
    expect(toBase64(fromPrompt)).toBe(GOLDEN);
  });

  it('binds the authorization to the provider, model and prompt', async () => {
    const base = {
      fromPublicKey: key,
      provider: 'gpu-1',
      model: 'llama-3.3-70b',
      timestamp: 1n,
      prompt: 'hello',
    };
    const same = toBase64(await runAuthorizationSigningBytes(base));

    for (const changed of [
      { ...base, provider: 'gpu-2' },
      { ...base, model: 'qwen-2.5-72b' },
      { ...base, prompt: 'an enormously expensive question' },
      { ...base, timestamp: 2n },
    ]) {
      expect(toBase64(await runAuthorizationSigningBytes(changed))).not.toBe(same);
    }
  });

  it('separates transcript fields so two conversations cannot share a payload', async () => {
    const base = { fromPublicKey: key, provider: 'p', model: 'm', timestamp: 1n };
    const joined = toBase64(
      await runAuthorizationSigningBytes({ ...base, messages: [{ role: 'CHAT_ROLE_USER', content: 'ab' }] }),
    );
    const split = toBase64(
      await runAuthorizationSigningBytes({
        ...base,
        messages: [
          { role: 'CHAT_ROLE_USER', content: 'a' },
          { role: 'CHAT_ROLE_USER', content: 'b' },
        ],
      }),
    );
    expect(joined).not.toBe(split);
  });

  it('sends the authorization only when one is given', async () => {
    const { client: c, calls } = client([
      { body: { payment: {}, job: {} } },
      { body: { payment: {}, job: {} } },
    ]);

    await c.runInferenceJob({ buyer: 'b', provider: 'p', model: 'm', prompt: 'hi' });
    await c.runInferenceJob({
      buyer: 'b',
      provider: 'p',
      model: 'm',
      prompt: 'hi',
      authorization: { publicKey: new Uint8Array(32), timestamp: 7n, signature: new Uint8Array(64) },
    });

    expect(JSON.parse(String(calls[0]!.init.body)).authorization).toBeUndefined();
    const sent = JSON.parse(String(calls[1]!.init.body)).authorization;
    expect(sent.timestamp).toBe('7');
    expect(typeof sent.publicKey).toBe('string');
  });
});
