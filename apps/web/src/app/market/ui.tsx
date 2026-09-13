'use client';

import { type ReactNode } from 'react';
import { FiCheck, FiChevronDown } from 'react-icons/fi';

/**
 * The pieces this page is built from.
 *
 * They exist so the directory stops looking like a debugging tool. The numbers
 * on it are read from a chain and every one of them is checkable, which is the
 * product; a table of undifferentiated monospace digits on a black field spends
 * that credibility rather than showing it.
 *
 * Everything here draws from the site's own tokens - the navy canvas, the
 * grayscale ramp, the electric blue - so the marketplace reads as part of the
 * same product as the pages that sell it, instead of an admin panel bolted to
 * the side.
 */

export function Card({ className = '', children }: { className?: string; children: ReactNode }) {
  return (
    <section
      className={`rounded-2xl border border-white/[0.07] bg-white/[0.02] shadow-card backdrop-blur-sm ${className}`}
    >
      {children}
    </section>
  );
}

/**
 * A column heading that can say what it means.
 *
 * The honest caveats on this page are not fine print to be tucked away - they
 * are the reason to believe the number beside them. Put where the question is
 * actually asked, at the head of the column, they get read; collected into an
 * essay underneath, they get scrolled past.
 */
export function ColumnLabel({
  children,
  explain,
  align = 'left',
}: {
  children: ReactNode;
  explain?: string;
  align?: 'left' | 'right';
}) {
  // Header padding MUST match the body cells or the label drifts off the column
  // it names: right-aligned numbers carry the cell's own right padding, and a
  // header without it sits a gutter's width away from the figures underneath.
  const base = `pb-3 pr-6 pt-5 text-[11px] font-medium uppercase tracking-[0.08em] text-grayscale-500 ${
    align === 'right' ? 'text-right' : 'text-left'
  }`;
  if (!explain) return <th className={base}>{children}</th>;
  return (
    <th className={base}>
      <span
        className='cursor-help border-b border-dashed border-grayscale-600 pb-0.5 hover:text-grayscale-300'
        title={explain}
      >
        {children}
      </span>
    </th>
  );
}

/** A headline figure. `exact` is the unrounded value, always one hover away. */
export function Stat({
  label,
  value,
  exact,
  sub,
  tone = 'default',
}: {
  label: string;
  value: string;
  exact?: string;
  sub?: string;
  tone?: 'default' | 'brand';
}) {
  return (
    <div className='rounded-2xl border border-white/[0.07] bg-white/[0.02] px-5 py-4'>
      <p className='text-[11px] font-medium uppercase tracking-[0.08em] text-grayscale-500'>{label}</p>
      <p
        className={`mt-1.5 font-mono text-[22px] font-semibold tabular-nums ${
          tone === 'brand' ? 'text-primary-300' : 'text-white'
        }`}
        title={exact}
      >
        {value}
      </p>
      {sub ? <p className='mt-0.5 text-xs text-grayscale-500'>{sub}</p> : null}
    </div>
  );
}

export function Segmented<T extends string>({
  options,
  value,
  onChange,
}: {
  options: Array<{ value: T; label: string }>;
  value: T;
  onChange: (v: T) => void;
}) {
  return (
    <div className='inline-flex rounded-full border border-white/[0.07] bg-white/[0.03] p-1'>
      {options.map((o) => (
        <button
          key={o.value}
          type='button'
          onClick={() => onChange(o.value)}
          aria-pressed={value === o.value}
          className={`rounded-full px-4 py-1.5 text-sm font-medium transition-colors ${
            value === o.value ? 'bg-white text-black' : 'text-grayscale-400 hover:text-white'
          }`}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

/** A filter you can see the state of without reading a checkbox. */
export function Toggle({
  on,
  onChange,
  children,
  explain,
}: {
  on: boolean;
  onChange: (v: boolean) => void;
  children: ReactNode;
  explain?: string;
}) {
  return (
    <button
      type='button'
      onClick={() => onChange(!on)}
      aria-pressed={on}
      title={explain}
      className={`inline-flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-xs font-medium transition-colors ${
        on
          ? 'border-primary-400/40 bg-primary/[0.14] text-primary-200'
          : 'border-white/[0.08] text-grayscale-400 hover:border-white/20 hover:text-grayscale-200'
      }`}
    >
      <span
        className={`flex h-3.5 w-3.5 items-center justify-center rounded-[4px] border ${
          on ? 'border-primary-400 bg-primary-400 text-white' : 'border-grayscale-600'
        }`}
      >
        {on ? <FiCheck className='h-2.5 w-2.5' strokeWidth={3} /> : null}
      </span>
      {children}
    </button>
  );
}

export function Select<T extends string>({
  value,
  onChange,
  options,
  label,
}: {
  value: T;
  onChange: (v: T) => void;
  options: Array<{ value: T; label: string }>;
  label: string;
}) {
  return (
    <div className='relative'>
      <select
        aria-label={label}
        value={value}
        onChange={(e) => onChange(e.target.value as T)}
        className='appearance-none rounded-full border border-white/[0.08] bg-white/[0.03] py-1.5 pl-3 pr-8 text-xs font-medium text-grayscale-200 outline-none transition-colors hover:border-white/20 focus:border-primary-400/50'
      >
        {options.map((o) => (
          <option key={o.value} value={o.value} className='bg-grayscale-900 text-white'>
            {o.label}
          </option>
        ))}
      </select>
      <FiChevronDown className='pointer-events-none absolute right-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-grayscale-500' />
    </div>
  );
}

/**
 * The one badge this marketplace has.
 *
 * Filled rather than outlined, because it needs to survive being one small
 * element in a dense row, and titled with what was actually CHECKED. A badge
 * whose meaning a reader has to guess is read as "this one is good", which is
 * the single thing it does not say.
 */
export function VouchedBadge({ operator }: { operator: string }) {
  return (
    <span
      className='inline-flex shrink-0 items-center gap-1 rounded-full bg-primary/[0.16] px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-primary-200 ring-1 ring-inset ring-primary-400/30'
      title={`${operator} - the maintainer account this chain names signed that it operates this node. An identity claim, checked against consensus state. It is not a rating and says nothing about answer quality.`}
    >
      <FiCheck className='h-3 w-3' strokeWidth={3} />
      {operator || 'Vouched'}
    </span>
  );
}

/**
 * An exact amount that can still be read at a glance.
 *
 * `712,800.000000005` is the truth and a settled total really can end in five
 * base units, but set in one weight it is sixteen characters the eye has to
 * parse before it learns the figure is about seven hundred thousand. Rounding
 * it away would be the easy fix and the wrong one - this page's whole claim is
 * that its numbers match a chain anybody can go and check.
 *
 * So nothing is dropped; the fraction is simply set quieter and smaller than
 * the part that carries the magnitude. Scannable and exact, rather than one at
 * the cost of the other.
 */
export function Amount({ value, muted = false }: { value: string; muted?: boolean }) {
  const dot = value.indexOf('.');
  const whole = dot === -1 ? value : value.slice(0, dot);
  const frac = dot === -1 ? '' : value.slice(dot);

  // Quieting the fraction only works when the WHOLE part carries the magnitude.
  // A price of 0.005 has none there, so the same treatment sets the one digit
  // that is zero in white and fades the three that are the number - a figure
  // rendered backwards. Below one, the fraction IS the amount.
  const fadeFraction = frac !== '' && whole.replace('-', '') !== '0';
  const strong = muted ? 'text-semantic-processing/80' : 'text-white';

  return (
    <span className='font-mono text-[13px] tabular-nums'>
      <span className={strong}>{whole}</span>
      {frac ? <span className={fadeFraction ? 'text-[11px] text-grayscale-500' : strong}>{frac}</span> : null}
    </span>
  );
}

/** Rows that hold the layout still while the first read is in flight. */
export function SkeletonRows({ rows = 4, cols = 6 }: { rows?: number; cols?: number }) {
  return (
    <>
      {Array.from({ length: rows }).map((_, r) => (
        <tr key={r} className='border-t border-white/[0.05]'>
          {Array.from({ length: cols }).map((_, c) => (
            <td key={c} className='py-4 pr-6'>
              <div
                className='h-3 animate-pulse rounded bg-white/[0.06]'
                style={{ width: c === 0 ? '60%' : '40%', animationDelay: `${(r * cols + c) * 40}ms` }}
              />
            </td>
          ))}
        </tr>
      ))}
    </>
  );
}

export const FIELD =
  'w-full rounded-xl border border-white/[0.08] bg-black/40 px-3.5 py-2.5 text-sm text-white placeholder:text-grayscale-600 outline-none transition-colors focus:border-primary-400/50';

export const PRIMARY_BUTTON =
  'inline-flex items-center justify-center gap-1.5 rounded-full bg-white px-4 py-2 text-sm font-semibold text-black transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40';

export const QUIET_BUTTON =
  'inline-flex items-center justify-center gap-1.5 rounded-full border border-white/[0.1] px-4 py-2 text-sm font-medium text-grayscale-200 transition-colors hover:border-white/25 hover:text-white disabled:cursor-not-allowed disabled:opacity-40';
