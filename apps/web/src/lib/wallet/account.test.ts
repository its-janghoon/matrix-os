import { describe, expect, it } from 'vitest';

import { accountIdProblem, canonicalAccountId, UnreachableAccountError } from './account';

// The EIP-55 form of a real address: what every wallet and explorer shows, and
// therefore what a person copies.
const DISPLAYED = '0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed';
const LOWER = DISPLAYED.toLowerCase();

describe('canonicalAccountId', () => {
  it('sends the form a person copies to the account their key controls', () => {
    expect(canonicalAccountId(`eth:${DISPLAYED}`)).toBe(`eth:${LOWER}`);
  });

  it('takes the all-lowercase machine form at face value', () => {
    expect(canonicalAccountId(`eth:${LOWER}`)).toBe(`eth:${LOWER}`);
  });

  it('refuses a mistyped address rather than repairing it', () => {
    // One character changed. Still 40 valid hex characters, so only the
    // checksum can tell - and viem's getAddress would have silently returned
    // the correctly-checksummed form of the WRONG address, sending the money to
    // somebody else.
    const typo = 'eth:0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeD';
    expect(() => canonicalAccountId(typo)).toThrow(UnreachableAccountError);
    expect(() => canonicalAccountId(typo)).toThrow(/will not reach anybody/);
  });

  it('never turns a mistyped address into a different valid one', () => {
    const typo = 'eth:0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeD';
    let settled: string | null = null;
    try {
      settled = canonicalAccountId(typo);
    } catch {
      settled = null;
    }
    expect(settled).toBeNull();
  });

  it('leaves every other kind of account id alone', () => {
    for (const id of [
      '5aaeb6053f3e94c9b9a09f33669435e7ef1beaed5aaeb6053f3e94c9b9a09f33',
      'bridge/escrow',
      'consensus/stake/bond/abc123',
      // A reserved recipient carries its terms in the NAME, so case there is
      // meaning. Touching it would change what it says.
      'infer/escrow/JOB-1/4000000.1700000000/provider/payer',
      '',
    ]) {
      expect(canonicalAccountId(id)).toBe(id);
    }
  });

  it('survives the whitespace a paste brings with it', () => {
    expect(canonicalAccountId(`  eth:${DISPLAYED}\n`)).toBe(`eth:${LOWER}`);
  });

  it('refuses an id that is not an address at all', () => {
    for (const id of [
      'eth:',
      'eth:not-an-address',
      `eth:${DISPLAYED.slice(0, -1)}`,
      `eth:${DISPLAYED}d`,
      'eth:0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeZ',
    ]) {
      expect(() => canonicalAccountId(id)).toThrow(UnreachableAccountError);
    }
  });

  it('is idempotent, so a caller that cannot tell is not punished for asking', () => {
    const once = canonicalAccountId(`eth:${DISPLAYED}`);
    expect(canonicalAccountId(once)).toBe(once);
  });
});

describe('accountIdProblem', () => {
  it('says nothing while the field is still empty', () => {
    expect(accountIdProblem('')).toBeNull();
    expect(accountIdProblem('   ')).toBeNull();
  });

  it('says nothing about an id that will work', () => {
    expect(accountIdProblem(`eth:${DISPLAYED}`)).toBeNull();
    expect(accountIdProblem('bridge/escrow')).toBeNull();
  });

  it('explains the problem rather than throwing it', () => {
    const problem = accountIdProblem('eth:0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeD');
    expect(problem).toMatch(/will not reach anybody/);
  });
});
