'use client';

import { Button } from '@/components/Button';
import { CopyButton } from '@/components/CopyButton';
import { Footer } from '@/components/Footer';
import Navigation from '@/components/Navigation';
import { Card, CheckItem, Eyebrow, IconBadge, PageHero, Section } from '@/components/marketing';
import { FiArrowRight, FiServer, FiTerminal } from 'react-icons/fi';

// The terminal snippet shown on this page, and the exact command string the
// copy button writes to the clipboard. These map one-to-one to commands the
// matrix CLI actually supports (status, quickstart, fund, provider register,
// job submit, wallet transfer).
const CLI_SNIPPET = `$ matrix --help
$ matrix status
$ matrix quickstart
$ matrix fund --account <acct> --amount 1000000
$ matrix provider register --id p1 --capacity 100 --price 5
$ matrix job submit --buyer <acct> --provider p1 --units 10
$ matrix wallet transfer --to <acct> --amount 200`;

// The clipboard payload strips the leading "$ " prompts so pasted lines run.
const CLI_COMMANDS = CLI_SNIPPET.split('\n')
  .map((line) => line.replace(/^\$ /, ''))
  .join('\n');

export default function CliPage() {
  return (
    <>
      <Navigation />

      <main className='min-h-screen bg-black text-white'>
        <PageHero
          eyebrow='Operations'
          title='Drive your node from the'
          gradient='terminal'
          lead='The matrix CLI is a command-line ops tool that talks to a running node over its gRPC market API. Check health, manage providers and jobs, read balances and the token chain, and sign and submit native MATRIX transfers from a local ed25519 wallet.'
          actions={
            <>
              <Button href='/docs/cli' variant='primary' size='lg'>
                <FiTerminal className='mr-2 h-4 w-4' />
                matrix CLI reference
              </Button>
              <Button href='/download' variant='secondary' size='lg'>
                Download
              </Button>
            </>
          }
        />

        <Section className='bg-section-glow'>
          <Card className='overflow-hidden'>
            <div className='grid grid-cols-1 items-center gap-10 lg:grid-cols-2'>
              <div>
                <Eyebrow>Operations</Eyebrow>
                <h2 className='mt-4 text-3xl font-bold tracking-tight'>A single tool for the whole node</h2>
                <p className='mt-4 text-grayscale-300'>
                  The <span className='font-medium text-white'>matrix</span> CLI is a command-line ops tool that talks
                  to a running node over its gRPC market API. Check health, manage providers and jobs, read balances and
                  the token chain, and sign and submit native MATRIX transfers from a local ed25519 wallet.
                </p>
                <ul className='mt-6 space-y-3'>
                  <CheckItem>Check node status and health over the gRPC market API</CheckItem>
                  <CheckItem>Register providers and submit, complete, and cancel jobs</CheckItem>
                  <CheckItem>Submit an LLM inference job and read its completion</CheckItem>
                  <CheckItem>Read balances, and the chain of signed transfers</CheckItem>
                  <CheckItem>Sign and submit native MATRIX transfers from a local ed25519 wallet</CheckItem>
                  <CheckItem>
                    Fund an account from the genesis reward pool, or run the whole loop with one quickstart command
                  </CheckItem>
                </ul>
                <div className='mt-6'>
                  <Button href='/docs/cli' variant='secondary' size='md'>
                    <FiTerminal className='mr-2 h-4 w-4' />
                    matrix CLI reference
                  </Button>
                </div>
              </div>
              <div className='rounded-2xl border border-white/10 bg-black/60 p-5 font-mono text-sm shadow-lg'>
                <div className='mb-3 flex items-center justify-between'>
                  <div className='flex items-center gap-1.5'>
                    <span className='h-3 w-3 rounded-full bg-accent-300/70' />
                    <span className='h-3 w-3 rounded-full bg-semantic-processing/70' />
                    <span className='h-3 w-3 rounded-full bg-semantic-success/70' />
                  </div>
                  <CopyButton value={CLI_COMMANDS} label='Copy CLI commands' />
                </div>
                <pre className='overflow-x-auto whitespace-pre-wrap break-words text-grayscale-300'>
                  <code>{CLI_SNIPPET}</code>
                </pre>
              </div>
            </div>
          </Card>
        </Section>

        {/* Related documentation */}
        <Section>
          <div className='mx-auto max-w-3xl text-center'>
            <Eyebrow>Related documentation</Eyebrow>
            <h2 className='mt-4 text-3xl font-bold tracking-tight sm:text-4xl'>The full command reference</h2>
          </div>
          <div className='mt-12 grid grid-cols-1 gap-6 md:grid-cols-2'>
            <Card>
              <div className='flex items-center gap-3'>
                <IconBadge icon={FiTerminal} />
                <h3 className='text-lg font-semibold'>matrix CLI reference</h3>
              </div>
              <p className='mt-3 text-grayscale-300'>
                Every command and flag: status, health, provider, job, balance, tx, and wallet over the gRPC market API.
              </p>
              <div className='mt-5'>
                <Button href='/docs/cli' variant='ghost' size='sm'>
                  Read the CLI reference
                  <FiArrowRight className='ml-2 h-4 w-4' />
                </Button>
              </div>
            </Card>
            <Card>
              <div className='flex items-center gap-3'>
                <IconBadge icon={FiServer} tone='secondary' />
                <h3 className='text-lg font-semibold'>Installation</h3>
              </div>
              <p className='mt-3 text-grayscale-300'>
                Install the tooling and bring up a node so the CLI has something to talk to.
              </p>
              <div className='mt-5'>
                <Button href='/docs/installation' variant='ghost' size='sm'>
                  Read the installation guide
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
              <h2 className='text-3xl font-bold tracking-tight sm:text-4xl'>Operate from the terminal</h2>
              <p className='mx-auto mt-4 max-w-2xl text-lg text-grayscale-300'>
                Manage providers, jobs, balances, and transfers from the command line. Download a build or read the CLI
                reference.
              </p>
              <div className='mt-8 flex flex-wrap justify-center gap-4'>
                <Button href='/download' variant='primary' size='lg'>
                  Download
                </Button>
                <Button href='/docs/cli' variant='outline' size='lg'>
                  matrix CLI reference
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
