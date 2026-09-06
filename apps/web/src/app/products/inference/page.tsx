'use client';

import { Button } from '@/components/Button';
import { CopyButton } from '@/components/CopyButton';
import { Footer } from '@/components/Footer';
import Navigation from '@/components/Navigation';
import { InferencePath } from '@/components/diagrams';
import { Card, CheckItem, Eyebrow, IconBadge, PageHero, Section } from '@/components/marketing';
import { FiArrowRight, FiBookOpen, FiCpu, FiGlobe, FiServer, FiShoppingCart } from 'react-icons/fi';

// The full, copy-paste-runnable sequence to get an inference job to settle
// against a fresh local node. It is accurate to the shipped CLI:
//   1. resolve the local wallet's account id into BUYER,
//   2. fund BUYER from the node's reward pool so it can afford the job,
//   3. register the demo provider on the order book with capacity + price
//      (the node auto-registers the GPU-free echo BACKEND for this provider id,
//      but a provider must still be listed on the order book to reserve against),
//   4. submit the job, which reserves -> fulfills -> settles in one command.
// The provider id matches the node's default Inference.EchoProvider, so no GPU
// is needed. `matrix fund` moves reward-pool MATRIX and, like every mutating
// market RPC, requires the node's API key when ACLs are enabled (the default);
// the note under the block covers adding `--api-key <key>` to each command.
const INFERENCE_CLI_COMMAND = [
  'BUYER=$(matrix wallet show | awk \'/^account:/{print $2}\')',
  'matrix fund --account "$BUYER" --amount 1000000',
  'matrix provider register --id demo-inference-provider --capacity 1000 --price 5',
  'matrix inference submit --buyer "$BUYER" \\',
  '  --provider demo-inference-provider --prompt "hello world"',
].join('\n');

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
                Serve inference from your own hardware, or from the built-in echo backend with no GPU at all.
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
                Already pay for an OpenAI-compatible API? Contribute by proxying requests through it.
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
            <InferencePath className='mx-auto max-w-md' />

            <p className='mt-4 text-grayscale-300'>
              Jobs arrive over the <span className='font-medium text-white'>matrix.inference.v1</span> InferenceService.
              A job is reported complete only once its settlement has committed, so a buyer is charged exactly once, for
              work that really happened, and never more than it reserved.
            </p>

            {/* Click-to-copy: the full `matrix` sequence that gets an inference
                job to settle against a fresh local node with the GPU-free echo
                backend. It funds the buyer and registers the demo provider first
                so the copied block succeeds end to end, not just the submit. */}
            <div className='mt-6 rounded-xl border border-white/10 bg-black/60 p-4'>
              <div className='mb-2 flex items-center justify-between'>
                <span className='text-xs font-medium uppercase tracking-wide text-grayscale-400'>
                  Run a job from the CLI (fund, register, submit)
                </span>
                <CopyButton value={INFERENCE_CLI_COMMAND} label='Copy inference commands' />
              </div>
              <pre className='overflow-x-auto text-sm text-grayscale-200'>
                <code>{INFERENCE_CLI_COMMAND}</code>
              </pre>
              <p className='mt-3 text-xs text-grayscale-400'>
                Against a fresh node the buyer must be funded and the demo provider registered before a job can reserve
                and settle, so the block does both first. It assumes a local wallet (<span className='text-grayscale-300'>matrix wallet create</span>) and a
                running node. Because a default node runs with ACLs enabled, add <span className='text-grayscale-300'>--api-key &lt;key&gt;</span> to each
                command (the key the node was started with); on an open dev node with ACLs disabled you can drop it, though
                that node will not expose reward-pool funding.
              </p>
            </div>
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
