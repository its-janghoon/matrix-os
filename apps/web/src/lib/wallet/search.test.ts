import { describe, expect, it } from 'vitest';

import { searchSellers, sellersByModel } from './search';
import type { Seller } from './node';

function seller(over: Partial<Seller> = {}): Seller {
  return {
    id: 'eth:0x00000000000000000000000000000000000000aa',
    nodeId: 'node-a',
    endpoint: 'http://seller-a:9093',
    models: ['llama-3.3-70b'],
    pricePerUnit: 5n,
    available: 100n,
    bonded: 0n,
    settledPayments: 0n,
    settledPayers: 0n,
    settledReceived: 0n,
    origin: 'remote',
    operatorAttested: false,
    operatorName: '',
    ...over,
  };
}

describe('searching a directory', () => {
  it('matches the four things a reader actually knows', () => {
    const sellers = [
      seller({ id: 'by-account', nodeId: 'n1', models: ['qwen-2.5'] }),
      seller({ id: 'by-node', nodeId: 'the-node-i-was-told', models: ['mistral'] }),
      seller({ id: 'by-address', nodeId: 'n3', endpoint: 'http://gpu-farm.example:9093', models: ['mixtral'] }),
      seller({ id: 'by-model', nodeId: 'n4', models: ['llama-3.3-70b'] }),
    ];

    expect(searchSellers(sellers, { text: 'by-account' }).map((s) => s.id)).toEqual(['by-account']);
    expect(searchSellers(sellers, { text: 'told' }).map((s) => s.id)).toEqual(['by-node']);
    expect(searchSellers(sellers, { text: 'gpu-farm' }).map((s) => s.id)).toEqual(['by-address']);
    expect(searchSellers(sellers, { text: 'llama' }).map((s) => s.id)).toEqual(['by-model']);
  });

  it('is case-insensitive, because nobody types an account id by hand', () => {
    const sellers = [seller({ models: ['Llama-3.3-70B'] })];
    expect(searchSellers(sellers, { text: 'LLAMA' })).toHaveLength(1);
    expect(searchSellers(sellers, { text: 'llama' })).toHaveLength(1);
  });

  it('narrows on a second word rather than widening', () => {
    const sellers = [
      seller({ id: 'a', models: ['llama-3.3-70b'], endpoint: 'http://fast:9093' }),
      seller({ id: 'b', models: ['llama-3.3-70b'], endpoint: 'http://slow:9093' }),
    ];
    // Two terms is a reader being MORE specific. Matching either would return
    // more results the more they typed, which is the opposite of searching.
    expect(searchSellers(sellers, { text: 'llama fast' }).map((s) => s.id)).toEqual(['a']);
  });

  /**
   * Numbers are deliberately not searched as text. Typing 5 would otherwise hit
   * every seller whose stake merely contains a 5 - which looks like a filter
   * doing something and is noise.
   */
  it('does not match a price or a stake as text', () => {
    const sellers = [seller({ pricePerUnit: 5n, bonded: 300_000n })];
    expect(searchSellers(sellers, { text: '300000' })).toHaveLength(0);
    expect(searchSellers(sellers, { text: '5' })).toHaveLength(0);
  });

  it('filters out what a buyer cannot use', () => {
    const sellers = [
      seller({ id: 'unreachable', endpoint: '' }),
      seller({ id: 'full', available: 0n }),
      seller({ id: 'anonymous', bonded: 0n }),
      seller({ id: 'usable', bonded: 1_000n }),
    ];

    expect(searchSellers(sellers, { reachableOnly: true }).map((s) => s.id)).not.toContain('unreachable');
    expect(searchSellers(sellers, { withCapacityOnly: true }).map((s) => s.id)).not.toContain('full');
    expect(searchSellers(sellers, { minBond: 500n }).map((s) => s.id)).toEqual(['usable']);
  });

  it('sorts cheapest first, and everything else largest first', () => {
    const sellers = [
      seller({ id: 'a', pricePerUnit: 9n, bonded: 1n, settledPayments: 1n, settledPayers: 1n }),
      seller({ id: 'b', pricePerUnit: 4n, bonded: 9n, settledPayments: 9n, settledPayers: 9n }),
    ];
    // Price is the one where LOW wins; a stake or a track record reads the other
    // way, and a table that sorted them the same would rank the least proven
    // seller first under a heading that says otherwise.
    expect(searchSellers(sellers, { sort: 'price' }).map((s) => s.id)).toEqual(['b', 'a']);
    expect(searchSellers(sellers, { sort: 'staked' }).map((s) => s.id)).toEqual(['b', 'a']);
    expect(searchSellers(sellers, { sort: 'paid' }).map((s) => s.id)).toEqual(['b', 'a']);
    expect(searchSellers(sellers, { sort: 'payers' }).map((s) => s.id)).toEqual(['b', 'a']);
  });

  it('orders equal rows the same way every time', () => {
    const sellers = [
      seller({ id: 'z', nodeId: 'n2', pricePerUnit: 5n }),
      seller({ id: 'a', nodeId: 'n1', pricePerUnit: 5n }),
      seller({ id: 'a', nodeId: 'n2', pricePerUnit: 5n }),
    ];
    // A table that reshuffled equal rows between refreshes makes a reader doubt
    // what they are reading, and two readers comparing screens see different
    // things.
    const once = searchSellers(sellers).map((s) => `${s.nodeId}:${s.id}`);
    const twice = searchSellers([...sellers].reverse()).map((s) => `${s.nodeId}:${s.id}`);
    expect(once).toEqual(twice);
    expect(once).toEqual(['n1:a', 'n2:a', 'n2:z']);
  });

  it('does not mutate what it was given', () => {
    const sellers = [seller({ id: 'b', pricePerUnit: 9n }), seller({ id: 'a', pricePerUnit: 1n })];
    const before = sellers.map((s) => s.id);
    searchSellers(sellers, { sort: 'price' });
    expect(sellers.map((s) => s.id)).toEqual(before);
  });
});

describe('grouping a directory by model', () => {
  it('summarises who has a model and what it costs', () => {
    const rows = sellersByModel([
      seller({ id: 'a', models: ['llama-3.3-70b'], pricePerUnit: 9n, bonded: 5n, settledPayments: 2n }),
      seller({ id: 'b', models: ['llama-3.3-70b', 'qwen-2.5'], pricePerUnit: 4n, bonded: 50n, settledPayments: 3n }),
    ]);

    const llama = rows.find((r) => r.model === 'llama-3.3-70b');
    expect(llama).toMatchObject({ sellers: 2, cheapest: 4n, dearest: 9n, topStake: 50n, settledPayments: 5n });
    expect(rows.find((r) => r.model === 'qwen-2.5')?.sellers).toBe(1);
  });

  /**
   * A seller with no address cannot take a prompt. Counting it would inflate
   * "3 sellers" into a number a reader cannot act on, and the cheapest price
   * shown might belong to somebody nobody can reach.
   */
  it('counts only sellers a buyer can actually reach', () => {
    const rows = sellersByModel([
      seller({ id: 'reachable', models: ['m'], pricePerUnit: 9n, endpoint: 'http://a:9093' }),
      seller({ id: 'compute-only', models: ['m'], pricePerUnit: 1n, endpoint: '' }),
    ]);
    expect(rows[0]).toMatchObject({ sellers: 1, cheapest: 9n });
  });

  it('is empty when nobody advertises a model', () => {
    expect(sellersByModel([seller({ models: [] })])).toEqual([]);
  });
});

describe('the one badge this marketplace has', () => {
  /**
   * Off by default, and that is the design rather than an oversight. Defaulting
   * a marketplace to its own operator's sellers would make every other listing
   * furniture, and the badge is an identity claim - "the maintainer this chain
   * names says it runs this" - not a quality one.
   */
  it('does not hide unattested sellers unless asked', () => {
    const sellers = [
      seller({ id: 'first-party', operatorAttested: true, operatorName: 'Matrix OS', pricePerUnit: 9n }),
      seller({ id: 'a-stranger', operatorAttested: false, pricePerUnit: 4n }),
    ];
    // Unfiltered, the cheapest still wins: a badge does not buy rank.
    expect(searchSellers(sellers).map((s) => s.id)).toEqual(['a-stranger', 'first-party']);
    expect(searchSellers(sellers, { attestedOnly: true }).map((s) => s.id)).toEqual(['first-party']);
  });

  it('finds a seller by the operator name somebody told you to look for', () => {
    const sellers = [
      seller({ id: 'ours', operatorAttested: true, operatorName: 'Matrix OS' }),
      seller({ id: 'theirs', operatorAttested: false, operatorName: '' }),
    ];
    expect(searchSellers(sellers, { text: 'matrix os' }).map((s) => s.id)).toEqual(['ours']);
  });

  it('counts attested sellers per model, so a reader can see there is a safe start', () => {
    const rows = sellersByModel([
      seller({ id: 'a', models: ['m'], operatorAttested: true }),
      seller({ id: 'b', models: ['m'], operatorAttested: false }),
    ]);
    expect(rows[0]).toMatchObject({ sellers: 2, attested: 1 });
  });
});