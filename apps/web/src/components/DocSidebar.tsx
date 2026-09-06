'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';

export default function DocSidebar() {
  const pathname = usePathname();

  const isActive = (path: string) => {
    return pathname === path;
  };

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

  return (
    <aside className='fixed w-64 h-screen overflow-y-auto bg-black border-r border-gray-800'>
      <nav className='p-6 space-y-8'>
        {navigation.map(section => (
          <div key={section.section}>
            <h2 className='text-sm font-semibold text-gray-400 uppercase tracking-wider mb-3'>{section.section}</h2>
            <ul className='space-y-2'>
              {section.items.map(item => (
                <li key={item.href}>
                  <Link
                    href={item.href}
                    className={`block px-3 py-2 rounded-md text-sm ${
                      isActive(item.href)
                        ? 'bg-blue-500/10 text-blue-400 font-medium'
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
    </aside>
  );
}
