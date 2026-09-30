/**
 * The web_search tool, as the browser offers it and runs it.
 *
 * The SCHEMA lives here as a fixed string, not as an object serialized at call time.
 * Those bytes go into the buyer's signature, and a re-serialized object is a
 * different byte string depending on which writer ran - so the text is the value.
 * Changing a character here changes the digest, which is correct: it is a different
 * tool being offered.
 *
 * The EXECUTION goes through this app's own route, which holds the search key. The
 * node never runs a tool; see docs/designs/tool-calls.md.
 */

import type { ExecutableTool } from './toolLoop';

/**
 * The argument schema, verbatim.
 *
 * Compact rather than pretty-printed: every byte is prompt tokens the buyer pays for
 * on every iteration of the loop, and a schema is sent again on each one.
 */
const WEB_SEARCH_PARAMETERS =
  '{"type":"object","properties":{"query":{"type":"string","description":"What to search for. A short phrase works better than a sentence."}},"required":["query"]}';

/** Where the browser sends the call. Same origin, so no key is in the page. */
const ROUTE = '/api/tools/web-search';

export interface SearchResult {
  title: string;
  url: string;
  snippet: string;
}

/**
 * Whether this deployment can actually run the search.
 *
 * Asked BEFORE offering the tool. Offering one that cannot run is worse than not
 * offering it: the model calls it, the call fails, and the buyer pays for a round
 * trip that never could have worked.
 */
export async function webSearchAvailable(): Promise<boolean> {
  try {
    const res = await fetch(ROUTE, { method: 'GET' });
    if (!res.ok) return false;
    const body = (await res.json()) as { available?: unknown };
    return body.available === true;
  } catch {
    return false;
  }
}

export function webSearchTool(): ExecutableTool {
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
        query = parsed.query;
      } catch (err) {
        throw new Error(
          `could not read the arguments: ${err instanceof Error ? err.message : String(err)}. ` +
            `Expected {"query": "..."}.`,
        );
      }

      const res = await fetch(ROUTE, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ query }),
      });
      const body = (await res.json().catch(() => ({}))) as {
        results?: SearchResult[];
        error?: string;
      };
      if (!res.ok) {
        throw new Error(body.error ?? `the search failed with status ${res.status}`);
      }
      const results = body.results ?? [];
      if (results.length === 0) {
        // Not an error: "nothing found" is a real answer and the model should say so
        // rather than retry the same query three more times at the buyer's expense.
        return `No results for ${JSON.stringify(query)}.`;
      }
      return formatForModel(query, results);
    },
  };
}

/**
 * The results as the model sees them.
 *
 * Plain text and not JSON, deliberately: JSON of this costs noticeably more tokens in
 * braces and quotes than the same content as lines, and the model is reading it
 * rather than parsing it. The buyer pays for those tokens on this iteration and every
 * later one, because the result stays in the transcript.
 */
function formatForModel(query: string, results: SearchResult[]): string {
  const lines = results.map((r, i) => `${i + 1}. ${r.title}\n   ${r.url}\n   ${r.snippet}`);
  return `Search results for ${JSON.stringify(query)}:\n\n${lines.join('\n\n')}`;
}
