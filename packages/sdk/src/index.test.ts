import { describe, expect, it, vi } from 'vitest';

import manifest from './rpc-manifest.json';
import { DEFAULT_ENDPOINT, MatrixClient, MatrixError, toBase64 } from './index';

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
});

describe('decoding', () => {
  it('maps a provider from proto JSON', async () => {
    const { client: c } = client([
      {
        body: {
          providers: [
            { id: 'gpu-1', capacity: '20', pricePerUnit: '5', available: '17', origin: 'PROVIDER_ORIGIN_LOCAL', peerId: '' },
          ],
        },
      },
    ]);

    const [provider] = await c.listProviders();

    expect(provider).toEqual({
      id: 'gpu-1',
      capacity: 20n,
      pricePerUnit: 5n,
      available: 17n,
      origin: 'PROVIDER_ORIGIN_LOCAL',
      peerId: '',
    });
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
            status: 'JOB_STATUS_COMPLETED',
            createdAt: '2026-01-01T00:00:00Z',
            updatedAt: '2026-01-01T00:00:01Z',
          },
        },
      },
    ]);

    const job = await c.completeJob('job-1');

    expect(job.price).toBe(21n);
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
    ],
    'matrix.inference.v1.InferenceService': ['SubmitInferenceJob', 'FulfillInferenceJob', 'GetInferenceJob'],
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
