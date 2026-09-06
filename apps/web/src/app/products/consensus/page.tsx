'use client';

import { Button } from '@/components/Button';
import { Footer } from '@/components/Footer';
import Navigation from '@/components/Navigation';
import { Card, CheckItem, Eyebrow, IconBadge, PageHero, Section } from '@/components/marketing';
import { FiArrowRight, FiBookOpen, FiGlobe, FiLayers, FiZap } from 'react-icons/fi';

export default function ConsensusPage() {
  return (
    <>
      <Navigation />

      <main className='min-h-screen bg-black text-white'>
        <PageHero
          eyebrow='Global consensus'
          title='One fast, globally agreed'
          gradient='ledger'
          lead='Every node applies the same ordered ledger. Matrix reaches agreement with a fast, leader-based BFT protocol built for speed first, not proof-of-work, so a payment is final in a single voting round.'
          actions={
            <>
              <Button href='/docs/architecture' variant='primary' size='lg'>
                Read the architecture
                <FiArrowRight className='ml-2 h-4 w-4' />
              </Button>
              <Button href='/docs/matrix-protocol' variant='secondary' size='lg'>
                Matrix Protocol
              </Button>
            </>
          }
        />

        <Section>
          <div className='grid grid-cols-1 gap-6 md:grid-cols-2'>
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

        {/* Related documentation */}
        <Section className='bg-section-glow'>
          <div className='mx-auto max-w-3xl text-center'>
            <Eyebrow>Related documentation</Eyebrow>
            <h2 className='mt-4 text-3xl font-bold tracking-tight sm:text-4xl'>Dig into the protocol</h2>
          </div>
          <div className='mt-12 grid grid-cols-1 gap-6 md:grid-cols-2'>
            <Card>
              <div className='flex items-center gap-3'>
                <IconBadge icon={FiBookOpen} />
                <h3 className='text-lg font-semibold'>Architecture</h3>
              </div>
              <p className='mt-3 text-grayscale-300'>
                How the BFT ledger, block chain, and settlement pipeline are wired together across the node.
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
                <IconBadge icon={FiLayers} tone='secondary' />
                <h3 className='text-lg font-semibold'>Matrix Protocol</h3>
              </div>
              <p className='mt-3 text-grayscale-300'>
                The protocol reference covering blocks, votes, quorum, and the hash-linked chain.
              </p>
              <div className='mt-5'>
                <Button href='/docs/matrix-protocol' variant='ghost' size='sm'>
                  Read the protocol
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
              <h2 className='text-3xl font-bold tracking-tight sm:text-4xl'>Run a node on the agreed ledger</h2>
              <p className='mx-auto mt-4 max-w-2xl text-lg text-grayscale-300'>
                Join the network and see every balance settle as one globally agreed fact. Download a build or read the
                docs.
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
