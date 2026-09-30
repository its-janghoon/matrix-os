import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';

import {
  DEFAULT_MAX_ITERATIONS,
  MAX_ITERATIONS_CEILING,
  runWithTools,
  type ExecutableTool,
} from './toolLoop';
import type { Message } from './signer';

/**
 * The loop is the ONLY bound on what one ask can spend.
 *
 * A budget's per-job cap bounds one job; nothing in the protocol bounds the number of
 * jobs, so a model that keeps calling a failing tool drains a budget through jobs
 * consensus is right to accept one at a time. These tests are about that counter, not
 * about tool plumbing: if they pass while the bound is broken they are worthless, so
 * each one asserts the number of JOBS as well as the outcome.
 */

const escrowCalls: { messages: Message[]; tools: unknown }[] = [];
let replies: { completion: string; toolCalls?: { id: string; name: string; arguments: string }[] }[] = [];

vi.mock('./escrow', () => ({
  chatEscrowed: vi.fn(async (_endpoint: string, _signer: unknown, opts: Record<string, unknown>) => {
    escrowCalls.push({ messages: opts.messages as Message[], tools: opts.tools });
    const next = replies.shift() ?? { completion: 'done' };
    return {
      completion: next.completion,
      reasoning: '',
      toolCalls: next.toolCalls ?? [],
      units: 10n,
      provider: 'p',
      model: 'm',
      promptTokens: 1,
      completionTokens: 1,
      receipt: '',
      seller: { endpoint: 'http://seller', id: 'p', pricePerUnit: 1n },
      cutShort: false,
    };
  }),
}));

function tool(name: string, run: (raw: string) => Promise<string>): ExecutableTool {
  return { definition: { name, description: '', parameters: '{}' }, run };
}

const signer = { kind: 'browser' as const, accountId: 'acct' } as never;
const ask: Message[] = [{ role: 'user', content: 'q' }];

beforeEach(() => {
  escrowCalls.length = 0;
  replies = [];
});
afterEach(() => {
  vi.clearAllMocks();
});

describe('runWithTools', () => {
  it('runs exactly one job when the model does not call anything', async () => {
    replies = [{ completion: 'the answer' }];
    const result = await runWithTools('http://node', signer, {
      model: 'm',
      messages: ask,
      tools: [tool('web_search', async () => 'unused')],
    });
    expect(result.completion).toBe('the answer');
    expect(result.exhausted).toBe(false);
    expect(escrowCalls).toHaveLength(1);
  });

  it('executes the call, feeds the result back, and answers on the second job', async () => {
    replies = [
      { completion: '', toolCalls: [{ id: 'c1', name: 'web_search', arguments: '{"query":"날씨"}' }] },
      { completion: '맑음' },
    ];
    const ran: string[] = [];
    const result = await runWithTools('http://node', signer, {
      model: 'm',
      messages: ask,
      tools: [
        tool('web_search', async (raw) => {
          ran.push(raw);
          return '맑음, 26도';
        }),
      ],
    });

    expect(ran).toEqual(['{"query":"날씨"}']);
    expect(result.completion).toBe('맑음');
    expect(escrowCalls).toHaveLength(2);
    // Two jobs, two settlements, and the total is what the reader is shown. A result
    // reporting only the last leg would understate a search by half.
    expect(result.units).toBe(20n);

    // The second request replays the model's OWN call beside the result. Without the
    // assistant turn the model sees a result answering nothing and re-calls the tool.
    const second = escrowCalls[1]!.messages;
    expect(second.map((m) => m.role)).toEqual(['user', 'assistant', 'tool']);
    expect(second[1]!.toolCalls).toEqual([
      { id: 'c1', name: 'web_search', arguments: '{"query":"날씨"}' },
    ]);
    expect(second[2]!.toolCallId).toBe('c1');
    expect(second[2]!.content).toBe('맑음, 26도');
  });

  it('offers the tools again on every iteration, not just the first', async () => {
    // A model that stops being shown its tools mid-conversation sees a transcript in
    // which it called something that no longer exists, and answers about the tool
    // rather than about the question.
    replies = [
      { completion: '', toolCalls: [{ id: 'c1', name: 't', arguments: '{}' }] },
      { completion: 'ok' },
    ];
    await runWithTools('http://node', signer, {
      model: 'm',
      messages: ask,
      tools: [tool('t', async () => 'r')],
    });
    expect(escrowCalls).toHaveLength(2);
    for (const call of escrowCalls) {
      expect(call.tools).toEqual([{ name: 't', description: '', parameters: '{}' }]);
    }
  });

  it('stops at the bound when the model never stops calling, and says it was cut off', async () => {
    // The runaway case: a tool that always "works" and a model that always calls it.
    // Unbounded this empties a budget through individually legal jobs.
    replies = Array.from({ length: 50 }, (_, i) => ({
      completion: '',
      toolCalls: [{ id: `c${i}`, name: 't', arguments: '{}' }],
    }));
    const result = await runWithTools('http://node', signer, {
      model: 'm',
      messages: ask,
      tools: [tool('t', async () => 'again')],
    });
    expect(escrowCalls).toHaveLength(DEFAULT_MAX_ITERATIONS);
    expect(result.exhausted).toBe(true);
    // The spend is reported rather than thrown away: the reader paid for those jobs.
    expect(result.units).toBe(BigInt(DEFAULT_MAX_ITERATIONS) * 10n);
  });

  it('caps a caller asking for more than the ceiling', async () => {
    // A page is not trusted with "unlimited", because a page can be wrong.
    replies = Array.from({ length: 500 }, (_, i) => ({
      completion: '',
      toolCalls: [{ id: `c${i}`, name: 't', arguments: '{}' }],
    }));
    const result = await runWithTools('http://node', signer, {
      model: 'm',
      messages: ask,
      tools: [tool('t', async () => 'again')],
      maxIterations: 10_000,
    });
    expect(escrowCalls).toHaveLength(MAX_ITERATIONS_CEILING);
    expect(result.exhausted).toBe(true);
  });

  it('never runs zero jobs, whatever a caller asks for', async () => {
    replies = [{ completion: 'answer' }];
    await runWithTools('http://node', signer, {
      model: 'm',
      messages: ask,
      tools: [tool('t', async () => 'r')],
      maxIterations: 0,
    });
    expect(escrowCalls).toHaveLength(1);
  });

  it('tells the MODEL a tool failed instead of ending the ask', async () => {
    // A thrown tool error that ended the turn would bill the reader for a dead end.
    // The model is the only party that can try something else.
    replies = [
      { completion: '', toolCalls: [{ id: 'c1', name: 't', arguments: '{}' }] },
      { completion: 'I could not search, but here is what I know' },
    ];
    const seen: { failed: boolean; result: string }[] = [];
    const result = await runWithTools('http://node', signer, {
      model: 'm',
      messages: ask,
      tools: [
        tool('t', async () => {
          throw new Error('upstream 502');
        }),
      ],
      onToolResult: (_c, r, failed) => seen.push({ failed, result: r }),
    });
    expect(seen[0]!.failed).toBe(true);
    expect(seen[0]!.result).toContain('upstream 502');
    expect(result.completion).toBe('I could not search, but here is what I know');
    expect(escrowCalls[1]!.messages.at(-1)!.content).toContain('upstream 502');
  });

  it('reports an invented tool to the model, naming what does exist', async () => {
    replies = [
      { completion: '', toolCalls: [{ id: 'c1', name: 'not_a_tool', arguments: '{}' }] },
      { completion: 'ok' },
    ];
    const result = await runWithTools('http://node', signer, {
      model: 'm',
      messages: ask,
      tools: [tool('web_search', async () => 'r')],
    });
    const handed = escrowCalls[1]!.messages.at(-1)!.content;
    expect(handed).toContain('not_a_tool');
    expect(handed).toContain('web_search');
    expect(result.completion).toBe('ok');
  });

  it('refuses two tools sharing a name before spending anything', async () => {
    // Ambiguous dispatch: the buyer signed for both definitions, so whichever this
    // picked, the signature would not settle which one ran. Refused before job one.
    await expect(
      runWithTools('http://node', signer, {
        model: 'm',
        messages: ask,
        tools: [tool('t', async () => 'a'), tool('t', async () => 'b')],
      }),
    ).rejects.toThrow(/share a name/);
    expect(escrowCalls).toHaveLength(0);
  });

  it('runs every call in a multi-call turn, in order, each with its own id', async () => {
    replies = [
      {
        completion: '',
        toolCalls: [
          { id: 'c1', name: 't', arguments: '{"n":1}' },
          { id: 'c2', name: 't', arguments: '{"n":2}' },
        ],
      },
      { completion: 'both' },
    ];
    await runWithTools('http://node', signer, {
      model: 'm',
      messages: ask,
      tools: [tool('t', async (raw) => `for ${raw}`)],
    });
    const second = escrowCalls[1]!.messages;
    expect(second.slice(-2).map((m) => m.toolCallId)).toEqual(['c1', 'c2']);
    expect(second.at(-1)!.content).toBe('for {"n":2}');
  });
});
