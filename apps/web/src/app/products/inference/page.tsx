'use client';

import { Button } from '@/components/Button';
import { Footer } from '@/components/Footer';
import Navigation from '@/components/Navigation';
import { Card, CheckItem, Eyebrow, IconBadge, PageHero, Section } from '@/components/marketing';
import { FiArrowRight, FiBookOpen, FiCpu, FiGlobe, FiServer, FiShoppingCart } from 'react-icons/fi';

export default function InferencePage() {
  return (
    <>
      <Navigation />

      <main className='min-h-screen bg-black text-white'>
        <PageHero
          eyebrow='LLM inference'
          eyebrowTone='secondary'
          title='Two ways to contribute real LLM'
          gradient='compute'
          lead='Inference is a pluggable backend. Contribute by running a model locally, or by proxying requests to a provider API you hold a key for. Either way the job settles in native MATRIX through consensus, priced per unit and capped at the amount reserved up front.'
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

        <Section className='bg-section-glow'>
          <div className='grid grid-cols-1 gap-6 md:grid-cols-2'>
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

        {/* Related documentation */}
        <Section>
          <div className='mx-auto max-w-3xl text-center'>
            <Eyebrow tone='secondary'>Related documentation</Eyebrow>
            <h2 className='mt-4 text-3xl font-bold tracking-tight sm:text-4xl'>Learn how inference settles</h2>
          </div>
          <div className='mt-12 grid grid-cols-1 gap-6 md:grid-cols-2'>
            <Card>
              <div className='flex items-center gap-3'>
                <IconBadge icon={FiShoppingCart} tone='secondary' />
                <h3 className='text-lg font-semibold'>Compute marketplace</h3>
              </div>
              <p className='mt-3 text-grayscale-300'>
                How inference jobs are priced, reserved, and settled alongside the rest of the compute market.
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
                Where the pluggable inference backends sit in the node and how settlement flows through consensus.
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
              <h2 className='text-3xl font-bold tracking-tight sm:text-4xl'>Contribute inference compute</h2>
              <p className='mx-auto mt-4 max-w-2xl text-lg text-grayscale-300'>
                Serve a local model or proxy a provider API, and earn native MATRIX for the work you do. Download a build
                or read the docs.
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
