'use client';

import { useEffect, useState } from 'react';
import { FiChevronDown } from 'react-icons/fi';

import { formatMatrix, parseMatrix, SYMBOL } from '@/lib/wallet/format';
import { reportProblem } from '@/lib/wallet/node';
import { bond, bondedFor, withdrawBond } from '@/lib/wallet/stake';
import { connectMetamask, metamaskAvailable } from '@/lib/wallet/metamask';
import type { Signer } from '@/lib/wallet/signer';

import { Card, FIELD, PRIMARY_BUTTON, QUIET_BUTTON } from './ui';

/**
 * The caveats, where somebody will actually read them.
 *
 * Every word is still here. What changed is that it is no longer six paragraphs
 * of prose stacked under the table by default: the short version that a reader
 * needs in order to trust a column now sits on the column itself, and this is
 * where the long form waits for anyone who wants it. Nothing is softened,
 * because the limits are the honest part of the product and hiding them would
 * make the badges mean more than they do.
 */
export function HonestNotes() {
  const [open, setOpen] = useState(false);
  return (
    <Card className='px-6 py-5'>
      <button
        type='button'
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className='flex w-full items-center justify-between text-left'
      >
        <span className='text-sm font-semibold text-white'>What these numbers are, and are not</span>
        <FiChevronDown
          className={`h-4 w-4 text-grayscale-500 transition-transform ${open ? 'rotate-180' : ''}`}
        />
      </button>
      <p className='mt-1.5 text-xs text-grayscale-500'>
        Every figure here is read from the chain the answering node holds, or observed by that node itself. None of it
        is reported by the seller.
      </p>

      {open ? (
        <div className='mt-5 space-y-3.5 border-t border-white/[0.07] pt-5 text-sm leading-relaxed text-grayscale-400'>
          <p>
            <strong className='font-semibold text-grayscale-200'>Staked</strong> is capital the seller has posted on
            this chain, and it cannot be pulled on demand - the chain holds a bond for a minimum number of blocks after
            it is posted. It does not make anyone honest and cannot be taken away for bad service: no protocol can
            judge whether a completion was really the model advertised. What it does is make a listing cost money,
            which is what stops one attacker from filling this table with cheap fake sellers.
          </p>
          <p>
            <strong className='font-semibold text-grayscale-200'>Settled</strong> figures are transfers on the chain
            this node holds, not claims by the seller. A settlement is an ordinary transfer, so they count every
            payment the account received - a seller can pay itself. Payers is the harder one to inflate: it costs a
            funded account each.
          </p>
          <p>
            <strong className='font-semibold text-grayscale-200'>The badge</strong> means one checkable thing: the
            account this chain names as its maintainer signed a statement that it operates that node. It is an identity
            claim, not a rating - it does not say those sellers answer better, and a reader who does not trust that
            account should ignore it. It cannot be forged: the signature is checked against consensus state, which only
            the current maintainer&apos;s own signature can rotate, and it expires so a badge cannot outlive the
            arrangement it describes.
          </p>
          <p>
            It exists because a new network is mostly strangers with no settled history to tell them apart, and
            somebody has to go first. The honest way for the people running one to do that is to run sellers themselves
            and say so.
          </p>
          <p>
            <strong className='font-semibold text-grayscale-200'>There is no uptime column</strong> because an
            announcement carries no uptime. A number a seller publishes about its own reliability costs nothing to
            inflate, so the protocol does not carry one and this page will not invent one.
          </p>
          <p>
            <strong className='font-semibold text-grayscale-200'>One node&apos;s view.</strong> This is read from a
            single node&apos;s directory. That node heard these announcements and holds the chain the stake and settled
            history come from, so a different node may have heard others.
          </p>
          <p className='text-grayscale-500'>
            None of this says the answers are any good. Judge that yourself, on a small job, before sending a large
            one.
          </p>
        </div>
      ) : null}
    </Card>
  );
}

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
          <p className='mt-2 text-sm leading-relaxed text-grayscale-400'>
            Staking is what makes your listing cost something, which is the only thing separating you from an attacker
            who made ten thousand of them. It is an ordinary signed transfer to a reserved recipient, so the wallet you
            already have can do it - no key leaves this page.
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
        <div className='mt-5 flex flex-wrap items-center gap-3'>
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
          <span className='text-xs text-grayscale-500'>Connect a wallet to stake.</span>
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
          <p className='text-xs leading-relaxed text-grayscale-500'>
            A withdrawal takes the whole bond - the amount is not yours to choose - and the chain refuses it until the
            bond has been posted for its minimum number of blocks. That is the point of it: a stake that can be pulled
            in the next block was never capital at risk.
          </p>
        </div>
      )}

      {status !== '' ? <p className='mt-4 text-sm text-semantic-success'>{status}</p> : null}
      {problem !== '' ? <p className='mt-4 text-sm text-semantic-error'>{problem}</p> : null}
    </Card>
  );
}
