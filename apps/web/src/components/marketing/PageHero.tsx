'use client';

import HeroBackdrop from '@/components/HeroBackdrop';
import { Eyebrow } from './Eyebrow';

/**
 * Shared product-page header.
 *
 * Renders a relative hero section with the CSS-only HeroBackdrop behind it, an
 * optional eyebrow pill, an h1 (pass `gradient` to render a gradient span after
 * the title, or supply richer markup via `children`), a lead paragraph, and an
 * optional actions slot for Buttons.
 */
const PageHero = ({
  eyebrow,
  eyebrowTone = 'primary',
  title,
  gradient,
  lead,
  actions,
  children,
}: {
  eyebrow?: React.ReactNode;
  eyebrowTone?: 'primary' | 'secondary' | 'accent';
  title: React.ReactNode;
  gradient?: React.ReactNode;
  lead?: React.ReactNode;
  actions?: React.ReactNode;
  children?: React.ReactNode;
}) => (
  <section className='relative overflow-hidden pt-16'>
    <HeroBackdrop />

    <div className='relative z-10 mx-auto w-full max-w-7xl px-4 py-24 sm:px-6 sm:py-28 lg:px-8'>
      <div className='mx-auto max-w-4xl text-center animate-fade-up'>
        {eyebrow ? (
          <div className='mb-6 flex flex-wrap items-center justify-center gap-2'>
            <Eyebrow tone={eyebrowTone}>{eyebrow}</Eyebrow>
          </div>
        ) : null}

        <h1 className='mb-6 text-4xl font-bold leading-[1.05] tracking-tight sm:text-5xl md:text-6xl'>
          {title}
          {gradient ? (
            <>
              {' '}
              <span className='bg-decorative-1 bg-clip-text text-transparent'>{gradient}</span>
            </>
          ) : null}
        </h1>

        {lead ? <p className='mx-auto mb-10 max-w-2xl text-lg text-grayscale-300 sm:text-xl'>{lead}</p> : null}

        {actions ? <div className='flex flex-wrap items-center justify-center gap-4'>{actions}</div> : null}

        {children}
      </div>
    </div>
  </section>
);

export default PageHero;
export { PageHero };
