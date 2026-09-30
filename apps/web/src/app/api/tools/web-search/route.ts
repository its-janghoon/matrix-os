/**
 * The web_search tool's execution, on the BUYER's own server.
 *
 * WHY HERE AND NOT ON THE NODE. The seller says what to call; the buyer calls it.
 * A seller that ran this would need the buyer's search credentials, which is the
 * custody story this network declined when it declined to let a validator buy on
 * someone's behalf. See docs/designs/tool-calls.md.
 *
 * WHY HERE AND NOT IN THE BROWSER. A search API needs a key, and a key in the page
 * is a key anyone can read and spend. It also needs a cross-origin request the
 * provider does not permit. So the browser calls this route, this route holds the
 * key, and the key never leaves the server.
 *
 * WHAT THIS IS NOT. It is not authenticated, and it is not free to run: anyone who
 * can reach this deployment can spend the configured search quota through it. That is
 * acceptable only because the quota is the whole exposure - no chain key, no budget,
 * no buyer account is reachable from here - and it is rate-limited per IP below. A
 * deployment that cares should put its own auth in front of it; this is said plainly
 * rather than left for someone to discover.
 */

import { NextResponse } from 'next/server';

/** The provider's key. Absent means the tool is not offered at all - see GET. */
const KEY_ENV = 'BRAVE_SEARCH_API_KEY';

const ENDPOINT = 'https://api.search.brave.com/res/v1/web/search';

/** How many results the model is shown. More is more prompt tokens the buyer pays for. */
const RESULT_COUNT = 5;

/**
 * How much of each snippet survives.
 *
 * Every byte here becomes prompt tokens on the NEXT job, which the buyer pays for at
 * the seller's price. An untrimmed search result set is several thousand characters
 * and would quietly double the cost of the answer it feeds.
 */
const SNIPPET_LIMIT = 300;

/** Upstream timeout. A tool that hangs holds a funded reservation open. */
const TIMEOUT_MS = 8_000;

/**
 * Per-IP rate limit, in memory.
 *
 * In memory means PER INSTANCE and lost on restart, so it is a brake and not a
 * guarantee - it stops one page looping on a broken tool from draining the quota in a
 * minute, which is the realistic failure. It does not stop a distributed caller, and
 * it is not meant to.
 */
const WINDOW_MS = 60_000;
const MAX_PER_WINDOW = 20;
const hits = new Map<string, number[]>();

function rateLimited(ip: string): boolean {
  const now = Date.now();
  const recent = (hits.get(ip) ?? []).filter((t) => now - t < WINDOW_MS);
  if (recent.length >= MAX_PER_WINDOW) {
    hits.set(ip, recent);
    return true;
  }
  recent.push(now);
  hits.set(ip, recent);
  // Bounded cleanup so a long-lived instance does not accumulate one entry per IP
  // for ever.
  if (hits.size > 5_000) {
    for (const [k, v] of hits) {
      if (v.every((t) => now - t >= WINDOW_MS)) hits.delete(k);
    }
  }
  return false;
}

/**
 * Whether the tool is available, so the page can decide whether to OFFER it.
 *
 * It matters that this is a separate question. A tool offered but not runnable is
 * worse than one never offered: the model calls it, the call fails, and the buyer
 * pays for a round trip that could not have worked.
 */
export async function GET(): Promise<NextResponse> {
  return NextResponse.json({ available: (process.env[KEY_ENV] ?? '') !== '' });
}

interface BraveResult {
  title?: string;
  url?: string;
  description?: string;
}

export async function POST(request: Request): Promise<NextResponse> {
  const key = process.env[KEY_ENV] ?? '';
  if (key === '') {
    return NextResponse.json(
      { error: `web search is not configured on this deployment (${KEY_ENV} is unset)` },
      { status: 503 },
    );
  }

  const ip =
    request.headers.get('x-forwarded-for')?.split(',')[0]?.trim() ||
    request.headers.get('x-real-ip') ||
    'unknown';
  if (rateLimited(ip)) {
    return NextResponse.json(
      { error: `too many searches: at most ${MAX_PER_WINDOW} per minute` },
      { status: 429 },
    );
  }

  let query = '';
  try {
    const body = (await request.json()) as { query?: unknown };
    query = typeof body.query === 'string' ? body.query.trim() : '';
  } catch {
    return NextResponse.json({ error: 'the request body must be JSON' }, { status: 400 });
  }
  if (query === '') {
    return NextResponse.json({ error: "'query' is required and must be a non-empty string" }, { status: 400 });
  }
  // Bounded before it reaches the provider: a model can emit an arbitrarily long
  // argument, and a 100KB query is a bill rather than a search.
  if (query.length > 400) query = query.slice(0, 400);

  const url = new URL(ENDPOINT);
  url.searchParams.set('q', query);
  url.searchParams.set('count', String(RESULT_COUNT));

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), TIMEOUT_MS);
  try {
    const upstream = await fetch(url, {
      headers: { Accept: 'application/json', 'X-Subscription-Token': key },
      signal: controller.signal,
    });
    if (!upstream.ok) {
      // The upstream body is NOT forwarded: an error page from a search provider can
      // echo the request, and the request carries the key in a header. The status is
      // enough for the model to know the call failed.
      return NextResponse.json(
        { error: `the search provider answered ${upstream.status}` },
        { status: 502 },
      );
    }
    const data = (await upstream.json()) as { web?: { results?: BraveResult[] } };
    const results = (data.web?.results ?? []).slice(0, RESULT_COUNT).map((r) => ({
      title: (r.title ?? '').slice(0, 200),
      url: r.url ?? '',
      snippet: stripTags(r.description ?? '').slice(0, SNIPPET_LIMIT),
    }));
    return NextResponse.json({ query, results });
  } catch (err) {
    const aborted = err instanceof Error && err.name === 'AbortError';
    return NextResponse.json(
      { error: aborted ? `the search timed out after ${TIMEOUT_MS}ms` : 'the search could not be run' },
      { status: 504 },
    );
  } finally {
    clearTimeout(timer);
  }
}

/**
 * Removes the markup a search provider puts around matched terms.
 *
 * Not for safety - this text is not rendered as HTML - but for cost and clarity: the
 * tags are prompt tokens the buyer pays for and mean nothing to the model.
 */
function stripTags(s: string): string {
  return s.replace(/<[^>]*>/g, '');
}
