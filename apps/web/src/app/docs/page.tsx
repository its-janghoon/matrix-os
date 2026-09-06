'use client';

import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';

export default function Docs() {
  return (
    <>
      <Navigation />
      <div className='min-h-screen bg-black'>
        <div className='pt-16'>
          {/* Documentation Layout */}
          <div className='flex'>
            <DocSidebar />

            {/* Main Content Area */}
            <main className='flex-1 ml-64 p-8'>
              <div className='max-w-4xl mx-auto'>
                <div className='bg-gradient-to-r from-blue-500/10 via-purple-500/10 to-blue-500/10 rounded-xl p-8 mb-8 border border-blue-500/20'>
                  <h1 className='text-4xl font-bold text-white mb-4'>Matrix OS Documentation</h1>
                  <p className='text-xl text-gray-300'>
                    Create distributed systems with Matrix OS. Explore the protocol, networking, and advanced features.
                  </p>
                </div>

                {/* Quick Links */}
                <div className='grid grid-cols-2 gap-6 mb-12'>
                  <div className='bg-gray-900 rounded-xl p-6 border border-gray-800'>
                    <h2 className='text-xl font-semibold text-white mb-4'>Quick Start</h2>
                    <p className='text-gray-300 mb-4'>Get up and running in minutes with our quick start guide.</p>
                    <a href='/docs/quickstart' className='text-blue-400 hover:text-blue-300'>
                      Learn more →
                    </a>
                  </div>
                </div>

                {/* Featured Documentation */}
                <div className='space-y-8'>
                  <section>
                    <h2 className='text-2xl font-bold text-white mb-4'>Getting Started</h2>
                    <div className='bg-gray-900 rounded-xl p-6 border border-gray-800'>
                      <ul className='space-y-4'>
                        <li>
                          <a href='/docs/introduction' className='block'>
                            <h3 className='text-lg font-semibold text-white mb-2'>Introduction</h3>
                            <p className='text-gray-300'>Learn about the core concepts and philosophy.</p>
                          </a>
                        </li>
                        <li>
                          <a href='/docs/installation' className='block'>
                            <h3 className='text-lg font-semibold text-white mb-2'>Installation</h3>
                            <p className='text-gray-300'>Step-by-step guide to installing and configuring.</p>
                          </a>
                        </li>
                        <li>
                          <a href='/docs/architecture' className='block'>
                            <h3 className='text-lg font-semibold text-white mb-2'>Architecture</h3>
                            <p className='text-gray-300'>Understand the system architecture and design.</p>
                          </a>
                        </li>
                      </ul>
                    </div>
                  </section>
                </div>
              </div>
            </main>
          </div>
        </div>
      </div>
    </>
  );
}
