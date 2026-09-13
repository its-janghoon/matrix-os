'use client';

import Link from 'next/link';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { FiRefreshCw, FiSearch } from 'react-icons/fi';

import Navigation from '@/components/Navigation';
import { compactMatrix, formatMatrix, parseMatrix, SYMBOL } from '@/lib/wallet/format';
import { DEFAULT_ENDPOINT, listSellers, reportProblem, type Seller } from '@/lib/wallet/node';
import { searchSellers, sellersByModel, type SortKey } from '@/lib/wallet/search';
import { browserSigner } from '@/lib/wallet/browserSigner';
import type { Signer } from '@/lib/wallet/signer';
import { loadWallet } from '@/lib/wallet/wallet';

import { StakePanel } from './panels';
import { ModelTable, SellerTable } from './tables';
import { Card, FIELD, Segmented, Select, Stat, Toggle } from './ui';

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
 *
 * ON THE LAYOUT. The first version led with an editable node address and a wall
 * of fourteen-digit integers, which is what a tool looks like when it is written
 * for the person who wrote it. Which node is answering is a setting, so it sits
 * in the corner where settings sit; the amounts are denominated in MATRIX,
 * because the reader is deciding how much to spend and cannot do that in base
 * units; and the caveats moved onto the columns they qualify, where they get
 * read instead of scrolled past.
 */
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

  // The floor is typed in MATRIX like every other amount here. An unparseable
  // entry applies NO floor rather than a guessed one: silently filtering on a
  // number the reader did not mean is worse than not filtering at all.
  const minBondUnits = minBond.trim() === '' ? undefined : (parseMatrix(minBond) ?? undefined);

  const shown = searchSellers(sellers, {
    text,
    minBond: minBondUnits,
    reachableOnly,
    attestedOnly,
    sort,
  });

  const models = useMemo(() => sellersByModel(sellers), [sellers]);
  const totals = useMemo(() => {
    let staked = 0n;
    let settled = 0n;
    let vouched = 0;
    for (const s of sellers) {
      staked += s.bonded;
      settled += s.settledReceived;
      if (s.operatorAttested) vouched += 1;
    }
    return { staked, settled, vouched };
  }, [sellers]);

  return (
    <>
      <Navigation />
      <main className='min-h-screen bg-background bg-section-glow px-4 pb-20 pt-24'>
        <div className='mx-auto max-w-6xl'>
          <header className='flex flex-wrap items-end justify-between gap-4'>
            <div>
              <h1 className='text-[28px] font-semibold tracking-tight text-white'>Compute marketplace</h1>
              <p className='mt-1.5 max-w-2xl text-sm text-grayscale-400'>
                Every seller this node has heard, ranked by what the chain can prove about them.
              </p>
            </div>
            <NodeChip endpoint={endpoint} setEndpoint={setEndpoint} loading={loading} failed={problem !== ''} onRefresh={refresh} />
          </header>

          {problem !== '' ? (
            <p className='mt-6 rounded-xl border border-semantic-error/30 bg-semantic-error/[0.07] px-4 py-3 text-sm text-red-300'>
              {problem}
            </p>
          ) : null}

          <div className='mt-7 grid grid-cols-2 gap-3 lg:grid-cols-4'>
            <Stat
              label='Sellers'
              value={sellers.length.toString()}
              sub={totals.vouched > 0 ? `${totals.vouched} vouched for` : 'none vouched for yet'}
            />
            <Stat label='Models' value={models.length.toString()} sub='offered by a reachable seller' />
            <Stat
              label={`Staked (${SYMBOL})`}
              value={compactMatrix(totals.staked)}
              exact={`${formatMatrix(totals.staked)} ${SYMBOL}`}
              sub='capital locked behind listings'
            />
            <Stat
              label={`Settled (${SYMBOL})`}
              value={compactMatrix(totals.settled)}
              exact={`${formatMatrix(totals.settled)} ${SYMBOL}`}
              sub='paid to sellers on this chain'
              tone='brand'
            />
          </div>

          <div className='mt-7 flex flex-wrap items-center justify-between gap-3'>
            <Segmented
              value={view}
              onChange={setView}
              options={[
                { value: 'sellers', label: 'Sellers' },
                { value: 'models', label: 'Models' },
              ]}
            />
            <span className='font-mono text-xs text-grayscale-500'>
              {view === 'sellers'
                ? `${shown.length} of ${sellers.length} shown`
                : `${models.length} ${models.length === 1 ? 'model' : 'models'}`}
            </span>
          </div>

          {view === 'sellers' ? (
            <>
              <Card className='mt-3 px-4 py-3.5'>
                <div className='flex flex-wrap items-center gap-3'>
                  <div className='relative min-w-[240px] flex-1'>
                    <FiSearch className='pointer-events-none absolute left-3.5 top-1/2 h-4 w-4 -translate-y-1/2 text-grayscale-600' />
                    <input
                      className={`${FIELD} pl-10`}
                      placeholder='Search by model, operator, address or account'
                      value={text}
                      onChange={(e) => setText(e.target.value)}
                      spellCheck={false}
                    />
                  </div>
                  <Select
                    label='Sort sellers'
                    value={sort}
                    onChange={setSort}
                    options={[
                      { value: 'price', label: 'Cheapest first' },
                      { value: 'staked', label: 'Most staked' },
                      { value: 'paid', label: 'Most payments' },
                      { value: 'payers', label: 'Most payers' },
                    ]}
                  />
                </div>

                <div className='mt-3 flex flex-wrap items-center gap-2'>
                  {/*
                    On by default: a seller with no address cannot take a prompt,
                    so showing one in a table whose last column offers to use it
                    offers something that does not exist.
                  */}
                  <Toggle
                    on={reachableOnly}
                    onChange={setReachableOnly}
                    explain='A seller that publishes no address cannot take a prompt.'
                  >
                    Can take a prompt
                  </Toggle>
                  {/*
                    Off by default. Defaulting a marketplace to its own operator's
                    sellers would make every other listing furniture, and the badge
                    is an identity claim rather than a quality one.
                  */}
                  <Toggle
                    on={attestedOnly}
                    onChange={setAttestedOnly}
                    explain='Only sellers this node verified a maintainer signature for. An identity claim, not a rating.'
                  >
                    Vouched for by the network
                  </Toggle>
                  <label className='ml-auto flex items-center gap-2 text-xs text-grayscale-500'>
                    Min stake
                    <span className='relative'>
                      <input
                        className='w-36 rounded-full border border-white/[0.08] bg-black/40 py-1.5 pl-3 pr-14 font-mono text-xs tabular-nums text-white outline-none transition-colors focus:border-primary-400/50'
                        placeholder='0'
                        value={minBond}
                        onChange={(e) => setMinBond(e.target.value)}
                        spellCheck={false}
                        inputMode='decimal'
                      />
                      <span className='pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-[10px] font-medium text-grayscale-600'>
                        {SYMBOL}
                      </span>
                    </span>
                  </label>
                </div>
              </Card>

              <div className='mt-3'>
                <SellerTable sellers={shown} loading={loading} endpoint={endpoint} searched={text.trim() !== ''} />
              </div>
            </>
          ) : (
            <>
              <div className='mt-3'>
                <ModelTable
                  rows={models}
                  onPick={(m) => {
                    setText(m);
                    setView('sellers');
                  }}
                />
              </div>
            </>
          )}

          {/*
            One line where seven paragraphs used to be.

            The caveats are still true and still matter, but an essay under the
            table was read by nobody, which made it decoration rather than
            honesty. The sentence that makes a column trustworthy now sits on
            that column's own heading, where the question is actually asked, and
            the full version is a click away for the reader who wants it.
          */}
          <p className='mt-4 text-xs text-grayscale-500'>
            Every figure here is read from the chain, never reported by the seller.{' '}
            <Link
              href='/docs/compute-marketplace#reading-the-directory'
              className='text-primary-300 underline-offset-4 hover:underline'
            >
              How to read them
            </Link>
          </p>

          <div className='mt-8'>
            <StakePanel endpoint={endpoint} signer={signer} onChanged={refresh} setSigner={setSigner} />
          </div>
        </div>
      </main>
    </>
  );
}

/**
 * Which node is answering, and a way to change it.
 *
 * It used to be the first thing on the page, in a card of its own, which put a
 * setting where the content belongs. It still has to be VISIBLE rather than
 * buried, because everything below is one node's view and a reader who forgets
 * that draws conclusions about the network from one peer's hearing - so it
 * states the address and whether the last read worked, and opens for editing
 * when asked.
 */
function NodeChip({
  endpoint,
  setEndpoint,
  loading,
  failed,
  onRefresh,
}: {
  endpoint: string;
  setEndpoint: (v: string) => void;
  loading: boolean;
  failed: boolean;
  onRefresh: () => void;
}) {
  const [open, setOpen] = useState(false);
  const dot = failed ? 'bg-semantic-error' : loading ? 'bg-semantic-processing' : 'bg-semantic-success';

  return (
    <div className='flex flex-col items-end gap-2'>
      <div className='flex items-center gap-2'>
        <button
          type='button'
          onClick={() => setOpen((v) => !v)}
          className='inline-flex items-center gap-2 rounded-full border border-white/[0.08] bg-white/[0.03] px-3 py-1.5 text-xs text-grayscale-300 transition-colors hover:border-white/20 hover:text-white'
          title='Everything on this page is one node&apos;s view. Click to read from a different one.'
        >
          <span className={`h-1.5 w-1.5 rounded-full ${dot}`} />
          <span className='font-mono'>{endpoint.replace(/^https?:\/\//, '')}</span>
        </button>
        {/*
          Deliberately NOT disabled while loading. A read that hangs would
          otherwise take the only control that recovers from it away, and the
          reader is left watching a spinner with nothing to press. A second read
          superseding a first is handled by the guard in the effect.
        */}
        <button
          type='button'
          onClick={onRefresh}
          aria-label='Refresh the directory'
          className='inline-flex h-8 w-8 items-center justify-center rounded-full border border-white/[0.08] bg-white/[0.03] text-grayscale-400 transition-colors hover:border-white/20 hover:text-white'
        >
          <FiRefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />
        </button>
      </div>
      {open ? (
        <input
          className={`${FIELD} w-[320px] font-mono text-xs`}
          value={endpoint}
          onChange={(e) => setEndpoint(e.target.value)}
          spellCheck={false}
          aria-label='Node address'
        />
      ) : null}
    </div>
  );
}
