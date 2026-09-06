'use client';

import { Button } from '@/components/Button';
import { Footer } from '@/components/Footer';
import Navigation from '@/components/Navigation';
import { MarketFlow } from '@/components/diagrams';
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

// The flow itself is carried by MarketFlow above the list; these are the
// one-line facts a reader needs per step, plus where to go for the detail. They
// used to be 40-60 word paragraphs restating the diagram in prose.
const steps = [
  {
    icon: FiServer,
    tone: 'primary' as const,
    title: 'Providers announce capacity',
    body: 'A signed announcement of available units and a price per unit, gossiped over libp2p. No broker.',
    href: '/products/marketplace',
    linkLabel: 'Compute marketplace',
  },
  {
    icon: FiShoppingCart,
    tone: 'secondary' as const,
    title: 'Buyers submit signed jobs',
    body: 'Price quoted from the provider rate, balance checked, capacity reserved. Nothing is paid yet.',
    href: '/products/marketplace',
    linkLabel: 'Compute marketplace',
  },
  {
    icon: FiCpu,
    tone: 'accent' as const,
    title: 'A pluggable backend runs it',
    body: 'A local runner, a provider-API proxy, or an echo backend for testing. Metered per unit.',
    href: '/products/inference',
    linkLabel: 'LLM inference',
  },
  {
    icon: FiGlobe,
    tone: 'primary' as const,
    title: 'Consensus settles the payment',
    body: 'The transfer commits in native MATRIX once more than two thirds of validators agree, capped at what was reserved.',
    href: '/products/consensus',
    linkLabel: 'Consensus',
  },
  {
    icon: FiLayers,
    tone: 'accent' as const,
    title: 'Balances are one agreed fact',
    body: 'Committed blocks are SHA-256 hash-linked into one chain every node shares, and can be validated end to end.',
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
          lead='Peer discovery, a signed compute market, pluggable inference and a BFT ledger, in one flow. Follow a single job from announcement to a globally agreed balance.'
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
              From idle capacity to a balance every node agrees on.
            </p>
          </div>

          <MarketFlow className='mx-auto mt-10 max-w-4xl' />

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
