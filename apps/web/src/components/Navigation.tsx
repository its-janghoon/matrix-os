'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useState } from 'react';
import { Button } from './Button';

export default function Navigation() {
  const pathname = usePathname();
  const [isMenuOpen, setIsMenuOpen] = useState(false);

  const isActive = (path: string) => pathname === path;

  return (
    <nav className='fixed top-0 left-0 right-0 bg-black/80 backdrop-blur-sm border-b border-gray-800 z-50'>
      <div className='max-w-4xl mx-auto px-4'>
        <div className='flex items-center justify-between h-16'>
          {/* Logo and Primary Nav */}
          <div className='flex items-center gap-8'>
            <Link href='/' className='flex items-center'>
              <div className='h-10 w-10 bg-white/10 rounded flex items-center justify-center border border-white/20'>
                <span className='text-xs font-semibold text-white tracking-wider'>ECIR</span>
              </div>
              <span className='ml-3 text-lg font-semibold text-white'>Labs</span>
            </Link>
            <div className='hidden md:flex items-center gap-6'>
              <Link
                href='/docs'
                className={`text-sm ${isActive('/docs') ? 'text-blue-400' : 'text-gray-300 hover:text-white'}`}
              >
                Documentation
              </Link>
              <Link
                href='/download'
                className={`text-sm ${isActive('/download') ? 'text-blue-400' : 'text-gray-300 hover:text-white'}`}
              >
                Download
              </Link>
            </div>
          </div>

          {/* Secondary Nav */}
          <div className='flex items-center gap-4'>
            <div className='hidden md:flex items-center gap-4'>
              <a
                href='https://github.com/ecirlabs/matrix-core'
                className='text-gray-300 hover:text-white'
                target='_blank'
                rel='noopener noreferrer'
              >
                <svg className='w-6 h-6' fill='currentColor' viewBox='0 0 24 24'>
                  <path
                    fillRule='evenodd'
                    clipRule='evenodd'
                    d='M12 2C6.477 2 2 6.477 2 12c0 4.42 2.87 8.17 6.84 9.5.5.08.66-.23.66-.5v-1.69c-2.77.6-3.36-1.34-3.36-1.34-.46-1.16-1.11-1.47-1.11-1.47-.91-.62.07-.6.07-.6 1 .07 1.53 1.03 1.53 1.03.87 1.52 2.34 1.07 2.91.83.09-.65.35-1.09.63-1.34-2.22-.25-4.55-1.11-4.55-4.92 0-1.11.38-2 1.03-2.71-.1-.25-.45-1.29.1-2.64 0 0 .84-.27 2.75 1.02.79-.22 1.65-.33 2.5-.33.85 0 1.71.11 2.5.33 1.91-1.29 2.75-1.02 2.75-1.02.55 1.35.2 2.39.1 2.64.65.71 1.03 1.6 1.03 2.71 0 3.82-2.34 4.66-4.57 4.91.36.31.69.92.69 1.85V21c0 .27.16.59.67.5C19.14 20.16 22 16.42 22 12A10 10 0 0012 2z'
                  />
                </svg>
              </a>
            </div>

            {/* Mobile Menu Button */}
            <button
              onClick={() => setIsMenuOpen(!isMenuOpen)}
              className='md:hidden p-2 text-gray-300 hover:text-white'
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
          <div className='md:hidden py-4 border-t border-gray-800'>
            <div className='flex flex-col gap-4'>
              <Link
                href='/docs'
                className={`text-sm ${isActive('/docs') ? 'text-blue-400' : 'text-gray-300'}`}
                onClick={() => setIsMenuOpen(false)}
              >
                Documentation
              </Link>
              <Link
                href='/download'
                className={`text-sm ${isActive('/download') ? 'text-blue-400' : 'text-gray-300'}`}
                onClick={() => setIsMenuOpen(false)}
              >
                Download
              </Link>
              <Link
                href='/blog'
                className={`text-sm ${isActive('/blog') ? 'text-blue-400' : 'text-gray-300'}`}
                onClick={() => setIsMenuOpen(false)}
              >
                Blog
              </Link>
              <div className='flex items-center gap-4 pt-4 border-t border-gray-800'>
                <a
                  href='https://github.com/ecirlabs/matrix-core'
                  className='text-gray-300 hover:text-white'
                  target='_blank'
                  rel='noopener noreferrer'
                >
                  <svg className='w-6 h-6' fill='currentColor' viewBox='0 0 24 24'>
                    <path
                      fillRule='evenodd'
                      clipRule='evenodd'
                      d='M12 2C6.477 2 2 6.477 2 12c0 4.42 2.87 8.17 6.84 9.5.5.08.66-.23.66-.5v-1.69c-2.77.6-3.36-1.34-3.36-1.34-.46-1.16-1.11-1.47-1.11-1.47-.91-.62.07-.6.07-.6 1 .07 1.53 1.03 1.53 1.03.87 1.52 2.34 1.07 2.91.83.09-.65.35-1.09.63-1.34-2.22-.25-4.55-1.11-4.55-4.92 0-1.11.38-2 1.03-2.71-.1-.25-.45-1.29.1-2.64 0 0 .84-.27 2.75 1.02.79-.22 1.65-.33 2.5-.33.85 0 1.71.11 2.5.33 1.91-1.29 2.75-1.02 2.75-1.02.55 1.35.2 2.39.1 2.64.65.71 1.03 1.6 1.03 2.71 0 3.82-2.34 4.66-4.57 4.91.36.31.69.92.69 1.85V21c0 .27.16.59.67.5C19.14 20.16 22 16.42 22 12A10 10 0 0012 2z'
                    />
                  </svg>
                </a>
                <a
                  href='https://discord.gg/ecirlabs'
                  className='text-gray-300 hover:text-white'
                  target='_blank'
                  rel='noopener noreferrer'
                >
                  <svg className='w-6 h-6' fill='currentColor' viewBox='0 0 24 24'>
                    <path d='M20.317 4.37a19.791 19.791 0 00-4.885-1.515.074.074 0 00-.079.037c-.21.375-.444.864-.608 1.25a18.27 18.27 0 00-5.487 0 12.64 12.64 0 00-.617-1.25.077.077 0 00-.079-.037A19.736 19.736 0 003.677 4.37a.07.07 0 00-.032.027C.533 9.046-.32 13.58.099 18.057a.082.082 0 00.031.057 19.9 19.9 0 005.993 3.03.078.078 0 00.084-.028c.462-.63.874-1.295 1.226-1.994.021-.041.001-.09-.041-.106a13.107 13.107 0 01-1.872-.892.075.075 0 01-.008-.125c.126-.095.252-.193.372-.292a.075.075 0 01.078-.01c3.927 1.793 8.18 1.793 12.061 0a.075.075 0 01.079.01c.12.098.246.198.373.292.044.032.04.1-.006.125-.598.35-1.22.645-1.873.892a.075.075 0 00-.041.106c.36.698.772 1.362 1.225 1.994a.076.076 0 00.084.028 19.834 19.834 0 006.002-3.03.077.077 0 00.032-.054c.5-5.177-.838-9.674-3.549-13.66a.061.061 0 00-.031-.03zM8.02 15.33c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.956-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.956 2.418-2.157 2.418zm7.975 0c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.955-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.946 2.418-2.157 2.418z' />
                  </svg>
                </a>
                <Button href='/docs/introduction' variant='primary' size='sm' className='w-full'>
                  Documentation
                </Button>
              </div>
            </div>
          </div>
        )}
      </div>
    </nav>
  );
}
