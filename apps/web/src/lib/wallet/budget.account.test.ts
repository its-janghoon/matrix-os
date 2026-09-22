import { describe, expect, it } from 'vitest';

import { budgetAccount, budgetFromAccount, type Budget } from './budget';

/**
 * A RESERVATION MUST FIT THE BUDGET PAYING FOR IT.
 *
 * /chat reserved a constant 4096 units. A budget opened through the same page caps
 * one job at a tenth of its deposit, so at 1000 base units per unit that was a
 * 4,096,000 reservation against a 417,710 cap — and consensus refuses a
 * budget-funded reservation over the cap by committing the deposit and moving
 * nothing. Observed live at transfer index 215, block 4140.
 *
 * The reservation is now read from the budget's own account NAME, which is the
 * authorisation itself rather than a copy of it kept beside it.
 */
const liveBudget: Budget = {
  buyer: 'eth:0xf9cd1de365a4ad85b295260c36dd9e3113991f54',
  delegate: '447eb81d40270bb7bcd5edc04aaca4cd8f47d676f3e5a6c402a2d5da676a20c2',
  perJobCap: 417_710n,
  maxPricePerUnit: 10_000n,
  expiry: 1_790_144_190n,
  nonce: 0n,
};

describe('reading a budget back out of its account id', () => {
  it('round-trips every term, because a name one field short authorises nothing', () => {
    const got = budgetFromAccount(budgetAccount(liveBudget));
    expect(got).not.toBeNull();
    expect(got).toEqual(liveBudget);
  });

  it('reads the live budget that produced the failure', () => {
    // The exact account name from the chain.
    const id =
      'spend/escrow/eth:0xf9cd1de365a4ad85b295260c36dd9e3113991f54.' +
      '447eb81d40270bb7bcd5edc04aaca4cd8f47d676f3e5a6c402a2d5da676a20c2.417710.10000.1790144190.0';
    const got = budgetFromAccount(id);
    expect(got?.perJobCap).toBe(417_710n);
    // 4096 units at 1000 each is 4,096,000, which is what was reserved and what
    // the cap refused. The cap affords 417 units, not 4096.
    expect(got!.perJobCap / 1_000n).toBe(417n);
  });

  it('is null for a wallet paying directly, which has no per-job cap', () => {
    expect(budgetFromAccount('eth:0xf9cd1de365a4ad85b295260c36dd9e3113991f54')).toBeNull();
    expect(budgetFromAccount('a'.repeat(64))).toBeNull();
  });

  it('is null for a budget-shaped name that does not carry six terms', () => {
    // Guessing at a malformed name would mean reasoning about a cap nobody signed.
    expect(budgetFromAccount('spend/escrow/not.enough.fields')).toBeNull();
    expect(budgetFromAccount('spend/escrow/a.b.notanumber.d.e.f')).toBeNull();
  });
});
