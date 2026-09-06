'use client';

import { Button } from '@/components/Button';
import { Footer } from '@/components/Footer';
import MatrixBackground from '@/components/MatrixBackground';
import Navigation from '@/components/Navigation';
import {
  FiCheck,
  FiCpu,
  FiDownload,
  FiGlobe,
  FiLayers,
  FiMonitor,
  FiServer,
  FiShoppingCart,
  FiZap,
} from 'react-icons/fi';

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
                  Idle machines announce capacity and pricing across the P2P network, and buyers discover them and submit
                  signed compute jobs. Payment settles from buyer to provider through cryptographically signed token
                  transfers recorded on an append-only, hash-chained transaction log.
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
                    Leave a machine running and register it as a provider. It signs and announces available capacity and
                    a price per compute unit across the P2P gossip network, then receives signed token transfers each
                    time a job it served completes.
                  </p>
                  <ul className='space-y-4'>
                    <CheckItem>Sign and announce idle capacity and a price per unit over P2P gossip</CheckItem>
                    <CheckItem>Capacity is reserved when a buyer&apos;s job is accepted</CheckItem>
                    <CheckItem>Signed token transfers settle to your account balance on job completion</CheckItem>
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
                    Discover providers across the P2P network and submit a signed job for the LLM compute and API
                    responses you need. The price is quoted up front from the provider&apos;s rate, and tokens only move
                    through a verified signed transfer once the work is done.
                  </p>
                  <ul className='space-y-4'>
                    <CheckItem>Discover local and remote providers over the P2P network</CheckItem>
                    <CheckItem>Submit signed jobs priced as units &times; provider rate</CheckItem>
                    <CheckItem>Balance is checked up front; a signed transfer charges you on completion</CheckItem>
                    <CheckItem>Interact from outside the node through the matrix.market.v1 gRPC API</CheckItem>
                  </ul>
                </div>
              </div>

              {/* How settlement works */}
              <div className='mt-6 p-8 rounded-2xl border border-grayscale-800 bg-black'>
                <div className='flex items-center gap-3 mb-4'>
                  <FiCpu className='w-5 h-5 text-primary-300' />
                  <h3 className='text-xl font-semibold'>How settlement works</h3>
                </div>
                <p className='text-grayscale-300'>
                  Settlement is cryptographic and shipped in the Matrix core. Each account is an ed25519 keypair, and
                  the earning currency is native <span className='text-white font-medium'>MATRIX</span>, the coin of the
                  Matrix L1 consensus chain. Tokens move only through transfers that are signed by the sender. Settlement routed
                  through the fast consensus chain is verified, ordered, and applied in one globally agreed ledger
                  before a balance can change. Nonces prevent replay and the committed chain can be validated end to end
                  to detect tampering, so a balance changes only through a valid signed transaction that the validator
                  set has agreed on.
                </p>
                <p className='text-grayscale-300 mt-4'>
                  Providers sign and announce their capacity and pricing over the libp2p gossip network, and remote
                  nodes discover them into a local registry. When a buyer pays a provider, the signed transfer is
                  proposed, voted on, and committed once a super-majority of validators agree, so every node applies
                  the same ordered ledger rather than a private per-node balance. An external gRPC API, the
                  {' '}<span className='text-white font-medium'>matrix.market.v1</span> MarketService, lets buyers and
                  providers register, discover local and remote providers, submit and manage jobs, read balances and
                  transactions, and broadcast signed transfers from outside the node.
                </p>
              </div>
            </div>
          </div>
        </section>

        {/* MATRIX Token Section */}
        <section id='token' className='py-24 bg-gradient-to-b from-black to-grayscale-900 relative z-10 scroll-mt-16'>
          <div className='container mx-auto px-4'>
            <div className='max-w-5xl mx-auto'>
              <div className='text-center mb-12'>
                <span className='inline-block px-3 py-1 text-xs font-medium bg-accent-200/10 text-accent-200 rounded-full border border-accent-200/20 mb-4'>
                  MATRIX Token
                </span>
                <h2 className='text-4xl font-bold mb-4'>The Native Coin You Earn and Spend for Compute</h2>
                <p className='text-xl text-grayscale-300 max-w-3xl mx-auto'>
                  MATRIX is the native coin of the Matrix L1 consensus chain, and the single source of truth for
                  balances and supply. Every compute job and every LLM inference request settles in native MATRIX
                  through consensus. A bridged wrapped ERC-20 mirror (wMATRIX) lets the same coin be represented on
                  Ethereum for a future exchange listing, backed 1:1 by native MATRIX locked on the L1.
                </p>
              </div>

              <div className='grid grid-cols-2 md:grid-cols-4 gap-4 mb-8'>
                <div className='p-6 rounded-2xl border border-grayscale-800 bg-black text-center'>
                  <div className='text-sm text-grayscale-500 mb-1'>Symbol</div>
                  <div className='text-lg font-semibold'>MATRIX</div>
                </div>
                <div className='p-6 rounded-2xl border border-grayscale-800 bg-black text-center'>
                  <div className='text-sm text-grayscale-500 mb-1'>Native decimals</div>
                  <div className='text-lg font-semibold'>9</div>
                </div>
                <div className='p-6 rounded-2xl border border-grayscale-800 bg-black text-center'>
                  <div className='text-sm text-grayscale-500 mb-1'>Wrapped decimals</div>
                  <div className='text-lg font-semibold'>18</div>
                </div>
                <div className='p-6 rounded-2xl border border-grayscale-800 bg-black text-center'>
                  <div className='text-sm text-grayscale-500 mb-1'>Max supply</div>
                  <div className='text-lg font-semibold'>1,000,000,000</div>
                </div>
              </div>

              <div className='p-8 rounded-2xl border border-grayscale-800 bg-gradient-to-b from-grayscale-900 to-black'>
                <div className='flex items-center gap-3 mb-4'>
                  <div className='h-11 w-11 rounded-xl bg-accent-200/10 border border-accent-200/20 flex items-center justify-center'>
                    <FiLayers className='w-5 h-5 text-accent-200' />
                  </div>
                  <h3 className='text-2xl font-semibold'>Native first, one currency end to end</h3>
                </div>
                <p className='text-grayscale-300 mb-6'>
                  Native MATRIX lives on our own fast leader-based BFT L1. It has 9 decimals, and its supply is capped
                  at 1,000,000,000 whole MATRIX. A genesis allocation plus a genesis-funded reward pool back provider
                  earnings as capped, supply-tracked issuance, not unlimited minting. Every balance change is a
                  committed consensus fact, so buyers and providers transact in one coin from end to end.
                </p>
                <ul className='space-y-4'>
                  <CheckItem>Native coin of the Matrix L1: the single source of truth for balances and supply</CheckItem>
                  <CheckItem>9 native decimals, capped at 1,000,000,000 whole MATRIX (no unlimited minting)</CheckItem>
                  <CheckItem>All compute and inference settlement is native MATRIX applied through consensus</CheckItem>
                  <CheckItem>The same 1,000,000,000 MATRIX maps to the 18-decimal wrapped mirror by an exact 1e9 factor</CheckItem>
                </ul>
              </div>

              {/* Bridge subsection */}
              <div className='mt-6 p-8 rounded-2xl border border-grayscale-800 bg-black'>
                <div className='flex items-center gap-3 mb-4'>
                  <div className='h-11 w-11 rounded-xl bg-accent-200/10 border border-accent-200/20 flex items-center justify-center'>
                    <FiLayers className='w-5 h-5 text-accent-200' />
                  </div>
                  <h3 className='text-2xl font-semibold'>The bridge: a wrapped mirror for listing</h3>
                </div>
                <p className='text-grayscale-300 mb-6'>
                  The ERC-20 is not the settlement token. It is wMATRIX, a wrapped mirror produced by a lock-and-mint
                  bridge so native MATRIX can be represented on Ethereum, for example for a future exchange listing.
                  Outstanding wrapped supply always equals the native MATRIX locked on the L1, so the mirror stays
                  backed 1:1. Native has 9 decimals and the wrapped token has 18, so one native base unit equals 1e9
                  wrapped base units, and the native cap of 1,000,000,000 MATRIX maps to the same amount on the wrapped
                  side. The bridge runs against local and test networks only. It is NOT deployed to any public Ethereum
                  network.
                </p>
                <ul className='space-y-4'>
                  <CheckItem>Native lock to wrapped mint: minting requires a threshold of validator secp256k1 attestations that native was locked</CheckItem>
                  <CheckItem>Wrapped burn to native unlock: a burn emits an on-chain Burned event that is decoded into an authorization to release the escrowed native MATRIX, applied exactly once per event</CheckItem>
                  <CheckItem>Backed 1:1 by locked native, reconciled through the exact 1e9 conversion factor</CheckItem>
                  <CheckItem>Local and test networks only, not deployed to a public Ethereum network</CheckItem>
                </ul>
              </div>
            </div>
          </div>
        </section>

        {/* Consensus Section */}
        <section
          id='consensus'
          className='py-24 bg-gradient-to-b from-grayscale-900 to-black relative z-10 scroll-mt-16'
        >
          <div className='container mx-auto px-4'>
            <div className='max-w-5xl mx-auto'>
              <div className='text-center mb-12'>
                <span className='inline-block px-3 py-1 text-xs font-medium bg-primary/10 text-primary-300 rounded-full border border-primary/20 mb-4'>
                  Global Consensus
                </span>
                <h2 className='text-4xl font-bold mb-4'>One Fast, Globally Agreed Ledger</h2>
                <p className='text-xl text-grayscale-300 max-w-3xl mx-auto'>
                  Every node applies the same ordered ledger. Matrix reaches agreement with a fast, leader-based BFT
                  protocol built for speed first, not proof-of-work, so a payment is final in a single voting round.
                </p>
              </div>

              <div className='grid grid-cols-1 md:grid-cols-2 gap-6'>
                <div className='p-8 rounded-2xl border border-grayscale-800 bg-black hover:border-primary-300/40 transition-colors'>
                  <div className='flex items-center gap-3 mb-4'>
                    <div className='h-11 w-11 rounded-xl bg-primary/10 border border-primary/20 flex items-center justify-center'>
                      <FiZap className='w-5 h-5 text-primary-300' />
                    </div>
                    <h3 className='text-2xl font-semibold'>Speed-first BFT</h3>
                  </div>
                  <p className='text-grayscale-300 mb-6'>
                    A fixed ed25519 validator set takes turns as leader in round-robin order. The leader proposes a
                    block, validators vote, and the block commits immediately once more than two-thirds agree. That
                    single-round fast path keeps settlement quick while tolerating faulty or offline validators.
                  </p>
                  <ul className='space-y-4'>
                    <CheckItem>Leader-based BFT over a fixed validator set (no mining, no proof-of-work)</CheckItem>
                    <CheckItem>Round-robin leader rotation with timeouts so a stalled leader is replaced</CheckItem>
                    <CheckItem>Commit on a greater-than-two-thirds quorum in a single voting round</CheckItem>
                  </ul>
                </div>

                <div className='p-8 rounded-2xl border border-grayscale-800 bg-black hover:border-primary-300/40 transition-colors'>
                  <div className='flex items-center gap-3 mb-4'>
                    <div className='h-11 w-11 rounded-xl bg-secondary/10 border border-secondary/20 flex items-center justify-center'>
                      <FiGlobe className='w-5 h-5 text-secondary-300' />
                    </div>
                    <h3 className='text-2xl font-semibold'>Globally agreed order</h3>
                  </div>
                  <p className='text-grayscale-300 mb-6'>
                    Committed blocks are SHA-256 hash-linked into a single chain that every node shares. Because all
                    nodes apply the same committed order, a balance is a network-wide fact instead of a private
                    per-node number, and the chain can be validated end to end to detect tampering.
                  </p>
                  <ul className='space-y-4'>
                    <CheckItem>Hash-linked committed block chain shared by every node</CheckItem>
                    <CheckItem>Deterministic ledger application, so all honest nodes converge</CheckItem>
                    <CheckItem>Authoritative agreed ledger where both compute and inference settle by default</CheckItem>
                  </ul>
                </div>
              </div>
            </div>
          </div>
        </section>

        {/* Inference Section */}
        <section
          id='inference'
          className='py-24 bg-gradient-to-b from-black to-grayscale-900 relative z-10 scroll-mt-16'
        >
          <div className='container mx-auto px-4'>
            <div className='max-w-5xl mx-auto'>
              <div className='text-center mb-12'>
                <span className='inline-block px-3 py-1 text-xs font-medium bg-secondary/10 text-secondary-300 rounded-full border border-secondary/20 mb-4'>
                  LLM Inference
                </span>
                <h2 className='text-4xl font-bold mb-4'>Two Ways to Contribute Real LLM Compute</h2>
                <p className='text-xl text-grayscale-300 max-w-3xl mx-auto'>
                  Inference is a pluggable backend. Contribute by running a model locally, or by proxying requests to a
                  provider API you hold a key for. Either way the job settles in native MATRIX through consensus, priced
                  per unit and capped at the amount reserved up front.
                </p>
              </div>

              <div className='grid grid-cols-1 md:grid-cols-2 gap-6'>
                <div className='p-8 rounded-2xl border border-grayscale-800 bg-gradient-to-b from-grayscale-900 to-black hover:border-primary-300/40 transition-colors'>
                  <div className='flex items-center gap-3 mb-4'>
                    <div className='h-11 w-11 rounded-xl bg-primary/10 border border-primary/20 flex items-center justify-center'>
                      <FiServer className='w-5 h-5 text-primary-300' />
                    </div>
                    <h3 className='text-2xl font-semibold'>Local runner</h3>
                  </div>
                  <p className='text-grayscale-300 mb-6'>
                    Point the node at a local model server that speaks the Ollama-style chat API and serve inference
                    straight from your own hardware. A built-in echo backend lets you run the whole flow without a GPU
                    for testing.
                  </p>
                  <ul className='space-y-4'>
                    <CheckItem>Ollama-style local HTTP backend for on-device models</CheckItem>
                    <CheckItem>GPU-free echo backend for development and testing</CheckItem>
                    <CheckItem>Earn native MATRIX for the tokens your machine actually serves</CheckItem>
                  </ul>
                </div>

                <div className='p-8 rounded-2xl border border-grayscale-800 bg-gradient-to-b from-grayscale-900 to-black hover:border-secondary-300/40 transition-colors'>
                  <div className='flex items-center gap-3 mb-4'>
                    <div className='h-11 w-11 rounded-xl bg-secondary/10 border border-secondary/20 flex items-center justify-center'>
                      <FiGlobe className='w-5 h-5 text-secondary-300' />
                    </div>
                    <h3 className='text-2xl font-semibold'>Provider-API proxy</h3>
                  </div>
                  <p className='text-grayscale-300 mb-6'>
                    Already pay for an OpenAI-compatible API? Contribute by proxying network requests through it. The
                    node forwards the chat completion and the API key is read from an environment variable, never
                    hardcoded or committed.
                  </p>
                  <ul className='space-y-4'>
                    <CheckItem>OpenAI-compatible chat completions backend</CheckItem>
                    <CheckItem>API key read from an environment variable, never stored in code</CheckItem>
                    <CheckItem>Jobs settle in native MATRIX through consensus, priced per unit and capped at the reservation</CheckItem>
                  </ul>
                </div>
              </div>

              <div className='mt-6 p-8 rounded-2xl border border-grayscale-800 bg-black'>
                <div className='flex items-center gap-3 mb-4'>
                  <FiCpu className='w-5 h-5 text-primary-300' />
                  <h3 className='text-xl font-semibold'>Metered and settled through consensus</h3>
                </div>
                <p className='text-grayscale-300'>
                  A buyer submits a job over the <span className='text-white font-medium'>matrix.inference.v1</span>{' '}
                  InferenceService, capacity is reserved with an up-front affordability check on the reserved price, and
                  the selected backend runs the request. The buyer-to-provider transfer is then settled in native
                  MATRIX through the consensus ledger, scaled by the provider&apos;s price per unit and capped at the reserved
                  amount, and the job is reported complete only once that settlement commits and applies, so a buyer is
                  charged exactly once for work that really happened and never more than it reserved.
                </p>
              </div>
            </div>
          </div>
        </section>

        {/* Console Section */}
        <section id='console' className='py-24 bg-gradient-to-b from-grayscale-900 to-black relative z-10 scroll-mt-16'>
          <div className='container mx-auto px-4'>
            <div className='max-w-5xl mx-auto'>
              <div className='text-center mb-12'>
                <span className='inline-block px-3 py-1 text-xs font-medium bg-secondary/10 text-secondary-300 rounded-full border border-secondary/20 mb-4'>
                  Matrix Console
                </span>
                <h2 className='text-4xl font-bold mb-4'>A Desktop App to Drive Your Node</h2>
                <p className='text-xl text-grayscale-300 max-w-3xl mx-auto'>
                  Matrix Console is a Tauri + React + Vite desktop app that connects to a local matrixd node to observe
                  and control the marketplace: providers, jobs, the MATRIX wallet, the consensus chain, and LLM
                  inference, all in one window.
                </p>
              </div>

              <div className='p-8 rounded-2xl border border-grayscale-800 bg-black'>
                <div className='flex items-center gap-3 mb-6'>
                  <div className='h-11 w-11 rounded-xl bg-secondary/10 border border-secondary/20 flex items-center justify-center'>
                    <FiMonitor className='w-5 h-5 text-secondary-300' />
                  </div>
                  <h3 className='text-2xl font-semibold'>One window for the whole node</h3>
                </div>
                <div className='grid grid-cols-1 md:grid-cols-2 gap-x-12 gap-y-4'>
                  <ul className='space-y-4'>
                    <CheckItem>Connect to a local matrixd node, or explore a built-in demo mode</CheckItem>
                    <CheckItem>Register provider capacity and browse local and remote providers</CheckItem>
                    <CheckItem>Submit, complete, and cancel compute jobs</CheckItem>
                  </ul>
                  <ul className='space-y-4'>
                    <CheckItem>Watch the MATRIX wallet balance and the settled ledger</CheckItem>
                    <CheckItem>Follow the consensus chain: committed height, round leader, and quorum</CheckItem>
                    <CheckItem>Run an inference job and see the completion and its settlement</CheckItem>
                  </ul>
                </div>
                <p className='text-grayscale-400 mt-6 text-sm'>
                  The React frontend builds and runs everywhere. Packaging the native desktop bundle additionally needs
                  the WebKitGTK/libsoup system libraries; see the console README for the connection configuration and
                  the documented native-build note.
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
