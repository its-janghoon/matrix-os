import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

/**
 * THE DEFECT THIS PINS. The site is dark-only: `html, body` set a near-black
 * background. They did NOT set a `color`, so anything that did not inherit an
 * explicit text colour rendered in the browser default - BLACK on near-black.
 *
 * It stayed invisible because every marketing page wrapped its own content in
 * `min-h-screen bg-black text-white`. /download does not (it composes
 * PageHero + Section directly), so its h1 was black on black and unreadable in
 * production while every test passed.
 *
 * There was also a SECOND stylesheet, src/app/globals.css, which did set body
 * color and was imported by nothing. It looked exactly like the place this was
 * already handled, so it is deleted rather than left as a trap - and this test
 * fails if it comes back, because two global stylesheets is the condition that
 * made the first bug hard to see.
 *
 * A DOM assertion cannot catch this: jsdom applies no stylesheet, so a
 * component test renders the same whether the rule exists or not. The
 * stylesheet text is the only available evidence.
 */
const stylesheet = readFileSync(join(process.cwd(), 'src/styles/globals.css'), 'utf8');

describe('the global stylesheet', () => {
  it('declares a foreground colour, not only a background', () => {
    // Matches `color: #fff` inside the html, body block. Without it the default
    // text colour is black, on a #060a16 background.
    const bodyBlock = stylesheet.match(/html,\s*body\s*\{([^}]*)\}/);
    expect(bodyBlock, 'no `html, body` block found in globals.css').not.toBeNull();
    expect(bodyBlock![1]).toMatch(/(^|[\s;])color\s*:/);
  });

  it('still sets the dark background the foreground is chosen against', () => {
    const bodyBlock = stylesheet.match(/html,\s*body\s*\{([^}]*)\}/);
    expect(bodyBlock![1]).toMatch(/background\s*:/);
  });

  it('is the only global stylesheet, so there is one place to look', () => {
    // src/app/globals.css was a full second copy that no layout imported.
    let orphan = false;
    try {
      readFileSync(join(process.cwd(), 'src/app/globals.css'), 'utf8');
      orphan = true;
    } catch {
      orphan = false;
    }
    expect(
      orphan,
      'src/app/globals.css is back. It is imported by no layout, so rules added ' +
        'there have no effect while looking like they do - which is how the ' +
        'black-on-black hero survived review.',
    ).toBe(false);
  });
});
