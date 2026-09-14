import { describe, expect, it } from 'vitest';

import { DEFAULT_ENDPOINT, LOCAL_ENDPOINT } from './node';

/**
 * The default endpoint was loopback, and loopback is a node the reader does not
 * have. /market asked a host that was not listening and rendered an empty table,
 * which does not read as "not connected" - it reads as a marketplace with no
 * sellers. The only audience with a node already running was the one audience
 * that did not need the page.
 */
describe('the node a page reaches for on its own', () => {
  it('is not the reader’s own machine', () => {
    expect(DEFAULT_ENDPOINT).not.toBe(LOCAL_ENDPOINT);
    expect(DEFAULT_ENDPOINT).not.toMatch(/127\.0\.0\.1|localhost|\[::1\]/);
  });

  it('is a URL a browser can actually fetch', () => {
    expect(() => new URL(DEFAULT_ENDPOINT)).not.toThrow();
    // A page served over HTTPS cannot fetch plain HTTP: the browser blocks it as
    // mixed content, and the failure looks like an unreachable node rather than
    // a scheme problem.
    expect(new URL(DEFAULT_ENDPOINT).protocol).toBe('https:');
  });

  it('still offers the local address for someone running a node', () => {
    expect(LOCAL_ENDPOINT).toBe('http://127.0.0.1:9093');
  });
});
