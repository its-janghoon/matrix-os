import { describe, expect, it } from 'vitest';

import { checkSettlement, maxUnitsFor } from './ceiling';

/**
 * The browser's ceiling must stay in step with the node's MaxUnitsForResponse.
 *
 * It is not a duplicate for its own sake: the node clamps a bill DOWN with it and the
 * browser REFUSES a bill with it, so the two disagreeing does not produce a warning -
 * it produces a reader told their seller is overcharging, after the deposit is funded,
 * who then forfeits the whole reservation at expiry rather than signing.
 */

const ask = [{ role: 'user', content: 'what is the weather' }];

const searchTool = {
  name: 'web_search',
  description: 'Search the web and return result titles and snippets.',
  parameters: '{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}',
};

describe('the ceiling with tools', () => {
  it('is unchanged for an exchange with no tools', () => {
    // The property that keeps every pre-v0.5.9 client agreeing with its node: passing
    // no tools, an empty array, or omitting the argument are one number.
    const base = maxUnitsFor(ask, 'sunny');
    expect(maxUnitsFor(ask, 'sunny', '')).toBe(base);
    expect(maxUnitsFor(ask, 'sunny', '', [])).toBe(base);
    expect(maxUnitsFor(ask, 'sunny', '', [], [])).toBe(base);
  });

  it('grows when a tool schema is offered, because the model was shown it', () => {
    // A schema is prompt tokens the seller really spent - here several times the size
    // of the question itself. A ceiling blind to it underpays the seller for work the
    // buyer received, which is the same failure reasoning text caused before it was
    // counted, in the same direction.
    const without = maxUnitsFor(ask, 'sunny');
    const withTool = maxUnitsFor(ask, 'sunny', '', [searchTool]);
    expect(withTool).toBeGreaterThan(without);
  });

  it('grows when the model emitted a call, because that is generated text', () => {
    const without = maxUnitsFor(ask, '', '', [searchTool]);
    const withCall = maxUnitsFor(ask, '', '', [searchTool], [
      { id: 'call_abc', name: 'web_search', arguments: '{"query":"seoul weather"}' },
    ]);
    expect(withCall).toBeGreaterThan(without);
  });

  it('counts BYTES, so a Korean tool description is not measured three times too tight', () => {
    // Go's len() counts bytes and JavaScript's .length counts UTF-16 units. Using
    // .length here would compute a ceiling a third of the node's for Korean text and
    // refuse every honest bill in it.
    const korean = { name: 't', description: '한국어 설명입니다', parameters: '{}' };
    const ascii = { name: 't', description: 'aaaaaaaaa', parameters: '{}' };
    // Equal in UTF-16 units and unequal in bytes: 9 units either way, 9 bytes for the
    // ASCII one and 23 for the Korean one. A .length-based ceiling would price them
    // identically, so this assertion is what makes the next line mean something.
    expect(korean.description.length).toBe(ascii.description.length);
    expect(maxUnitsFor(ask, 'x', '', [korean])).toBeGreaterThan(maxUnitsFor(ask, 'x', '', [ascii]));
  });

  it('accepts a tool-call bill it would have refused without the tool text', () => {
    // The concrete regression: a run whose only output was a call, priced honestly.
    // Before the tool contribution this settlement was above the ceiling and the
    // browser refused it.
    const calls = [
      {
        id: 'call_abc123',
        name: 'web_search',
        arguments: '{"query":"서울 오늘 날씨 기온 강수확률"}',
      },
    ];
    const honest = maxUnitsFor(ask, '', '', [searchTool], calls);
    const blind = maxUnitsFor(ask, '');
    expect(honest).toBeGreaterThan(blind);

    const amount = blind + 1n;
    expect(
      checkSettlement({
        amount,
        reserved: honest * 2n,
        pricePerUnit: 1n,
        messages: ask,
        completion: '',
        tools: [searchTool],
        toolCalls: calls,
      }),
    ).toEqual({ ok: true });

    // And the same bill is still refused when the tools are not declared, which is
    // what stops this from being a way to widen the ceiling for free.
    const refused = checkSettlement({
      amount: honest * 10n,
      reserved: honest * 100n,
      pricePerUnit: 1n,
      messages: ask,
      completion: '',
      tools: [searchTool],
      toolCalls: calls,
    });
    expect(refused.ok).toBe(false);
  });
});
