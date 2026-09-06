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
 * The latest non-draft, non-prerelease release, or null when there is none.
 *
 * Returns null on any failure rather than throwing. This runs at build time, so
 * a rate-limited or unreachable API must degrade to the "build from source"
 * state instead of failing the build.
 */
export async function getLatestRelease(): Promise<Release | null> {
  let releases: GhRelease[];
  try {
    const res = await fetch(`https://api.github.com/repos/${REPO}/releases?per_page=10`, {
      headers: { Accept: 'application/vnd.github+json' },
      // Re-checked hourly, so a new release appears without a redeploy.
      next: { revalidate: 3600 },
    });
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
