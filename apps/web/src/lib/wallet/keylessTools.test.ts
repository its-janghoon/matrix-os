import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { keylessTools, weatherTool, wikipediaTool } from './keylessTools';

/**
 * These two tools exist because there is no keyless general web search a page can call.
 * Measured, not assumed: Brave/Serper/Tavily need keys, DuckDuckGo's keyless Instant
 * Answer API returns an empty object for an ordinary query, and public SearXNG
 * instances answer 403/429 or serve HTML with no CORS header.
 *
 * The test that matters most is the geocoding one. "대구" resolves to a village in NORTH
 * KOREA before the city of 2.4 million, so a tool that reported only a temperature would
 * answer the wrong country's weather and nobody could tell.
 */

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
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

const SEOUL = {
  name: '서울특별시',
  latitude: 37.566,
  longitude: 126.9784,
  country: '대한민국',
  admin1: '서울특별시',
  timezone: 'Asia/Seoul',
};

const FORECAST = {
  current: {
    time: '2026-09-30T18:15',
    temperature_2m: 21.8,
    relative_humidity_2m: 77,
    precipitation: 0.1,
    weather_code: 51,
    wind_speed_10m: 8.2,
  },
  daily: {
    time: ['2026-09-30', '2026-10-01'],
    temperature_2m_max: [24.0, 19.8],
    temperature_2m_min: [15.4, 12.2],
    precipitation_probability_max: [90, 2],
  },
};

describe('weather', () => {
  it('reports the readings and turns the WMO code into words', async () => {
    fetchMock.mockResolvedValueOnce(ok({ results: [SEOUL] })).mockResolvedValueOnce(ok(FORECAST));
    const out = await weatherTool().run('{"place":"Seoul"}');

    expect(out).toContain('21.8');
    // The model must not be handed a bare 51 and left to guess.
    expect(out).toContain('light drizzle');
    expect(out).not.toMatch(/code 51/);
    expect(out).toContain('90%');
    expect(out).toContain('2026-10-01');
  });

  it('names the place, its region AND its country, so a wrong match is visible', async () => {
    // The whole defence against a silent wrong answer. A reader given only a temperature
    // cannot tell that "대구" resolved to North Korea; a reader shown the resolved place
    // can.
    fetchMock.mockResolvedValueOnce(ok({ results: [SEOUL] })).mockResolvedValueOnce(ok(FORECAST));
    const out = await weatherTool().run('{"place":"Seoul"}');
    expect(out).toContain('서울특별시');
    expect(out).toContain('대한민국');
    expect(out).toContain('Asia/Seoul');
  });

  it('lists the other places with the same name that it did not use', async () => {
    // The real Open-Meteo response for "대구": a North Korean village first, the South
    // Korean towns after. Whatever it picks, the rejected candidates are reported so the
    // model can say "I used this one" and be corrected.
    const dprk = { name: '대구', latitude: 38.72445, longitude: 127.64274, country: '조선민주주의인민공화국', admin1: '강원도' };
    const rok = { name: '대구', latitude: 34.32341, longitude: 126.69866, country: '대한민국', admin1: '전라남도' };
    fetchMock
      .mockResolvedValueOnce(ok({ results: [dprk, rok] }))
      .mockResolvedValueOnce(ok(FORECAST));
    const out = await weatherTool().run('{"place":"대구"}');

    expect(out).toContain('조선민주주의인민공화국');
    expect(out).toContain('not used');
    expect(out).toContain('대한민국');
  });

  it('says nothing matched, and what to try, rather than answering anyway', async () => {
    // A local-script name often finds nothing - "서울" returns no results where "Seoul"
    // works. The model needs to be told to romanize it, not told the weather is unknown.
    fetchMock.mockResolvedValueOnce(ok({ results: [] }));
    const out = await weatherTool().run('{"place":"서울"}');
    expect(out).toContain('No place matched');
    expect(out).toMatch(/romanized/);
    // And it did NOT go on to ask for a forecast at a made-up coordinate.
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('reports an unknown weather code as the code rather than mislabelling it', async () => {
    fetchMock
      .mockResolvedValueOnce(ok({ results: [SEOUL] }))
      .mockResolvedValueOnce(ok({ ...FORECAST, current: { ...FORECAST.current, weather_code: 7 } }));
    const out = await weatherTool().run('{"place":"Seoul"}');
    expect(out).toContain('code 7');
  });
});

describe('wikipedia', () => {
  it('asks the Korean Wikipedia for a question in Korean', async () => {
    // Asking the English one is not an error but a much worse article, and the reader's
    // own language is the better default for a question asked in it.
    fetchMock
      .mockResolvedValueOnce(ok({ query: { search: [{ title: '서울특별시' }] } }))
      .mockResolvedValueOnce(
        ok({ query: { pages: { '1': { title: '서울특별시', extract: '서울특별시는 대한민국의 수도이다.' } } } }),
      );
    const out = await wikipediaTool().run('{"query":"서울"}');

    expect(String(fetchMock.mock.calls[0]![0])).toContain('ko.wikipedia.org');
    expect(out).toContain('대한민국의 수도');
    expect(out).toContain('https://ko.wikipedia.org/wiki/');
  });

  it('asks the English Wikipedia otherwise', async () => {
    fetchMock
      .mockResolvedValueOnce(ok({ query: { search: [{ title: 'Seoul' }] } }))
      .mockResolvedValueOnce(ok({ query: { pages: { '1': { title: 'Seoul', extract: 'Seoul is the capital.' } } } }));
    await wikipediaTool().run('{"query":"Seoul"}');
    expect(String(fetchMock.mock.calls[0]![0])).toContain('en.wikipedia.org');
  });

  it('names the other matches, so the model can ask again for a better one', async () => {
    fetchMock
      .mockResolvedValueOnce(
        ok({ query: { search: [{ title: '인공지능' }, { title: '멀티모달 학습' }, { title: '챗봇' }] } }),
      )
      .mockResolvedValueOnce(ok({ query: { pages: { '1': { title: '인공지능', extract: '인공지능이란...' } } } }));
    const out = await wikipediaTool().run('{"query":"멀티모달 인공지능"}');
    expect(out).toContain('Other articles matched');
    expect(out).toContain('멀티모달 학습');
  });

  it('says so when nothing matched', async () => {
    fetchMock.mockResolvedValueOnce(ok({ query: { search: [] } }));
    const out = await wikipediaTool().run('{"query":"zzzzqqq"}');
    expect(out).toContain('no article');
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('truncates a long extract, because it is re-paid for on every later iteration', async () => {
    fetchMock
      .mockResolvedValueOnce(ok({ query: { search: [{ title: 'T' }] } }))
      .mockResolvedValueOnce(ok({ query: { pages: { '1': { title: 'T', extract: 'x'.repeat(5_000) } } } }));
    const out = await wikipediaTool().run('{"query":"T"}');
    expect(out.length).toBeLessThan(1_200);
    expect(out).toContain('…');
  });
});

describe('both tools', () => {
  it('throw on arguments they cannot read, so the loop tells the MODEL', async () => {
    for (const tool of keylessTools()) {
      await expect(tool.run('not json')).rejects.toThrow(/could not read the arguments/);
      await expect(tool.run('{}')).rejects.toThrow(/must be a non-empty string/);
    }
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('are offered without configuration, and do not claim to be web search', async () => {
    const tools = keylessTools();
    expect(tools.map((t) => t.definition.name)).toEqual(['weather', 'wikipedia']);
    // The descriptions are what the model decides from, so they must not promise
    // general search - there is none - and must steer it off what these cannot do.
    const all = tools.map((t) => t.definition.description ?? '').join(' ');
    expect(all).not.toMatch(/search the web/i);
    expect(all).toMatch(/[Nn]ot for news/);
  });

  it('bound an over-long argument before it leaves the browser', async () => {
    fetchMock.mockResolvedValueOnce(ok({ results: [] }));
    await weatherTool().run(JSON.stringify({ place: 'x'.repeat(5_000) }));
    const url = String(fetchMock.mock.calls[0]![0]);
    expect(url).toContain(`name=${'x'.repeat(200)}`);
    expect(url).not.toContain('x'.repeat(201));
  });
});
