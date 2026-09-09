import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  NodeError,
  getBridgeReadiness,
  getBridgeReconciliation,
  getLockAttestation,
  isBridgeReadinessTransportError,
  isNotFoundNodeError,
  submitSignedTransfer,
} from './node';

function response(body: unknown, ok = true, status = 200): Response {
  return { ok, status, text: async () => JSON.stringify(body) } as Response;
}

afterEach(() => vi.unstubAllGlobals());

describe('public bridge Connect wrappers', () => {
  it('encodes a caller-signed transfer and decodes settlement bigints', async () => {
    const fetchMock = vi.fn(async (_url: string, _init: RequestInit) => response({
      transaction: { index: '1', from: 'eth:0x1', to: 'bridge/lock/x', amount: '100', nonce: '7', blockHeight: '9' },
      committed: true,
      applied: true,
    }));
    vi.stubGlobal('fetch', fetchMock);
    const settled = await submitSignedTransfer('https://node.example', {
      fromPublicKey: new Uint8Array(20),
      to: 'bridge/lock/x',
      amount: 100n,
      nonce: 7n,
      prevHash: new Uint8Array(),
      signature: new Uint8Array(65),
      timestamp: 8n,
    });
    expect(settled.transaction).toMatchObject({ amount: 100n, nonce: 7n, blockHeight: 9n });
    const body = JSON.parse((fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1].body as string);
    expect(body).toMatchObject({ amount: '100', nonce: '7', timestamp: '8' });
    expect((fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1].headers).not.toHaveProperty('Authorization');
  });

  it('encodes and decodes the readiness challenge-response without numeric loss', async () => {
    const challenge = new Uint8Array(32).fill(0xa5);
    const signature = new Uint8Array(65).fill(0x44);
    const fetchMock = vi.fn(async () => response({
      chainId: '84532',
      contract: '0x2222222222222222222222222222222222222222',
      attestor: '0x3333333333333333333333333333333333333333',
      minLockNative: '100000000000',
      challenge: Buffer.from(challenge).toString('base64'),
      signature: Buffer.from(signature).toString('base64'),
    }));
    vi.stubGlobal('fetch', fetchMock);
    const got = await getBridgeReadiness('https://node.example', challenge);
    expect(got).toMatchObject({ chainId: 84532n, minLockNative: 100000000000n });
    expect(got.challenge).toEqual(challenge);
    expect(got.signature).toEqual(signature);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toContain('/GetBridgeReadiness');
    expect(JSON.parse(init.body as string)).toEqual({ challenge: Buffer.from(challenge).toString('base64') });
  });

  it('only classifies unreachable and deadline errors as tolerable readiness transport failures', () => {
    expect(isBridgeReadinessTransportError(new NodeError('unreachable', 'down'))).toBe(true);
    expect(isBridgeReadinessTransportError(new NodeError('deadline_exceeded', 'slow'))).toBe(true);
    expect(isBridgeReadinessTransportError(new NodeError('failed_precondition', 'misconfigured'))).toBe(false);
  });

  it('decodes attestation and reconciliation values without number rounding', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response({
        recipient: '0x1', erc20Amount: '100000000000000000000', lockId: '0xaa', signature: '0xbb', attestor: '0x2', nativeAmount: '100000000000',
      }))
      .mockResolvedValueOnce(response({
        lockedNative: '10', unlockedNative: '2', outstandingNative: '8', escrowBalance: '8', outstandingErc20: '8000000000', blockHeight: '5',
      }));
    vi.stubGlobal('fetch', fetchMock);
    expect((await getLockAttestation('https://node.example', '0xaa')).erc20Amount).toBe(100000000000000000000n);
    expect(await getBridgeReconciliation('https://node.example')).toMatchObject({ outstandingNative: 8n, outstandingErc20: 8000000000n });
  });

  it('preserves structured Connect codes so only not_found is retryable', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => response({ code: 'not_found', message: 'not committed yet' }, false, 404)));
    let caught: unknown;
    try { await getLockAttestation('https://node.example', '0xaa'); } catch (error) { caught = error; }
    expect(caught).toBeInstanceOf(NodeError);
    expect(isNotFoundNodeError(caught)).toBe(true);
    expect(isNotFoundNodeError(new NodeError('failed_precondition', 'bridge disabled'))).toBe(false);
  });
});


describe('attestation request deadline', () => {
  it('fails closed instead of hanging forever', async () => {
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init: RequestInit) =>
      new Promise<Response>((_resolve, reject) => {
        init.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
      }),
    ));
    await expect(getLockAttestation('https://node.example', '0xaa', 5)).rejects.toMatchObject({
      code: 'deadline_exceeded',
    });
  });
});
