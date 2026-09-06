'use client';

import { Button } from '@/components/Button';
import { Footer } from '@/components/Footer';
import Navigation from '@/components/Navigation';
import { Card, Eyebrow, IconBadge, PageHero, Section } from '@/components/marketing';
import { FiArrowRight, FiBookOpen, FiCpu, FiGlobe, FiLayers, FiServer, FiShoppingCart } from 'react-icons/fi';

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
  'Round-Robin Leader',
  'matrix CLI',
  'ed25519 Signing',
];

const steps = [
  {
    icon: FiServer,
    tone: 'primary' as const,
    title: 'Providers announce capacity',
    body: 'A provider node signs its available capacity and a price per compute unit and announces it across the libp2p gossip network. Remote nodes discover those announcements into a local registry, so buyers can find both local and remote providers.',
    href: '/products/marketplace',
    linkLabel: 'Compute marketplace',
  },
  {
    icon: FiShoppingCart,
    tone: 'secondary' as const,
    title: 'Buyers discover and submit signed jobs',
    body: 'A buyer discovers providers over the P2P network and submits a signed job through the matrix.market.v1 MarketService. The price is quoted up front from the provider rate, the balance is checked, and capacity is reserved for the job.',
    href: '/products/marketplace',
    linkLabel: 'Compute marketplace',
  },
  {
    icon: FiCpu,
    tone: 'accent' as const,
    title: 'The selected inference backend runs the work',
    body: 'The pluggable inference backend runs the request: an Ollama-style local runner, a GPU-free echo backend for testing, or an OpenAI-compatible provider-API proxy whose key is read from an environment variable. The work is metered per unit.',
    href: '/products/inference',
    linkLabel: 'LLM inference',
  },
  {
    icon: FiGlobe,
    tone: 'primary' as const,
    title: 'Settlement commits through fast BFT consensus',
    body: 'The buyer-to-provider transfer settles in native MATRIX through the fast leader-based BFT ledger. A round-robin leader proposes the block, and it commits once a greater-than-two-thirds quorum agrees in a single voting round, capped at the amount reserved up front.',
    href: '/products/consensus',
    linkLabel: 'Consensus',
  },
  {
    icon: FiLayers,
    tone: 'accent' as const,
    title: 'Balances are one globally agreed fact',
    body: 'Committed blocks are SHA-256 hash-linked into a single chain every node shares, so a MATRIX balance is a network-wide fact instead of a private per-node number. The chain can be validated end to end to detect tampering.',
    href: '/products/token',
    linkLabel: 'MATRIX token',
  },
];

export default function HowItWorksPage() {
  return (
    <>
      <Navigation />

      <main className='min-h-screen bg-black text-white'>
        <PageHero
          eyebrow='How it works'
          title='A cohesive stack, end to'
          gradient='end'
          lead='Matrix ties peer discovery, a signed compute market, pluggable inference, and a fast BFT consensus ledger into one flow. Follow a single job from announcement to a globally agreed balance, and jump into the product it belongs to at each step.'
          actions={
            <>
              <Button href='/docs/architecture' variant='primary' size='lg'>
                Read the architecture
                <FiArrowRight className='ml-2 h-4 w-4' />
              </Button>
              <Button href='/download' variant='secondary' size='lg'>
                Download
              </Button>
            </>
          }
        />

        {/* End-to-end walkthrough */}
        <Section>
          <div className='mx-auto max-w-3xl text-center'>
            <Eyebrow>End to end</Eyebrow>
            <h2 className='mt-4 text-3xl font-bold tracking-tight sm:text-4xl'>One job, five steps</h2>
            <p className='mt-4 text-lg text-grayscale-300'>
              From a provider announcing idle capacity to a balance that every node agrees on, here is how a single unit
              of compute flows through the Matrix stack.
            </p>
          </div>

          <div className='mt-14 space-y-6'>
            {steps.map((step, index) => (
              <Card key={step.title}>
                <div className='flex flex-col gap-4 sm:flex-row sm:items-start'>
                  <div className='flex items-center gap-4'>
                    <span className='flex h-10 w-10 shrink-0 items-center justify-center rounded-full border border-white/10 bg-white/[0.04] text-sm font-semibold text-grayscale-300'>
                      {index + 1}
                    </span>
                    <IconBadge icon={step.icon} tone={step.tone} />
                  </div>
                  <div className='flex-1'>
                    <h3 className='text-xl font-semibold'>{step.title}</h3>
                    <p className='mt-3 text-grayscale-300'>{step.body}</p>
                    <div className='mt-4'>
                      <Button href={step.href} variant='ghost' size='sm'>
                        {step.linkLabel}
                        <FiArrowRight className='ml-2 h-4 w-4' />
                      </Button>
                    </div>
                  </div>
                </div>
              </Card>
            ))}
          </div>
        </Section>

        {/* Ecosystem / building blocks band (original placeholder tiles) */}
        <Section className='bg-section-glow'>
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

        {/* Related documentation */}
        <Section>
          <div className='mx-auto max-w-3xl text-center'>
            <Eyebrow>Related documentation</Eyebrow>
            <h2 className='mt-4 text-3xl font-bold tracking-tight sm:text-4xl'>See the full architecture</h2>
          </div>
          <div className='mt-12 grid grid-cols-1 gap-6 md:grid-cols-2'>
            <Card>
              <div className='flex items-center gap-3'>
                <IconBadge icon={FiBookOpen} />
                <h3 className='text-lg font-semibold'>Architecture</h3>
              </div>
              <p className='mt-3 text-grayscale-300'>
                How discovery, the market API, inference backends, and the BFT ledger fit together across the node.
              </p>
              <div className='mt-5'>
                <Button href='/docs/architecture' variant='ghost' size='sm'>
                  Read the architecture
                  <FiArrowRight className='ml-2 h-4 w-4' />
                </Button>
              </div>
            </Card>
            <Card>
              <div className='flex items-center gap-3'>
                <IconBadge icon={FiServer} tone='secondary' />
                <h3 className='text-lg font-semibold'>Introduction</h3>
              </div>
              <p className='mt-3 text-grayscale-300'>
                New to Matrix? Start with the introduction for the big picture before diving into each product.
              </p>
              <div className='mt-5'>
                <Button href='/docs/introduction' variant='ghost' size='sm'>
                  Read the introduction
                  <FiArrowRight className='ml-2 h-4 w-4' />
                </Button>
              </div>
            </Card>
          </div>
        </Section>

        {/* CTA */}
        <Section>
          <div className='relative overflow-hidden rounded-4xl border border-white/10 p-[1px]'>
            <div className='absolute inset-0 bg-decorative-1 bg-gradient-200 animate-gradient-pan opacity-90' />
            <div className='relative rounded-4xl bg-black/80 px-6 py-16 text-center backdrop-blur-sm sm:px-12'>
              <h2 className='text-3xl font-bold tracking-tight sm:text-4xl'>Ready to see it end to end?</h2>
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
