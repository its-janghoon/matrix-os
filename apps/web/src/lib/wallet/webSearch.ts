/**
 * The web_search tool, run BY THE BROWSER with the reader's own key.
 *
 * WHY NOT ON A SERVER. It was on one - a route in this app holding a deployment-wide
 * key - and that made search the only centralised dependency in an otherwise
 * self-custody design. The wallet is in this browser, the budget's delegate key is in
 * this browser, the settlement is signed in this browser; a search key held by
 * whoever deployed the page meant one shared quota anybody could drain through an
 * unauthenticated route, and meant `/chat` could not search unless that particular
 * deployment was alive and funded. The reader's own key removes all three.
 *
 * WHY THIS IS POSSIBLE AT ALL. Not every search API can be called from a page: the
 * first provider tried, Brave, answers a CORS preflight with 405 and no headers, so a
 * browser cannot reach it and a server is forced. The two here were checked and do
 * send the headers - Serper `access-control-allow-origin: *` with `X-API-KEY` in the
 * allow-list, Tavily reflecting the origin with `content-type`. That is why the
 * provider is part of the stored setting rather than a detail: it is the thing that
 * decides whether this design works.
 *
 * WHERE THE KEY LIVES. localStorage on this origin, which any script here can read.
 * That is a real exposure and it is not a new one: the same origin already holds the
 * delegate key that SPENDS the reader's budget, which is worth strictly more than a
 * search quota. A key that cannot be stolen would have to be non-extractable like the
 * wallet's, and a search API needs the bytes in a header.
 *
 * The node never runs a tool. See docs/designs/tool-calls.md.
 */

import type { ExecutableTool } from './toolLoop';

/**
 * The argument schema, verbatim.
 *
 * A fixed string, not an object serialized at call time: these bytes are inside the
 * buyer's signature, and a re-serialized object is a different byte string depending
 * on which writer ran. Compact rather than pretty-printed, because every byte is
 * prompt tokens the reader pays for on every iteration of the loop.
 */
const WEB_SEARCH_PARAMETERS =
  '{"type":"object","properties":{"query":{"type":"string","description":"What to search for. A short phrase works better than a sentence."}},"required":["query"]}';

/** How many results the model is shown. More results is more prompt tokens paid for. */
const RESULT_COUNT = 5;

/**
 * How much of each snippet survives.
 *
 * The result stays in the transcript for every later iteration, so an untrimmed result
 * set is not paid for once - it is paid for again on each round trip.
 */
const SNIPPET_LIMIT = 300;

/** A search that hangs holds a funded reservation open, so it is bounded. */
const TIMEOUT_MS = 8_000;

const STORAGE_KEY = 'matrix-search-key';

/** The providers that can actually be called from a page. Verified, not assumed. */
export type SearchProvider = 'serper' | 'tavily';

export interface SearchCredential {
  provider: SearchProvider;
  key: string;
}

export interface SearchResult {
  title: string;
  url: string;
  snippet: string;
}

/**
 * Reads the reader's stored search credential, or null.
 *
 * Null is the ordinary case and means the tool is not offered at all - not that it is
 * offered and fails. A tool the model can call but nothing can run costs the reader a
 * round trip that could never have worked.
 */
export function storedSearchCredential(): SearchCredential | null {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (raw === null) return null;
    const parsed = JSON.parse(raw) as { provider?: unknown; key?: unknown };
    const provider = parsed.provider;
    if (provider !== 'serper' && provider !== 'tavily') return null;
    if (typeof parsed.key !== 'string' || parsed.key.trim() === '') return null;
    return { provider, key: parsed.key.trim() };
  } catch {
    // Storage unavailable, or a record from an older shape. Either way: no tool.
    return null;
  }
}

export function rememberSearchCredential(credential: SearchCredential): void {
  window.localStorage.setItem(
    STORAGE_KEY,
    JSON.stringify({ provider: credential.provider, key: credential.key.trim() }),
  );
}

export function forgetSearchCredential(): void {
  try {
    window.localStorage.removeItem(STORAGE_KEY);
  } catch {
    // Nothing to do: the tool simply keeps being offered until the page reloads.
  }
}

/**
 * A guess at which provider a pasted key belongs to, for the settings field.
 *
 * Tavily documents a `tvly-` prefix; Serper's keys are 64 hex characters. It is a
 * CONVENIENCE and the caller still stores an explicit provider - guessing wrong would
 * otherwise send the key to the wrong company, which is worse than asking.
 */
export function guessProvider(key: string): SearchProvider | null {
  const k = key.trim();
  if (k.startsWith('tvly-')) return 'tavily';
  if (/^[0-9a-f]{64}$/i.test(k)) return 'serper';
  return null;
}

export function webSearchTool(credential: SearchCredential): ExecutableTool {
  return {
    definition: {
      name: 'web_search',
      description:
        'Search the web for current information. Use it for anything that changes over ' +
        'time - news, prices, weather, who holds a role now - or that you are unsure about.',
      parameters: WEB_SEARCH_PARAMETERS,
    },
    async run(rawArguments: string): Promise<string> {
      // The model's arguments, which may not be valid JSON at all. A parse failure is
      // THROWN so the loop reports it to the model as a failed call: the model is the
      // only party that can emit better arguments, and swallowing it would have the
      // model believe the search found nothing.
      let query: string;
      try {
        const parsed = JSON.parse(rawArguments) as { query?: unknown };
        if (typeof parsed.query !== 'string' || parsed.query.trim() === '') {
          throw new Error("'query' must be a non-empty string");
        }
        query = parsed.query.trim();
      } catch (err) {
        throw new Error(
          `could not read the arguments: ${err instanceof Error ? err.message : String(err)}. ` +
            `Expected {"query": "..."}.`,
        );
      }
      // Bounded before it leaves: a model can emit an arbitrarily long argument, and a
      // 100KB query is a bill rather than a search.
      if (query.length > 400) query = query.slice(0, 400);

      const results = await search(credential, query);
      if (results.length === 0) {
        // Not an error. "Nothing found" is a real answer, and a model told the search
        // failed would retry the same query at the reader's expense.
        return `No results for ${JSON.stringify(query)}.`;
      }
      return formatForModel(query, results);
    },
  };
}

async function search(credential: SearchCredential, query: string): Promise<SearchResult[]> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), TIMEOUT_MS);
  try {
    const res =
      credential.provider === 'serper'
        ? await fetch('https://google.serper.dev/search', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'X-API-KEY': credential.key },
            body: JSON.stringify({ q: query, num: RESULT_COUNT }),
            signal: controller.signal,
          })
        : await fetch('https://api.tavily.com/search', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            // Tavily takes the key in the BODY, which is why it needs no header in the
            // CORS allow-list and why its preflight passes with content-type alone.
            body: JSON.stringify({
              api_key: credential.key,
              query,
              max_results: RESULT_COUNT,
            }),
            signal: controller.signal,
          });

    if (!res.ok) {
      // The response body is NOT quoted back. A provider's error page can echo the
      // request, and for Tavily the request carries the key - so echoing it would put
      // the reader's key into the transcript, which then travels to the seller inside
      // the next job's prompt.
      throw new Error(
        res.status === 401 || res.status === 403
          ? `the search key was rejected (${res.status}). Check it in settings.`
          : `the search provider answered ${res.status}`,
      );
    }
    const body = (await res.json()) as unknown;
    return credential.provider === 'serper' ? fromSerper(body) : fromTavily(body);
  } catch (err) {
    if (err instanceof Error && err.name === 'AbortError') {
      throw new Error(`the search timed out after ${TIMEOUT_MS}ms`);
    }
    throw err;
  } finally {
    clearTimeout(timer);
  }
}

function fromSerper(body: unknown): SearchResult[] {
  const organic = (body as { organic?: unknown }).organic;
  if (!Array.isArray(organic)) return [];
  return organic.slice(0, RESULT_COUNT).map((raw) => {
    const r = raw as { title?: unknown; link?: unknown; snippet?: unknown };
    return {
      title: text(r.title).slice(0, 200),
      url: text(r.link),
      snippet: text(r.snippet).slice(0, SNIPPET_LIMIT),
    };
  });
}

function fromTavily(body: unknown): SearchResult[] {
  const results = (body as { results?: unknown }).results;
  if (!Array.isArray(results)) return [];
  return results.slice(0, RESULT_COUNT).map((raw) => {
    const r = raw as { title?: unknown; url?: unknown; content?: unknown };
    return {
      title: text(r.title).slice(0, 200),
      url: text(r.url),
      snippet: text(r.content).slice(0, SNIPPET_LIMIT),
    };
  });
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

/**
 * The results as the model sees them.
 *
 * Plain lines rather than JSON, deliberately: the braces and quotes of JSON are
 * noticeably more tokens for the same content, the model is reading this rather than
 * parsing it, and the reader pays for those tokens again on every later iteration
 * because the result stays in the transcript.
 */
function formatForModel(query: string, results: SearchResult[]): string {
  const lines = results.map((r, i) => `${i + 1}. ${r.title}\n   ${r.url}\n   ${r.snippet}`);
  return `Search results for ${JSON.stringify(query)}:\n\n${lines.join('\n\n')}`;
}
