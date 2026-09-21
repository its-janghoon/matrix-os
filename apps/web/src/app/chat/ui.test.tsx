import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { budgetAccount, type Budget } from '@/lib/wallet/budget';
import type { Signer } from '@/lib/wallet/signer';

import { StrandedBudget } from './ui';

/**
 * THE BUTTON THAT WAS NOT THERE.
 *
 * A budget this browser cannot SPEND from is still one its owner can CLOSE - the
 * buyer signs a close, and the delegate key that spends it has no part in it. The
 * page used to put the only Close button on a card gated on that delegate, so a
 * browser that had lost the key could see the money, be told to connect the
 * owning wallet, do it, and find nothing.
 *
 * These assertions are on the single condition that governs the button: the
 * connected wallet owns the budget.
 */
const budget: Budget = {
  buyer: 'eth:0x856e3fff84a5e833420b43cec0b4e13c16779817',
  delegate: 'a'.repeat(64),
  perJobCap: 10_000n,
  maxPricePerUnit: 10_000n,
  expiry: 1_789_650_000n,
  nonce: 7n,
};

function walletFor(accountId: string): Signer {
  return {
    kind: 'browser',
    accountId,
    signRunAuthorization: () => Promise.reject(new Error('not used')),
    signPayment: () => Promise.reject(new Error('not used')),
  };
}

const noop = () => {};

function renderCard(wallet: Signer | null) {
  return render(
    <StrandedBudget
      budget={budget}
      endpoint='http://127.0.0.1:8080'
      wallet={wallet}
      onClosed={noop}
      onChanged={noop}
      setProblem={noop}
    />,
  );
}

describe('the card for a budget this browser cannot spend from', () => {
  it('offers to close it when the wallet that owns it is connected', () => {
    renderCard(walletFor(budget.buyer));
    expect(screen.getByRole('button', { name: /close and take back/i })).toBeTruthy();
  });

  it('does not offer to close it with no wallet connected, and says what to do', () => {
    renderCard(null);
    expect(screen.queryByRole('button', { name: /close and take back/i })).toBeNull();
    // The instruction has to be true: connecting THIS account is what brings the
    // button, and it now does.
    expect(screen.getByText(budget.buyer)).toBeTruthy();
  });

  it('does not offer to close somebody else\'s budget', () => {
    renderCard(walletFor('eth:0x0000000000000000000000000000000000000001'));
    expect(screen.queryByRole('button', { name: /close and take back/i })).toBeNull();
  });

  it('always names the account, because the name is the only way back to it', () => {
    for (const wallet of [null, walletFor(budget.buyer)]) {
      const { unmount } = renderCard(wallet);
      expect(screen.getByText(budgetAccount(budget))).toBeTruthy();
      unmount();
    }
  });
});
