/**
 * Reads published releases from the GitHub API so the download page can only
 * ever show artifacts that exist.
 *
 * The page this feeds used to hard-code six binary URLs under /downloads, six
 * SHA256 checksums and six file sizes. None of it was real: the directory was
 * never created, and the checksums were placeholder digests (the first one is
 * the SHA-256 of the empty string). Everything here is derived from the API
 * response instead, so an empty release list renders as "nothing published
 * yet" rather than as six dead links.
 */
export const REPO = 'savagemanage/matrix-os';

/** Platforms the release workflow builds for, in display order. */
export const TARGETS = [
  { goos: 'linux', goarch: 'amd64', os: 'Linux', arch: 'x64' },
  { goos: 'linux', goarch: 'arm64', os: 'Linux', arch: 'arm64' },
  { goos: 'darwin', goarch: 'amd64', os: 'macOS', arch: 'Intel' },
  { goos: 'darwin', goarch: 'arm64', os: 'macOS', arch: 'Apple silicon' },
  { goos: 'windows', goarch: 'amd64', os: 'Windows', arch: 'x64' },
  { goos: 'windows', goarch: 'arm64', os: 'Windows', arch: 'arm64' },
] as const;

export type ReleaseAsset = {
  name: string;
  url: string;
  /** Byte count reported by GitHub, not an estimate. */
  size: number;
  os: string;
  arch: string;
};

export type Release = {
  tag: string;
  htmlUrl: string;
  publishedAt: string | null;
  assets: ReleaseAsset[];
  /** The SHA256SUMS file the workflow generates over the published archives. */
  checksumsUrl: string | null;
};

type GhAsset = { name: string; browser_download_url: string; size: number };
type GhRelease = {
  tag_name: string;
  html_url: string;
  published_at: string | null;
  draft: boolean;
  prerelease: boolean;
  assets: GhAsset[];
};

export function formatBytes(bytes: number): string {
  if (bytes <= 0) return '0 B';
  const mb = bytes / 1024 / 1024;
  if (mb >= 1) return `${mb.toFixed(1)} MB`;
  return `${Math.round(bytes / 1024)} KB`;
}

/**
 * Headers for the releases request.
 *
 * The User-Agent is not optional: GitHub rejects API requests without one. The
 * token is - an unauthenticated caller gets 60 requests per hour per IP, which
 * a shared build IP can exhaust, so a build environment that has a token in
 * GITHUB_TOKEN or GH_TOKEN gets the far larger authenticated bucket instead.
 */
function releaseRequestHeaders(token?: string): Record<string, string> {
  const headers: Record<string, string> = {
    Accept: 'application/vnd.github+json',
    'User-Agent': 'ecirlabs-web',
    'X-GitHub-Api-Version': '2022-11-28',
  };
  if (token) headers.Authorization = `Bearer ${token}`;
  return headers;
}

/**
 * Fetches the release list, retrying anonymously if a token was rejected.
 *
 * The retry matters because the token is picked up from the environment rather
 * than configured for this purpose: a CI image or a proxy can leave a
 * GITHUB_TOKEN in the environment that api.github.com will not accept, and
 * sending it turns a request that would have worked into a 401. Falling back to
 * an anonymous request keeps a bad token from being worse than no token.
 */
async function fetchReleases(): Promise<Response> {
  const token = process.env.GITHUB_TOKEN || process.env.GH_TOKEN;
  const url = `https://api.github.com/repos/${REPO}/releases?per_page=10`;
  // Re-checked hourly, so a new release appears without a redeploy.
  const next = { revalidate: 3600 } as const;

  if (token) {
    const authed = await fetch(url, { headers: releaseRequestHeaders(token), next });
    if (authed.ok || (authed.status !== 401 && authed.status !== 403)) return authed;
  }
  return fetch(url, { headers: releaseRequestHeaders(), next });
}

/**
 * The latest non-draft, non-prerelease release, or null when there is none.
 *
 * Returns null on any failure rather than throwing. This runs at build time, so
 * a rate-limited or unreachable API must degrade to the "build from source"
 * state instead of failing the build.
 */
export async function getLatestRelease(): Promise<Release | null> {
  let releases: GhRelease[];
  try {
    const res = await fetchReleases();
    if (!res.ok) return null;
    releases = (await res.json()) as GhRelease[];
  } catch {
    return null;
  }
  if (!Array.isArray(releases)) return null;

  const latest = releases.find((r) => !r.draft && !r.prerelease);
  if (!latest) return null;

  const assets: ReleaseAsset[] = [];
  for (const target of TARGETS) {
    // The workflow names archives matrix-os-<tag>-<goos>-<goarch>.{tar.gz,zip}.
    const suffix = `-${target.goos}-${target.goarch}.`;
    const asset = latest.assets.find(
      (a) => a.name.includes(suffix) && (a.name.endsWith('.tar.gz') || a.name.endsWith('.zip')),
    );
    if (!asset) continue;
    assets.push({
      name: asset.name,
      url: asset.browser_download_url,
      size: asset.size,
      os: target.os,
      arch: target.arch,
    });
  }

  const checksums = latest.assets.find((a) => a.name === 'SHA256SUMS');

  return {
    tag: latest.tag_name,
    htmlUrl: latest.html_url,
    publishedAt: latest.published_at,
    assets,
    checksumsUrl: checksums?.browser_download_url ?? null,
  };
}
