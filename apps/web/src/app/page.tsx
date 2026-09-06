'use client';

import { Button } from '@/components/Button';
import { Footer } from '@/components/Footer';
import MatrixBackground from '@/components/MatrixBackground';
import Navigation from '@/components/Navigation';
import { FiDownload } from 'react-icons/fi';

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
                <span className='px-3 py-1 text-xs font-medium bg-blue-500/10 text-blue-400 rounded-full'>
                  v0.1.0-alpha
                </span>
                <a
                  href='https://github.com/ecirlabs/matrix-core'
                  className='px-3 py-1 text-xs font-medium bg-gray-800 text-gray-300 rounded-full hover:bg-gray-700 transition-colors'
                >
                  Star on GitHub
                </a>
              </div>

              <h1 className='text-6xl md:text-7xl font-bold mb-6 bg-gradient-to-r from-blue-400 via-purple-400 to-blue-600 text-transparent bg-clip-text'>
                Matrix OS
              </h1>
              <p className='text-xl md:text-2xl text-gray-300 mb-8 max-w-3xl mx-auto'>
                An operating fabric that lets any device spin up, trade, and orchestrate autonomous agents—forming a
                decentralized digital civilization.
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
        <section className='py-24 bg-gradient-to-b from-black to-gray-900 relative z-10'>
          <div className='container mx-auto px-4'>
            <div className='max-w-4xl mx-auto'>
              <h2 className='text-4xl font-bold mb-12 text-center'>Core Principles</h2>
              <div className='grid grid-cols-1 md:grid-cols-2 gap-12'>
                <div>
                  <h3 className='text-2xl font-semibold mb-4'>Own Your Execution</h3>
                  <p className='text-gray-300 mb-6'>
                    One static binary per node—no hidden cloud dependencies. Intelligence runs where your data lives.
                  </p>
                  <ul className='space-y-4'>
                    <li className='flex gap-3'>
                      <svg
                        className='w-6 h-6 text-blue-400 shrink-0'
                        fill='none'
                        viewBox='0 0 24 24'
                        stroke='currentColor'
                      >
                        <path strokeLinecap='round' strokeLinejoin='round' strokeWidth={2} d='M5 13l4 4L19 7' />
                      </svg>
                      <span className='text-gray-300'>Local-first computing</span>
                    </li>
                    <li className='flex gap-3'>
                      <svg
                        className='w-6 h-6 text-blue-400 shrink-0'
                        fill='none'
                        viewBox='0 0 24 24'
                        stroke='currentColor'
                      >
                        <path strokeLinecap='round' strokeLinejoin='round' strokeWidth={2} d='M5 13l4 4L19 7' />
                      </svg>
                      <span className='text-gray-300'>No cloud dependencies</span>
                    </li>
                    <li className='flex gap-3'>
                      <svg
                        className='w-6 h-6 text-blue-400 shrink-0'
                        fill='none'
                        viewBox='0 0 24 24'
                        stroke='currentColor'
                      >
                        <path strokeLinecap='round' strokeLinejoin='round' strokeWidth={2} d='M5 13l4 4L19 7' />
                      </svg>
                      <span className='text-gray-300'>Device-first architecture</span>
                    </li>
                  </ul>
                </div>
                <div>
                  <h3 className='text-2xl font-semibold mb-4'>Privacy by Locality</h3>
                  <p className='text-gray-300 mb-6'>
                    Your data never exits the device unless you explicitly sign it. Full control over your information.
                  </p>
                  <ul className='space-y-4'>
                    <li className='flex gap-3'>
                      <svg
                        className='w-6 h-6 text-blue-400 shrink-0'
                        fill='none'
                        viewBox='0 0 24 24'
                        stroke='currentColor'
                      >
                        <path strokeLinecap='round' strokeLinejoin='round' strokeWidth={2} d='M5 13l4 4L19 7' />
                      </svg>
                      <span className='text-gray-300'>Data sovereignty</span>
                    </li>
                    <li className='flex gap-3'>
                      <svg
                        className='w-6 h-6 text-blue-400 shrink-0'
                        fill='none'
                        viewBox='0 0 24 24'
                        stroke='currentColor'
                      >
                        <path strokeLinecap='round' strokeLinejoin='round' strokeWidth={2} d='M5 13l4 4L19 7' />
                      </svg>
                      <span className='text-gray-300'>Signed data transfers</span>
                    </li>
                    <li className='flex gap-3'>
                      <svg
                        className='w-6 h-6 text-blue-400 shrink-0'
                        fill='none'
                        viewBox='0 0 24 24'
                        stroke='currentColor'
                      >
                        <path strokeLinecap='round' strokeLinejoin='round' strokeWidth={2} d='M5 13l4 4L19 7' />
                      </svg>
                      <span className='text-gray-300'>Local-only by default</span>
                    </li>
                  </ul>
                </div>
              </div>
            </div>
          </div>
        </section>

        {/* Technical Features Section */}
        <section className='py-24 bg-gradient-to-b from-gray-900 to-black relative z-10'>
          <div className='container mx-auto px-4'>
            <div className='max-w-4xl mx-auto'>
              <h2 className='text-4xl font-bold mb-12 text-center'>Technical Features</h2>
              <div className='grid grid-cols-1 md:grid-cols-2 gap-12'>
                <div>
                  <h3 className='text-2xl font-semibold mb-4'>Secure Runtime</h3>
                  <p className='text-gray-300 mb-6'>
                    Built with security-first principles using Wasmtime sandbox and advanced encryption.
                  </p>
                  <ul className='space-y-4'>
                    <li className='flex gap-3'>
                      <svg
                        className='w-6 h-6 text-purple-400 shrink-0'
                        fill='none'
                        viewBox='0 0 24 24'
                        stroke='currentColor'
                      >
                        <path strokeLinecap='round' strokeLinejoin='round' strokeWidth={2} d='M5 13l4 4L19 7' />
                      </svg>
                      <span className='text-gray-300'>Wasm sandbox environment</span>
                    </li>
                    <li className='flex gap-3'>
                      <svg
                        className='w-6 h-6 text-purple-400 shrink-0'
                        fill='none'
                        viewBox='0 0 24 24'
                        stroke='currentColor'
                      >
                        <path strokeLinecap='round' strokeLinejoin='round' strokeWidth={2} d='M5 13l4 4L19 7' />
                      </svg>
                      <span className='text-gray-300'>Resource-capped execution</span>
                    </li>
                    <li className='flex gap-3'>
                      <svg
                        className='w-6 h-6 text-purple-400 shrink-0'
                        fill='none'
                        viewBox='0 0 24 24'
                        stroke='currentColor'
                      >
                        <path strokeLinecap='round' strokeLinejoin='round' strokeWidth={2} d='M5 13l4 4L19 7' />
                      </svg>
                      <span className='text-gray-300'>Secure messaging protocols</span>
                    </li>
                  </ul>
                </div>
                <div>
                  <h3 className='text-2xl font-semibold mb-4'>Distributed Infrastructure</h3>
                  <p className='text-gray-300 mb-6'>
                    Peer-to-peer architecture with built-in support for distributed computing and storage.
                  </p>
                  <ul className='space-y-4'>
                    <li className='flex gap-3'>
                      <svg
                        className='w-6 h-6 text-purple-400 shrink-0'
                        fill='none'
                        viewBox='0 0 24 24'
                        stroke='currentColor'
                      >
                        <path strokeLinecap='round' strokeLinejoin='round' strokeWidth={2} d='M5 13l4 4L19 7' />
                      </svg>
                      <span className='text-gray-300'>libp2p + Noise/TLS 1.3</span>
                    </li>
                    <li className='flex gap-3'>
                      <svg
                        className='w-6 h-6 text-purple-400 shrink-0'
                        fill='none'
                        viewBox='0 0 24 24'
                        stroke='currentColor'
                      >
                        <path strokeLinecap='round' strokeLinejoin='round' strokeWidth={2} d='M5 13l4 4L19 7' />
                      </svg>
                      <span className='text-gray-300'>CRDT-based data fabric</span>
                    </li>
                    <li className='flex gap-3'>
                      <svg
                        className='w-6 h-6 text-purple-400 shrink-0'
                        fill='none'
                        viewBox='0 0 24 24'
                        stroke='currentColor'
                      >
                        <path strokeLinecap='round' strokeLinejoin='round' strokeWidth={2} d='M5 13l4 4L19 7' />
                      </svg>
                      <span className='text-gray-300'>NAT traversal support</span>
                    </li>
                  </ul>
                </div>
              </div>
            </div>
          </div>
        </section>

        {/* Use Cases Section */}
        <section className='py-24 bg-gradient-to-b from-black to-gray-900 relative z-10'>
          <div className='container mx-auto px-4'>
            <div className='max-w-4xl mx-auto'>
              <h2 className='text-4xl font-bold mb-12 text-center'>Use Cases</h2>
              <div className='grid grid-cols-1 md:grid-cols-3 gap-6'>
                <div className='p-6 rounded-xl border border-gray-800 bg-gradient-to-b from-gray-900 to-black'>
                  <h3 className='text-xl font-semibold mb-4'>End Users</h3>
                  <p className='text-gray-400'>
                    Run copilots that learn locally, borrow compute from friends, and never leak data.
                  </p>
                </div>
                <div className='p-6 rounded-xl border border-gray-800 bg-gradient-to-b from-gray-900 to-black'>
                  <h3 className='text-xl font-semibold mb-4'>Developers</h3>
                  <p className='text-gray-400'>
                    Publish Wasm micro-agents that scale from Raspberry Pi to 128-core workstations.
                  </p>
                </div>
                <div className='p-6 rounded-xl border border-gray-800 bg-gradient-to-b from-gray-900 to-black'>
                  <h3 className='text-xl font-semibold mb-4'>Enterprises</h3>
                  <p className='text-gray-400'>
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
              <div className='bg-gradient-to-r from-blue-600/20 via-purple-600/20 to-blue-600/20 rounded-xl p-12 border border-blue-500/20'>
                <h2 className='text-4xl font-bold mb-6'>Ready to Get Started?</h2>
                <p className='text-xl text-gray-300 mb-8'>
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
        </section>
      </main>
      <Footer />
    </>
  );
}
