import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

// The install snippet used to set OS=$(uname -s). On Git Bash that is
// mingw64_nt-10.0-..., which is not a GOOS, so the download 404s and tar
// looks for a .tar.gz Windows never ships. Pin the mapping here so a
// "simplify the snippet" edit cannot put that bug back.
describe('installation FETCH_RELEASE snippet', () => {
  const source = readFileSync(join(dirname(fileURLToPath(import.meta.url)), 'page.tsx'), 'utf8');
  const match = source.match(/const FETCH_RELEASE = `([\s\S]*?)`;/);
  const snippet = match?.[1] ?? '';

  it('maps Git Bash uname to the windows zip the release workflow publishes', () => {
    expect(snippet).toContain('mingw*|msys*|cygwin*) OS=windows; EXT=zip');
    expect(snippet).toContain('matrix-os-$TAG-$OS-$ARCH.$EXT');
    expect(snippet).not.toMatch(/OS=\$\(uname -s/);
    expect(snippet).toContain('unzip -o');
  });
});
