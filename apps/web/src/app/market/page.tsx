'use client';

import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';

import Navigation from '@/components/Navigation';
import { DEFAULT_ENDPOINT, listSellers, reportProblem, type Seller } from '@/lib/wallet/node';
import { searchSellers, sellersByModel, type ModelRow, type SortKey } from '@/lib/wallet/search';
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

  const [view, setView] = useState<'sellers' | 'models'>('sellers');
  const [text, setText] = useState('');
  const [minBond, setMinBond] = useState('');
  const [reachableOnly, setReachableOnly] = useState(true);
  const [attestedOnly, setAttestedOnly] = useState(false);
  const [sort, setSort] = useState<SortKey>('price');

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

  const shown = searchSellers(sellers, {
    text,
    minBond: minBond.trim() === '' ? undefined : BigInt(minBond.trim()),
    reachableOnly,
    attestedOnly,
    sort,
  });

  return (
    <>
      <Navigation />
      <main className='min-h-screen bg-black px-4 pb-16 pt-24'>
        <div className='mx-auto max-w-6xl space-y-6'>
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

          <Filters
            view={view}
            setView={setView}
            text={text}
            setText={setText}
            minBond={minBond}
            setMinBond={setMinBond}
            reachableOnly={reachableOnly}
            setReachableOnly={setReachableOnly}
            attestedOnly={attestedOnly}
            setAttestedOnly={setAttestedOnly}
            sort={sort}
            setSort={setSort}
            total={sellers.length}
            showing={shown.length}
          />

          {view === 'models' ? (
            <ModelTable rows={sellersByModel(sellers)} onPick={(m) => { setText(m); setView('sellers'); }} />
          ) : (
            <SellerTable sellers={shown} loading={loading} endpoint={endpoint} searched={text.trim() !== ''} />
          )}
          <WhatTheseNumbersMean />
          <StakePanel endpoint={endpoint} signer={signer} onChanged={refresh} setSigner={setSigner} />
        </div>
      </main>
    </>
  );
}

function SellerTable({
  sellers,
  loading,
  endpoint,
  searched,
}: {
  sellers: Seller[];
  loading: boolean;
  endpoint: string;
  searched: boolean;
}) {
  if (loading && sellers.length === 0) {
    return <p className='text-sm text-gray-400'>Reading the directory...</p>;
  }
  if (sellers.length === 0) {
    return (
      <div className={CARD}>
        <p className='text-sm text-gray-400'>
          {searched
            ? 'Nothing here matches. A directory only holds what THIS node has heard, so a seller you know of may simply not have reached it.'
            : 'This node has heard nothing. Either nobody is selling, or it has no peers to hear them from.'}
        </p>
      </div>
    );
  }

  // Already ordered by the query; re-sorting here would silently override the
  // column the reader chose.
  const sorted = sellers;

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
            <th className='pb-2 pr-4 text-right font-normal uppercase tracking-wide'>Payers</th>
            <th className='pb-2 font-normal uppercase tracking-wide'> </th>
          </tr>
        </thead>
        <tbody className='text-gray-200'>
          {sorted.map((s) => (
            <tr key={`${s.nodeId}:${s.id}`} className='border-t border-gray-800'>
              <td className='py-2 pr-4'>
                {s.endpoint === '' ? (
                  <span className='text-gray-600'>no address; compute only</span>
                ) : (
                  <span className='flex items-center gap-2'>
                    {s.endpoint}
                    {s.operatorAttested ? (
                      // Titled with what was actually checked. A badge whose
                      // meaning a reader has to guess becomes "this one is
                      // good", which is the one thing it does not say.
                      <span
                        className='rounded border border-sky-500/40 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-sky-300'
                        title={`${s.operatorName} - the maintainer this chain names signed that it operates this node. An identity claim, not a rating.`}
                      >
                        {s.operatorName || 'attested'}
                      </span>
                    ) : null}
                  </span>
                )}
              </td>
              <td className='py-2 pr-4 text-gray-400'>{s.models.join(', ') || '-'}</td>
              <td className='py-2 pr-4 text-right'>{s.pricePerUnit.toString()}</td>
              <td className={`py-2 pr-4 text-right ${s.bonded === 0n ? 'text-amber-500/80' : ''}`}>
                {s.bonded.toString()}
              </td>
              <td className='py-2 pr-4 text-right'>{s.settledPayments.toString()}</td>
              <td className='py-2 pr-4 text-right'>{s.settledPayers.toString()}</td>
              <td className='py-2 text-right'>
                {s.endpoint === '' || s.models.length === 0 ? (
                  <span className='text-gray-700'>-</span>
                ) : (
                  // The choice travels as an IDENTITY, and deliberately not as an
                  // address: a link carrying its own endpoint would let whoever
                  // sent it route the recipient's prompt at a host of their
                  // choosing while the page named the seller they thought they
                  // picked. The chat page reads the address back from the
                  // directory itself.
                  <Link
                    className='text-gray-300 underline underline-offset-2 hover:text-white'
                    href={{
                      pathname: '/chat',
                      query: {
                        node: s.nodeId,
                        provider: s.id,
                        model: s.models[0],
                        endpoint,
                      },
                    }}
                  >
                    Buy here
                  </Link>
                )}
              </td>
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
        <strong className='text-gray-300'>The badge</strong> means one checkable thing: the account this chain names as
        its maintainer signed a statement that it operates that node. It is an identity claim, not a rating - it does
        not say those sellers answer better, and a reader who does not trust that account should ignore it. It cannot be
        forged: the signature is checked against consensus state, which only the current maintainer&apos;s own signature
        can rotate, and it expires so a badge cannot outlive the arrangement it describes.
      </p>
      <p>
        It exists because a new network is mostly strangers with no settled history to tell them apart, and somebody has
        to go first. The honest way for the people running one to do that is to run sellers themselves and say so.
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

/**
 * The controls, and a count of what they left.
 *
 * The count is not decoration. A filter that quietly removes everything looks
 * identical to a network where nobody is selling, and a reader who cannot tell
 * those apart goes looking for a problem that is on their own screen.
 */
function Filters({
  view,
  setView,
  text,
  setText,
  minBond,
  setMinBond,
  reachableOnly,
  setReachableOnly,
  attestedOnly,
  setAttestedOnly,
  sort,
  setSort,
  total,
  showing,
}: {
  view: 'sellers' | 'models';
  setView: (v: 'sellers' | 'models') => void;
  text: string;
  setText: (v: string) => void;
  minBond: string;
  setMinBond: (v: string) => void;
  reachableOnly: boolean;
  setReachableOnly: (v: boolean) => void;
  attestedOnly: boolean;
  setAttestedOnly: (v: boolean) => void;
  sort: SortKey;
  setSort: (v: SortKey) => void;
  total: number;
  showing: number;
}) {
  const tab = (active: boolean) =>
    `rounded-lg px-3 py-1.5 text-sm ${active ? 'bg-white text-black' : 'border border-gray-700 text-gray-300'}`;

  return (
    <section className={`${CARD} space-y-4`}>
      <div className='flex flex-wrap items-center gap-2'>
        <button className={tab(view === 'sellers')} onClick={() => setView('sellers')}>
          Sellers
        </button>
        <button className={tab(view === 'models')} onClick={() => setView('models')}>
          Models
        </button>
        <span className='ml-auto font-mono text-xs text-gray-500'>
          {view === 'sellers' ? `${showing} of ${total} shown` : `${total} listings`}
        </span>
      </div>

      {view === 'sellers' ? (
        <>
          <input
            className={FIELD}
            placeholder='search by model, address, node or payout account'
            value={text}
            onChange={(e) => setText(e.target.value)}
            spellCheck={false}
          />
          <div className='flex flex-wrap items-center gap-3 text-xs text-gray-400'>
            <label className='flex items-center gap-2'>
              <span className='uppercase tracking-wide text-gray-500'>Sort</span>
              <select
                className='rounded border border-gray-700 bg-black/60 px-2 py-1 text-gray-200 outline-none'
                value={sort}
                onChange={(e) => setSort(e.target.value as SortKey)}
              >
                <option value='price'>cheapest first</option>
                <option value='staked'>most staked</option>
                <option value='paid'>most payments</option>
                <option value='payers'>most payers</option>
              </select>
            </label>
            <label className='flex items-center gap-2'>
              <span className='uppercase tracking-wide text-gray-500'>Min stake</span>
              <input
                className='w-40 rounded border border-gray-700 bg-black/60 px-2 py-1 font-mono text-gray-200 outline-none'
                placeholder='0'
                value={minBond}
                onChange={(e) => setMinBond(e.target.value.replace(/[^0-9]/g, ''))}
                spellCheck={false}
              />
            </label>
            <label className='flex items-center gap-2'>
              <input
                type='checkbox'
                checked={reachableOnly}
                onChange={(e) => setReachableOnly(e.target.checked)}
              />
              {/*
                On by default: a seller with no address cannot take a prompt, so
                showing one in a table whose last column says "Buy here" offers
                something that does not exist.
              */}
              Only sellers that can take a prompt
            </label>
            <label className='flex items-center gap-2'>
              {/*
                Off by default. Defaulting a marketplace to its own operator's
                sellers would make every other listing furniture, and the badge
                is an identity claim rather than a quality one.
              */}
              <input type='checkbox' checked={attestedOnly} onChange={(e) => setAttestedOnly(e.target.checked)} />
              Only sellers the network vouches for
            </label>
          </div>
        </>
      ) : (
        <p className='text-xs text-gray-500'>
          What this node has heard anyone offer, by model. Counts only sellers with an address, because one without it
          cannot take a prompt - a count including them is a number you cannot act on.
        </p>
      )}
    </section>
  );
}

/**
 * The models view: the question a buyer usually arrives with.
 *
 * Most people know the model they want and not the box that will serve it, so
 * this answers "who has it and what does it cost" before anything about sellers.
 * Picking one drops into the seller list already searched for it.
 */
function ModelTable({ rows, onPick }: { rows: ModelRow[]; onPick: (model: string) => void }) {
  if (rows.length === 0) {
    return (
      <div className={CARD}>
        <p className='text-sm text-gray-400'>
          Nobody reachable is advertising a model. A provider that names none cannot be routed to, which is how every
          buyer finds a seller.
        </p>
      </div>
    );
  }
  return (
    <div className={`${CARD} overflow-x-auto`}>
      <table className='w-full min-w-[640px] text-left font-mono text-xs'>
        <thead>
          <tr className='text-gray-500'>
            <th className='pb-2 pr-4 font-normal uppercase tracking-wide'>Model</th>
            <th className='pb-2 pr-4 text-right font-normal uppercase tracking-wide'>Sellers</th>
            <th className='pb-2 pr-4 text-right font-normal uppercase tracking-wide'>Price/unit</th>
            <th className='pb-2 pr-4 text-right font-normal uppercase tracking-wide'>Largest stake</th>
            <th className='pb-2 pr-4 text-right font-normal uppercase tracking-wide'>Payments</th>
            <th className='pb-2 text-right font-normal uppercase tracking-wide'>Vouched</th>
          </tr>
        </thead>
        <tbody className='text-gray-200'>
          {rows.map((r) => (
            <tr key={r.model} className='border-t border-gray-800'>
              <td className='py-2 pr-4'>
                <button className='underline underline-offset-2 hover:text-white' onClick={() => onPick(r.model)}>
                  {r.model}
                </button>
              </td>
              <td className='py-2 pr-4 text-right'>{r.sellers}</td>
              <td className='py-2 pr-4 text-right'>
                {r.cheapest.toString()}
                {r.cheapest === r.dearest ? '' : ` - ${r.dearest.toString()}`}
              </td>
              <td className={`py-2 pr-4 text-right ${r.topStake === 0n ? 'text-amber-500/80' : ''}`}>
                {r.topStake.toString()}
              </td>
              <td className='py-2 pr-4 text-right'>{r.settledPayments.toString()}</td>
              <td className={`py-2 text-right ${r.attested > 0 ? 'text-sky-300' : 'text-gray-600'}`}>{r.attested}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}