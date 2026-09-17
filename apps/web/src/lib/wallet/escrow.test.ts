import { afterEach, describe, expect, it, vi } from 'vitest';

import { streamRpc } from './escrow';

/**
 * The frame reader, against the shapes a real network produces.
 *
 * Connect puts one enveloped frame per message on an ordinary HTTP response:
 * flags byte, four-byte big-endian length, then the JSON. A reader that assumed
 * one frame per chunk works perfectly against a fast local node and corrupts the
 * first long answer that crosses a real network, which is the kind of bug that
 * ships.
 */

function frame(payload: string, flags = 0): Uint8Array {
  const body = new TextEncoder().encode(payload);
  const out = new Uint8Array(5 + body.length);
  const view = new DataView(out.buffer);
  view.setUint8(0, flags);
  view.setUint32(1, body.length, false);
  out.set(body, 5);
  return out;
}

function concat(parts: Uint8Array[]): Uint8Array {
  const total = parts.reduce((n, p) => n + p.length, 0);
  const out = new Uint8Array(total);
  let at = 0;
  for (const p of parts) {
    out.set(p, at);
    at += p.length;
  }
  return out;
}

/** Serves `bytes` in chunks of exactly `size`, to force awkward boundaries. */
function respondIn(bytes: Uint8Array, size: number) {
  return () =>
    Promise.resolve({
      ok: true,
      status: 200,
      body: new ReadableStream<Uint8Array>({
        start(controller) {
          for (let at = 0; at < bytes.length; at += size) {
            controller.enqueue(bytes.subarray(at, Math.min(at + size, bytes.length)));
          }
          controller.close();
        },
      }),
    } as unknown as Response);
}

async function collect(): Promise<Record<string, unknown>[]> {
  const out: Record<string, unknown>[] = [];
  for await (const f of streamRpc('http://node.test', 'svc', 'Method', {})) out.push(f);
  return out;
}

afterEach(() => vi.unstubAllGlobals());

describe('the Connect frame reader', () => {
  const stream = concat([
    frame(JSON.stringify({ delta: 'he' })),
    frame(JSON.stringify({ delta: 'llo' })),
    frame(JSON.stringify({ delta: ' world', jobId: 'j1' })),
    frame('{}', 0x02),
  ]);

  // Every chunk size from one byte upward: a frame split anywhere, several
  // frames arriving together, and the exact-boundary case in between.
  for (const size of [1, 3, 7, 13, 64, 4096]) {
    it(`reassembles the same messages when the body arrives ${size} bytes at a time`, async () => {
      vi.stubGlobal('fetch', respondIn(stream, size));
      const got = await collect();
      expect(got.map((f) => f.delta)).toEqual(['he', 'llo', ' world']);
      expect(got[2].jobId).toBe('j1');
    });
  }

  it('raises the error in the end-of-stream frame rather than ending quietly', async () => {
    // A failure after the first byte can only reach a caller here, so swallowing
    // it would report a truncated answer as a complete one.
    const withError = concat([
      frame(JSON.stringify({ delta: 'partial' })),
      frame(JSON.stringify({ error: { code: 'internal', message: 'the backend died' } }), 0x02),
    ]);
    vi.stubGlobal('fetch', respondIn(withError, 5));
    await expect(collect()).rejects.toThrow('the backend died');
  });

  it('ends cleanly on an end-of-stream frame with no error', async () => {
    vi.stubGlobal('fetch', respondIn(concat([frame('{"delta":"x"}'), frame('{}', 0x02)]), 2));
    await expect(collect()).resolves.toHaveLength(1);
  });

  it('reports a failed request rather than yielding nothing', async () => {
    vi.stubGlobal('fetch', () =>
      Promise.resolve({ ok: false, status: 401, text: () => Promise.resolve('unauthenticated') } as unknown as Response),
    );
    await expect(collect()).rejects.toThrow('401');
  });
});
