'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useEffect, useState } from 'react';
import { FiMenu, FiX } from 'react-icons/fi';

const navigation = [
  {
    section: 'Getting Started',
    items: [
      { name: 'Introduction', href: '/docs/introduction' },
      { name: 'Installation', href: '/docs/installation' },
      { name: 'Quick Start', href: '/docs/quickstart' },
    ],
  },
  {
    section: 'Core Concepts',
    items: [
      { name: 'Architecture', href: '/docs/architecture' },
      { name: 'Compute Marketplace', href: '/docs/compute-marketplace' },
      { name: 'Configuration', href: '/docs/configuration' },
    ],
  },
  {
    section: 'Tools',
    items: [{ name: 'matrix CLI', href: '/docs/cli' }],
  },
  {
    section: 'Protocols',
    items: [
      { name: 'Matrix Protocol', href: '/docs/matrix-protocol' },
      { name: 'Soul Protocol', href: '/docs/soul-protocol' },
    ],
  },
  {
    section: 'Guides',
    items: [
      { name: 'Agent Development', href: '/docs/guides/agent-development' },
      { name: 'Network Setup', href: '/docs/guides/network-setup' },
    ],
  },
];

/**
 * Documentation navigation.
 *
 * Two layouts, not one. From `lg` up it is a fixed rail beside the article; a
 * fixed 256px rail is wrong on a phone, where it covered the page it was meant
 * to navigate and left the text squeezed under it. Below `lg` it collapses into
 * a button that opens the same list as an overlay drawer, and the article gets
 * the full width.
 */
export default function DocSidebar() {
  const pathname = usePathname();
  const [open, setOpen] = useState(false);

  // Escape closes it, matching every other overlay on the site.
  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open]);

  const isActive = (path: string) => pathname === path;

  const list = (
    <nav className='space-y-8 p-6'>
      {navigation.map((section) => (
        <div key={section.section}>
          <h2 className='mb-3 text-sm font-semibold uppercase tracking-wider text-gray-400'>
            {section.section}
          </h2>
          <ul className='space-y-2'>
            {section.items.map((item) => (
              <li key={item.href}>
                <Link
                  href={item.href}
                  // The drawer covers the article it just navigated to, so a
                  // chosen link closes it.
                  onClick={() => setOpen(false)}
                  aria-current={isActive(item.href) ? 'page' : undefined}
                  className={`block rounded-md px-3 py-2 text-sm ${
                    isActive(item.href)
                      ? 'bg-accent-300/10 font-medium text-accent-200'
                      : 'text-gray-300 hover:bg-gray-900 hover:text-gray-100'
                  }`}
                >
                  {item.name}
                </Link>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </nav>
  );

  return (
    <>
      {/* Phone and tablet: a button, and the same list as a drawer. */}
      <div className='lg:hidden'>
        <button
          type='button'
          onClick={() => setOpen(true)}
          aria-expanded={open}
          aria-controls='doc-nav-drawer'
          className='sticky top-16 z-30 flex w-full items-center gap-2 border-b border-gray-800 bg-black/90 px-4 py-3 text-sm font-medium text-gray-200 backdrop-blur'
        >
          <FiMenu className='h-4 w-4' aria-hidden />
          Documentation menu
        </button>

        {open ? (
          <div className='fixed inset-0 z-50 lg:hidden'>
            <button
              type='button'
              aria-label='Close documentation menu'
              onClick={() => setOpen(false)}
              className='absolute inset-0 h-full w-full bg-black/70'
            />
            <div
              id='doc-nav-drawer'
              className='absolute inset-y-0 left-0 flex w-72 max-w-[85vw] flex-col overflow-y-auto border-r border-gray-800 bg-black'
            >
              <div className='flex items-center justify-between border-b border-gray-800 px-6 py-4'>
                <span className='text-sm font-semibold uppercase tracking-wider text-gray-400'>Docs</span>
                <button
                  type='button'
                  onClick={() => setOpen(false)}
                  aria-label='Close documentation menu'
                  className='rounded-md p-1 text-gray-400 hover:bg-gray-900 hover:text-white'
                >
                  <FiX className='h-5 w-5' aria-hidden />
                </button>
              </div>
              {list}
            </div>
          </div>
        ) : null}
      </div>

      {/* Desktop: the fixed rail. */}
      <aside className='hidden h-screen w-64 shrink-0 overflow-y-auto border-r border-gray-800 bg-black lg:fixed lg:block'>
        {list}
      </aside>
    </>
  );
}
