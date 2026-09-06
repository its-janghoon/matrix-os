'use client';

import Link from 'next/link';
import { useEffect, useState } from 'react';
import { GITHUB_URL } from '@/lib/releases';

export default function Error({ reset }: { error: Error; reset: () => void }) {
  const [showRetry, setShowRetry] = useState(false);

  useEffect(() => {
    // Show retry button after 2 seconds
    const timer = setTimeout(() => setShowRetry(true), 2000);
    return () => clearTimeout(timer);
  }, []);

  return (
    <main className='min-h-screen bg-black text-white p-8'>
      <div className='max-w-3xl mx-auto'>
        {/* Error Stack */}
        <div className='bg-gray-900 rounded-lg overflow-hidden shadow-xl border border-gray-700 mb-8'>
          <div className='bg-red-500/10 border-b border-red-500/20 px-4 py-3'>
            <div className='flex items-start gap-3'>
              <div className='text-red-500'>
                <svg className='w-6 h-6' fill='none' viewBox='0 0 24 24' stroke='currentColor'>
                  <path
                    strokeLinecap='round'
                    strokeLinejoin='round'
                    strokeWidth={2}
                    d='M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z'
                  />
                </svg>
              </div>
              <div>
                <h2 className='text-lg font-semibold text-red-500'>
                  UnexpectedFeatureError: Something went terribly wrong
                </h2>
                <p className='text-gray-400 text-sm'>
                  at Object.makeItWork (/universe/reality/production/soul-os/src/features/everything/index.ts:42:0)
                </p>
              </div>
            </div>
          </div>

          <div className='p-4 font-mono text-sm'>
            <div className='text-gray-400'>
              <p className='mb-2'>Stack trace (for the curious developers):</p>
              <pre className='overflow-x-auto'>
                <code>{`Error: Something went terribly wrong
    at makeItWork (/soul-os/src/features/everything/index.ts:42:0)
    at Object.fixAllBugs (/soul-os/src/utils/impossible.ts:404:20)
    at Promise.resolve.then (/soul-os/node_modules/optimism/dist/index.js:1337:42)
    at process._tickCallback (internal/process/next_tick.js:68:7)
    at Function.Module.runMain (internal/modules/cjs/loader.js:834:11)
    at startup (internal/bootstrap/node.js:283:19)
    at bootstrapNodeJSCore (internal/bootstrap/node.js:622:3)
    at Object.coffee.brew (/soul-os/src/utils/developer-fuel.ts:24:7)
Root cause: Probably cosmic rays. Or a missing semicolon. We're still investigating.`}</code>
              </pre>
            </div>
          </div>
        </div>

        <div className='text-center'>
          <h1 className='text-4xl font-bold mb-4'>500: Internal Server Confusion</h1>
          <p className='text-gray-400 mb-8'>
            Our servers are having an existential crisis. Don&apos;t worry, we&apos;ve dispatched our best rubber ducks
            to debug
            the situation.
          </p>

          <div className='bg-gray-900/50 border border-gray-800 rounded-lg p-6 mb-8'>
            <h2 className='text-xl font-semibold mb-4'>While You Wait</h2>
            <div className='text-left space-y-3 text-gray-400'>
              <p>• Our engineers are furiously typing away at their mechanical keyboards</p>
              <p>• Stack Overflow is being thoroughly searched</p>
              <p>• Git blame is being carefully analyzed</p>
              <p>• Coffee is being consumed at an alarming rate</p>
            </div>
          </div>

          <div className='flex gap-4 justify-center'>
            {showRetry && (
              <button
                onClick={reset}
                className='px-6 py-3 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors'
              >
                Try Again
              </button>
            )}
            <Link
              href='/'
              className='px-6 py-3 border border-gray-700 text-gray-300 rounded-lg hover:bg-white/5 transition-colors'
            >
              Return Home
            </Link>
            <Link
              href={`${GITHUB_URL}/issues`}
              className='px-6 py-3 border border-gray-700 text-gray-300 rounded-lg hover:bg-white/5 transition-colors'
            >
              Report Issue
            </Link>
          </div>
        </div>
      </div>
    </main>
  );
}
