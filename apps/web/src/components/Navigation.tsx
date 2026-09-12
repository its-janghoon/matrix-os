'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useEffect, useRef, useState } from 'react';
import { FiChevronDown } from 'react-icons/fi';
import { Button } from './Button';
import { BrandMark } from '@/components/BrandMark';
import { GITHUB_URL } from '@/lib/releases';

type NavLink = { label: string; href: string; description?: string; external?: boolean };
type NavMenu = { label: string; href?: string; links?: NavLink[] };

const productLinks: NavLink[] = [
  { label: 'Compute Marketplace', href: '/products/marketplace' },
  { label: 'MATRIX Token', href: '/products/token' },
  { label: 'Base Bridge', href: '/bridge', description: 'Lock native MATRIX or burn wMATRIX with MetaMask' },
  { label: 'Consensus', href: '/products/consensus' },
  { label: 'LLM Inference', href: '/products/inference' },
  { label: 'Matrix Console', href: '/products/console' },
  { label: 'matrix CLI', href: '/products/cli' },
  { label: 'Chat (self-custody)', href: '/chat', description: 'Pay for inference with a key your browser holds' },
];
const developerLinks: NavLink[] = [
  { label: 'Documentation', href: '/docs' },
  { label: 'Quickstart', href: '/docs/quickstart' },
  { label: 'Installation', href: '/docs/installation' },
  { label: 'Architecture', href: '/docs/architecture' },
  { label: 'matrix CLI reference', href: '/docs/cli' },
];

const resourceLinks: NavLink[] = [
  { label: 'Matrix Protocol', href: '/docs/matrix-protocol' },
  { label: 'Soul Protocol', href: '/docs/soul-protocol' },
  { label: 'Download', href: '/download' },
  { label: 'GitHub', href: GITHUB_URL, external: true },
];

const menus: NavMenu[] = [
  { label: 'Products', links: productLinks },
  { label: 'How it works', href: '/how-it-works' },
  { label: 'Developers', links: developerLinks },
  { label: 'Resources', links: resourceLinks },
];

const GitHubIcon = ({ className = 'w-5 h-5' }: { className?: string }) => (
  <svg className={className} fill='currentColor' viewBox='0 0 24 24'>
    <path
      fillRule='evenodd'
      clipRule='evenodd'
      d='M12 2C6.477 2 2 6.477 2 12c0 4.42 2.87 8.17 6.84 9.5.5.08.66-.23.66-.5v-1.69c-2.77.6-3.36-1.34-3.36-1.34-.46-1.16-1.11-1.47-1.11-1.47-.91-.62.07-.6.07-.6 1 .07 1.53 1.03 1.53 1.03.87 1.52 2.34 1.07 2.91.83.09-.65.35-1.09.63-1.34-2.22-.25-4.55-1.11-4.55-4.92 0-1.11.38-2 1.03-2.71-.1-.25-.45-1.29.1-2.64 0 0 .84-.27 2.75 1.02.79-.22 1.65-.33 2.5-.33.85 0 1.71.11 2.5.33 1.91-1.29 2.75-1.02 2.75-1.02.55 1.35.2 2.39.1 2.64.65.71 1.03 1.6 1.03 2.71 0 3.82-2.34 4.66-4.57 4.91.36.31.69.92.69 1.85V21c0 .27.16.59.67.5C19.14 20.16 22 16.42 22 12A10 10 0 0012 2z'
    />
  </svg>
);

const DropdownLink = ({ link, onNavigate }: { link: NavLink; onNavigate: () => void }) => {
  const className =
    'block rounded-xl px-3 py-2 text-sm font-medium text-grayscale-300 transition-colors hover:bg-white/[0.06] hover:text-white';
  if (link.external) {
    return (
      <a href={link.href} target='_blank' rel='noopener noreferrer' className={className} onClick={onNavigate}>
        {link.label}
      </a>
    );
  }
  return (
    <Link href={link.href} className={className} onClick={onNavigate}>
      {link.label}
    </Link>
  );
};

export default function Navigation() {
  const pathname = usePathname();
  const [isMenuOpen, setIsMenuOpen] = useState(false);
  const [openMenu, setOpenMenu] = useState<string | null>(null);
  const [openMobileGroup, setOpenMobileGroup] = useState<string | null>(null);
  const [scrolled, setScrolled] = useState(false);
  const navRef = useRef<HTMLElement | null>(null);

  const isActive = (path: string) => pathname === path;

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 8);
    onScroll();
    window.addEventListener('scroll', onScroll, { passive: true });
    return () => window.removeEventListener('scroll', onScroll);
  }, []);

  // Close desktop dropdowns on outside click or Escape.
  useEffect(() => {
    const onClickOutside = (event: MouseEvent) => {
      if (navRef.current && !navRef.current.contains(event.target as Node)) {
        setOpenMenu(null);
      }
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setOpenMenu(null);
      }
    };
    document.addEventListener('mousedown', onClickOutside);
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('mousedown', onClickOutside);
      document.removeEventListener('keydown', onKeyDown);
    };
  }, []);

  const closeAll = () => {
    setIsMenuOpen(false);
    setOpenMenu(null);
    setOpenMobileGroup(null);
  };

  return (
    <nav
      ref={navRef}
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
            {/* The wordmark stays real text rather than part of an SVG lockup:
                an SVG loaded through <img> or next/image cannot reach the page's
                Inter, so a lockup file would silently fall back to a system
                sans here. */}
            <Link
              href='/'
              className='flex items-center gap-2.5 group'
              aria-label='Matrix OS, home'
              onClick={closeAll}
            >
              <BrandMark size={32} className='shrink-0' />
              <span className='text-lg font-semibold tracking-tight text-white'>
                Matrix <span className='text-secondary-400'>OS</span>
              </span>
            </Link>

            <div className='hidden lg:flex items-center gap-1'>
              {menus.map((menu) =>
                menu.links ? (
                  <div
                    key={menu.label}
                    className='relative'
                    onMouseEnter={() => setOpenMenu(menu.label)}
                    onMouseLeave={() => setOpenMenu((current) => (current === menu.label ? null : current))}
                  >
                    <button
                      type='button'
                      aria-haspopup='true'
                      aria-expanded={openMenu === menu.label}
                      onClick={() => setOpenMenu((current) => (current === menu.label ? null : menu.label))}
                      className='inline-flex items-center gap-1 rounded-full px-3 py-2 text-sm font-medium text-grayscale-300 transition-colors hover:text-white'
                    >
                      {menu.label}
                      <FiChevronDown
                        className={`h-3.5 w-3.5 transition-transform duration-200 ${
                          openMenu === menu.label ? 'rotate-180' : ''
                        }`}
                      />
                    </button>
                    {openMenu === menu.label && (
                      <div className='absolute left-0 top-full w-64 pt-2'>
                        <div className='rounded-2xl border border-white/10 bg-black/90 p-2 shadow-card backdrop-blur-xl'>
                          {menu.links.map((link) => (
                            <DropdownLink key={link.href} link={link} onNavigate={closeAll} />
                          ))}
                        </div>
                      </div>
                    )}
                  </div>
                ) : (
                  <Link
                    key={menu.label}
                    href={menu.href!}
                    className={`rounded-full px-3 py-2 text-sm font-medium transition-colors ${
                      isActive(menu.href!) ? 'text-primary-300' : 'text-grayscale-300 hover:text-white'
                    }`}
                  >
                    {menu.label}
                  </Link>
                )
              )}
            </div>
          </div>

          {/* Secondary Nav */}
          <div className='flex items-center gap-3'>
            <a
              href={GITHUB_URL}
              className='hidden sm:inline-flex text-grayscale-300 hover:text-white transition-colors'
              target='_blank'
              rel='noopener noreferrer'
              aria-label='GitHub'
            >
              <GitHubIcon />
            </a>
            <Button href='/download' variant='primary' size='sm' className='hidden sm:inline-flex'>
              Get started
            </Button>

            {/* Mobile Menu Button */}
            <button
              onClick={() => setIsMenuOpen(!isMenuOpen)}
              className='lg:hidden p-2 text-grayscale-300 hover:text-white transition-colors'
              aria-label='Toggle menu'
              aria-expanded={isMenuOpen}
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
            <div className='flex flex-col gap-1'>
              {menus.map((menu) =>
                menu.links ? (
                  <div key={menu.label} className='border-b border-white/5 pb-1'>
                    <button
                      type='button'
                      aria-expanded={openMobileGroup === menu.label}
                      onClick={() =>
                        setOpenMobileGroup((current) => (current === menu.label ? null : menu.label))
                      }
                      className='flex w-full items-center justify-between px-1 py-3 text-sm font-semibold text-white'
                    >
                      {menu.label}
                      <FiChevronDown
                        className={`h-4 w-4 transition-transform duration-200 ${
                          openMobileGroup === menu.label ? 'rotate-180' : ''
                        }`}
                      />
                    </button>
                    {openMobileGroup === menu.label && (
                      <div className='flex flex-col gap-1 pb-2 pl-3'>
                        {menu.links.map((link) => (
                          <DropdownLink key={link.href} link={link} onNavigate={closeAll} />
                        ))}
                      </div>
                    )}
                  </div>
                ) : (
                  <Link
                    key={menu.label}
                    href={menu.href!}
                    className={`px-1 py-3 text-sm font-semibold ${
                      isActive(menu.href!) ? 'text-primary-300' : 'text-white'
                    }`}
                    onClick={closeAll}
                  >
                    {menu.label}
                  </Link>
                )
              )}

              <div className='mt-3 flex items-center gap-4 border-t border-white/10 pt-4'>
                <a
                  href={GITHUB_URL}
                  className='text-grayscale-300 hover:text-white transition-colors'
                  target='_blank'
                  rel='noopener noreferrer'
                  aria-label='GitHub'
                >
                  <GitHubIcon className='w-6 h-6' />
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
