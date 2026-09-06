import Link from 'next/link';

import { BrandMark } from '@/components/BrandMark';
import { GITHUB_URL } from '@/lib/releases';

const columns: { title: string; links: { label: string; href: string; external?: boolean }[] }[] = [
  {
    title: 'Products',
    links: [
      { label: 'Compute Marketplace', href: '/products/marketplace' },
      { label: 'MATRIX Token', href: '/products/token' },
      { label: 'Consensus', href: '/products/consensus' },
      { label: 'LLM Inference', href: '/products/inference' },
      { label: 'Matrix Console', href: '/products/console' },
      { label: 'matrix CLI', href: '/products/cli' },
    ],
  },
  {
    title: 'Developers',
    links: [
      { label: 'Documentation', href: '/docs' },
      { label: 'Quickstart', href: '/docs/quickstart' },
      { label: 'Installation', href: '/docs/installation' },
      { label: 'Architecture', href: '/docs/architecture' },
      { label: 'Configuration', href: '/docs/configuration' },
      { label: 'matrix CLI', href: '/docs/cli' },
    ],
  },
  {
    title: 'Resources',
    links: [
      { label: 'How it works', href: '/how-it-works' },
      { label: 'Matrix Protocol', href: '/docs/matrix-protocol' },
      { label: 'Soul Protocol', href: '/docs/soul-protocol' },
      { label: 'Download', href: '/download' },
      { label: 'GitHub', href: GITHUB_URL, external: true },
    ],
  },
];

export function Footer() {
  return (
    <footer className='relative border-t border-white/10 bg-black text-grayscale-400'>
      <div className='pointer-events-none absolute inset-x-0 top-0 h-px bg-decorative-1 opacity-60' />
      <div className='mx-auto max-w-7xl px-4 sm:px-6 lg:px-8 py-16'>
        <div className='grid grid-cols-2 gap-10 md:grid-cols-5'>
          <div className='col-span-2'>
            {/* The same mark and wordmark as the header. This used to be a
                rounded square with the letters ECIR in it - a placeholder that
                outlived the brand and left the footer showing something the
                rest of the site had stopped using. */}
            <Link href='/' className='flex items-center gap-2.5' aria-label='Matrix OS, home'>
              <BrandMark size={36} className='shrink-0' />
              <span className='text-lg font-semibold tracking-tight text-white'>
                Matrix <span className='text-secondary-400'>OS</span>
              </span>
            </Link>
            <p className='mt-4 max-w-sm text-sm leading-relaxed text-grayscale-400'>
              A peer-to-peer marketplace for compute and LLM inference, settled in native MATRIX on a fast
              leader-based BFT Layer 1. Own your execution, keep data local, and transact in one coin end to end.
            </p>
            <div className='mt-6 flex items-center gap-4'>
              <a
                href={GITHUB_URL}
                target='_blank'
                rel='noopener noreferrer'
                className='text-grayscale-400 hover:text-white transition-colors'
                aria-label='GitHub'
              >
                <svg className='w-5 h-5' fill='currentColor' viewBox='0 0 24 24'>
                  <path
                    fillRule='evenodd'
                    clipRule='evenodd'
                    d='M12 2C6.477 2 2 6.477 2 12c0 4.42 2.87 8.17 6.84 9.5.5.08.66-.23.66-.5v-1.69c-2.77.6-3.36-1.34-3.36-1.34-.46-1.16-1.11-1.47-1.11-1.47-.91-.62.07-.6.07-.6 1 .07 1.53 1.03 1.53 1.03.87 1.52 2.34 1.07 2.91.83.09-.65.35-1.09.63-1.34-2.22-.25-4.55-1.11-4.55-4.92 0-1.11.38-2 1.03-2.71-.1-.25-.45-1.29.1-2.64 0 0 .84-.27 2.75 1.02.79-.22 1.65-.33 2.5-.33.85 0 1.71.11 2.5.33 1.91-1.29 2.75-1.02 2.75-1.02.55 1.35.2 2.39.1 2.64.65.71 1.03 1.6 1.03 2.71 0 3.82-2.34 4.66-4.57 4.91.36.31.69.92.69 1.85V21c0 .27.16.59.67.5C19.14 20.16 22 16.42 22 12A10 10 0 0012 2z'
                  />
                </svg>
              </a>
            </div>
          </div>

          {columns.map((col) => (
            <div key={col.title}>
              <h3 className='text-xs font-semibold uppercase tracking-[0.14em] text-grayscale-500'>{col.title}</h3>
              <ul className='mt-4 space-y-3 text-sm'>
                {col.links.map((link) =>
                  link.external ? (
                    <li key={link.href}>
                      <a
                        href={link.href}
                        target='_blank'
                        rel='noopener noreferrer'
                        className='text-grayscale-400 transition-colors hover:text-white'
                      >
                        {link.label}
                      </a>
                    </li>
                  ) : (
                    <li key={link.href}>
                      <Link href={link.href} className='text-grayscale-400 transition-colors hover:text-white'>
                        {link.label}
                      </Link>
                    </li>
                  )
                )}
              </ul>
            </div>
          ))}
        </div>

        <div className='mt-14 flex flex-col items-center justify-between gap-4 border-t border-white/10 pt-8 text-sm text-grayscale-500 md:flex-row'>
          <p>&copy; {new Date().getFullYear()} ECIR Labs. All rights reserved.</p>
          <p>Native-first compute, settled through consensus.</p>
        </div>
      </div>
    </footer>
  );
}
