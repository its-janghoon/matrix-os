import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { budgetAccount, forgetBudget, recallAnyBudget, recallBudget, rememberBudget, type Budget } from './budget';

/**
 * A REFRESH MUST NOT HIDE AN ACCOUNT WITH THE READER'S MONEY IN IT.
 *
 * MetaMask needs an explicit connect, so /chat reloads holding no owner - and
 * everything about a budget was gated on knowing the owner. The card vanished,
 * taking with it the only Close button and the only copy of the budget's NAME,
 * which is the one string required to close it. That is the failure the runbook
 * calls "a budget account with money and no name", produced by the page.
 */
const budget: Budget = {
  buyer: 'eth:0x856e3fff84a5e833420b43cec0b4e13c16779817',
  delegate: 'a'.repeat(64),
  perJobCap: 10000n,
  maxPricePerUnit: 10000n,
  expiry: 1789650000n,
  nonce: 7n,
};

beforeEach(() => window.localStorage.clear());
afterEach(() => window.localStorage.clear());

describe('recalling a budget', () => {
  it('finds one without being told whose it is', () => {
    rememberBudget(budget);
    const got = recallAnyBudget();
    expect(got).not.toBeNull();
    // Every term, because the account NAME is built from all of them and a name
    // one field short cannot close anything.
    expect(budgetAccount(got!)).toBe(budgetAccount(budget));
    expect(got!.buyer).toBe(budget.buyer);
  });

  it('returns null when nothing was remembered', () => {
    expect(recallAnyBudget()).toBeNull();
  });

  it('returns null after it is forgotten', () => {
    rememberBudget(budget);
    forgetBudget();
    expect(recallAnyBudget()).toBeNull();
  });

  it('still refuses to hand one owner another owner\'s budget', () => {
    // recallAnyBudget is for SHOWING. The owner-checked read is what decides
    // whether a budget can be spent from, and that rule does not move.
    rememberBudget(budget);
    expect(recallBudget('eth:0xsomebodyelse')).toBeNull();
    expect(recallBudget(budget.buyer)).not.toBeNull();
  });

  it('survives storage holding something that is not a budget', () => {
    window.localStorage.setItem('matrix.budget.v1', 'not json');
    expect(recallAnyBudget()).toBeNull();
  });
});
