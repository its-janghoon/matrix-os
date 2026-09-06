'use client';

import { Button } from '@/components/Button';
import { Footer } from '@/components/Footer';
import HeroBackdrop from '@/components/HeroBackdrop';
import Navigation from '@/components/Navigation';
import { Card, CheckItem, Eyebrow, IconBadge, Section } from '@/components/marketing';
import {
  FiArrowRight,
  FiCpu,
  FiDownload,
  FiGlobe,
  FiLayers,
  FiMonitor,
  FiServer,
  FiShoppingCart,
  FiTerminal,
  FiZap,
} from 'react-icons/fi';

// Original placeholder tiles for the ecosystem band (NOT real third-party logos).
const ecosystemTiles = [
  'libp2p Gossip',
  'ed25519 Wallets',
  'BFT Consensus',
  'gRPC Market API',
  'Ollama Runner',
  'OpenAI Proxy',
  'wMATRIX Bridge',
  'Matrix Console',
  'Hash-Chained Ledger',
  'Wasm Sandbox',
  'matrix CLI',
  'CRDT Data Fabric',
];

export default function Home() {
  return (
    <>
      <Navigation />

      <main className='min-h-screen bg-black text-white'>
        {/* Hero */}
        <div className='relative flex min-h-screen items-center overflow-hidden pt-16'>
          <HeroBackdrop />

          <div className='relative z-10 mx-auto w-full max-w-7xl px-4 py-24 sm:px-6 lg:px-8'>
            <div className='mx-auto max-w-4xl text-center animate-fade-up'>
              <div className='mb-6 flex flex-wrap items-center justify-center gap-2'>
                <Eyebrow>v0.1.0-alpha</Eyebrow>
                <a
                  href='https://github.com/ecirlabs/matrix-core'
                  className='inline-flex items-center gap-1.5 rounded-full border border-white/10 bg-white/[0.04] px-3 py-1 text-xs font-medium text-grayscale-300 transition-colors hover:border-white/20 hover:text-white'
                >
                  Star on GitHub
                  <FiArrowRight className='h-3 w-3' />
                </a>
              </div>

              <h1 className='mb-6 text-5xl font-bold leading-[1.05] tracking-tight sm:text-6xl md:text-7xl'>
                The peer-to-peer market for{' '}
                <span className='bg-decorative-1 bg-clip-text text-transparent'>compute and inference</span>
              </h1>
              <p className='mx-auto mb-10 max-w-2xl text-lg text-grayscale-300 sm:text-xl'>
                Matrix turns idle machines into a global marketplace. Buyers pay for LLM compute and API responses in
                native MATRIX, settled through a fast leader-based BFT Layer 1, so every balance is one globally agreed
                fact, not a private per-node number.
              </p>

              <div className='flex flex-wrap items-center justify-center gap-4'>
                <Button href='/download' variant='primary' size='lg'>
                  <FiDownload className='mr-2 h-5 w-5' />
                  Download
                </Button>
                <Button href='/docs/introduction' variant='secondary' size='lg'>
                  Read the docs
                  <FiArrowRight className='ml-2 h-4 w-4' />
                </Button>
              </div>

              {/* Hero stat strip */}
              <div className='mx-auto mt-16 grid max-w-3xl grid-cols-2 gap-4 sm:grid-cols-4'>
                {[
                  { k: 'Settlement', v: 'Native MATRIX' },
                  { k: 'Consensus', v: 'Fast BFT L1' },
                  { k: 'Finality', v: 'One round' },
                  { k: 'Max supply', v: '1,000,000,000' },
                ].map((s) => (
                  <div
                    key={s.k}
                    className='rounded-2xl border border-white/10 bg-white/[0.03] px-4 py-4 backdrop-blur-sm'
                  >
                    <div className='text-[11px] uppercase tracking-[0.12em] text-grayscale-500'>{s.k}</div>
                    <div className='mt-1 text-sm font-semibold text-white'>{s.v}</div>
                  </div>
                ))}
              </div>
            </div>
          </div>
        </div>

        {/* Principles band */}
        <Section className='bg-section-glow'>
          <div className='mx-auto max-w-3xl text-center'>
            <Eyebrow>Design principles</Eyebrow>
            <h2 className='mt-4 text-3xl font-bold tracking-tight sm:text-4xl'>Built to run where your data lives</h2>
          </div>
          <div className='mt-14 grid grid-cols-1 gap-6 md:grid-cols-2'>
            <Card>
              <IconBadge icon={FiServer} />
              <h3 className='mt-5 text-xl font-semibold'>Own your execution</h3>
              <p className='mt-3 text-grayscale-300'>
                One static binary per node, with no hidden cloud dependencies. Intelligence runs where your data lives.
              </p>
              <ul className='mt-6 space-y-3'>
                <CheckItem>Local-first computing</CheckItem>
                <CheckItem>No cloud dependencies</CheckItem>
                <CheckItem>Device-first architecture</CheckItem>
              </ul>
            </Card>
            <Card>
              <IconBadge icon={FiGlobe} tone='secondary' />
              <h3 className='mt-5 text-xl font-semibold'>Privacy by locality</h3>
              <p className='mt-3 text-grayscale-300'>
                Your data never exits the device unless you explicitly sign it. You keep full control over your
                information.
              </p>
              <ul className='mt-6 space-y-3'>
                <CheckItem>Data sovereignty</CheckItem>
                <CheckItem>Signed data transfers</CheckItem>
                <CheckItem>Local-only by default</CheckItem>
              </ul>
            </Card>
          </div>
        </Section>

        {/* Marketplace */}
        <Section id='marketplace'>
          <div className='mx-auto max-w-3xl text-center'>
            <Eyebrow tone='secondary'>Compute marketplace</Eyebrow>
            <h2 className='mt-4 text-3xl font-bold tracking-tight sm:text-4xl'>A peer-to-peer market for compute</h2>
            <p className='mt-4 text-lg text-grayscale-300'>
              Idle machines announce capacity and pricing across the P2P network, and buyers discover them and submit
              signed compute jobs. Payment settles from buyer to provider through cryptographically signed token
              transfers recorded on an append-only, hash-chained transaction log.
            </p>
          </div>

          <div className='mt-14 grid grid-cols-1 gap-6 md:grid-cols-2'>
            <Card>
              <div className='flex items-center gap-3'>
                <IconBadge icon={FiServer} />
                <h3 className='text-xl font-semibold'>Providers earn</h3>
              </div>
              <p className='mt-4 text-grayscale-300'>
                Leave a machine running and register it as a provider. It signs and announces available capacity and a
                price per compute unit across the P2P gossip network, then receives signed token transfers each time a
                job it served completes.
              </p>
              <ul className='mt-6 space-y-3'>
                <CheckItem>Sign and announce idle capacity and a price per unit over P2P gossip</CheckItem>
                <CheckItem>Capacity is reserved when a buyer&apos;s job is accepted</CheckItem>
                <CheckItem>Signed token transfers settle to your account balance on job completion</CheckItem>
              </ul>
            </Card>

            <Card>
              <div className='flex items-center gap-3'>
                <IconBadge icon={FiShoppingCart} tone='secondary' />
                <h3 className='text-xl font-semibold'>Buyers pay for compute</h3>
              </div>
              <p className='mt-4 text-grayscale-300'>
                Discover providers across the P2P network and submit a signed job for the LLM compute and API responses
                you need. The price is quoted up front from the provider&apos;s rate, and tokens only move through a
                verified signed transfer once the work is done.
              </p>
              <ul className='mt-6 space-y-3'>
                <CheckItem>Discover local and remote providers over the P2P network</CheckItem>
                <CheckItem>Submit signed jobs priced as units &times; provider rate</CheckItem>
                <CheckItem>Balance is checked up front; a signed transfer charges you on completion</CheckItem>
                <CheckItem>Interact from outside the node through the matrix.market.v1 gRPC API</CheckItem>
              </ul>
            </Card>
          </div>

          <Card className='mt-6'>
            <div className='flex items-center gap-3'>
              <IconBadge icon={FiCpu} />
              <h3 className='text-lg font-semibold'>How settlement works</h3>
            </div>
            <p className='mt-4 text-grayscale-300'>
              Settlement is cryptographic and shipped in the Matrix core. Each account is an ed25519 keypair, and the
              earning currency is native <span className='font-medium text-white'>MATRIX</span>, the coin of the Matrix
              L1 consensus chain. Tokens move only through transfers that are signed by the sender. Settlement routed
              through the fast consensus chain is verified, ordered, and applied in one globally agreed ledger before a
              balance can change. Nonces prevent replay and the committed chain can be validated end to end to detect
              tampering, so a balance changes only through a valid signed transaction that the validator set has agreed
              on.
            </p>
            <p className='mt-4 text-grayscale-300'>
              Providers sign and announce their capacity and pricing over the libp2p gossip network, and remote nodes
              discover them into a local registry. When a buyer pays a provider, the signed transfer is proposed, voted
              on, and committed once a super-majority of validators agree, so every node applies the same ordered ledger
              rather than a private per-node balance. An external gRPC API, the{' '}
              <span className='font-medium text-white'>matrix.market.v1</span> MarketService, lets buyers and providers
              register, discover local and remote providers, submit and manage jobs, read balances and transactions,
              and broadcast signed transfers from outside the node.
            </p>
          </Card>
        </Section>

        {/* MATRIX Token */}
        <Section id='token' className='bg-section-glow'>
          <div className='mx-auto max-w-3xl text-center'>
            <Eyebrow tone='accent'>MATRIX coin</Eyebrow>
            <h2 className='mt-4 text-3xl font-bold tracking-tight sm:text-4xl'>
              The native coin you earn and spend for compute
            </h2>
            <p className='mt-4 text-lg text-grayscale-300'>
              MATRIX is the native coin of the Matrix L1 consensus chain, and the single source of truth for balances
              and supply. Every compute job and every LLM inference request settles in native MATRIX through consensus.
              A bridged wrapped ERC-20 mirror (wMATRIX) lets the same coin be represented on Ethereum for a future
              exchange listing, backed 1:1 by native MATRIX locked on the L1.
            </p>
          </div>

          <div className='mt-12 grid grid-cols-2 gap-4 md:grid-cols-4'>
            {[
              { k: 'Symbol', v: 'MATRIX' },
              { k: 'Native decimals', v: '9' },
              { k: 'Wrapped decimals', v: '18' },
              { k: 'Max supply', v: '1,000,000,000' },
            ].map((s) => (
              <div
                key={s.k}
                className='rounded-2xl border border-white/10 bg-white/[0.03] p-6 text-center backdrop-blur-sm'
              >
                <div className='text-sm text-grayscale-500'>{s.k}</div>
                <div className='mt-1 text-lg font-semibold text-white'>{s.v}</div>
              </div>
            ))}
          </div>

          <Card className='mt-6'>
            <div className='flex items-center gap-3'>
              <IconBadge icon={FiLayers} tone='accent' />
              <h3 className='text-xl font-semibold'>Native first, one currency end to end</h3>
            </div>
            <p className='mt-4 text-grayscale-300'>
              Native MATRIX lives on our own fast leader-based BFT L1. It has 9 decimals, and its supply is capped at
              1,000,000,000 whole MATRIX. A genesis allocation plus a genesis-funded reward pool back provider earnings
              as capped, supply-tracked issuance, not unlimited minting. Every balance change is a committed consensus
              fact, so buyers and providers transact in one coin from end to end.
            </p>
            <ul className='mt-6 grid gap-3 sm:grid-cols-2'>
              <CheckItem>Native coin of the Matrix L1: the single source of truth for balances and supply</CheckItem>
              <CheckItem>9 native decimals, capped at 1,000,000,000 whole MATRIX (no unlimited minting)</CheckItem>
              <CheckItem>All compute and inference settlement is native MATRIX applied through consensus</CheckItem>
              <CheckItem>The same 1,000,000,000 MATRIX maps to the 18-decimal wrapped mirror by an exact 1e9 factor</CheckItem>
            </ul>
          </Card>

          <Card className='mt-6'>
            <div className='flex items-center gap-3'>
              <IconBadge icon={FiLayers} tone='accent' />
              <h3 className='text-xl font-semibold'>The bridge: a wrapped mirror for listing</h3>
            </div>
            <p className='mt-4 text-grayscale-300'>
              The ERC-20 is not the settlement token. It is wMATRIX, a wrapped mirror produced by a lock-and-mint bridge
              so native MATRIX can be represented on Ethereum, for example for a future exchange listing. Outstanding
              wrapped supply always equals the native MATRIX locked on the L1, so the mirror stays backed 1:1. Native
              has 9 decimals and the wrapped token has 18, so one native base unit equals 1e9 wrapped base units, and
              the native cap of 1,000,000,000 MATRIX maps to the same amount on the wrapped side. The bridge runs
              against local and test networks only. It is NOT deployed to any public Ethereum network.
            </p>
            <ul className='mt-6 grid gap-3 sm:grid-cols-2'>
              <CheckItem>Native lock to wrapped mint: minting requires a threshold of validator secp256k1 attestations that native was locked</CheckItem>
              <CheckItem>Wrapped burn to native unlock: a burn emits an on-chain Burned event that is decoded into an authorization to release the escrowed native MATRIX, applied exactly once per event</CheckItem>
              <CheckItem>Backed 1:1 by locked native, reconciled through the exact 1e9 conversion factor</CheckItem>
              <CheckItem>Local and test networks only, not deployed to a public Ethereum network</CheckItem>
            </ul>
          </Card>
        </Section>

        {/* Consensus */}
        <Section id='consensus'>
          <div className='mx-auto max-w-3xl text-center'>
            <Eyebrow>Global consensus</Eyebrow>
            <h2 className='mt-4 text-3xl font-bold tracking-tight sm:text-4xl'>One fast, globally agreed ledger</h2>
            <p className='mt-4 text-lg text-grayscale-300'>
              Every node applies the same ordered ledger. Matrix reaches agreement with a fast, leader-based BFT
              protocol built for speed first, not proof-of-work, so a payment is final in a single voting round.
            </p>
          </div>

          <div className='mt-14 grid grid-cols-1 gap-6 md:grid-cols-2'>
            <Card>
              <div className='flex items-center gap-3'>
                <IconBadge icon={FiZap} />
                <h3 className='text-xl font-semibold'>Speed-first BFT</h3>
              </div>
              <p className='mt-4 text-grayscale-300'>
                A fixed ed25519 validator set takes turns as leader in round-robin order. The leader proposes a block,
                validators vote, and the block commits immediately once more than two-thirds agree. That single-round
                fast path keeps settlement quick while tolerating faulty or offline validators.
              </p>
              <ul className='mt-6 space-y-3'>
                <CheckItem>Leader-based BFT over a fixed validator set (no mining, no proof-of-work)</CheckItem>
                <CheckItem>Round-robin leader rotation with timeouts so a stalled leader is replaced</CheckItem>
                <CheckItem>Commit on a greater-than-two-thirds quorum in a single voting round</CheckItem>
              </ul>
            </Card>

            <Card>
              <div className='flex items-center gap-3'>
                <IconBadge icon={FiGlobe} tone='secondary' />
                <h3 className='text-xl font-semibold'>Globally agreed order</h3>
              </div>
              <p className='mt-4 text-grayscale-300'>
                Committed blocks are SHA-256 hash-linked into a single chain that every node shares. Because all nodes
                apply the same committed order, a balance is a network-wide fact instead of a private per-node number,
                and the chain can be validated end to end to detect tampering.
              </p>
              <ul className='mt-6 space-y-3'>
                <CheckItem>Hash-linked committed block chain shared by every node</CheckItem>
                <CheckItem>Deterministic ledger application, so all honest nodes converge</CheckItem>
                <CheckItem>Authoritative agreed ledger where both compute and inference settle by default</CheckItem>
              </ul>
            </Card>
          </div>
        </Section>

        {/* Inference */}
        <Section id='inference' className='bg-section-glow'>
          <div className='mx-auto max-w-3xl text-center'>
            <Eyebrow tone='secondary'>LLM inference</Eyebrow>
            <h2 className='mt-4 text-3xl font-bold tracking-tight sm:text-4xl'>
              Two ways to contribute real LLM compute
            </h2>
            <p className='mt-4 text-lg text-grayscale-300'>
              Inference is a pluggable backend. Contribute by running a model locally, or by proxying requests to a
              provider API you hold a key for. Either way the job settles in native MATRIX through consensus, priced per
              unit and capped at the amount reserved up front.
            </p>
          </div>

          <div className='mt-14 grid grid-cols-1 gap-6 md:grid-cols-2'>
            <Card>
              <div className='flex items-center gap-3'>
                <IconBadge icon={FiServer} />
                <h3 className='text-xl font-semibold'>Local runner</h3>
              </div>
              <p className='mt-4 text-grayscale-300'>
                Point the node at a local model server that speaks the Ollama-style chat API and serve inference
                straight from your own hardware. A built-in echo backend lets you run the whole flow without a GPU for
                testing.
              </p>
              <ul className='mt-6 space-y-3'>
                <CheckItem>Ollama-style local HTTP backend for on-device models</CheckItem>
                <CheckItem>GPU-free echo backend for development and testing</CheckItem>
                <CheckItem>Earn native MATRIX for the tokens your machine actually serves</CheckItem>
              </ul>
            </Card>

            <Card>
              <div className='flex items-center gap-3'>
                <IconBadge icon={FiGlobe} tone='secondary' />
                <h3 className='text-xl font-semibold'>Provider-API proxy</h3>
              </div>
              <p className='mt-4 text-grayscale-300'>
                Already pay for an OpenAI-compatible API? Contribute by proxying network requests through it. The node
                forwards the chat completion and the API key is read from an environment variable, never hardcoded or
                committed.
              </p>
              <ul className='mt-6 space-y-3'>
                <CheckItem>OpenAI-compatible chat completions backend</CheckItem>
                <CheckItem>API key read from an environment variable, never stored in code</CheckItem>
                <CheckItem>Jobs settle in native MATRIX through consensus, priced per unit and capped at the reservation</CheckItem>
              </ul>
            </Card>
          </div>

          <Card className='mt-6'>
            <div className='flex items-center gap-3'>
              <IconBadge icon={FiCpu} />
              <h3 className='text-lg font-semibold'>Metered and settled through consensus</h3>
            </div>
            <p className='mt-4 text-grayscale-300'>
              A buyer submits a job over the <span className='font-medium text-white'>matrix.inference.v1</span>{' '}
              InferenceService, capacity is reserved with an up-front affordability check on the reserved price, and the
              selected backend runs the request. The buyer-to-provider transfer is then settled in native MATRIX through
              the consensus ledger, scaled by the provider&apos;s price per unit and capped at the reserved amount, and
              the job is reported complete only once that settlement commits and applies, so a buyer is charged exactly
              once for work that really happened and never more than it reserved.
            </p>
          </Card>
        </Section>

        {/* Console */}
        <Section id='console'>
          <div className='mx-auto max-w-3xl text-center'>
            <Eyebrow tone='secondary'>Matrix Console</Eyebrow>
            <h2 className='mt-4 text-3xl font-bold tracking-tight sm:text-4xl'>A desktop app to drive your node</h2>
            <p className='mt-4 text-lg text-grayscale-300'>
              Matrix Console is a Tauri + React + Vite desktop app that connects to a local matrixd node to observe and
              control the marketplace: providers, jobs, the MATRIX wallet, the consensus chain, and LLM inference, all
              in one window.
            </p>
          </div>

          <Card className='mt-14'>
            <div className='flex items-center gap-3'>
              <IconBadge icon={FiMonitor} tone='secondary' />
              <h3 className='text-xl font-semibold'>One window for the whole node</h3>
            </div>
            <div className='mt-6 grid grid-cols-1 gap-x-12 gap-y-3 md:grid-cols-2'>
              <ul className='space-y-3'>
                <CheckItem>Connect to a local matrixd node, or explore a built-in demo mode</CheckItem>
                <CheckItem>Register provider capacity and browse local and remote providers</CheckItem>
                <CheckItem>Submit, complete, and cancel compute jobs</CheckItem>
              </ul>
              <ul className='space-y-3'>
                <CheckItem>Watch the MATRIX wallet balance and the settled ledger</CheckItem>
                <CheckItem>Follow the consensus chain: committed height, round leader, and quorum</CheckItem>
                <CheckItem>Run an inference job and see the completion and its settlement</CheckItem>
              </ul>
            </div>
            <p className='mt-6 text-sm text-grayscale-400'>
              The React frontend builds and runs everywhere. Packaging the native desktop bundle additionally needs the
              WebKitGTK/libsoup system libraries; see the console README for the connection configuration and the
              documented native-build note.
            </p>
          </Card>
        </Section>

        {/* CLI band */}
        <Section className='bg-section-glow'>
          <Card className='overflow-hidden'>
            <div className='grid grid-cols-1 items-center gap-10 lg:grid-cols-2'>
              <div>
                <Eyebrow>Operations</Eyebrow>
                <h2 className='mt-4 text-3xl font-bold tracking-tight'>Drive your node from the terminal</h2>
                <p className='mt-4 text-grayscale-300'>
                  The <span className='font-medium text-white'>matrix</span> CLI is a command-line ops tool that talks
                  to a running node over its gRPC market API. Check health, manage providers and jobs, read balances and
                  the token chain, and sign and submit native MATRIX transfers from a local ed25519 wallet.
                </p>
                <div className='mt-6'>
                  <Button href='/docs/cli' variant='secondary' size='md'>
                    <FiTerminal className='mr-2 h-4 w-4' />
                    matrix CLI reference
                  </Button>
                </div>
              </div>
              <div className='rounded-2xl border border-white/10 bg-black/60 p-5 font-mono text-sm shadow-lg'>
                <div className='mb-3 flex items-center gap-1.5'>
                  <span className='h-3 w-3 rounded-full bg-accent-300/70' />
                  <span className='h-3 w-3 rounded-full bg-semantic-processing/70' />
                  <span className='h-3 w-3 rounded-full bg-semantic-success/70' />
                </div>
                <pre className='overflow-x-auto whitespace-pre-wrap break-words text-grayscale-300'>
                  <code>{`$ matrix --help
$ matrix status
$ matrix provider register --id p1 --capacity 100 --price 5
$ matrix job submit --buyer <acct> --provider p1 --units 10
$ matrix wallet transfer --to <acct> --amount 200`}</code>
                </pre>
              </div>
            </div>
          </Card>
        </Section>

        {/* Ecosystem / building blocks band (original placeholder tiles) */}
        <Section>
          <div className='mx-auto max-w-3xl text-center'>
            <Eyebrow tone='secondary'>Built on open building blocks</Eyebrow>
            <h2 className='mt-4 text-3xl font-bold tracking-tight sm:text-4xl'>A cohesive stack, end to end</h2>
            <p className='mt-4 text-lg text-grayscale-300'>
              From peer discovery to signed settlement, every layer of the Matrix stack is designed to work together.
            </p>
          </div>
          <div className='mt-12 grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4'>
            {ecosystemTiles.map((tile) => (
              <div
                key={tile}
                className='flex items-center justify-center rounded-2xl border border-white/10 bg-white/[0.03] px-4 py-6 text-center text-sm font-medium text-grayscale-300 backdrop-blur-sm transition-colors hover:border-primary-300/40 hover:text-white'
              >
                {tile}
              </div>
            ))}
          </div>
        </Section>

        {/* Final CTA */}
        <Section>
          <div className='relative overflow-hidden rounded-4xl border border-white/10 p-[1px]'>
            <div className='absolute inset-0 bg-decorative-1 bg-gradient-200 animate-gradient-pan opacity-90' />
            <div className='relative rounded-4xl bg-black/80 px-6 py-16 text-center backdrop-blur-sm sm:px-12'>
              <h2 className='text-3xl font-bold tracking-tight sm:text-4xl'>Ready to get started?</h2>
              <p className='mx-auto mt-4 max-w-2xl text-lg text-grayscale-300'>
                Run a node, register compute, and settle in native MATRIX. Explore the docs or download a build to join
                the network.
              </p>
              <div className='mt-8 flex flex-wrap justify-center gap-4'>
                <Button href='/docs/introduction' variant='primary' size='lg'>
                  Read the docs
                </Button>
                <Button href='/download' variant='outline' size='lg'>
                  Download now
                </Button>
              </div>
            </div>
          </div>
        </Section>
      </main>
      <Footer />
    </>
  );
}
