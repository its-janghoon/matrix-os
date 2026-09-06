'use client';

import { Button } from '@/components/Button';
import { Footer } from '@/components/Footer';
import Navigation from '@/components/Navigation';
import { Card, CheckItem, Eyebrow, IconBadge, PageHero, Section } from '@/components/marketing';
import { FiArrowRight, FiBookOpen, FiGithub, FiMonitor, FiZap } from 'react-icons/fi';
import { GITHUB_URL } from '@/lib/releases';

export default function ConsolePage() {
  return (
    <>
      <Navigation />

      <main className='min-h-screen bg-black text-white'>
        <PageHero
          eyebrow='Matrix Console'
          eyebrowTone='secondary'
          title='A desktop app to drive your'
          gradient='node'
          lead='Matrix Console is a Tauri + React + Vite desktop app that connects to a local matrixd node to observe and control the marketplace: providers, jobs, the MATRIX wallet, the consensus chain, and LLM inference, all in one window.'
          actions={
            <>
              <Button href='/download' variant='primary' size='lg'>
                Download
                <FiArrowRight className='ml-2 h-4 w-4' />
              </Button>
              <Button href='/docs/quickstart' variant='secondary' size='lg'>
                Read the quickstart
              </Button>
            </>
          }
        />

        <Section>
          <Card>
            <div className='flex items-center gap-3'>
              <IconBadge icon={FiMonitor} tone='secondary' />
              <h3 className='text-xl font-semibold'>One window for the whole node</h3>
            </div>
            <div className='mt-6 grid grid-cols-1 gap-x-12 gap-y-3 md:grid-cols-2'>
              <ul className='space-y-3'>
                <CheckItem>Connect to a local matrixd node, or explore a built-in demo mode</CheckItem>
                <CheckItem>Register provider capacity and browse local and remote providers</CheckItem>
                <CheckItem>Submit, complete, and cancel compute jobs</CheckItem>
              </ul>
              <ul className='space-y-3'>
                <CheckItem>Watch the MATRIX wallet balance and the settled ledger</CheckItem>
                <CheckItem>Follow the consensus chain: committed height, round leader, and quorum</CheckItem>
                <CheckItem>Explore inference in the built-in demo mode and follow job status as it completes</CheckItem>
              </ul>
            </div>
            <p className='mt-6 text-sm text-grayscale-400'>
              The React frontend builds and runs everywhere. Packaging the native desktop bundle additionally needs the
              WebKitGTK/libsoup system libraries; see the console README for the connection configuration and the
              documented native-build note.
            </p>
            <div className='mt-6'>
              <Button
                href={`${GITHUB_URL}/tree/main/apps/console`}
                variant='secondary'
                size='md'
              >
                <FiGithub className='mr-2 h-4 w-4' />
                Console README on GitHub
              </Button>
            </div>
          </Card>
        </Section>

        {/* Related documentation */}
        <Section className='bg-section-glow'>
          <div className='mx-auto max-w-3xl text-center'>
            <Eyebrow tone='secondary'>Related documentation</Eyebrow>
            <h2 className='mt-4 text-3xl font-bold tracking-tight sm:text-4xl'>Get the console running</h2>
          </div>
          <div className='mt-12 grid grid-cols-1 gap-6 md:grid-cols-2'>
            <Card>
              <div className='flex items-center gap-3'>
                <IconBadge icon={FiBookOpen} />
                <h3 className='text-lg font-semibold'>Introduction</h3>
              </div>
              <p className='mt-3 text-grayscale-300'>
                Start here to understand what a Matrix node does and how the console fits into the stack.
              </p>
              <div className='mt-5'>
                <Button href='/docs/introduction' variant='ghost' size='sm'>
                  Read the introduction
                  <FiArrowRight className='ml-2 h-4 w-4' />
                </Button>
              </div>
            </Card>
            <Card>
              <div className='flex items-center gap-3'>
                <IconBadge icon={FiZap} tone='secondary' />
                <h3 className='text-lg font-semibold'>Quickstart</h3>
              </div>
              <p className='mt-3 text-grayscale-300'>
                Bring up a local matrixd node and connect the console to it in a few steps.
              </p>
              <div className='mt-5'>
                <Button href='/docs/quickstart' variant='ghost' size='sm'>
                  Read the quickstart
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
              <h2 className='text-3xl font-bold tracking-tight sm:text-4xl'>Drive your node from the desktop</h2>
              <p className='mx-auto mt-4 max-w-2xl text-lg text-grayscale-300'>
                Observe providers, jobs, the wallet, the chain, and inference in one window. Download a build to get
                started.
              </p>
              <div className='mt-8 flex flex-wrap justify-center gap-4'>
                <Button href='/download' variant='primary' size='lg'>
                  Download
                </Button>
                <Button href='/docs/quickstart' variant='outline' size='lg'>
                  Read the quickstart
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
