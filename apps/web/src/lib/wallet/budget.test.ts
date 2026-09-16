import { describe, expect, it } from 'vitest';

import { budgetAccount, budgetCloseRecipient, budgetExpired, type Budget } from './budget';

// A budget account's NAME is its terms, and both a browser and a node have to
// spell it identically or the deposit lands somewhere the other cannot name.
// This asserts the exact string for fixed terms; the Go side asserts the same
// literal in token/spendescrow_test.go, so changing either one fails loudly
// rather than stranding somebody's money.
const FIXED: Budget = {
  buyer: 'eth:0x1111111111111111111111111111111111111111',
  delegate: '2'.repeat(64),
  perJobCap: 200000n,
  maxPricePerUnit: 500n,
  expiry: 1893456000n,
  nonce: 7n,
};

const FIXED_TERMS =
  'eth:0x1111111111111111111111111111111111111111.' +
  '2222222222222222222222222222222222222222222222222222222222222222.200000.500.1893456000.7';

describe('a budget account name', () => {
  it('is the exact string the chain names the same budget', () => {
    expect(budgetAccount(FIXED)).toBe(`spend/escrow/${FIXED_TERMS}`);
    expect(budgetCloseRecipient(FIXED)).toBe(`spend/close/${FIXED_TERMS}`);
  });

  // Every term is part of the identity, so changing any of them is a different
  // account. A page that treated two of these as one budget would show a
  // balance that is not there.
  it('changes completely when any term changes', () => {
    const variants: Budget[] = [
      { ...FIXED, perJobCap: FIXED.perJobCap + 1n },
      { ...FIXED, maxPricePerUnit: FIXED.maxPricePerUnit + 1n },
      { ...FIXED, expiry: FIXED.expiry + 1n },
      { ...FIXED, nonce: FIXED.nonce + 1n },
      { ...FIXED, delegate: '3'.repeat(64) },
    ];
    for (const v of variants) {
      expect(budgetAccount(v)).not.toBe(budgetAccount(FIXED));
    }
  });

  // Expiry is the moment it is dead, not the last moment it lives - the same
  // boundary consensus applies, so the page and the chain agree about a budget
  // in its final second.
  it('is expired at its expiry second and not before', () => {
    expect(budgetExpired(FIXED, new Date(Number(FIXED.expiry) * 1000))).toBe(true);
    expect(budgetExpired(FIXED, new Date((Number(FIXED.expiry) - 1) * 1000))).toBe(false);
  });
});
