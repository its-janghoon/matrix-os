'use client';

import { Button } from '@/components/Button';
import { Footer } from '@/components/Footer';
import Navigation from '@/components/Navigation';
import { JobLifecycle } from '@/components/diagrams';
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
                Leave a machine running, register it, and get paid per job it serves.
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
                Find capacity, submit a signed job, pay only for work that completed.
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
              Every account is an ed25519 keypair and every transfer is signed by its sender. A balance changes only
              through a signed transfer the validator set has committed, and nonces make a replay impossible.
            </p>

            <JobLifecycle className='mx-auto max-w-md' />

            <p className='mt-4 text-grayscale-300'>
              Everything above is reachable from outside the node through the{' '}
              <span className='font-medium text-white'>matrix.market.v1</span> MarketService: register, discover local
              and remote providers, submit and manage jobs, read balances and transactions, and broadcast signed
              transfers.
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
