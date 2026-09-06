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
                      How the shipped Matrix compute marketplace fits together: native MATRIX, the coin of the Matrix
                      L1 that you earn and spend, the fast consensus chain that agrees the ledger, the pluggable LLM
                      inference backends that do the work, and the bridged wrapped ERC-20 mirror for exchange listing.
                    </p>
                  </div>

                  {/* Token */}
                  <h2 className='text-3xl font-bold text-white mt-8 mb-6'>Native MATRIX, the settlement coin</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      MATRIX is the native coin of the Matrix L1 consensus chain and the single source of truth for
                      balances and supply. Buyers pay in it and providers earn it, and every compute and inference
                      settlement transacts in native MATRIX through consensus. It is one currency end to end, not a
                      separate notion of credits.
                    </p>
                    <ul className='text-gray-100 space-y-2 list-disc pl-6'>
                      <li>Symbol <strong>MATRIX</strong>, 9 native decimals on the Matrix L1</li>
                      <li>Supply capped at 1,000,000,000 whole MATRIX; balances live on the consensus ledger</li>
                      <li>A genesis allocation plus a genesis-funded reward pool back earnings as capped, supply-tracked issuance (no unlimited minting)</li>
                      <li>All settlement is applied deterministically once a consensus block commits</li>
                    </ul>
                  </div>

                  {/* Bridge */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>The bridged wrapped ERC-20 mirror</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      The ERC-20 is not the settlement token. It is wMATRIX, a wrapped mirror produced by a
                      lock-and-mint bridge so native MATRIX can be represented on Ethereum, for example for a future
                      exchange listing. The wrapped supply always equals the native MATRIX locked on the L1, so it
                      stays backed 1:1.
                    </p>
                    <ul className='text-gray-100 space-y-2 list-disc pl-6'>
                      <li><strong>Lock to mint</strong> — native MATRIX is locked on the L1, and a threshold of validator secp256k1 attestations authorizes minting the matching wMATRIX</li>
                      <li><strong>Burn to unlock</strong> — burning wMATRIX emits an on-chain Burned event; the bridge decodes that event into an unlock authorization (keyed by transaction hash and log index) and releases the escrowed native MATRIX back on the L1 exactly once per event. Today the event is fed in by an operator or the end-to-end test rather than a always-on log watcher.</li>
                      <li>Native has 9 decimals and wMATRIX has 18, so one native base unit equals 1e9 wrapped base units and the 1,000,000,000 MATRIX cap maps to the same money on both sides</li>
                      <li>Runs against local and test networks only; it is not deployed to any public Ethereum network</li>
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
                      Consensus is the authoritative, globally agreed ledger for marketplace settlement. Both the
                      compute marketplace and the LLM inference marketplace settle buyer to provider through consensus
                      in native MATRIX by default. The earlier per-node pairwise settlement and the direct token-chain
                      transfer remain as separate, non-default primitives for point-to-point transfers, but the
                      marketplace flows no longer use them.
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
                      reserved with an up-front affordability check on the reserved price, the backend runs the request,
                      and the job settles buyer-to-provider in native MATRIX through the consensus ledger. The charge is scaled
                      by the provider&apos;s price per unit and capped at the reserved amount, and the job is reported
                      COMPLETED only once that settlement commits and applies on the ledger.
                    </p>
                  </div>

                  {/* Console */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Matrix Console</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      Matrix Console is a Tauri + React + Vite desktop app that connects to a local matrixd node to
                      observe and control the marketplace from one window: node connection, providers, jobs, the native
                      MATRIX wallet and settled ledger, the consensus chain, and LLM inference.
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
