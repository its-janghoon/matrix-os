'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useEffect, useState } from 'react';
import { Button } from './Button';

const sectionLinks = [
  { label: 'Marketplace', href: '/#marketplace' },
  { label: 'MATRIX', href: '/#token' },
  { label: 'Consensus', href: '/#consensus' },
  { label: 'Inference', href: '/#inference' },
  { label: 'Console', href: '/#console' },
];

export default function Navigation() {
  const pathname = usePathname();
  const [isMenuOpen, setIsMenuOpen] = useState(false);
  const [scrolled, setScrolled] = useState(false);

  const isActive = (path: string) => pathname === path;

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 8);
    onScroll();
    window.addEventListener('scroll', onScroll, { passive: true });
    return () => window.removeEventListener('scroll', onScroll);
  }, []);

  return (
    <nav
      className={`fixed top-0 left-0 right-0 z-50 transition-all duration-300 ${
        scrolled
          ? 'bg-black/70 backdrop-blur-xl border-b border-white/10 shadow-md'
          : 'bg-transparent border-b border-transparent'
      }`}
    >
      <div className='mx-auto max-w-7xl px-4 sm:px-6 lg:px-8'>
        <div className='flex items-center justify-between h-16'>
          {/* Logo and Primary Nav */}
          <div className='flex items-center gap-10'>
            <Link href='/' className='flex items-center group'>
              <div className='h-9 w-9 rounded-xl bg-decorative-1 flex items-center justify-center shadow-glow'>
                <span className='text-[11px] font-bold text-white tracking-wider'>ECIR</span>
              </div>
              <span className='ml-2.5 text-lg font-semibold text-white tracking-tight'>Matrix</span>
            </Link>
            <div className='hidden lg:flex items-center gap-7'>
              {sectionLinks.map((link) => (
                <Link
                  key={link.href}
                  href={link.href}
                  className='text-sm font-medium text-grayscale-300 transition-colors hover:text-white'
                >
                  {link.label}
                </Link>
              ))}
              <Link
                href='/docs'
                className={`text-sm font-medium transition-colors ${
                  isActive('/docs') ? 'text-primary-300' : 'text-grayscale-300 hover:text-white'
                }`}
              >
                Docs
              </Link>
            </div>
          </div>

          {/* Secondary Nav */}
          <div className='flex items-center gap-3'>
            <a
              href='https://github.com/ecirlabs/matrix-core'
              className='hidden sm:inline-flex text-grayscale-300 hover:text-white transition-colors'
              target='_blank'
              rel='noopener noreferrer'
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
            <Button href='/download' variant='primary' size='sm' className='hidden sm:inline-flex'>
              Get started
            </Button>

            {/* Mobile Menu Button */}
            <button
              onClick={() => setIsMenuOpen(!isMenuOpen)}
              className='lg:hidden p-2 text-grayscale-300 hover:text-white transition-colors'
              aria-label='Toggle menu'
            >
              <svg className='w-6 h-6' fill='none' stroke='currentColor' viewBox='0 0 24 24'>
                {isMenuOpen ? (
                  <path strokeLinecap='round' strokeLinejoin='round' strokeWidth={2} d='M6 18L18 6M6 6l12 12' />
                ) : (
                  <path strokeLinecap='round' strokeLinejoin='round' strokeWidth={2} d='M4 6h16M4 12h16M4 18h16' />
                )}
              </svg>
            </button>
          </div>
        </div>

        {/* Mobile Menu */}
        {isMenuOpen && (
          <div className='lg:hidden py-4 border-t border-white/10'>
            <div className='flex flex-col gap-4'>
              {sectionLinks.map((link) => (
                <Link
                  key={link.href}
                  href={link.href}
                  className='text-sm font-medium text-grayscale-300 hover:text-white transition-colors'
                  onClick={() => setIsMenuOpen(false)}
                >
                  {link.label}
                </Link>
              ))}
              <Link
                href='/docs'
                className={`text-sm font-medium ${isActive('/docs') ? 'text-primary-300' : 'text-grayscale-300'}`}
                onClick={() => setIsMenuOpen(false)}
              >
                Docs
              </Link>
              <Link
                href='/download'
                className={`text-sm font-medium ${isActive('/download') ? 'text-primary-300' : 'text-grayscale-300'}`}
                onClick={() => setIsMenuOpen(false)}
              >
                Download
              </Link>
              <div className='flex items-center gap-4 pt-4 border-t border-white/10'>
                <a
                  href='https://github.com/ecirlabs/matrix-core'
                  className='text-grayscale-300 hover:text-white transition-colors'
                  target='_blank'
                  rel='noopener noreferrer'
                  aria-label='GitHub'
                >
                  <svg className='w-6 h-6' fill='currentColor' viewBox='0 0 24 24'>
                    <path
                      fillRule='evenodd'
                      clipRule='evenodd'
                      d='M12 2C6.477 2 2 6.477 2 12c0 4.42 2.87 8.17 6.84 9.5.5.08.66-.23.66-.5v-1.69c-2.77.6-3.36-1.34-3.36-1.34-.46-1.16-1.11-1.47-1.11-1.47-.91-.62.07-.6.07-.6 1 .07 1.53 1.03 1.53 1.03.87 1.52 2.34 1.07 2.91.83.09-.65.35-1.09.63-1.34-2.22-.25-4.55-1.11-4.55-4.92 0-1.11.38-2 1.03-2.71-.1-.25-.45-1.29.1-2.64 0 0 .84-.27 2.75 1.02.79-.22 1.65-.33 2.5-.33.85 0 1.71.11 2.5.33 1.91-1.29 2.75-1.02 2.75-1.02.55 1.35.2 2.39.1 2.64.65.71 1.03 1.6 1.03 2.71 0 3.82-2.34 4.66-4.57 4.91.36.31.69.92.69 1.85V21c0 .27.16.59.67.5C19.14 20.16 22 16.42 22 12A10 10 0 0012 2z'
                    />
                  </svg>
                </a>
                <Button href='/download' variant='primary' size='sm' className='w-full'>
                  Get started
                </Button>
              </div>
            </div>
          </div>
        )}
      </div>
    </nav>
  );
}
