'use client';

import { Button } from '@/components/Button';
import { Footer } from '@/components/Footer';
import Navigation from '@/components/Navigation';
import { TokenBridge } from '@/components/diagrams';
import { Card, CheckItem, Eyebrow, IconBadge, PageHero, Section } from '@/components/marketing';
import { bridgeConfigState } from '@/lib/bridge/config';
import { FiArrowRight, FiBookOpen, FiLayers, FiShoppingCart } from 'react-icons/fi';

export default function TokenPage() {
  const bridgeConfigured = bridgeConfigState.config !== null;
  return (
    <>
      <Navigation />

      <main className='min-h-screen bg-black text-white'>
        <PageHero
          eyebrow='MATRIX coin'
          eyebrowTone='accent'
          title='The native coin you earn and spend for'
          gradient='compute'
          lead='MATRIX is the native coin of the Matrix L1 consensus chain, and the single source of truth for balances and supply. Every compute job and every LLM inference request settles in native MATRIX through consensus. A bridged wrapped ERC-20 mirror (wMATRIX) lets the same coin be represented on Ethereum for a future exchange listing, backed 1:1 by native MATRIX locked on the L1.'
          actions={
            <>
              <Button href='/download' variant='primary' size='lg'>
                Download
                <FiArrowRight className='ml-2 h-4 w-4' />
              </Button>
              <Button href='/docs/architecture' variant='secondary' size='lg'>
                Read the architecture
              </Button>
            </>
          }
        />

        {/* Stat strip + cards */}
        <Section className='bg-section-glow'>
          <div className='grid grid-cols-2 gap-4 md:grid-cols-4'>
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
              Public launch provider emissions are zero. Providers publish MATRIX-denominated quotes and earn buyer
              payments; no stablecoin peg, oracle or per-block token emission subsidizes the price.
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
              wMATRIX is not the settlement token. It is a mirror, minted only against native MATRIX locked in escrow,
              so it can be represented on Base. {bridgeConfigured ? (
                <strong className='font-semibold text-white'>This build contains an explicit Base target; configuration alone does not prove the contract is deployed, verified or live. Inspect its chain and address before signing.</strong>
              ) : (
                <strong className='font-semibold text-white'>This site has no public bridge deployment configured, so no contract address or validator endpoint is assumed and active bridge controls remain unavailable.</strong>
              )}
            </p>

            <TokenBridge />
            <ul className='mt-6 grid gap-3 sm:grid-cols-2'>
              <CheckItem>Native lock to wrapped mint: a lock is at least 100 MATRIX, and minting requires a threshold of the fixed secp256k1 attestors registered at WrappedMatrix deployment</CheckItem>
              <CheckItem>Fixed EVM attestors are separate from the dynamic native bonded-open validator set; validator joins and exits do not update the contract committee</CheckItem>
              <CheckItem>Wrapped burn to native unlock: confirmed Burned events are always ordered through native consensus and applied exactly once</CheckItem>
              <CheckItem>The user submits Base transactions and pays Base gas from their own wallet; there is no relayer</CheckItem>
              <CheckItem>Backed 1:1 by locked native with an exact 1e9 decimal conversion; this is not a USD, USDC or stablecoin peg</CheckItem>
              <CheckItem>Production mint ceiling: 6% of native maximum supply, while every minted unit still needs collateral</CheckItem>
              <CheckItem>{bridgeConfigured ? 'A Base target is configured; verify its chain, contract code and endpoints independently before use' : 'No public bridge target is configured; the site does not invent a contract address or validator endpoint'}</CheckItem>
            </ul>
          </Card>
        </Section>

        {/* Related documentation */}
        <Section>
          <div className='mx-auto max-w-3xl text-center'>
            <Eyebrow tone='accent'>Related documentation</Eyebrow>
            <h2 className='mt-4 text-3xl font-bold tracking-tight sm:text-4xl'>Understand the coin</h2>
          </div>
          <div className='mt-12 grid grid-cols-1 gap-6 md:grid-cols-2'>
            <Card>
              <div className='flex items-center gap-3'>
                <IconBadge icon={FiShoppingCart} tone='secondary' />
                <h3 className='text-lg font-semibold'>Compute marketplace</h3>
              </div>
              <p className='mt-3 text-grayscale-300'>
                See how native MATRIX changes hands when buyers pay providers for compute and inference.
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
                <IconBadge icon={FiBookOpen} tone='accent' />
                <h3 className='text-lg font-semibold'>Architecture</h3>
              </div>
              <p className='mt-3 text-grayscale-300'>
                How supply, the reward pool, and the wMATRIX bridge fit into the wider Matrix design.
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
              <h2 className='text-3xl font-bold tracking-tight sm:text-4xl'>Earn and spend native MATRIX</h2>
              <p className='mx-auto mt-4 max-w-2xl text-lg text-grayscale-300'>
                Run a node, serve compute, and settle in the coin of the Matrix L1. Download a build or read the docs to
                begin.
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
