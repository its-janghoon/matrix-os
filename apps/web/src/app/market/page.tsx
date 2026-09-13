'use client';

import { useCallback, useEffect, useState } from 'react';

import Navigation from '@/components/Navigation';
import { DEFAULT_ENDPOINT, listSellers, reportProblem, type Seller } from '@/lib/wallet/node';
import { bond, bondedFor, withdrawBond } from '@/lib/wallet/stake';
import { browserSigner } from '@/lib/wallet/browserSigner';
import { connectMetamask, metamaskAvailable } from '@/lib/wallet/metamask';
import type { Signer } from '@/lib/wallet/signer';
import { loadWallet } from '@/lib/wallet/wallet';

/**
 * Who is selling on this network, and what the chain says about them.
 *
 * WHY A PAGE OF ITS OWN. The chat client picks a seller automatically - cheapest
 * that can be reached - which is the right default and the wrong thing to be the
 * only option. The numbers that let a buyer refuse a stranger were being
 * computed, carried across the network and then never shown to the person whose
 * money it was.
 *
 * WHAT IS WORTH TRUSTING HERE, and the page says it rather than implying it with
 * badges. Every figure in the table is read from the chain the ANSWERING node
 * holds, or observed by that node itself. None of it is reported by the seller:
 * an announcement carries no uptime, no latency and no rating, because those are
 * exactly the numbers a seller can type anything into.
 *
 * And none of it says the answers are any good. Nothing on a chain can.
 */

const CARD = 'rounded-xl border border-gray-800 bg-gray-900/50 p-6';
const FIELD =
  'w-full rounded-lg border border-gray-700 bg-black/60 px-3 py-2 font-mono text-sm text-gray-100 ' +
  'outline-none focus:border-gray-500';
const BUTTON =
  'rounded-lg bg-white px-4 py-2 text-sm font-semibold text-black disabled:cursor-not-allowed disabled:opacity-40';

export default function MarketPage() {
  const [endpoint, setEndpoint] = useState(DEFAULT_ENDPOINT);
  const [sellers, setSellers] = useState<Seller[]>([]);
  const [loading, setLoading] = useState(true);
  const [problem, setProblem] = useState('');
  const [signer, setSigner] = useState<Signer | null>(null);
  const [reloads, setReloads] = useState(0);

  // The `live` guard is not ceremony. Changing the node re-runs this, and
  // without it a slow answer from the PREVIOUS node can land after a fast one
  // from the new one and quietly replace it - a directory showing a different
  // node's sellers under this node's address.
  useEffect(() => {
    let live = true;
    listSellers(endpoint)
      .then((got) => {
        if (!live) return;
        setSellers(got);
        setProblem('');
      })
      .catch((err) => {
        if (!live) return;
        setProblem(reportProblem(err));
        setSellers([]);
      })
      .finally(() => {
        if (live) setLoading(false);
      });
    return () => {
      live = false;
    };
  }, [endpoint, reloads]);

  const refresh = useCallback(() => {
    setLoading(true);
    setReloads((n) => n + 1);
  }, []);

  useEffect(() => {
    let live = true;
    void loadWallet()
      .then((w) => {
        if (live) setSigner(w ? browserSigner(w) : null);
      })
      .catch(() => {
        if (live) setSigner(null);
      });
    return () => {
      live = false;
    };
  }, []);

  return (
    <>
      <Navigation />
      <main className='min-h-screen bg-black px-4 pb-16 pt-24'>
        <div className='mx-auto max-w-5xl space-y-6'>
          <header>
            <h1 className='text-2xl font-semibold text-gray-100'>Who is selling</h1>
            <p className='mt-2 text-sm text-gray-400'>
              Read from one node&apos;s directory. That node heard these announcements and holds the chain the stake and
              settled history are read from, so a different node may have heard others.
            </p>
          </header>

          <section className={CARD}>
            <label className='mb-2 block text-xs uppercase tracking-wide text-gray-500'>Node</label>
            <div className='flex flex-wrap gap-2'>
              <input
                className={`${FIELD} flex-1`}
                value={endpoint}
                onChange={(e) => setEndpoint(e.target.value)}
                spellCheck={false}
              />
              {/*
                Deliberately NOT disabled while loading. A read that hangs would
                otherwise take the only control that recovers from it away, and
                the reader is left looking at "Reading..." with nothing to press.
                A second read superseding a first is handled by the guard in the
                effect, so pressing it again is safe.
              */}
              <button className={BUTTON} onClick={refresh}>
                {loading ? 'Reading...' : 'Refresh'}
              </button>
            </div>
          </section>

          {problem !== '' ? (
            <p className='rounded-lg border border-red-500/30 bg-red-500/5 p-4 text-sm text-red-300'>{problem}</p>
          ) : null}

          <SellerTable sellers={sellers} loading={loading} />
          <WhatTheseNumbersMean />
          <StakePanel endpoint={endpoint} signer={signer} onChanged={refresh} setSigner={setSigner} />
        </div>
      </main>
    </>
  );
}

function SellerTable({ sellers, loading }: { sellers: Seller[]; loading: boolean }) {
  if (loading && sellers.length === 0) {
    return <p className='text-sm text-gray-400'>Reading the directory...</p>;
  }
  if (sellers.length === 0) {
    return (
      <div className={CARD}>
        <p className='text-sm text-gray-400'>
          This node has heard nothing. Either nobody is selling, or it has no peers to hear them from.
        </p>
      </div>
    );
  }

  const sorted = [...sellers].sort((a, b) =>
    a.pricePerUnit === b.pricePerUnit ? a.id.localeCompare(b.id) : a.pricePerUnit < b.pricePerUnit ? -1 : 1,
  );

  return (
    <div className={`${CARD} overflow-x-auto`}>
      <table className='w-full min-w-[720px] text-left font-mono text-xs'>
        <thead>
          <tr className='text-gray-500'>
            <th className='pb-2 pr-4 font-normal uppercase tracking-wide'>Reached at</th>
            <th className='pb-2 pr-4 font-normal uppercase tracking-wide'>Models</th>
            <th className='pb-2 pr-4 text-right font-normal uppercase tracking-wide'>Price/unit</th>
            <th className='pb-2 pr-4 text-right font-normal uppercase tracking-wide'>Staked</th>
            <th className='pb-2 pr-4 text-right font-normal uppercase tracking-wide'>Paid</th>
            <th className='pb-2 text-right font-normal uppercase tracking-wide'>Payers</th>
          </tr>
        </thead>
        <tbody className='text-gray-200'>
          {sorted.map((s) => (
            <tr key={`${s.nodeId}:${s.id}`} className='border-t border-gray-800'>
              <td className='py-2 pr-4'>
                {s.endpoint === '' ? (
                  <span className='text-gray-600'>no address; compute only</span>
                ) : (
                  s.endpoint
                )}
              </td>
              <td className='py-2 pr-4 text-gray-400'>{s.models.join(', ') || '-'}</td>
              <td className='py-2 pr-4 text-right'>{s.pricePerUnit.toString()}</td>
              <td className={`py-2 pr-4 text-right ${s.bonded === 0n ? 'text-amber-500/80' : ''}`}>
                {s.bonded.toString()}
              </td>
              <td className='py-2 pr-4 text-right'>{s.settledPayments.toString()}</td>
              <td className='py-2 text-right'>{s.settledPayers.toString()}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function WhatTheseNumbersMean() {
  return (
    <section className={`${CARD} space-y-3 text-sm text-gray-400`}>
      <h2 className='font-semibold text-gray-200'>What these numbers are, and are not</h2>
      <p>
        <strong className='text-gray-300'>Staked</strong> is capital the seller has posted on this chain, and it cannot
        be pulled on demand - the chain holds a bond for a minimum number of blocks after it is posted. It does not make
        anyone honest and cannot be taken away for bad service: no protocol can judge whether a completion was really the
        model advertised. What it does is make a listing cost money, which is what stops one attacker from filling this
        table with cheap fake sellers.
      </p>
      <p>
        <strong className='text-gray-300'>Paid</strong> and <strong className='text-gray-300'>Payers</strong> are settled
        transfers on the chain this node holds, not claims by the seller. A settlement is an ordinary transfer, so they
        count every payment the account received - a seller can pay itself. Payers is the harder one to inflate: it costs
        a funded account each.
      </p>
      <p>
        There is no uptime column because an announcement carries no uptime. A number a seller publishes about its own
        reliability costs nothing to inflate, so the protocol does not carry one and this page will not invent one.
      </p>
      <p className='text-gray-500'>
        None of this says the answers are any good. Judge that yourself, on a small job, before sending a large one.
      </p>
    </section>
  );
}

function StakePanel({
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

  const run = async (what: 'bond' | 'withdraw') => {
    if (!signer) return;
    setBusy(what);
    setProblem('');
    setStatus('');
    try {
      if (what === 'bond') {
        const value = BigInt(amount.trim() === '' ? '0' : amount.trim());
        await bond(endpoint, signer, value);
        setStatus(`Staked ${value} base units.`);
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
    <section className={`${CARD} space-y-4`}>
      <div>
        <h2 className='font-semibold text-gray-200'>Selling here?</h2>
        <p className='mt-2 text-sm text-gray-400'>
          Staking is what makes your listing cost something, which is the only thing separating you from an attacker who
          made ten thousand of them. It is an ordinary signed transfer to a reserved recipient, so the wallet you already
          have can do it - no key leaves this page.
        </p>
      </div>

      {signer === null ? (
        <div className='space-y-2'>
          <p className='text-sm text-gray-400'>Connect a wallet to stake.</p>
          <button
            className={BUTTON}
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
            Connect MetaMask
          </button>
        </div>
      ) : (
        <div className='space-y-3'>
          <p className='font-mono text-xs text-gray-500'>
            {signer.accountId}
            {staked !== null ? ` - ${staked.toString()} base units staked` : ''}
          </p>
          <div className='flex flex-wrap gap-2'>
            <input
              className={`${FIELD} flex-1`}
              placeholder='amount in base units'
              value={amount}
              onChange={(e) => setAmount(e.target.value.replace(/[^0-9]/g, ''))}
              spellCheck={false}
            />
            <button className={BUTTON} disabled={busy !== '' || amount === ''} onClick={() => void run('bond')}>
              {busy === 'bond' ? 'Signing...' : 'Stake'}
            </button>
            <button
              className='rounded-lg border border-gray-700 px-4 py-2 text-sm font-semibold text-gray-200 disabled:opacity-40'
              disabled={busy !== '' || staked === null || staked === 0n}
              onClick={() => void run('withdraw')}
            >
              {busy === 'withdraw' ? 'Signing...' : 'Withdraw all'}
            </button>
          </div>
          <p className='text-xs text-gray-500'>
            A withdrawal takes the whole bond - the amount is not yours to choose - and the chain refuses it until the
            bond has been posted for its minimum number of blocks. That is the point of it: a stake that can be pulled in
            the next block was never capital at risk.
          </p>
        </div>
      )}

      {status !== '' ? <p className='text-sm text-emerald-400/80'>{status}</p> : null}
      {problem !== '' ? <p className='text-sm text-red-300'>{problem}</p> : null}
    </section>
  );
}
