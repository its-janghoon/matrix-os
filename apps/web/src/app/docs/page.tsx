import DocSidebar from '@/components/DocSidebar';
import { NodeStack } from '@/components/diagrams';
import Navigation from '@/components/Navigation';
import Link from 'next/link';

export const metadata = {
  title: 'Documentation',
  description:
    'Every Matrix OS documentation page, what is in it, and where to start: a node and a settled compute job, then the protocol underneath.',
};

/**
 * This page was filler: a two-column "Quick Links" grid holding a single card,
 * one sentence about creating distributed systems and exploring "advanced
 * features", and links to four of the eleven documentation pages. An index that
 * hides most of what it indexes is worse than no index, because a reader
 * concludes the rest does not exist.
 *
 * The list below is the sidebar's, with a line each saying what the page
 * actually contains.
 */
type Entry = { name: string; href: string; blurb: string };

const sections: { section: string; blurb: string; items: Entry[] }[] = [
  {
    section: 'Getting started',
    blurb: 'From nothing to a settled job.',
    items: [
      {
        name: 'Introduction',
        href: '/docs/introduction',
        blurb: 'What Matrix OS is, what it does today, and - stated plainly - what it does not do.',
      },
      {
        name: 'Installation',
        href: '/docs/installation',
        blurb: 'A release archive, or a build from source. Both binaries, and how to check what you got.',
      },
      {
        name: 'Quickstart',
        href: '/docs/quickstart',
        blurb: 'Three commands: a node, a funded wallet, and a compute job that settles. With the output a real run prints.',
      },
    ],
  },
  {
    section: 'Core concepts',
    blurb: 'What is inside the node you just started.',
    items: [
      {
        name: 'Architecture',
        href: '/docs/architecture',
        blurb: 'The parts of one matrixd process, which of them talk to the network, and what they share.',
      },
      {
        name: 'Compute marketplace',
        href: '/docs/compute-marketplace',
        blurb: 'How a job is priced, reserved and settled; the coin, the consensus that agrees it, and the wrapped ERC-20 mirror.',
      },
      {
        name: 'Configuration',
        href: '/docs/configuration',
        blurb: 'Every field in the file matrixd -init writes, including the API key and the validator-set knobs.',
      },
    ],
  },
  {
    section: 'Tools',
    blurb: 'Driving a running node.',
    items: [
      {
        name: 'matrix CLI',
        href: '/docs/cli',
        blurb: 'Every command and flag, which of the node’s ports each one talks to, and what job settlement requires.',
      },
    ],
  },
  {
    section: 'Protocols',
    blurb: 'The wire formats and the rules.',
    items: [
      {
        name: 'Matrix Protocol',
        href: '/docs/matrix-protocol',
        blurb: 'Gossip topics, the block and vote messages, block sync, and the equivocation evidence a node can prove.',
      },
      {
        name: 'Soul Protocol',
        href: '/docs/soul-protocol',
        blurb: 'Protocol Buffers definitions and in-process Go types. No service serves it yet, and the page says so.',
      },
    ],
  },
  {
    section: 'Guides',
    blurb: 'Longer walk-throughs.',
    items: [
      {
        name: 'Agent development',
        href: '/docs/guides/agent-development',
        blurb: 'The four host functions the WebAssembly runtime exposes, its memory ceiling and run-time deadline, and how to load a module into it.',
      },
      {
        name: 'Network setup',
        href: '/docs/guides/network-setup',
        blurb: 'Two nodes on one ledger: what must match, and how the validator set changes while the network runs.',
      },
    ],
  },
];

export default function Docs() {
  return (
    <>
      <Navigation />
      <div className='min-h-screen bg-black'>
        <div className='pt-16'>
          <div className='flex flex-col lg:flex-row'>
            <DocSidebar />

            <main className='min-w-0 flex-1 p-4 sm:p-6 lg:ml-64 lg:p-8'>
              <div className='mx-auto max-w-4xl'>
                <div className='mb-8 rounded-xl border border-primary-400/20 bg-gradient-to-r from-primary-400/10 via-accent-300/10 to-primary-400/10 p-8'>
                  <h1 className='mb-4 text-4xl font-bold text-white'>Documentation</h1>
                  <p className='text-xl text-gray-100'>
                    Everything here describes code that runs. If you are starting from nothing, go to the{' '}
                    <Link href='/docs/quickstart' className='text-accent-200 underline hover:text-accent-100'>
                      quickstart
                    </Link>{' '}
                    - a node and a settled compute job take about a minute.
                  </p>
                </div>

                <NodeStack />

                <div className='space-y-10'>
                  {sections.map((s) => (
                    <section key={s.section}>
                      <h2 className='text-2xl font-bold text-white'>{s.section}</h2>
                      <p className='mb-4 mt-1 text-gray-400'>{s.blurb}</p>
                      <ul className='space-y-3'>
                        {s.items.map((item) => (
                          <li key={item.href}>
                            <Link
                              href={item.href}
                              className='block rounded-xl border border-gray-800 bg-gray-900/50 p-5 transition-colors hover:border-primary-400/40 hover:bg-gray-900'
                            >
                              <h3 className='text-lg font-semibold text-white'>{item.name}</h3>
                              <p className='mt-1 text-gray-300'>{item.blurb}</p>
                            </Link>
                          </li>
                        ))}
                      </ul>
                    </section>
                  ))}
                </div>
              </div>
            </main>
          </div>
        </div>
      </div>
    </>
  );
}
