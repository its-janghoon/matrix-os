'use client';

import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';

export default function ComputeMarketplaceDocs() {
  return (
    <>
      <Navigation />
      <div className='min-h-screen bg-black'>
        <div className='pt-16'>
          <div className='flex'>
            <DocSidebar />

            {/* Main Content */}
            <main className='flex-1 ml-64 p-8'>
              <div className='max-w-4xl mx-auto'>
                <article className='text-gray-100'>
                  {/* Hero Section */}
                  <div className='bg-gradient-to-r from-blue-500/10 via-purple-500/10 to-blue-500/10 rounded-xl p-8 mb-12 border border-blue-500/20'>
                    <h1 className='text-4xl font-bold text-white mb-4'>Compute Marketplace</h1>
                    <p className='text-xl text-gray-100'>
                      How the shipped Matrix compute marketplace fits together: the MATRIX ERC-20 token you earn and
                      spend, the fast consensus chain that agrees the ledger, and the pluggable LLM inference backends
                      that do the work.
                    </p>
                  </div>

                  {/* Token */}
                  <h2 className='text-3xl font-bold text-white mt-8 mb-6'>MATRIX, the settlement token</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      MATRIX is the settlement and earning currency of the marketplace. Buyers pay in it and providers
                      earn it. It is a standards-compliant ERC-20 token, so any wallet or exchange that supports ERC-20
                      can integrate it.
                    </p>
                    <ul className='text-gray-100 space-y-2 list-disc pl-6'>
                      <li>Name <strong>Matrix Compute Token</strong>, symbol <strong>MATRIX</strong>, 18 decimals</li>
                      <li>Inherits OpenZeppelin&apos;s audited ERC20, plus ERC20Permit for gasless approvals</li>
                      <li>Owner-only mint capped at a fixed maximum supply of 1,000,000,000 MATRIX</li>
                      <li>Ships as a Hardhat project with a full test suite and a local deploy script</li>
                    </ul>
                  </div>

                  {/* Consensus */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Fast consensus chain</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      Payments are ordered and finalized by a fast, leader-based BFT protocol built for speed rather
                      than proof-of-work. A fixed ed25519 validator set takes turns as leader in round-robin order.
                    </p>
                    <ul className='text-gray-100 space-y-2 list-disc pl-6'>
                      <li>The leader proposes a block and validators vote on it</li>
                      <li>A block commits in a single round once more than two-thirds of validators agree</li>
                      <li>Round timeouts rotate the leader if one stalls, so progress continues</li>
                      <li>Committed blocks are SHA-256 hash-linked into one chain every node shares</li>
                      <li>All nodes apply the same ordered ledger, so a balance is a network-wide fact</li>
                    </ul>
                    <p className='text-gray-100 leading-relaxed mt-4 mb-0'>
                      This replaces the earlier per-node pairwise settlement with a single, globally agreed ledger.
                    </p>
                  </div>

                  {/* Inference */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>LLM inference backends</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      Inference is a pluggable backend, so a provider can contribute compute in two ways:
                    </p>
                    <ul className='text-gray-100 space-y-2 list-disc pl-6'>
                      <li>
                        <strong>Local runner</strong> — serve a model from your own hardware through an Ollama-style
                        local HTTP API. A GPU-free echo backend runs the whole flow for testing.
                      </li>
                      <li>
                        <strong>Provider-API proxy</strong> — proxy requests through an OpenAI-compatible API. The key
                        is read from an environment variable and never hardcoded or committed.
                      </li>
                    </ul>
                    <p className='text-gray-100 leading-relaxed mt-4 mb-0'>
                      Jobs are submitted over the <code>matrix.inference.v1</code> InferenceService, capacity is
                      reserved with an up-front affordability check, the backend runs the request, and the job settles
                      buyer-to-provider in MATRIX through the consensus ledger for the units actually reported.
                    </p>
                  </div>

                  {/* Console */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Matrix Console</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      Matrix Console is a Tauri + React + Vite desktop app that connects to a local matrixd node to
                      observe and control the marketplace from one window: node connection, providers, jobs, the MATRIX
                      wallet and settled ledger, the consensus chain, and LLM inference.
                    </p>
                    <p className='text-gray-100 leading-relaxed mb-0'>
                      The React frontend builds and runs everywhere. Packaging the native desktop bundle additionally
                      needs the WebKitGTK/libsoup system libraries.
                    </p>
                  </div>

                  {/* Next Steps */}
                  <div className='bg-blue-500/10 rounded-xl p-6 mt-8 border border-blue-500/20'>
                    <h2 className='text-2xl font-bold text-white mb-4'>Next Steps</h2>
                    <ul className='text-gray-100 space-y-3 list-disc pl-6 mb-0'>
                      <li>
                        <a href='/docs/architecture' className='text-blue-400 hover:text-blue-300 underline'>
                          See how the marketplace fits the Matrix OS architecture
                        </a>
                      </li>
                      <li>
                        <a href='/docs/matrix-protocol' className='text-blue-400 hover:text-blue-300 underline'>
                          Learn about the Matrix Protocol
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
