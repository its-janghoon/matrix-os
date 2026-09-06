import Link from 'next/link';

const productLinks = [
  { label: 'Download', href: '/download' },
  { label: 'Documentation', href: '/docs' },
  { label: 'Quickstart', href: '/docs/quickstart' },
  { label: 'Architecture', href: '/docs/architecture' },
];

const resourceLinks = [
  { label: 'Introduction', href: '/docs/introduction' },
  { label: 'Matrix Protocol', href: '/docs/matrix-protocol' },
  { label: 'Soul Protocol', href: '/docs/soul-protocol' },
  { label: 'Configuration', href: '/docs/configuration' },
];

export function Footer() {
  return (
    <footer className='border-t border-grayscale-800 bg-black text-grayscale-400'>
      <div className='max-w-6xl mx-auto px-4 py-16'>
        <div className='grid grid-cols-1 gap-10 md:grid-cols-4'>
          <div className='md:col-span-2'>
            <div className='flex items-center'>
              <div className='h-10 w-10 rounded flex items-center justify-center border border-white/20 bg-white/10'>
                <span className='text-xs font-semibold text-white tracking-wider'>ECIR</span>
              </div>
              <span className='ml-3 text-lg font-semibold text-white'>Labs</span>
            </div>
            <p className='mt-4 max-w-sm text-sm text-grayscale-400'>
              Matrix OS is an open operating fabric for decentralized intelligence: own your execution, keep data local,
              and trade idle compute across a peer-to-peer network.
            </p>
          </div>

          <div>
            <h3 className='text-sm font-semibold text-white'>Product</h3>
            <ul className='mt-4 space-y-3 text-sm'>
              {productLinks.map((link) => (
                <li key={link.href}>
                  <Link href={link.href} className='text-grayscale-400 transition-colors hover:text-primary-300'>
                    {link.label}
                  </Link>
                </li>
              ))}
            </ul>
          </div>

          <div>
            <h3 className='text-sm font-semibold text-white'>Resources</h3>
            <ul className='mt-4 space-y-3 text-sm'>
              {resourceLinks.map((link) => (
                <li key={link.href}>
                  <Link href={link.href} className='text-grayscale-400 transition-colors hover:text-primary-300'>
                    {link.label}
                  </Link>
                </li>
              ))}
              <li>
                <a
                  href='https://github.com/ecirlabs/matrix-core'
                  target='_blank'
                  rel='noopener noreferrer'
                  className='text-grayscale-400 transition-colors hover:text-primary-300'
                >
                  GitHub
                </a>
              </li>
            </ul>
          </div>
        </div>

        <div className='mt-12 flex flex-col items-center justify-between gap-4 border-t border-grayscale-800 pt-8 text-sm text-grayscale-500 md:flex-row'>
          <p>&copy; {new Date().getFullYear()} ECIR Labs. All rights reserved.</p>
          <p>Matrix OS &mdash; decentralized intelligence, built in the open.</p>
        </div>
      </div>
    </footer>
  );
}
