import { afterEach, describe, expect, it, vi } from 'vitest';

import { formatBytes, getLatestRelease, TARGETS } from './releases';

/**
 * The shape below is the real GitHub response for v0.1.0, trimmed to the fields
 * the module reads. Asset names and byte counts are the published ones, so the
 * mapping is exercised against the names the release workflow actually emits
 * rather than against names invented for the test.
 */
const V010 = {
  tag_name: 'v0.1.0',
  html_url: 'https://github.com/savagemanage/matrix-os/releases/tag/v0.1.0',
  published_at: '2026-09-06T16:29:47Z',
  draft: false,
  prerelease: false,
  assets: [
    { name: 'matrix-os-v0.1.0-darwin-amd64.tar.gz', size: 19992429 },
    { name: 'matrix-os-v0.1.0-darwin-arm64.tar.gz', size: 18754132 },
    { name: 'matrix-os-v0.1.0-linux-amd64.tar.gz', size: 19598052 },
    { name: 'matrix-os-v0.1.0-linux-arm64.tar.gz', size: 17971935 },
    { name: 'matrix-os-v0.1.0-windows-amd64.zip', size: 19895646 },
    { name: 'matrix-os-v0.1.0-windows-arm64.zip', size: 18003087 },
    { name: 'SHA256SUMS', size: 612 },
  ].map((a) => ({
    ...a,
    browser_download_url: `https://github.com/savagemanage/matrix-os/releases/download/v0.1.0/${a.name}`,
  })),
};

function mockJson(body: unknown, ok = true, status = 200) {
  const fetchMock = vi.fn(async () => ({ ok, status, json: async () => body }));
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

afterEach(() => {
  vi.unstubAllGlobals();
  delete process.env.GITHUB_TOKEN;
  delete process.env.GH_TOKEN;
});

describe('getLatestRelease', () => {
  it('maps every published archive in TARGETS order', async () => {
    mockJson([V010]);

    const release = await getLatestRelease();

    expect(release).not.toBeNull();
    expect(release!.tag).toBe('v0.1.0');
    expect(release!.publishedAt).toBe('2026-09-06T16:29:47Z');
    expect(release!.assets.map((a) => `${a.os} ${a.arch}`)).toEqual(
      TARGETS.map((t) => `${t.os} ${t.arch}`),
    );
    expect(release!.assets.map((a) => a.name)).toEqual([
      'matrix-os-v0.1.0-linux-amd64.tar.gz',
      'matrix-os-v0.1.0-linux-arm64.tar.gz',
      'matrix-os-v0.1.0-darwin-amd64.tar.gz',
      'matrix-os-v0.1.0-darwin-arm64.tar.gz',
      'matrix-os-v0.1.0-windows-amd64.zip',
      'matrix-os-v0.1.0-windows-arm64.zip',
    ]);
    expect(release!.assets.map((a) => a.size)).toEqual([
      19598052, 17971935, 19992429, 18754132, 19895646, 18003087,
    ]);
    expect(release!.checksumsUrl).toBe(
      'https://github.com/savagemanage/matrix-os/releases/download/v0.1.0/SHA256SUMS',
    );
  });

  it('sends a User-Agent and no Authorization when no token is present', async () => {
    const fetchMock = mockJson([V010]);

    await getLatestRelease();

    const headers = (fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1]
      .headers as Record<string, string>;
    expect(headers['User-Agent']).toBe('ecirlabs-web');
    expect(headers.Authorization).toBeUndefined();
  });

  it('authenticates when a token is in the environment', async () => {
    process.env.GITHUB_TOKEN = 'ghp_example';
    const fetchMock = mockJson([V010]);

    const release = await getLatestRelease();

    expect(release!.tag).toBe('v0.1.0');
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const headers = (fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1]
      .headers as Record<string, string>;
    expect(headers.Authorization).toBe('Bearer ghp_example');
  });

  it('retries anonymously when the environment token is rejected', async () => {
    // The container this was built in exports a placeholder GITHUB_TOKEN that
    // api.github.com answers with 401; without the retry the page would render
    // its "no release yet" state while a release existed.
    process.env.GITHUB_TOKEN = 'not-a-real-token';
    const fetchMock = vi.fn(async (_url: string, init: RequestInit) => {
      const headers = init.headers as Record<string, string>;
      if (headers.Authorization) {
        return { ok: false, status: 401, json: async () => ({ message: 'Bad credentials' }) };
      }
      return { ok: true, status: 200, json: async () => [V010] };
    });
    vi.stubGlobal('fetch', fetchMock);

    const release = await getLatestRelease();

    expect(release!.tag).toBe('v0.1.0');
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('does not retry anonymously when the authenticated request simply fails', async () => {
    process.env.GITHUB_TOKEN = 'ghp_example';
    const fetchMock = mockJson({ message: 'Internal Server Error' }, false, 500);

    expect(await getLatestRelease()).toBeNull();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('skips drafts and prereleases and takes the first real release', async () => {
    mockJson([
      { ...V010, tag_name: 'v0.2.0-rc.1', prerelease: true },
      { ...V010, tag_name: 'v0.2.0', draft: true },
      V010,
    ]);

    const release = await getLatestRelease();

    expect(release!.tag).toBe('v0.1.0');
  });

  it('returns null when only drafts and prereleases exist', async () => {
    mockJson([{ ...V010, draft: true }, { ...V010, prerelease: true }]);

    expect(await getLatestRelease()).toBeNull();
  });

  it('returns null for an empty release list', async () => {
    mockJson([]);

    expect(await getLatestRelease()).toBeNull();
  });

  it('returns null when the API rate-limits the build', async () => {
    mockJson({ message: 'API rate limit exceeded' }, false, 403);

    expect(await getLatestRelease()).toBeNull();
  });

  it('returns null instead of throwing when the network fails', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('fetch failed');
      }),
    );

    expect(await getLatestRelease()).toBeNull();
  });

  it('returns null when the body is not an array', async () => {
    mockJson({ message: 'Not Found' });

    expect(await getLatestRelease()).toBeNull();
  });

  it('ignores assets that are not archives', async () => {
    mockJson([
      {
        ...V010,
        assets: [
          {
            name: 'matrix-os-v0.1.0-linux-amd64.txt',
            size: 10,
            browser_download_url: 'https://example.invalid/notes.txt',
          },
        ],
      },
    ]);

    const release = await getLatestRelease();

    expect(release!.assets).toEqual([]);
    expect(release!.checksumsUrl).toBeNull();
  });
});

describe('formatBytes', () => {
  it('reports megabytes to one decimal place', () => {
    expect(formatBytes(19598052)).toBe('18.7 MB');
  });

  it('reports kilobytes below a megabyte', () => {
    expect(formatBytes(612)).toBe('1 KB');
    expect(formatBytes(400 * 1024)).toBe('400 KB');
  });

  it('handles zero and negative sizes', () => {
    expect(formatBytes(0)).toBe('0 B');
    expect(formatBytes(-1)).toBe('0 B');
  });
});
