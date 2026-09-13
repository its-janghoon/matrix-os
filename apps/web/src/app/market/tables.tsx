'use client';

import Link from 'next/link';
import { FiArrowRight } from 'react-icons/fi';

import { formatMatrix, pricePerMillionUnits, shortAccount, shortEndpoint } from '@/lib/wallet/format';
import type { Seller } from '@/lib/wallet/node';
import type { ModelRow } from '@/lib/wallet/search';

import { Amount, Card, ColumnLabel, SkeletonRows, VouchedBadge } from './ui';

/**
 * WHAT IDENTIFIES A SELLER HERE, and why it is not the address it answers on.
 *
 * The table used to lead with `http://127.0.0.1:19004`, which is where a seller
 * can be reached today and not who it is: an operator moving a box changes it,
 * and two rows a week apart cannot be told apart by it. The payout ACCOUNT is
 * the identity - it is what the stake is posted from, what the settled history
 * is counted against, and what an attestation is bound to. So it leads, and the
 * address it currently answers on sits underneath as the operational detail it
 * is.
 */

const STAKE_EXPLAIN =
  'Capital posted on this chain by the seller, which the chain locks for a minimum number of blocks. It does not make anyone honest and cannot be taken for bad service - what it does is make a listing cost money, so one attacker cannot fill this table with fake sellers.';
const SETTLED_EXPLAIN =
  'Settled transfers on the chain the answering node holds, not claims by the seller. A settlement is an ordinary transfer, so this counts every payment the account received - including from itself. Payers is the harder one to inflate: it costs a funded account each.';
// A unit is defined by the protocol and documented in the consumer runbook; the
// page had been quoting a price in a denomination it never named, which is not
// a price a buyer can compare against anything.
const PRICE_EXPLAIN =
  'One unit is one token - prompt and completion summed - as reported by the model server that did the work. Quoted per million units, because the per-unit price is a few billionths of a MATRIX and leading zeros defeat comparison. The conversion is exact.';

export function SellerTable({
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
  if (!loading && sellers.length === 0) {
    return (
      <Card className='px-6 py-14 text-center'>
        <p className='text-sm text-grayscale-400'>
          {searched
            ? 'Nothing here matches. A directory only holds what THIS node has heard, so a seller you know of may simply not have reached it.'
            : 'This node has heard nothing. Either nobody is selling, or it has no peers to hear them from.'}
        </p>
      </Card>
    );
  }

  return (
    <>
      {/*
        A phone gets cards rather than the same table scrolled sideways. Six
        columns in 390px cuts off after MODELS, and a table clipped mid-row
        hides the price, the stake and the settled history behind a horizontal
        scroll nothing on screen announces - so a reader on a phone decides
        between sellers on the two columns that survived, which are the two that
        say least.
      */}
      <div className='space-y-3 md:hidden'>
        {loading && sellers.length === 0 ? (
          <Card className='px-5 py-10 text-center text-sm text-grayscale-500'>Reading the directory...</Card>
        ) : (
          sellers.map((s) => <SellerCard key={`${s.nodeId}:${s.id}`} seller={s} endpoint={endpoint} />)
        )}
      </div>
      <div className='hidden md:block'>
        <SellerRows sellers={sellers} loading={loading} endpoint={endpoint} />
      </div>
    </>
  );
}

function SellerCard({ seller: s, endpoint }: { seller: Seller; endpoint: string }) {
  const reachable = s.endpoint !== '' && s.models.length > 0;
  return (
    <Card className='px-5 py-4'>
      <div className='flex flex-wrap items-center gap-2'>
        <span className='font-mono text-[13px] text-white' title={s.id}>
          {shortAccount(s.id)}
        </span>
        {s.operatorAttested ? <VouchedBadge operator={s.operatorName} /> : null}
      </div>
      <p className='mt-1 font-mono text-[11px] text-grayscale-500'>
        {s.endpoint === '' ? 'no address - compute only' : shortEndpoint(s.endpoint)}
      </p>

      {s.models.length > 0 ? (
        <div className='mt-3 flex flex-wrap gap-1'>
          {s.models.map((m) => (
            <span key={m} className='rounded-md bg-white/[0.06] px-2 py-0.5 font-mono text-[11px] text-grayscale-200'>
              {m}
            </span>
          ))}
        </div>
      ) : null}

      {/*
        Two across, then Settled on a line of its own. An exact settled total
        carries nine decimal places, and in a third of a phone's width it runs
        off the card - so the column that needs the room gets the full width
        rather than the amount getting rounded to fit.
      */}
      <dl className='mt-4 space-y-3 border-t border-white/[0.06] pt-3'>
        <div className='grid grid-cols-2 gap-3'>
          <div>
            <dt className='text-[10px] uppercase tracking-[0.08em] text-grayscale-500' title={PRICE_EXPLAIN}>
              Price / 1M units
            </dt>
            <dd className='mt-0.5'>
              <Amount value={pricePerMillionUnits(s.pricePerUnit)} />
            </dd>
          </div>
          <div>
            <dt className='text-[10px] uppercase tracking-[0.08em] text-grayscale-500' title={STAKE_EXPLAIN}>
              Staked
            </dt>
            <dd className='mt-0.5'>
              <Amount value={formatMatrix(s.bonded)} muted={s.bonded === 0n} />
            </dd>
          </div>
        </div>
        <div>
          <dt className='text-[10px] uppercase tracking-[0.08em] text-grayscale-500' title={SETTLED_EXPLAIN}>
            Settled
          </dt>
          <dd className='mt-0.5 flex flex-wrap items-baseline gap-x-2'>
            <Amount value={formatMatrix(s.settledReceived)} />
            <span className='font-mono text-[10px] tabular-nums text-grayscale-500'>
              {s.settledPayments.toString()} paid / {s.settledPayers.toString()} payers
            </span>
          </dd>
        </div>
      </dl>

      {reachable ? (
        <Link
          href={{ pathname: '/chat', query: { node: s.nodeId, provider: s.id, model: s.models[0], endpoint } }}
          className='mt-4 flex w-full items-center justify-center gap-1.5 rounded-full border border-white/[0.1] py-2 text-xs font-semibold text-grayscale-200'
        >
          Use this seller
          <FiArrowRight className='h-3 w-3' />
        </Link>
      ) : null}
    </Card>
  );
}

function SellerRows({ sellers, loading, endpoint }: { sellers: Seller[]; loading: boolean; endpoint: string }) {
  return (
    <Card className='overflow-x-auto'>
      <table className='w-full min-w-[860px] border-collapse text-left'>
        <thead>
          <tr className='border-b border-white/[0.07]'>
            <th className='w-[34%] pb-3 pl-6 pr-6 pt-5 text-left text-[11px] font-medium uppercase tracking-[0.08em] text-grayscale-500'>
              Seller
            </th>
            <ColumnLabel>Models</ColumnLabel>
            <ColumnLabel align='right' explain={PRICE_EXPLAIN}>
              Price / 1M units
            </ColumnLabel>
            <ColumnLabel align='right' explain={STAKE_EXPLAIN}>
              Staked
            </ColumnLabel>
            <ColumnLabel align='right' explain={SETTLED_EXPLAIN}>
              Settled
            </ColumnLabel>
            <th className='pb-3 pr-6 pt-5' />
          </tr>
        </thead>
        <tbody>
          {loading && sellers.length === 0 ? (
            <SkeletonRows rows={4} cols={6} />
          ) : (
            // Already ordered by the query; re-sorting here would silently
            // override the column the reader chose.
            sellers.map((s) => <SellerRow key={`${s.nodeId}:${s.id}`} seller={s} endpoint={endpoint} />)
          )}
        </tbody>
      </table>
    </Card>
  );
}

function SellerRow({ seller: s, endpoint }: { seller: Seller; endpoint: string }) {
  const reachable = s.endpoint !== '' && s.models.length > 0;
  return (
    <tr className='group border-t border-white/[0.05] transition-colors hover:bg-white/[0.025]'>
      <td className='py-4 pl-6 pr-6'>
        <div className='flex items-center gap-2'>
          <span className='font-mono text-[13px] text-white' title={s.id}>
            {shortAccount(s.id)}
          </span>
          {s.operatorAttested ? <VouchedBadge operator={s.operatorName} /> : null}
        </div>
        <p className='mt-1 font-mono text-[11px] text-grayscale-500'>
          {s.endpoint === '' ? (
            <span className='text-grayscale-600'>no address - compute only</span>
          ) : (
            shortEndpoint(s.endpoint)
          )}
        </p>
      </td>

      <td className='py-4 pr-6'>
        {s.models.length === 0 ? (
          <span className='text-xs text-grayscale-600'>-</span>
        ) : (
          <div className='flex flex-wrap gap-1'>
            {s.models.map((m) => (
              <span
                key={m}
                className='rounded-md bg-white/[0.06] px-2 py-0.5 font-mono text-[11px] text-grayscale-200'
              >
                {m}
              </span>
            ))}
          </div>
        )}
      </td>

      <td className='py-4 pr-6 text-right'>
        <Amount value={pricePerMillionUnits(s.pricePerUnit)} />
      </td>

      <td className='py-4 pr-6 text-right'>
        {s.bonded === 0n ? (
          <span title='This seller has posted no capital, so its listing cost nothing to create.'>
            <Amount value='0' muted />
          </span>
        ) : (
          <Amount value={formatMatrix(s.bonded)} />
        )}
      </td>

      <td className='py-4 pr-6 text-right'>
        <Amount value={formatMatrix(s.settledReceived)} />
        <p className='mt-0.5 font-mono text-[11px] tabular-nums text-grayscale-500'>
          {s.settledPayments.toString()} paid / {s.settledPayers.toString()} payers
        </p>
      </td>

      <td className='py-4 pr-6 text-right'>
        {!reachable ? (
          <span className='text-xs text-grayscale-700'>-</span>
        ) : (
          // The choice travels as an IDENTITY, and deliberately not as an
          // address: a link carrying its own endpoint would let whoever sent it
          // route the recipient's prompt at a host of their choosing while the
          // page named the seller they thought they picked. The chat page reads
          // the address back from the directory itself.
          <Link
            href={{
              pathname: '/chat',
              query: { node: s.nodeId, provider: s.id, model: s.models[0], endpoint },
            }}
            className='inline-flex items-center gap-1.5 rounded-full border border-white/[0.1] px-3.5 py-1.5 text-xs font-semibold text-grayscale-200 transition-colors group-hover:border-primary-400/50 group-hover:bg-primary/[0.12] group-hover:text-white'
          >
            Use
            <FiArrowRight className='h-3 w-3' />
          </Link>
        )}
      </td>
    </tr>
  );
}

/**
 * The models view: the question a buyer usually arrives with.
 *
 * Most people know the model they want and not the box that will serve it, so
 * this answers "who has it and what does it cost" before anything about
 * sellers. Picking one drops into the seller list already searched for it.
 */
export function ModelTable({ rows, onPick }: { rows: ModelRow[]; onPick: (model: string) => void }) {
  if (rows.length === 0) {
    return (
      <Card className='px-6 py-14 text-center'>
        <p className='text-sm text-grayscale-400'>
          Nobody reachable is advertising a model. A provider that names none cannot be routed to, which is how every
          buyer finds a seller.
        </p>
      </Card>
    );
  }
  return (
    <Card className='overflow-x-auto'>
      <table className='w-full min-w-[760px] border-collapse text-left'>
        <thead>
          <tr className='border-b border-white/[0.07]'>
            <th className='w-[32%] pb-3 pl-6 pr-6 pt-5 text-left text-[11px] font-medium uppercase tracking-[0.08em] text-grayscale-500'>
              Model
            </th>
            <ColumnLabel
              align='right'
              explain='Counts only sellers with an address, because one without it cannot take a prompt - a count including them is a number you cannot act on.'
            >
              Sellers
            </ColumnLabel>
            <ColumnLabel align='right' explain={PRICE_EXPLAIN}>
              Price / 1M units
            </ColumnLabel>
            <ColumnLabel align='right' explain={STAKE_EXPLAIN}>
              Largest stake
            </ColumnLabel>
            <ColumnLabel align='right' explain={SETTLED_EXPLAIN}>
              Payments
            </ColumnLabel>
            <th className='pb-3 pr-6 pt-5 text-right text-[11px] font-medium uppercase tracking-[0.08em] text-grayscale-500'>
              Vouched
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr
              key={r.model}
              className='group cursor-pointer border-t border-white/[0.05] transition-colors hover:bg-white/[0.025]'
              onClick={() => onPick(r.model)}
            >
              <td className='py-4 pl-6 pr-6'>
                <button
                  className='font-mono text-[13px] text-white underline-offset-4 group-hover:underline'
                  onClick={(e) => {
                    e.stopPropagation();
                    onPick(r.model);
                  }}
                >
                  {r.model}
                </button>
              </td>
              <td className='py-4 pr-6 text-right font-mono text-[13px] tabular-nums text-white'>{r.sellers}</td>
              <td className='py-4 pr-6 text-right'>
                <Amount value={pricePerMillionUnits(r.cheapest)} />
                {r.cheapest === r.dearest ? null : (
                  <span className='font-mono text-[13px] tabular-nums text-grayscale-500'>
                    {' - '}
                    {pricePerMillionUnits(r.dearest)}
                  </span>
                )}
              </td>
              <td className='py-4 pr-6 text-right'>
                <Amount value={formatMatrix(r.topStake)} muted={r.topStake === 0n} />
              </td>
              <td className='py-4 pr-6 text-right font-mono text-[13px] tabular-nums text-white'>
                {r.settledPayments.toString()}
              </td>
              <td className='py-4 pr-6 text-right'>
                {r.attested > 0 ? (
                  <span className='font-mono text-[13px] tabular-nums text-primary-300'>{r.attested}</span>
                ) : (
                  <span className='font-mono text-[13px] tabular-nums text-grayscale-600'>0</span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </Card>
  );
}
