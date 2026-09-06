'use client';

import { Button } from '@/components/Button';
import { CopyButton } from '@/components/CopyButton';
import { Footer } from '@/components/Footer';
import HeroBackdrop from '@/components/HeroBackdrop';
import Navigation from '@/components/Navigation';
import { MarketFlow } from '@/components/diagrams';
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
} from 'react-icons/fi';
import { GITHUB_URL } from '@/lib/releases';

// The copy-paste quickstart: build the daemon + CLI, boot a local dev node, and
// run the one-command zero-to-first-job loop. These commands match exactly what
// the matrix CLI supports (matrixd --init/start, matrix quickstart).
const QUICKSTART_COMMAND = [
  'go build -o matrixd ./cmd/matrixd && go build -o matrix ./cmd/matrix',
  './matrixd --init && ./matrixd &',
  './matrix quickstart',
].join('\n');

// Six-card product overview. Each summary is condensed from the dedicated
// /products/* page copy and links out to the full page.
const products = [
  {
    icon: FiShoppingCart,
    tone: 'primary' as const,
    title: 'Compute Marketplace',
    body: 'Idle machines announce capacity and pricing over the P2P network, and buyers discover them and submit signed compute jobs that settle in native MATRIX.',
    href: '/products/marketplace',
  },
  {
    icon: FiLayers,
    tone: 'accent' as const,
    title: 'MATRIX Token',
    body: 'The native coin of the Matrix L1 and the single source of truth for balances, capped at 1,000,000,000 whole MATRIX with a 1:1 bridged wrapped mirror.',
    href: '/products/token',
  },
  {
    icon: FiGlobe,
    tone: 'secondary' as const,
    title: 'Consensus',
    body: 'A fast leader-based BFT Layer 1 with round-robin leaders, so every balance is one globally agreed, hash-linked fact.',
    href: '/products/consensus',
  },
  {
    icon: FiCpu,
    tone: 'primary' as const,
    title: 'LLM Inference',
    body: 'A pluggable backend: run models locally with an Ollama-style runner, test with a GPU-free echo backend, or proxy an OpenAI-compatible provider API.',
    href: '/products/inference',
  },
  {
    icon: FiMonitor,
    tone: 'secondary' as const,
    title: 'Matrix Console',
    body: 'A Tauri + React + Vite desktop app that connects to a local matrixd node to drive providers, jobs, the MATRIX wallet, consensus, and inference.',
    href: '/products/console',
  },
  {
    icon: FiTerminal,
    tone: 'accent' as const,
    title: 'matrix CLI',
    body: 'A command-line ops tool that talks to a running node over its gRPC market API to manage providers and jobs, read balances, and sign MATRIX transfers.',
    href: '/products/cli',
  },
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
                  href={GITHUB_URL}
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

              {/* Copy-paste quickstart: zero to a funded account and a first job. */}
              <div className='mx-auto mt-10 max-w-2xl rounded-2xl border border-white/10 bg-black/60 p-4 text-left font-mono text-sm shadow-lg backdrop-blur-sm'>
                <div className='mb-3 flex items-center justify-between'>
                  <span className='text-[11px] uppercase tracking-[0.12em] text-grayscale-500'>
                    Quickstart: zero to first job
                  </span>
                  <CopyButton value={QUICKSTART_COMMAND} label='Copy quickstart commands' />
                </div>
                <pre className='overflow-x-auto whitespace-pre-wrap break-words text-grayscale-300'>
                  <code>{`$ go build -o matrixd ./cmd/matrixd && go build -o matrix ./cmd/matrix
$ ./matrixd --init && ./matrixd &
$ ./matrix quickstart`}</code>
                </pre>
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

        {/* Product overview */}
        <Section>
          <div className='mx-auto max-w-3xl text-center'>
            <Eyebrow tone='secondary'>Product overview</Eyebrow>
            <h2 className='mt-4 text-3xl font-bold tracking-tight sm:text-4xl'>One stack, six products</h2>
            <p className='mt-4 text-lg text-grayscale-300'>
              From a signed compute market to a fast BFT ledger, every layer of Matrix works together. Explore each
              product to see how it fits the whole.
            </p>
          </div>

          <div className='mt-14 grid grid-cols-1 gap-6 md:grid-cols-2 lg:grid-cols-3'>
            {products.map((product) => (
              <Card key={product.title} className='flex flex-col'>
                <div className='flex items-center gap-3'>
                  <IconBadge icon={product.icon} tone={product.tone} />
                  <h3 className='text-lg font-semibold'>{product.title}</h3>
                </div>
                <p className='mt-4 flex-1 text-grayscale-300'>{product.body}</p>
                <div className='mt-6'>
                  <Button href={product.href} variant='ghost' size='sm'>
                    Learn more
                    <FiArrowRight className='ml-2 h-4 w-4' />
                  </Button>
                </div>
              </Card>
            ))}
          </div>
        </Section>

        {/* How it works teaser */}
        <Section className='bg-section-glow'>
          <Card className='overflow-hidden'>
            <div className='max-w-2xl'>
              <Eyebrow>How it works</Eyebrow>
              <h2 className='mt-4 text-3xl font-bold tracking-tight'>Follow one job, end to end</h2>
              <p className='mt-4 text-grayscale-300'>
                Idle capacity in, a signed job through, one agreed balance out.
              </p>
            </div>

            {/* Full width rather than a side column: the diagram has a minimum
                readable width, and squeezed into half a card it would scroll
                horizontally even on a desktop. */}
            <MarketFlow />

            <Button href='/how-it-works' variant='secondary' size='md'>
              See how it works
              <FiArrowRight className='ml-2 h-4 w-4' />
            </Button>
          </Card>
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
