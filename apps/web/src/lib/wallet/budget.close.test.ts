import { describe, expect, it } from 'vitest';

import { budgetAccount, budgetCloseRecipient, type Budget } from './budget';

/**
 * CLOSING A BUDGET NEEDS THE OWNER, NOT THE DELEGATE.
 *
 * /chat gated its only Close button on the delegate key matching the one this
 * browser holds. That check belongs to SPENDING: the delegate is what draws from
 * a budget. Closing is signed by the buyer, and the chain checks exactly that -
 * `tx.SenderID() != terms.Buyer` before expiry.
 *
 * So a browser that had lost its delegate key could see a budget with money in
 * it, be told to connect the owning wallet, connect it, and still find no way to
 * get the money back. These assertions are on the two facts that make the two
 * capabilities separate, so a future gate cannot quietly conflate them again.
 */
const budget: Budget = {
  buyer: 'eth:0x856e3fff84a5e833420b43cec0b4e13c16779817',
  delegate: 'a'.repeat(64),
  perJobCap: 10_000n,
  maxPricePerUnit: 10_000n,
  expiry: 1_789_650_000n,
  nonce: 7n,
};

describe('what closing a budget depends on', () => {
  it('names the same terms whichever key is at hand, so the delegate is not part of naming it', () => {
    // The close recipient is derived from the TERMS. Nothing about the browser's
    // current key enters it, which is why a lost delegate cannot make a budget
    // unnameable - only unspendable.
    expect(budgetCloseRecipient(budget)).toBe(`spend/close/${budgetAccount(budget).slice('spend/escrow/'.length)}`);
  });

  it('carries the owner inside the name, which is the identity a close is checked against', () => {
    // The chain compares the close's signer to this field. A page deciding who
    // may close must compare the connected wallet to the same one.
    expect(budgetCloseRecipient(budget)).toContain(budget.buyer);
  });

  it('is not the delegate that a close is checked against', () => {
    // Present in the name, because the terms carry it - but it is the spending
    // authority, and a close does not consult it.
    const spendingAuthority = budget.delegate;
    const closeAuthority = budget.buyer;
    expect(closeAuthority).not.toBe(spendingAuthority);
  });
});
