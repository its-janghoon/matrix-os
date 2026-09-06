'use client';

import { Button } from '@/components/Button';
import { Footer } from '@/components/Footer';
import MatrixBackground from '@/components/MatrixBackground';
import Navigation from '@/components/Navigation';
import { FiCheck, FiCpu, FiDownload, FiServer, FiShoppingCart } from 'react-icons/fi';

const CheckItem = ({ children }: { children: React.ReactNode }) => (
  <li className='flex gap-3'>
    <FiCheck className='w-5 h-5 mt-0.5 text-primary-300 shrink-0' />
    <span className='text-grayscale-300'>{children}</span>
  </li>
);

export default function Home() {
  return (
    <>
      <Navigation />

      <main className='min-h-screen bg-black text-white pt-16 relative'>
        {/* Hero Section with Matrix Background */}
        <div className='relative h-screen flex items-center'>
          <div className='absolute inset-0 z-0'>
            <MatrixBackground />
          </div>

          <div className='container mx-auto px-4 relative z-10'>
            <div className='max-w-4xl mx-auto text-center'>
              <div className='flex items-center justify-center gap-2 mb-6'>
                <span className='px-3 py-1 text-xs font-medium bg-primary/10 text-primary-300 rounded-full border border-primary/20'>
                  v0.1.0-alpha
                </span>
                <a
                  href='https://github.com/ecirlabs/matrix-core'
                  className='px-3 py-1 text-xs font-medium bg-grayscale-800 text-grayscale-300 rounded-full hover:bg-grayscale-700 transition-colors'
                >
                  Star on GitHub
                </a>
              </div>

              <h1 className='text-6xl md:text-7xl font-bold mb-6 bg-decorative-1 text-transparent bg-clip-text'>
                Matrix OS
              </h1>
              <p className='text-xl md:text-2xl text-grayscale-300 mb-8 max-w-3xl mx-auto'>
                An operating fabric that lets any device spin up, trade, and orchestrate autonomous agents&mdash;forming
                a decentralized digital civilization.
              </p>

              <div className='flex flex-wrap items-center justify-center gap-4 mb-12'>
                <Button href='/download' variant='primary' size='lg'>
                  <FiDownload className='w-5 h-5 mr-2' />
                  Download
                </Button>
                <Button href='/docs/introduction' variant='secondary' size='lg'>
                  <svg className='w-5 h-5 mr-2' fill='none' viewBox='0 0 24 24' stroke='currentColor'>
                    <path
                      strokeLinecap='round'
                      strokeLinejoin='round'
                      strokeWidth={2}
                      d='M12 6.253v13m0-13C10.832 5.477 9.246 5 7.5 5S4.168 5.477 3 6.253v13C4.168 18.477 5.754 18 7.5 18s3.332.477 4.5 1.253m0-13C13.168 5.477 14.754 5 16.5 5c1.747 0 3.332.477 4.5 1.253v13C19.832 18.477 18.247 18 16.5 18c-1.746 0-3.332.477-4.5 1.253'
                    />
                  </svg>
                  Documentation
                </Button>
              </div>
            </div>
          </div>
        </div>

        {/* Core Principles Section */}
        <section className='py-24 bg-gradient-to-b from-black to-grayscale-900 relative z-10'>
          <div className='container mx-auto px-4'>
            <div className='max-w-4xl mx-auto'>
              <h2 className='text-4xl font-bold mb-12 text-center'>Core Principles</h2>
              <div className='grid grid-cols-1 md:grid-cols-2 gap-12'>
                <div>
                  <h3 className='text-2xl font-semibold mb-4'>Own Your Execution</h3>
                  <p className='text-grayscale-300 mb-6'>
                    One static binary per node&mdash;no hidden cloud dependencies. Intelligence runs where your data
                    lives.
                  </p>
                  <ul className='space-y-4'>
                    <CheckItem>Local-first computing</CheckItem>
                    <CheckItem>No cloud dependencies</CheckItem>
                    <CheckItem>Device-first architecture</CheckItem>
                  </ul>
                </div>
                <div>
                  <h3 className='text-2xl font-semibold mb-4'>Privacy by Locality</h3>
                  <p className='text-grayscale-300 mb-6'>
                    Your data never exits the device unless you explicitly sign it. Full control over your information.
                  </p>
                  <ul className='space-y-4'>
                    <CheckItem>Data sovereignty</CheckItem>
                    <CheckItem>Signed data transfers</CheckItem>
                    <CheckItem>Local-only by default</CheckItem>
                  </ul>
                </div>
              </div>
            </div>
          </div>
        </section>

        {/* Marketplace Section */}
        <section id='marketplace' className='py-24 bg-gradient-to-b from-grayscale-900 to-black relative z-10 scroll-mt-16'>
          <div className='container mx-auto px-4'>
            <div className='max-w-5xl mx-auto'>
              <div className='text-center mb-12'>
                <span className='inline-block px-3 py-1 text-xs font-medium bg-secondary/10 text-secondary-300 rounded-full border border-secondary/20 mb-4'>
                  Compute Marketplace
                </span>
                <h2 className='text-4xl font-bold mb-4'>A Peer-to-Peer Market for Compute</h2>
                <p className='text-xl text-grayscale-300 max-w-3xl mx-auto'>
                  Idle machines register as compute providers and advertise capacity. Buyers submit paid compute jobs.
                  A compute-credits ledger settles payment from buyer to provider the moment a job completes.
                </p>
              </div>

              <div className='grid grid-cols-1 md:grid-cols-2 gap-6'>
                {/* Providers */}
                <div className='p-8 rounded-2xl border border-grayscale-800 bg-gradient-to-b from-grayscale-900 to-black hover:border-primary-300/40 transition-colors'>
                  <div className='flex items-center gap-3 mb-4'>
                    <div className='h-11 w-11 rounded-xl bg-primary/10 border border-primary/20 flex items-center justify-center'>
                      <FiServer className='w-5 h-5 text-primary-300' />
                    </div>
                    <h3 className='text-2xl font-semibold'>Providers earn</h3>
                  </div>
                  <p className='text-grayscale-300 mb-6'>
                    Leave a machine running and register it as a provider. It advertises available capacity and a price
                    per compute unit, then earns compute credits each time a job it served completes.
                  </p>
                  <ul className='space-y-4'>
                    <CheckItem>Register idle capacity and set a price per unit</CheckItem>
                    <CheckItem>Capacity is reserved when a buyer&apos;s job is accepted</CheckItem>
                    <CheckItem>Credits land in your ledger balance on job completion</CheckItem>
                  </ul>
                </div>

                {/* Buyers */}
                <div className='p-8 rounded-2xl border border-grayscale-800 bg-gradient-to-b from-grayscale-900 to-black hover:border-secondary-300/40 transition-colors'>
                  <div className='flex items-center gap-3 mb-4'>
                    <div className='h-11 w-11 rounded-xl bg-secondary/10 border border-secondary/20 flex items-center justify-center'>
                      <FiShoppingCart className='w-5 h-5 text-secondary-300' />
                    </div>
                    <h3 className='text-2xl font-semibold'>Buyers pay for compute</h3>
                  </div>
                  <p className='text-grayscale-300 mb-6'>
                    Submit a paid job for the LLM compute and API responses you need. The price is quoted up front from
                    the provider&apos;s rate, and credits only move once the work is done.
                  </p>
                  <ul className='space-y-4'>
                    <CheckItem>Submit jobs priced as units &times; provider rate</CheckItem>
                    <CheckItem>Balance is checked up front; you are charged on completion</CheckItem>
                    <CheckItem>Cancel a pending job to release the reserved capacity</CheckItem>
                  </ul>
                </div>
              </div>

              {/* How settlement works */}
              <div className='mt-6 p-8 rounded-2xl border border-grayscale-800 bg-black'>
                <div className='flex items-center gap-3 mb-4'>
                  <FiCpu className='w-5 h-5 text-primary-300' />
                  <h3 className='text-xl font-semibold'>How settlement works today</h3>
                </div>
                <p className='text-grayscale-300'>
                  The compute-credits ledger is implemented and shipped in the Matrix core: balances are persisted in
                  the node&apos;s embedded key-value store, jobs move through pending, running, and completed states,
                  and each completed job transfers credits atomically from buyer to provider. Registered providers and
                  their open jobs are rehydrated from disk when a node restarts, so reserved capacity and pending work
                  survive downtime.
                </p>
              </div>

              {/* Roadmap callout */}
              <div className='mt-6 p-6 rounded-2xl border border-secondary-300/30 bg-secondary/5'>
                <div className='flex items-center gap-2 mb-2'>
                  <span className='px-2.5 py-0.5 text-xs font-semibold bg-secondary/20 text-secondary-300 rounded-full uppercase tracking-wide'>
                    Roadmap
                  </span>
                  <span className='text-sm text-grayscale-400'>Not yet implemented</span>
                </div>
                <p className='text-grayscale-300'>
                  Settlement runs on <span className='text-white font-medium'>internal compute credits</span> today.
                  A live cryptocurrency token and on-chain payment for cross-network settlement are planned roadmap
                  items&mdash;there is no live token yet.
                </p>
              </div>
            </div>
          </div>
        </section>

        {/* Technical Features Section */}
        <section className='py-24 bg-gradient-to-b from-black to-grayscale-900 relative z-10'>
          <div className='container mx-auto px-4'>
            <div className='max-w-4xl mx-auto'>
              <h2 className='text-4xl font-bold mb-12 text-center'>Technical Features</h2>
              <div className='grid grid-cols-1 md:grid-cols-2 gap-12'>
                <div>
                  <h3 className='text-2xl font-semibold mb-4'>Secure Runtime</h3>
                  <p className='text-grayscale-300 mb-6'>
                    Built with security-first principles using Wasmtime sandbox and advanced encryption.
                  </p>
                  <ul className='space-y-4'>
                    <CheckItem>Wasm sandbox environment</CheckItem>
                    <CheckItem>Resource-capped execution</CheckItem>
                    <CheckItem>Secure messaging protocols</CheckItem>
                  </ul>
                </div>
                <div>
                  <h3 className='text-2xl font-semibold mb-4'>Distributed Infrastructure</h3>
                  <p className='text-grayscale-300 mb-6'>
                    Peer-to-peer architecture with built-in support for distributed computing and storage.
                  </p>
                  <ul className='space-y-4'>
                    <CheckItem>libp2p + Noise/TLS 1.3</CheckItem>
                    <CheckItem>CRDT-based data fabric</CheckItem>
                    <CheckItem>NAT traversal support</CheckItem>
                  </ul>
                </div>
              </div>
            </div>
          </div>
        </section>

        {/* Use Cases Section */}
        <section className='py-24 bg-gradient-to-b from-grayscale-900 to-black relative z-10'>
          <div className='container mx-auto px-4'>
            <div className='max-w-4xl mx-auto'>
              <h2 className='text-4xl font-bold mb-12 text-center'>Use Cases</h2>
              <div className='grid grid-cols-1 md:grid-cols-3 gap-6'>
                <div className='p-6 rounded-xl border border-grayscale-800 bg-gradient-to-b from-grayscale-900 to-black hover:border-primary-300/40 transition-colors'>
                  <h3 className='text-xl font-semibold mb-4'>End Users</h3>
                  <p className='text-grayscale-400'>
                    Run copilots that learn locally, borrow compute from friends, and never leak data.
                  </p>
                </div>
                <div className='p-6 rounded-xl border border-grayscale-800 bg-gradient-to-b from-grayscale-900 to-black hover:border-primary-300/40 transition-colors'>
                  <h3 className='text-xl font-semibold mb-4'>Developers</h3>
                  <p className='text-grayscale-400'>
                    Publish Wasm micro-agents that scale from Raspberry Pi to 128-core workstations.
                  </p>
                </div>
                <div className='p-6 rounded-xl border border-grayscale-800 bg-gradient-to-b from-grayscale-900 to-black hover:border-primary-300/40 transition-colors'>
                  <h3 className='text-xl font-semibold mb-4'>Enterprises</h3>
                  <p className='text-grayscale-400'>
                    Weave on-prem nodes into public swarms while preserving data sovereignty.
                  </p>
                </div>
              </div>
            </div>
          </div>
        </section>

        {/* CTA Section */}
        <section className='py-24 relative z-10'>
          <div className='container mx-auto px-4'>
            <div className='max-w-4xl mx-auto text-center'>
              <div className='bg-decorative-1 rounded-2xl p-[1px]'>
                <div className='rounded-2xl bg-black/70 p-12'>
                  <h2 className='text-4xl font-bold mb-6'>Ready to Get Started?</h2>
                  <p className='text-xl text-grayscale-300 mb-8'>
                    Join the growing community of developers building the future of AI agents.
                  </p>
                  <div className='flex flex-wrap justify-center gap-4'>
                    <Button href='/docs/introduction' variant='primary' size='lg'>
                      Read the Docs
                    </Button>
                    <Button href='/download' variant='outline' size='lg'>
                      Download Now
                    </Button>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </section>
      </main>
      <Footer />
    </>
  );
}
