import { describe, expect, it } from 'vitest';

import { checkSettlement, maxUnitsFor } from './ceiling';

/**
 * The vectors here are asserted BY THE GO TOO, in
 * services/core/internal/inference/ceiling_vector_test.go. Two implementations
 * of one bound only stay equal if something fails when they stop being.
 *
 * A client that computed a TIGHTER ceiling than the node would refuse bills the
 * node considers honest, which reads to a buyer as the seller cheating; a looser
 * one would wave through the overcharge this exists to catch.
 */
describe('the ceiling agrees with the node, byte for byte', () => {
  const cases: Array<{ name: string; messages: Array<{ role: string; content: string }>; completion: string; reasoning: string; want: bigint }> = [
    {
      name: 'a short ascii exchange falls to the floor',
      messages: [{ role: 'user', content: 'hi' }],
      completion: 'hello',
      reasoning: '',
      want: 64n,
    },
    {
      name: 'plain ascii above the floor',
      messages: [{ role: 'user', content: 'a'.repeat(200) }],
      completion: 'b'.repeat(100),
      reasoning: '',
      want: 336n,
    },
    {
      name: 'reasoning is counted, because the buyer receives and pays for it',
      messages: [{ role: 'user', content: 'a'.repeat(100) }],
      completion: 'b'.repeat(50),
      reasoning: 'c'.repeat(400),
      want: 586n,
    },
    {
      // The trap this pair exists for. Go's len() counts BYTES; JavaScript's
      // .length counts UTF-16 code units. A Korean character is three bytes and
      // one unit, so .length would compute a third of the real ceiling here and
      // refuse every honest bill in a Korean conversation.
      name: 'korean text is counted in bytes, not code units',
      messages: [{ role: 'user', content: '안녕하세요'.repeat(40) }],
      completion: '반갑습니다'.repeat(40),
      reasoning: '',
      want: 1236n,
    },
    {
      name: 'an emoji is four bytes and two code units',
      messages: [{ role: 'user', content: '🙂'.repeat(100) }],
      completion: '',
      reasoning: '',
      want: 436n,
    },
    {
      name: 'several messages each carry the template overhead',
      messages: [
        { role: 'system', content: 'be brief' },
        { role: 'user', content: 'x'.repeat(300) },
        { role: 'assistant', content: 'y'.repeat(300) },
      ],
      completion: 'z'.repeat(300),
      reasoning: '',
      want: 991n,
    },
  ];

  for (const c of cases) {
    it(c.name, () => {
      expect(maxUnitsFor(c.messages, c.completion, c.reasoning)).toBe(c.want);
    });
  }
});

describe('checkSettlement', () => {
  const messages = [{ role: 'user', content: 'hi' }];

  it('accepts a bill the answer can honestly account for', () => {
    const got = checkSettlement({
      amount: 50n,
      reserved: 5000n,
      pricePerUnit: 1n,
      messages,
      completion: 'hello',
    });
    expect(got.ok).toBe(true);
  });

  it('refuses a bill larger than the answer can account for', () => {
    // The floor is 64 units, so at 1 per unit anything over 64 is unaccountable.
    const got = checkSettlement({
      amount: 5000n,
      reserved: 100000n,
      pricePerUnit: 1n,
      messages,
      completion: 'hello',
    });
    expect(got.ok).toBe(false);
    if (!got.ok) expect(got.reason).toContain('honestly');
  });

  it('refuses a settlement above the reservation, which consensus would refuse too', () => {
    const got = checkSettlement({
      amount: 200n,
      reserved: 100n,
      pricePerUnit: 1n,
      messages,
      completion: 'hello',
    });
    expect(got.ok).toBe(false);
    if (!got.ok) expect(got.reason).toContain('exceeds the reservation');
  });

  it('refuses a settlement of nothing', () => {
    const got = checkSettlement({
      amount: 0n,
      reserved: 100n,
      pricePerUnit: 1n,
      messages,
      completion: 'hello',
    });
    expect(got.ok).toBe(false);
  });

  it('skips the ceiling when the price is unknown rather than guessing at it', () => {
    // A provider that has left the order book has no price here. Refusing on a
    // number we do not have would strand a buyer who owes a real bill.
    const got = checkSettlement({
      amount: 999999n,
      reserved: 1000000n,
      pricePerUnit: 0n,
      messages,
      completion: 'hello',
    });
    expect(got.ok).toBe(true);
  });
});
