'use client';

import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';
import { FiCopy } from 'react-icons/fi';
import { Toaster, toast } from 'sonner';
import { GITHUB_URL } from '@/lib/releases';

export default function MatrixOsIntroduction() {
  const copyCode = (code: string) => {
    navigator.clipboard.writeText(code);
    toast.success('Code copied to clipboard');
  };

  return (
    <>
      <Navigation />
      <Toaster position='top-right' />
      <div className='min-h-screen bg-black'>
        <div className='pt-16'>
          <div className='flex flex-col lg:flex-row'>
            <DocSidebar />

            {/* Main Content */}
            <main className='min-w-0 flex-1 p-4 sm:p-6 lg:ml-64 lg:p-8'>
              <div className='max-w-4xl mx-auto'>
                <article className='text-gray-100'>
                  {/* Hero Section */}
                  <div className='bg-gradient-to-r from-blue-500/10 via-purple-500/10 to-blue-500/10 rounded-xl p-8 mb-12 border border-blue-500/20'>
                    <h1 className='text-4xl font-bold text-white mb-4'>Welcome to Matrix OS</h1>
                    <p className='text-xl text-gray-100'>
                      Matrix OS is a revolutionary distributed operating system that empowers devices to run autonomous
                      AI agents in a secure, scalable, and decentralized environment. Built for the future of computing.
                    </p>
                  </div>

                  <h2 className='text-3xl font-bold text-white mt-8 mb-6'>What is Matrix OS?</h2>
                  <p className='text-gray-100 text-lg leading-relaxed mb-6'>
                    Matrix OS reimagines how devices interact and compute in our increasingly connected world. It&apos;s
                    not just an operating system—it&apos;s a complete platform that enables devices to become
                    intelligent,
                    autonomous participants in a decentralized network.
                  </p>

                  <div className='grid grid-cols-1 md:grid-cols-2 gap-6 my-8'>
                    <div className='bg-gray-900 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>AI-First Architecture</h3>
                      <p className='text-gray-100 leading-relaxed mb-0'>
                        Built from the ground up to support AI agents, with native support for machine learning models,
                        neural networks, and advanced decision-making systems.
                      </p>
                    </div>
                    <div className='bg-gray-900 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>Decentralized by Design</h3>
                      <p className='text-gray-100 leading-relaxed mb-0'>
                        Every device is a sovereign node, capable of independent operation while seamlessly
                        participating in the larger network.
                      </p>
                    </div>
                  </div>

                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Core Features</h2>

                  <div className='space-y-6'>
                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>Autonomous Agents</h3>
                      <p className='text-gray-100 leading-relaxed mb-4'>Create and deploy AI agents that can:</p>
                      <ul className='text-gray-100 space-y-2 list-disc pl-6'>
                        <li>Learn and adapt from their environment</li>
                        <li>Make decisions based on complex criteria</li>
                        <li>Collaborate with other agents in the network</li>
                        <li>Execute tasks without constant supervision</li>
                      </ul>
                    </div>

                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>Secure Communication</h3>
                      <p className='text-gray-100 leading-relaxed mb-4'>
                        Enterprise-grade security built into every layer:
                      </p>
                      <ul className='text-gray-100 space-y-2 list-disc pl-6'>
                        <li>End-to-end encryption for all communications</li>
                        <li>Zero-knowledge proofs for privacy-preserving operations</li>
                        <li>Secure enclaves for sensitive computations</li>
                        <li>Role-based access control system</li>
                      </ul>
                    </div>

                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>Network Elements</h3>
                      <p className='text-gray-100 leading-relaxed mb-4'>
                        Matrix OS uses a powerful system of network elements that form the building blocks of
                        distributed applications. Here&apos;s a simple example:
                      </p>
                      <div className='bg-black rounded-lg p-4'>
                        <div className='flex justify-between items-center mb-2'>
                          <span className='text-sm text-gray-400'>Network Element Example</span>
                          <button
                            onClick={() =>
                              copyCode(`import { NetworkElement } from '@matrix-os/core';

@NetworkElement({
  name: 'data-node',
  protocol: 'matrix-v1',
  capabilities: ['storage', 'compute']
})
export class DataNode {
  async syncData(peer: string) {
    // Secure data synchronization
    await this.verifyPeer(peer);
    const data = await this.fetchEncryptedData(peer);
    return this.processData(data);
  }
}`)
                            }
                            className='p-2 hover:bg-gray-800 rounded transition-colors'
                          >
                            <FiCopy className='w-4 h-4' />
                          </button>
                        </div>
                        <pre className='text-sm text-gray-300 overflow-x-auto whitespace-pre-wrap break-words'>
                          <code>
                            {`import { NetworkElement } from '@matrix-os/core';

@NetworkElement({
  name: 'data-node',
  protocol: 'matrix-v1',
  capabilities: ['storage', 'compute']
})
export class DataNode {
  async syncData(peer: string) {
    // Secure data synchronization
    await this.verifyPeer(peer);
    const data = await this.fetchEncryptedData(peer);
    return this.processData(data);
  }
}`}
                          </code>
                        </pre>
                      </div>
                    </div>
                  </div>

                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Getting Started</h2>
                  <p className='text-gray-100 text-lg leading-relaxed mb-4'>
                    Ready to dive in? Follow these steps to begin your journey with Matrix OS:
                  </p>
                  <ol className='text-gray-100 space-y-2 list-decimal pl-6'>
                    <li className='leading-relaxed'>
                      Start with our{' '}
                      <a href='/docs/installation' className='text-blue-400 hover:text-blue-300 underline'>
                        Installation Guide
                      </a>{' '}
                      to set up Matrix OS on your device
                    </li>
                    <li className='leading-relaxed'>
                      Follow the{' '}
                      <a href='/docs/quickstart' className='text-blue-400 hover:text-blue-300 underline'>
                        Quick Start Tutorial
                      </a>{' '}
                      to create your first agent
                    </li>
                  </ol>

                  <div className='bg-blue-500/10 rounded-xl p-6 mt-8 border border-blue-500/20'>
                    <h3 className='text-xl font-bold text-white mb-3'>Join the Community</h3>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      Matrix OS is backed by a vibrant community of developers, researchers, and enthusiasts. Get
                      involved:
                    </p>
                    <ul className='text-gray-100 space-y-2 list-disc pl-6 mb-0'>
                      <li>
                        <a
                          href={GITHUB_URL}
                          className='text-blue-400 hover:text-blue-300 underline'
                        >
                          Contribute on GitHub
                        </a>
                      </li>
                    </ul>
                  </div>
                </article>
              </div>
            </main>
          </div>
        </div>
      </div>
    </>
  );
}
