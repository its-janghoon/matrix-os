'use client';

import { Button } from '@/components/Button';
import { Footer } from '@/components/Footer';
import Navigation from '@/components/Navigation';
import { Card, CheckItem, Eyebrow, IconBadge, PageHero, Section } from '@/components/marketing';
import { FiArrowRight, FiBookOpen, FiCpu, FiServer, FiShoppingCart } from 'react-icons/fi';

export default function MarketplacePage() {
  return (
    <>
      <Navigation />

      <main className='min-h-screen bg-black text-white'>
        <PageHero
          eyebrow='Compute marketplace'
          eyebrowTone='secondary'
          title='A peer-to-peer market for'
          gradient='compute'
          lead='Idle machines announce capacity and pricing across the P2P network, and buyers discover them and submit signed compute jobs. Payment settles from buyer to provider through cryptographically signed token transfers recorded on an append-only, hash-chained transaction log.'
          actions={
            <>
              <Button href='/download' variant='primary' size='lg'>
                Download
                <FiArrowRight className='ml-2 h-4 w-4' />
              </Button>
              <Button href='/docs/compute-marketplace' variant='secondary' size='lg'>
                Read the marketplace docs
              </Button>
            </>
          }
        />

        {/* Providers / buyers */}
        <Section>
          <div className='grid grid-cols-1 gap-6 md:grid-cols-2'>
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

        {/* Related documentation */}
        <Section className='bg-section-glow'>
          <div className='mx-auto max-w-3xl text-center'>
            <Eyebrow>Related documentation</Eyebrow>
            <h2 className='mt-4 text-3xl font-bold tracking-tight sm:text-4xl'>Go deeper on the marketplace</h2>
          </div>
          <div className='mt-12 grid grid-cols-1 gap-6 md:grid-cols-2'>
            <Card>
              <div className='flex items-center gap-3'>
                <IconBadge icon={FiShoppingCart} tone='secondary' />
                <h3 className='text-lg font-semibold'>Compute marketplace</h3>
              </div>
              <p className='mt-3 text-grayscale-300'>
                How providers register, how buyers discover and submit jobs, and how the matrix.market.v1 MarketService
                is used from outside the node.
              </p>
              <div className='mt-5'>
                <Button href='/docs/compute-marketplace' variant='ghost' size='sm'>
                  Read the guide
                  <FiArrowRight className='ml-2 h-4 w-4' />
                </Button>
              </div>
            </Card>
            <Card>
              <div className='flex items-center gap-3'>
                <IconBadge icon={FiBookOpen} />
                <h3 className='text-lg font-semibold'>Architecture</h3>
              </div>
              <p className='mt-3 text-grayscale-300'>
                Where the marketplace sits in the Matrix stack: gossip discovery, signed transfers, and consensus
                settlement.
              </p>
              <div className='mt-5'>
                <Button href='/docs/architecture' variant='ghost' size='sm'>
                  Read the architecture
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
              <h2 className='text-3xl font-bold tracking-tight sm:text-4xl'>Join the compute market</h2>
              <p className='mx-auto mt-4 max-w-2xl text-lg text-grayscale-300'>
                Register a provider, submit a job, and settle in native MATRIX. Download a build or start with the
                introduction.
              </p>
              <div className='mt-8 flex flex-wrap justify-center gap-4'>
                <Button href='/download' variant='primary' size='lg'>
                  Download
                </Button>
                <Button href='/docs/introduction' variant='outline' size='lg'>
                  Read the docs
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
