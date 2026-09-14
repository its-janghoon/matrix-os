'use client';

import Link from 'next/link';
import { useEffect, useState } from 'react';

import { formatMatrix, parseMatrix, SYMBOL } from '@/lib/wallet/format';
import { reportProblem } from '@/lib/wallet/node';
import { bond, bondedFor, withdrawBond } from '@/lib/wallet/stake';
import { connectMetamask, metamaskAvailable } from '@/lib/wallet/metamask';
import type { Signer } from '@/lib/wallet/signer';

import { Card, FIELD, PRIMARY_BUTTON, QUIET_BUTTON } from './ui';

export function StakePanel({
  endpoint,
  signer,
  setSigner,
  onChanged,
}: {
  endpoint: string;
  signer: Signer | null;
  setSigner: (s: Signer | null) => void;
  onChanged: () => void;
}) {
  const [amount, setAmount] = useState('');
  const [staked, setStaked] = useState<bigint | null>(null);
  const [busy, setBusy] = useState('');
  const [status, setStatus] = useState('');
  const [problem, setProblem] = useState('');

  useEffect(() => {
    let live = true;
    // Resolved rather than branched, so nothing sets state synchronously here
    // and a stale read cannot overwrite a newer one.
    const read = signer ? bondedFor(endpoint, signer.accountId) : Promise.resolve(null);
    read
      .then((v) => {
        if (live) setStaked(v);
      })
      .catch(() => {
        if (live) setStaked(null);
      });
    return () => {
      live = false;
    };
  }, [endpoint, signer, status]);

  // Typed in whole MATRIX, the denomination on every other number on this page.
  // A field that silently took base units would let somebody who typed "300000"
  // meaning a stake post a millionth of it and see a success message.
  const parsed = parseMatrix(amount);
  const amountValid = parsed !== null && parsed > 0n;

  const run = async (what: 'bond' | 'withdraw') => {
    if (!signer) return;
    setBusy(what);
    setProblem('');
    setStatus('');
    try {
      if (what === 'bond') {
        if (parsed === null || parsed <= 0n) {
          setProblem(`Enter an amount in ${SYMBOL}, with at most nine decimal places.`);
          return;
        }
        await bond(endpoint, signer, parsed);
        setStatus(`Staked ${formatMatrix(parsed)} ${SYMBOL}.`);
        setAmount('');
      } else {
        await withdrawBond(endpoint, signer);
        setStatus('The whole bond was returned.');
      }
      onChanged();
    } catch (err) {
      setProblem(reportProblem(err));
    } finally {
      setBusy('');
    }
  };

  return (
    <Card className='px-6 py-6'>
      <div className='flex flex-wrap items-start justify-between gap-4'>
        <div className='max-w-2xl'>
          <h2 className='text-base font-semibold text-white'>Selling compute here?</h2>
          <p className='mt-2 text-sm text-grayscale-400'>
            A stake makes your listing cost something. Your wallet signs it - no key leaves this page.{' '}
            <Link
              href='/docs/compute-marketplace#reading-the-directory'
              className='whitespace-nowrap text-primary-300 underline-offset-4 hover:underline'
            >
              Why it matters
            </Link>
          </p>
        </div>
        {signer !== null && staked !== null ? (
          <div className='rounded-xl border border-white/[0.07] bg-white/[0.02] px-4 py-3 text-right'>
            <p className='text-[11px] uppercase tracking-[0.08em] text-grayscale-500'>Your stake</p>
            <p className='mt-0.5 font-mono text-lg font-semibold tabular-nums text-white'>
              {formatMatrix(staked)} <span className='text-xs font-normal text-grayscale-500'>{SYMBOL}</span>
            </p>
          </div>
        ) : null}
      </div>

      {signer === null ? (
        <div className='mt-5'>
          <button
            className={PRIMARY_BUTTON}
            onClick={async () => {
              setProblem('');
              try {
                if (!metamaskAvailable()) {
                  setProblem('No wallet extension is installed on this page.');
                  return;
                }
                setSigner(await connectMetamask());
              } catch (err) {
                setProblem(reportProblem(err));
              }
            }}
          >
            Connect wallet
          </button>
        </div>
      ) : (
        <div className='mt-5 space-y-3'>
          <p className='font-mono text-xs text-grayscale-500'>{signer.accountId}</p>
          <div className='flex flex-wrap gap-2'>
            <div className='relative flex-1'>
              <input
                className={`${FIELD} pr-20 font-mono tabular-nums`}
                placeholder='0.0'
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                spellCheck={false}
                inputMode='decimal'
              />
              <span className='pointer-events-none absolute right-3.5 top-1/2 -translate-y-1/2 text-xs font-medium text-grayscale-500'>
                {SYMBOL}
              </span>
            </div>
            <button className={PRIMARY_BUTTON} disabled={busy !== '' || !amountValid} onClick={() => void run('bond')}>
              {busy === 'bond' ? 'Signing...' : 'Stake'}
            </button>
            <button
              className={QUIET_BUTTON}
              disabled={busy !== '' || staked === null || staked === 0n}
              onClick={() => void run('withdraw')}
            >
              {busy === 'withdraw' ? 'Signing...' : 'Withdraw all'}
            </button>
          </div>
          {amount !== '' && !amountValid ? (
            <p className='text-xs text-semantic-error'>
              Enter an amount in {SYMBOL}, with at most nine decimal places - the precision this chain carries.
            </p>
          ) : null}
          {/*
            Kept, and kept short. This is not a caveat about what a number means
            - it is a rule the reader will hit: they press Withdraw, get the
            whole bond back rather than the part they expected, or get refused
            outright. A surprise is worse than a line of text.
          */}
          <p className='text-xs text-grayscale-500'>
            Withdrawing takes the whole bond, and only after its minimum number of blocks.
          </p>
        </div>
      )}

      {status !== '' ? <p className='mt-4 text-sm text-semantic-success'>{status}</p> : null}
      {problem !== '' ? <p className='mt-4 text-sm text-semantic-error'>{problem}</p> : null}
    </Card>
  );
}
