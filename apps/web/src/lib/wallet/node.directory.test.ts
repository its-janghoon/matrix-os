import { afterEach, describe, expect, it, vi } from 'vitest';

import { listSellers, sellerFor } from './node';

function response(body: unknown, ok = true, status = 200): Response {
  return { ok, status, text: async () => JSON.stringify(body) } as Response;
}

afterEach(() => vi.unstubAllGlobals());

const HOME = 'http://home-node:9093';

/** One directory entry, as ListProviders returns it. */
function provider(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 'eth:0x00000000000000000000000000000000000000aa',
    nodeId: 'node-a',
    endpoint: 'http://seller-a:9093',
    origin: 'PROVIDER_ORIGIN_REMOTE',
    models: ['llama-3.3-70b'],
    pricePerUnit: '5',
    available: '100',
    bonded: '0',
    settledPayments: '0',
    settledPayers: '0',
    ...over,
  };
}

describe('picking a seller a buyer can actually reach', () => {
  /**
   * THE BUG THIS PINS. Inference is served by the node that OWNS the provider.
   * Selection used to return a bare provider id and the caller sent the request
   * to its OWN node, which holds local providers only and refuses anything else
   * with "provider not found".
   *
   * It was invisible only because nothing announced: every registry was empty,
   * so a remote seller was never the cheapest and the path was never taken. The
   * moment discovery started working, the cheapest seller was routinely somebody
   * else's and the buy failed against a node that had never heard of it.
   */
  it('sends the buyer to the seller"s own endpoint, not the one it asked', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => response({ providers: [provider({ endpoint: 'http://seller-a:9093' })] })),
    );

    const seller = await sellerFor(HOME, 'llama-3.3-70b');
    expect(seller.endpoint).toBe('http://seller-a:9093');
    expect(seller.endpoint).not.toBe(HOME);
  });

  it('reaches a LOCAL seller at the node that was asked', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        response({ providers: [provider({ origin: 'PROVIDER_ORIGIN_LOCAL', endpoint: '' })] }),
      ),
    );

    // A local provider is served by the node being asked, so its address is the
    // one the caller already has - and the announcement carries none.
    const seller = await sellerFor(HOME, 'llama-3.3-70b');
    expect(seller.endpoint).toBe(HOME);
  });

  it('skips a seller that published no address', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        response({
          providers: [
            // Cheaper, but unreachable: a node selling compute units publishes no
            // HTTP address, and routing a prompt at it would fail after the buyer
            // had already been told who they were buying from.
            provider({ id: 'compute-only', pricePerUnit: '1', endpoint: '' }),
            provider({ id: 'reachable', pricePerUnit: '9', endpoint: 'http://seller-b:9093' }),
          ],
        }),
      ),
    );

    const seller = await sellerFor(HOME, 'llama-3.3-70b');
    expect(seller.id).toBe('reachable');
  });

  it('picks the cheapest, and breaks ties the same way twice', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        response({
          providers: [
            provider({ id: 'b-seller', pricePerUnit: '4', endpoint: 'http://b:9093' }),
            provider({ id: 'a-seller', pricePerUnit: '4', endpoint: 'http://a:9093' }),
            provider({ id: 'expensive', pricePerUnit: '40', endpoint: 'http://c:9093' }),
          ],
        }),
      ),
    );

    const first = await sellerFor(HOME, 'llama-3.3-70b');
    const second = await sellerFor(HOME, 'llama-3.3-70b');
    expect(first.id).toBe('a-seller');
    expect(second.id).toBe(first.id);
  });
});

describe('a buyer refusing sellers who staked nothing', () => {
  /**
   * A bond does not make a seller honest - nothing can - but it makes a LISTING
   * cost capital, which is what stops one attacker from filling a directory with
   * cheap fake sellers. The cheap one here is exactly that shape: undercutting
   * everybody and staked on nothing.
   */
  it('takes a dearer seller over an unbonded one when a floor is set', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        response({
          providers: [
            provider({ id: 'anonymous', pricePerUnit: '1', bonded: '0', endpoint: 'http://cheap:9093' }),
            provider({ id: 'staked', pricePerUnit: '20', bonded: '500', endpoint: 'http://staked:9093' }),
          ],
        }),
      ),
    );

    const unfiltered = await sellerFor(HOME, 'llama-3.3-70b');
    expect(unfiltered.id).toBe('anonymous');

    const filtered = await sellerFor(HOME, 'llama-3.3-70b', { minBond: 100n });
    expect(filtered.id).toBe('staked');
  });

  it('says so plainly when the floor leaves nobody', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => response({ providers: [provider({ bonded: '0' })] })),
    );

    await expect(sellerFor(HOME, 'llama-3.3-70b', { minBond: 100n })).rejects.toThrow(/bonded/);
  });
});

describe('what the directory reports', () => {
  it('reads the chain-backed figures as bigints, not numbers', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        response({
          providers: [
            provider({ bonded: '300000000000000', settledPayments: '41', settledPayers: '7' }),
          ],
        }),
      ),
    );

    const [seller] = await listSellers(HOME);
    // A bond is denominated in base units at 9 decimals, so a real one is past
    // what a JavaScript number holds exactly.
    expect(seller.bonded).toBe(300000000000000n);
    expect(seller.settledPayments).toBe(41n);
    expect(seller.settledPayers).toBe(7n);
  });
});

describe('buying from a seller the reader picked', () => {
  /**
   * THE REASON A CHOICE CARRIES NO ADDRESS. It travels as an identity - which
   * node, which payout account - and the address is read back from the live
   * directory here.
   *
   * A choice that carried its own endpoint would mean a link someone was handed
   * could route their prompt at a host of the sender's choosing, while the page
   * named the seller they believed they had picked. The identity is safe to
   * carry because it is only a lookup key. The endpoint is not, because it is
   * where the request goes.
   */
  it('reads the picked seller"s address from the directory, not from the choice', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        response({
          providers: [
            provider({ id: 'wanted', nodeId: 'node-wanted', endpoint: 'http://real-seller:9093' }),
            provider({ id: 'other', nodeId: 'node-other', endpoint: 'http://other:9093' }),
          ],
        }),
      ),
    );

    const seller = await sellerFor(HOME, 'llama-3.3-70b', {
      chosen: { nodeId: 'node-wanted', providerId: 'wanted' },
    });
    expect(seller.endpoint).toBe('http://real-seller:9093');
  });

  it('needs BOTH halves of the identity, since neither alone names one offer', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        response({
          providers: [
            // One payout account, two boxes: an operator who added a second GPU.
            provider({ id: 'shared', nodeId: 'node-a', endpoint: 'http://a:9093', pricePerUnit: '9' }),
            provider({ id: 'shared', nodeId: 'node-b', endpoint: 'http://b:9093', pricePerUnit: '4' }),
          ],
        }),
      ),
    );

    const a = await sellerFor(HOME, 'llama-3.3-70b', { chosen: { nodeId: 'node-a', providerId: 'shared' } });
    const b = await sellerFor(HOME, 'llama-3.3-70b', { chosen: { nodeId: 'node-b', providerId: 'shared' } });
    // Picked by payout account alone, these two would be indistinguishable, and
    // a reader who chose the dearer box for a reason would silently get the
    // other one.
    expect(a.endpoint).toBe('http://a:9093');
    expect(b.endpoint).toBe('http://b:9093');
  });

  it('fails loudly when the picked seller is gone, rather than serving somebody else', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => response({ providers: [provider({ id: 'someone-else', nodeId: 'node-else' })] })),
    );

    // Silently falling back to the cheapest would be the worst outcome: the
    // reader picked a seller for a reason and would be served by a stranger
    // without being told.
    await expect(
      sellerFor(HOME, 'llama-3.3-70b', { chosen: { nodeId: 'node-gone', providerId: 'gone' } }),
    ).rejects.toThrow(/not offering/);
  });

  it('refuses a picked seller that publishes no address', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        response({ providers: [provider({ id: 'compute', nodeId: 'node-c', endpoint: '' })] }),
      ),
    );

    await expect(
      sellerFor(HOME, 'llama-3.3-70b', { chosen: { nodeId: 'node-c', providerId: 'compute' } }),
    ).rejects.toThrow(/no address/);
  });

  it('honours the pick over the bond floor, because the reader chose knowingly', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        response({
          providers: [provider({ id: 'unbonded', nodeId: 'node-u', bonded: '0', endpoint: 'http://u:9093' })],
        }),
      ),
    );

    // The floor exists to decide for a reader who is NOT choosing. One who
    // picked a specific seller from a table showing its stake has already made
    // that decision, and overriding it would be the page second-guessing them.
    const seller = await sellerFor(HOME, 'llama-3.3-70b', {
      minBond: 1_000n,
      chosen: { nodeId: 'node-u', providerId: 'unbonded' },
    });
    expect(seller.id).toBe('unbonded');
  });
});