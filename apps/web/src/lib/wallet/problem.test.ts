import { describe, expect, it } from 'vitest';

import { NodeError, reportProblem } from './node';

/**
 * What a reader is told when a purchase fails.
 *
 * A node relays consensus's own words, and those are written for someone reading
 * a transaction. On the live site a chat window waited a minute and then said
 * "a draw pays a seller; returning money to the buyer is a close" - exact, and
 * useless to the person who had just tried to buy something. A reader who cannot
 * act on an error has been told nothing.
 */
describe('failures a reader can act on', () => {
  it('explains a budget that belongs to the seller', () => {
    const got = reportProblem(
      new NodeError('internal',
        'inference: submit the payment for job "b0ef6359": consensus: invalid message: ' +
          'a draw pays a seller; returning money to the buyer is a close'),
    );
    expect(got).toContain('cannot pay its own owner');
    expect(got).toContain('different seller');
    // And it does NOT leave the consensus phrasing in, which is the whole point.
    expect(got).not.toContain('is a close');
  });

  it('explains a budget opened for a browser key that is gone', () => {
    const got = reportProblem(
      new NodeError('internal', 'consensus: invalid message: only the delegate abc123 may draw on this budget, not def456'),
    );
    expect(got).toContain('Clearing site data');
    expect(got).toContain('close it');
  });

  it('keeps the number when a draw is over the per-job cap, and says what to change', () => {
    const got = reportProblem(new NodeError('internal', 'a draw of 5000 exceeds the per-job cap of 1000'));
    expect(got).toContain('5000');
    expect(got).toContain('per-job cap');
    expect(got).toContain('higher per-job cap');
  });

  it('passes anything else through unchanged', () => {
    // A wrapper that paraphrased every error would eventually paraphrase one it
    // had misread, and an exact message nobody understands still beats a
    // friendly one that is wrong.
    const exact = 'consensus: invalid message: something nobody has written a translation for';
    expect(reportProblem(new NodeError('internal', exact))).toBe(exact);
  });

  it('still names the two configuration mistakes that produce a dead page', () => {
    const got = reportProblem(new NodeError('unauthenticated', 'authentication required'));
    expect(got).toContain('public_reads');
    expect(got).toContain('signed_writes');
  });
});
