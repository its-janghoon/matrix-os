import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  forgetSearchCredential,
  guessProvider,
  rememberSearchCredential,
  storedSearchCredential,
  webSearchTool,
} from './webSearch';

/**
 * The search runs in the BROWSER with the reader's own key, which is what removes the
 * shared quota and the unauthenticated route the server version needed.
 *
 * The test that matters most here is the last one: the key must not reach the
 * transcript. A tool result becomes a message in the next job's prompt, and that prompt
 * goes to the SELLER - so a provider error page echoed into the result would hand the
 * reader's search key to whoever is serving the model.
 */

const SERPER_KEY = 'a'.repeat(64);
const TAVILY_KEY = 'tvly-secret-key-value';

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  window.localStorage.clear();
  fetchMock = vi.fn();
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

function ok(body: unknown) {
  return { ok: true, status: 200, json: async () => body };
}

describe('the stored credential', () => {
  it('round-trips, and an absent one is null rather than a broken tool', () => {
    expect(storedSearchCredential()).toBeNull();
    rememberSearchCredential({ provider: 'serper', key: SERPER_KEY });
    expect(storedSearchCredential()).toEqual({ provider: 'serper', key: SERPER_KEY });
    forgetSearchCredential();
    expect(storedSearchCredential()).toBeNull();
  });

  it('refuses a record it cannot trust rather than guessing a provider', () => {
    // A key sent to the wrong company is worse than no key. An unknown provider, a
    // missing key and unparseable JSON all read as "no tool offered".
    window.localStorage.setItem('matrix-search-key', JSON.stringify({ provider: 'brave', key: 'x' }));
    expect(storedSearchCredential()).toBeNull();
    window.localStorage.setItem('matrix-search-key', JSON.stringify({ provider: 'serper' }));
    expect(storedSearchCredential()).toBeNull();
    window.localStorage.setItem('matrix-search-key', 'not json');
    expect(storedSearchCredential()).toBeNull();
  });

  it('guesses a provider only from a shape it recognises', () => {
    expect(guessProvider(TAVILY_KEY)).toBe('tavily');
    expect(guessProvider(SERPER_KEY)).toBe('serper');
    expect(guessProvider('something-else')).toBeNull();
  });
});

describe('the tool, per provider', () => {
  it('sends the Serper key as a header and reads its organic results', async () => {
    fetchMock.mockResolvedValue(
      ok({
        organic: [
          { title: '서울 날씨', link: 'https://example.com/a', snippet: '맑음, 최고 26도' },
          { title: 'Seoul forecast', link: 'https://example.com/b', snippet: 'Showers later' },
        ],
      }),
    );
    const out = await webSearchTool({ provider: 'serper', key: SERPER_KEY }).run(
      '{"query":"서울 날씨"}',
    );

    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe('https://google.serper.dev/search');
    expect((init as { headers: Record<string, string> }).headers['X-API-KEY']).toBe(SERPER_KEY);
    expect(out).toContain('서울 날씨');
    expect(out).toContain('https://example.com/a');
    expect(out).toContain('맑음, 최고 26도');
  });

  it('sends the Tavily key in the body, which is why its preflight passes', async () => {
    // Tavily takes the key as a body field, so the request needs no custom header and
    // its CORS allow-list only has to include content-type. That is the mechanism, not
    // a style choice.
    fetchMock.mockResolvedValue(
      ok({ results: [{ title: 'T', url: 'https://example.com/t', content: 'clean snippet' }] }),
    );
    await webSearchTool({ provider: 'tavily', key: TAVILY_KEY }).run('{"query":"x"}');

    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe('https://api.tavily.com/search');
    const headers = (init as { headers: Record<string, string> }).headers;
    expect(Object.keys(headers)).toEqual(['Content-Type']);
    expect(JSON.parse((init as { body: string }).body).api_key).toBe(TAVILY_KEY);
  });

  it('says nothing found rather than failing, so the model does not retry', async () => {
    // "Nothing found" is a real answer. A model told the search FAILED would try the
    // same query again at the reader's expense.
    fetchMock.mockResolvedValue(ok({ organic: [] }));
    const out = await webSearchTool({ provider: 'serper', key: SERPER_KEY }).run('{"query":"zzz"}');
    expect(out).toContain('No results');
  });
});

describe('the tool, when things go wrong', () => {
  it('throws on arguments it cannot read, so the loop tells the MODEL', async () => {
    const tool = webSearchTool({ provider: 'serper', key: SERPER_KEY });
    await expect(tool.run('not json')).rejects.toThrow(/could not read the arguments/);
    await expect(tool.run('{"query":""}')).rejects.toThrow(/non-empty/);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('names a rejected key as a settings problem rather than a provider outage', async () => {
    fetchMock.mockResolvedValue({ ok: false, status: 401, json: async () => ({}) });
    await expect(
      webSearchTool({ provider: 'serper', key: SERPER_KEY }).run('{"query":"x"}'),
    ).rejects.toThrow(/key was rejected.*settings/s);
  });

  it('bounds an over-long query before it leaves the browser', async () => {
    fetchMock.mockResolvedValue(ok({ organic: [] }));
    await webSearchTool({ provider: 'serper', key: SERPER_KEY }).run(
      JSON.stringify({ query: 'x'.repeat(5_000) }),
    );
    const body = JSON.parse((fetchMock.mock.calls[0]![1] as { body: string }).body);
    expect(body.q.length).toBe(400);
  });

  it('NEVER puts the key in what the model is told', async () => {
    // The one that matters. A tool result becomes a message in the next job's prompt,
    // and that prompt goes to the seller - so a provider error body echoed into the
    // result would hand the reader's key to whoever is serving the model. Tavily's
    // request carries the key in its body, which is exactly what such a page echoes.
    const echoed = {
      ok: false,
      status: 400,
      json: async () => ({ error: `bad request: {"api_key":"${TAVILY_KEY}","query":"x"}` }),
    };
    fetchMock.mockResolvedValue(echoed);

    let message = '';
    try {
      await webSearchTool({ provider: 'tavily', key: TAVILY_KEY }).run('{"query":"x"}');
    } catch (err) {
      message = err instanceof Error ? err.message : String(err);
    }
    expect(message).not.toBe('');
    expect(message).not.toContain(TAVILY_KEY);
    expect(message).toContain('400');
  });

  it('does not leak the key through a successful result either', async () => {
    fetchMock.mockResolvedValue(
      ok({ results: [{ title: 'T', url: 'https://e.com', content: 'ok' }] }),
    );
    const out = await webSearchTool({ provider: 'tavily', key: TAVILY_KEY }).run('{"query":"x"}');
    expect(out).not.toContain(TAVILY_KEY);
  });
});
