'use client';

import { useState } from 'react';

import { budgetAccount, budgetExpired, closeBudget, forgetBudget, type Budget } from '@/lib/wallet/budget';
import { reportProblem } from '@/lib/wallet/node';
import type { Signer } from '@/lib/wallet/signer';

const CARD = 'rounded-xl border border-gray-800 bg-gray-900/50 p-6';

/**
 * A budget this browser knows about but cannot SPEND from.
 *
 * WHY IT EXISTS. Reloading /chat with a budget open made the card - and with it
 * the only Close button and the only copy of the account's NAME - disappear.
 * MetaMask needs an explicit connect, so the first render has no owner to check
 * a budget against, and the card was gated on that owner. From the reader's
 * chair an account holding their money had simply gone.
 *
 * WHY IT NOW CLOSES. Telling the reader to connect the owning wallet was only
 * half true: connecting it brought back nothing, because the Close button lived
 * on a card gated on the DELEGATE key rather than the owner. A browser that had
 * lost that key - cleared data, another profile - could therefore see the budget,
 * be told what to do, do it, and still have no way to get the money out. Closing
 * never needed the delegate; the owner signs it. So the button is here, on the
 * one condition that actually governs it: the connected wallet owns the budget.
 */
export function StrandedBudget({
  budget,
  endpoint,
  wallet,
  onClosed,
  onChanged,
  setProblem,
}: {
  budget: Budget;
  endpoint: string;
  wallet: Signer | null;
  onClosed: () => void;
  onChanged: () => void;
  setProblem: (s: string) => void;
}) {
  const account = budgetAccount(budget);
  const dead = budgetExpired(budget);
  // The only thing that decides whether this page can close the budget. Not the
  // delegate, which spends it, and not whether this browser opened it.
  const owned = wallet !== null && wallet.accountId === budget.buyer;
  const [busy, setBusy] = useState(false);
  return (
    <section className={`${CARD} border-amber-500/40`}>
      <p className='text-sm text-amber-200'>
        {dead
          ? 'A budget you opened here has expired. Nothing more can be spent from it, and the rest is still yours.'
          : 'A budget you opened here is still open, and this browser cannot spend from it.'}
      </p>
      <p className='mt-2 text-sm text-gray-400'>
        {owned ? (
          <>
            This browser no longer holds the key that spends it, so messages cannot be paid from it here. Closing it
            does not need that key - only <span className='text-gray-100'>{budget.buyer}</span>, which is connected.
          </>
        ) : (
          <>
            Connect <span className='text-gray-100'>{budget.buyer}</span> above, and the button to close it and take
            back what is left comes back with it.
          </>
        )}
      </p>
      <p className='mt-3 break-all font-mono text-xs text-gray-500'>{account}</p>
      <p className='mt-2 text-xs text-gray-500'>
        That is the budget&apos;s name. Closing it means naming it, so keep the line if you keep anything - expiry does
        not lose the balance, it only stops further spending.
      </p>
      <div className='mt-4 flex flex-wrap gap-3'>
        {owned ? (
          <button
            className='rounded-lg bg-white px-4 py-2 text-sm font-semibold text-black disabled:opacity-40'
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              setProblem('');
              try {
                await closeBudget(endpoint, wallet, budget);
                forgetBudget();
                onClosed();
                onChanged();
              } catch (err) {
                setProblem(reportProblem(err));
              } finally {
                setBusy(false);
              }
            }}
          >
            {busy ? 'closing...' : 'close and take back what is left'}
          </button>
        ) : null}
        <button
          className='rounded-lg border border-gray-600 px-4 py-2 text-sm font-semibold text-gray-100'
          onClick={() => {
            void navigator.clipboard?.writeText(account);
          }}
        >
          copy the budget&apos;s name
        </button>
      </div>
    </section>
  );
}
