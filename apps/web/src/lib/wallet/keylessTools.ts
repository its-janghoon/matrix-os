/**
 * The tools that work with NO KEY, called straight from the browser.
 *
 * WHY THERE IS NO web_search HERE. There is no keyless general web search a page can
 * call, and that was measured rather than assumed:
 *
 *   - Brave, Serper, Tavily and every other general engine require a key.
 *   - DuckDuckGo's keyless Instant Answer API returns an EMPTY object for an ordinary
 *     query ("서울 날씨") and for a bare entity ("Seoul") alike - it answers a narrow
 *     set of canned questions, not searches.
 *   - Public SearXNG instances either refuse the request (403/429), or answer 200 with
 *     an HTML page because `format=json` is disabled, and none send a CORS header.
 *
 * So general search is out. These two are in, because they are keyless, they send
 * `access-control-allow-origin: *`, and between them they cover the questions that
 * actually sent a reader looking for search: what the weather is, and what a thing is.
 *
 * WHAT THIS CANNOT DO, stated here so the model's own description can say it: news,
 * prices, "who holds this role now", anything on a specific web page. Those need a key
 * and are therefore not offered at all rather than offered and failing.
 *
 * The node never runs a tool. See docs/designs/tool-calls.md.
 */

import type { ExecutableTool } from './toolLoop';

/** A tool that hangs holds a funded reservation open, so every call is bounded. */
const TIMEOUT_MS = 8_000;

/**
 * How much prose the model is shown.
 *
 * A result stays in the transcript for every later iteration of the loop, so this is
 * not paid for once - it is paid for again on each round trip.
 */
const EXTRACT_LIMIT = 700;

async function getJSON(url: string): Promise<unknown> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), TIMEOUT_MS);
  try {
    const res = await fetch(url, { signal: controller.signal });
    if (!res.ok) throw new Error(`the service answered ${res.status}`);
    return await res.json();
  } catch (err) {
    if (err instanceof Error && err.name === 'AbortError') {
      throw new Error(`it did not answer within ${TIMEOUT_MS}ms`);
    }
    throw err;
  } finally {
    clearTimeout(timer);
  }
}

/**
 * Reads one string argument out of the model's raw JSON.
 *
 * A parse failure THROWS, so the loop reports it to the model as a failed call: the
 * model is the only party that can emit better arguments, and swallowing it would have
 * the model believe the tool found nothing.
 */
function oneStringArg(rawArguments: string, field: string): string {
  let value: unknown;
  try {
    value = (JSON.parse(rawArguments) as Record<string, unknown>)[field];
  } catch (err) {
    throw new Error(
      `could not read the arguments: ${err instanceof Error ? err.message : String(err)}. ` +
        `Expected {"${field}": "..."}.`,
    );
  }
  if (typeof value !== 'string' || value.trim() === '') {
    throw new Error(`'${field}' must be a non-empty string`);
  }
  // Bounded before it leaves: a model can emit an arbitrarily long argument.
  return value.trim().slice(0, 200);
}

// ---------------------------------------------------------------------------
// weather
// ---------------------------------------------------------------------------

interface GeoResult {
  name: string;
  latitude: number;
  longitude: number;
  country?: string;
  admin1?: string;
  timezone?: string;
}

/**
 * WMO weather codes, as words.
 *
 * The forecast reports a number and the model should not be left guessing what 51
 * means. Only the codes that occur are spelled out; anything else is reported as the
 * bare code rather than mislabelled.
 */
const WEATHER_CODES: Record<number, string> = {
  0: 'clear',
  1: 'mainly clear',
  2: 'partly cloudy',
  3: 'overcast',
  45: 'fog',
  48: 'freezing fog',
  51: 'light drizzle',
  53: 'drizzle',
  55: 'heavy drizzle',
  61: 'light rain',
  63: 'rain',
  65: 'heavy rain',
  71: 'light snow',
  73: 'snow',
  75: 'heavy snow',
  80: 'light showers',
  81: 'showers',
  82: 'violent showers',
  95: 'thunderstorm',
  96: 'thunderstorm with hail',
  99: 'thunderstorm with heavy hail',
};

export function weatherTool(): ExecutableTool {
  return {
    definition: {
      name: 'weather',
      description:
        'Current weather and a short forecast for a place. Give the place name in ' +
        'English or romanized (Seoul, Daegu, Busan) — a name in local script often ' +
        'finds nothing or the wrong country.',
      parameters:
        '{"type":"object","properties":{"place":{"type":"string","description":"City or place name, English or romanized."}},"required":["place"]}',
    },
    async run(rawArguments: string): Promise<string> {
      const place = oneStringArg(rawArguments, 'place');

      const geo = (await getJSON(
        `https://geocoding-api.open-meteo.com/v1/search?count=5&language=en&name=${encodeURIComponent(place)}`,
      )) as { results?: GeoResult[] };
      const candidates = geo.results ?? [];
      if (candidates.length === 0) {
        // A clear "not found" the model can act on, rather than a wrong answer. It is
        // told what to try instead, because the usual cause is a local-script name.
        return `No place matched ${JSON.stringify(place)}. Try the English or romanized name.`;
      }
      const chosen = candidates[0]!;

      const forecast = (await getJSON(
        `https://api.open-meteo.com/v1/forecast?latitude=${chosen.latitude}&longitude=${chosen.longitude}` +
          `&current=temperature_2m,relative_humidity_2m,precipitation,weather_code,wind_speed_10m` +
          `&daily=temperature_2m_max,temperature_2m_min,precipitation_probability_max` +
          `&forecast_days=2&timezone=${encodeURIComponent(chosen.timezone ?? 'auto')}`,
      )) as {
        current?: Record<string, number | string>;
        daily?: Record<string, (number | string)[]>;
      };

      const now = forecast.current ?? {};
      const daily = forecast.daily ?? {};
      const code = Number(now.weather_code);
      const condition = WEATHER_CODES[code] ?? `code ${code}`;

      // WHERE IT RESOLVED TO IS PART OF THE ANSWER, not a detail. A Korean place name
      // can geocode to the wrong country silently - "대구" returns a village in North
      // Korea before the city of 2.4 million - so the place, its region and its country
      // are reported and the model is expected to pass them on. A reader can then catch
      // a wrong match; a reader given only a temperature cannot.
      const where = [chosen.name, chosen.admin1, chosen.country].filter(Boolean).join(', ');
      const lines = [
        `Weather for ${where} (lat ${chosen.latitude}, lon ${chosen.longitude}, ` +
          `times in ${chosen.timezone ?? 'local time'}):`,
        `now, at ${String(now.time ?? '?')}: ${now.temperature_2m}°C, ${condition}, ` +
          `humidity ${now.relative_humidity_2m}%, precipitation ${now.precipitation}mm, ` +
          `wind ${now.wind_speed_10m}km/h`,
      ];
      const days = daily.time ?? [];
      for (let i = 0; i < days.length; i++) {
        lines.push(
          `${String(days[i])}: high ${daily.temperature_2m_max?.[i]}°C, ` +
            `low ${daily.temperature_2m_min?.[i]}°C, ` +
            `chance of precipitation ${daily.precipitation_probability_max?.[i]}%`,
        );
      }
      if (candidates.length > 1) {
        const others = candidates
          .slice(1, 4)
          .map((c) => [c.name, c.country].filter(Boolean).join(', '))
          .join('; ');
        lines.push(`Other places with this name, not used: ${others}.`);
      }
      return lines.join('\n');
    },
  };
}

// ---------------------------------------------------------------------------
// wikipedia
// ---------------------------------------------------------------------------

/**
 * Which Wikipedia to ask.
 *
 * Korean first for a query containing Hangul, English otherwise. Asking the wrong one
 * is not an error but a much worse article, and the reader's own language is the better
 * default for a question asked in it.
 */
function wikiHost(query: string): string {
  return /[\uAC00-\uD7A3]/.test(query) ? 'ko.wikipedia.org' : 'en.wikipedia.org';
}

export function wikipediaTool(): ExecutableTool {
  return {
    definition: {
      name: 'wikipedia',
      description:
        'Look something up on Wikipedia: a place, person, company, technology, event, ' +
        'or definition. Good for what a thing IS. Not for news, prices or anything ' +
        'that changed recently.',
      parameters:
        '{"type":"object","properties":{"query":{"type":"string","description":"What to look up."}},"required":["query"]}',
    },
    async run(rawArguments: string): Promise<string> {
      const query = oneStringArg(rawArguments, 'query');
      const host = wikiHost(query);

      const found = (await getJSON(
        `https://${host}/w/api.php?action=query&list=search&format=json&origin=*&srlimit=3` +
          `&srsearch=${encodeURIComponent(query)}`,
      )) as { query?: { search?: { title?: string }[] } };
      const titles = (found.query?.search ?? [])
        .map((s) => s.title)
        .filter((t): t is string => typeof t === 'string' && t !== '');
      if (titles.length === 0) {
        return `Wikipedia has no article matching ${JSON.stringify(query)}.`;
      }

      const best = titles[0]!;
      const page = (await getJSON(
        `https://${host}/w/api.php?action=query&prop=extracts&exintro=1&explaintext=1` +
          `&redirects=1&format=json&origin=*&titles=${encodeURIComponent(best)}`,
      )) as { query?: { pages?: Record<string, { title?: string; extract?: string }> } };
      const first = Object.values(page.query?.pages ?? {})[0];
      const extract = (first?.extract ?? '').trim();
      if (extract === '') {
        return `The Wikipedia article "${best}" has no summary to read.`;
      }

      const lines = [
        `Wikipedia — ${first?.title ?? best}`,
        `https://${host}/wiki/${encodeURIComponent((first?.title ?? best).replace(/ /g, '_'))}`,
        '',
        extract.length > EXTRACT_LIMIT ? `${extract.slice(0, EXTRACT_LIMIT)}…` : extract,
      ];
      if (titles.length > 1) {
        // The other hits, so the model can ask again for a better one instead of
        // answering from an article that was merely the top match.
        lines.push('', `Other articles matched: ${titles.slice(1).join('; ')}.`);
      }
      return lines.join('\n');
    },
  };
}

/**
 * Every tool that needs no key. Offered on every send, because there is nothing to
 * configure and nothing that can be missing.
 */
export function keylessTools(): ExecutableTool[] {
  return [weatherTool(), wikipediaTool()];
}
