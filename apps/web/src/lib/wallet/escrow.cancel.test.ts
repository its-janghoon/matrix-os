import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Seller } from './node';
import type { Signer } from './signer';

/**
 * CANCELLING MUST NOT COST THE READER THE WHOLE RESERVATION.
 *
 * The settlement arrives on the stream's LAST frame. A reader who presses stop
 * never receives it - and with nothing to sign, the provider claims the entire
 * reservation at its expiry rather than the fraction the run actually cost. So a
 * stop that merely aborted the fetch would be the most expensive button on the
 * page, and it would look like the cheapest.
 *
 * What is asserted here is the recovery: stop, ask the node for the settlement
 * of the job it is no longer streaming, check it against the text that DID
 * arrive, sign it, and report the answer as partial.
 */

const calls: Array<{ method: string; body: Record<string, unknown> }> = [];

/** What the node asks for when the settlement is recovered. Per-test. */
let recoveredAmount = '40';

const seller = {
  id: 'provider-1',
  nodeId: 'node-1',
  endpoint: 'http://seller.test',
  models: ['m'],
  pricePerUnit: 1n,
  available: 1000n,
  bonded: 0n,
  settledPayments: 0n,
  settledPayers: 0n,
  settledReceived: 0n,
  origin: 'remote',
} as unknown as Seller;

vi.mock('./node', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./node')>();
  return {
    ...actual,
    sellerFor: () => Promise.resolve(seller),
    rpc: (_endpoint: string, _service: string, method: string, body: Record<string, unknown>) => {
      calls.push({ method, body });
      switch (method) {
        case 'ReserveInferenceEscrow':
          return Promise.resolve({
            payment: {
              jobId: 'job-1',
              to: 'infer/escrow/job-1/5000.99/provider-1/buyer-1',
              amount: '5000',
              nonce: '7',
              timestamp: '1700000000000000000',
              prevHash: '',
            },
            claimableAt: '1700001800',
          });
        case 'FundInferenceEscrow':
          return Promise.resolve({ job: {} });
        case 'RecoverEscrowedInferenceJob':
          return Promise.resolve({
            cutShort: true,
            job: { completion: 'he', reasoning: '' },
            payment: {
              to: 'infer/settle/job-1/5000.99/provider-1/buyer-1',
              amount: recoveredAmount,
              nonce: '8',
              timestamp: '1700000000000000001',
              prevHash: '',
            },
          });
        case 'SettleEscrowedInferenceJob':
          return Promise.resolve({
            job: {
              completion: 'he',
              reasoning: '',
              units: recoveredAmount,
              provider: 'provider-1',
              model: 'm',
              usage: { promptTokens: 2, completionTokens: 1 },
              receipt: '',
            },
          });
        default:
          throw new Error(`unexpected rpc ${method}`);
      }
    },
  };
});

const signer = {
  accountId: 'buyer-1',
  kind: 'browser',
  signRunAuthorization: () =>
    Promise.resolve({ publicKey: new Uint8Array([1]), timestamp: 1n, signature: new Uint8Array([2]) }),
  signPayment: () => Promise.resolve({ fromPublicKey: new Uint8Array([1]), signature: new Uint8Array([3]) }),
} as unknown as Signer;

function frame(payload: string, flags = 0): Uint8Array {
  const body = new TextEncoder().encode(payload);
  const out = new Uint8Array(5 + body.length);
  const view = new DataView(out.buffer);
  view.setUint8(0, flags);
  view.setUint32(1, body.length, false);
  out.set(body, 5);
  return out;
}

/**
 * A stream that delivers one delta and then stalls, so only the abort ends it -
 * the shape a reader pressing stop mid-answer actually produces. A stream that
 * completed on its own would test the ordinary path with an extra step.
 */
function stallingStream() {
  return (_url: string, init?: RequestInit) =>
    Promise.resolve({
      ok: true,
      status: 200,
      body: new ReadableStream<Uint8Array>({
        start(c) {
          c.enqueue(frame(JSON.stringify({ delta: 'he' })));
          init?.signal?.addEventListener('abort', () => {
            const err = new Error('aborted');
            err.name = 'AbortError';
            c.error(err);
          });
          // Nothing else is ever enqueued: the model is still working.
        },
      }),
    } as unknown as Response);
}

beforeEach(() => {
  recoveredAmount = '40';
  calls.length = 0;
  vi.stubGlobal('fetch', stallingStream());
});

afterEach(() => vi.unstubAllGlobals());

describe('stopping a funded run', () => {
  it('recovers the settlement, signs it, and reports the answer as partial', async () => {
    const { chatEscrowed } = await import('./escrow');
    const controller = new AbortController();

    const deltas: string[] = [];
    const settled = await chatEscrowed('http://reader.test', signer, {
      model: 'm',
      messages: [{ role: 'user', content: 'hi' }],
      signal: controller.signal,
      // Stop the moment there is something on screen, which is when a reader can
      // first decide this is not the answer they wanted.
      onDelta: (d) => {
        deltas.push(d);
        controller.abort();
      },
    });

    expect(deltas).toEqual(['he']);
    // The text the READER saw, not the node's copy: they are the same here, and
    // the local one is what the bill was checked against.
    expect(settled.completion).toBe('he');
    expect(settled.cutShort).toBe(true);
    expect(settled.units).toBe(40n);

    expect(calls.map((c) => c.method)).toEqual([
      'ReserveInferenceEscrow',
      'FundInferenceEscrow',
      'RecoverEscrowedInferenceJob',
      'SettleEscrowedInferenceJob',
    ]);
    // Settling is the whole point of recovering: without it the reservation of
    // 5000 goes to the provider instead of the 40 the partial answer cost.
    expect(calls[3].body.amount).toBe('40');
    expect(calls[3].body.id).toBe('job-1');
    // The recovery presents the same credential the stream does, because it
    // hands back the completion.
    expect(calls[2].body.authorization).toEqual(calls[0].body.authorization);
  });

  it('refuses to sign a recovered settlement the partial answer cannot account for', async () => {
    // 'he' is two bytes, so the 64-unit floor at 1 each is the most it can have
    // cost. A node asking 4000 for two letters is asking for most of the cap,
    // and a cancelled job is exactly where a seller would try it: the reader
    // stopped looking.
    recoveredAmount = '4000';
    const { chatEscrowed, SettlementRefused } = await import('./escrow');
    const controller = new AbortController();

    const run = chatEscrowed('http://reader.test', signer, {
      model: 'm',
      messages: [{ role: 'user', content: 'hi' }],
      signal: controller.signal,
      onDelta: () => controller.abort(),
    });

    await expect(run).rejects.toBeInstanceOf(SettlementRefused);
    // Refused BEFORE signing. A refusal after would be a signature already given.
    expect(calls.map((c) => c.method)).not.toContain('SettleEscrowedInferenceJob');
  });
});
