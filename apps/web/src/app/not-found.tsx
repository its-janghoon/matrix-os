'use client';

import Link from 'next/link';
import { useEffect, useState } from 'react';
import { GITHUB_URL } from '@/lib/releases';

export default function NotFound() {
  const [terminalLines, setTerminalLines] = useState<string[]>([]);
  const [showContent, setShowContent] = useState(false);

  useEffect(() => {
    const lines = [
      'soul-os: command not found: ' + window.location.pathname,
      'soul-os: error code 404',
      'soul-os: attempting to resolve...',
      'soul-os: checking node_modules...',
      'soul-os: checking git history...',
      'soul-os: checking Stack Overflow...',
      'soul-os: checking if it works on my machine...',
      'soul-os: nope, definitely a 404.',
    ];

    let currentLine = 0;
    const interval = setInterval(() => {
      if (currentLine < lines.length) {
        setTerminalLines(prev => [...prev, lines[currentLine]]);
        currentLine++;
      } else {
        clearInterval(interval);
        setShowContent(true);
      }
    }, 200);

    return () => clearInterval(interval);
  }, []);

  return (
    <main className='min-h-screen bg-black text-white p-8'>
      <div className='max-w-3xl mx-auto'>
        {/* Terminal Window */}
        <div className='bg-gray-900 rounded-lg overflow-hidden shadow-xl border border-gray-700'>
          {/* Terminal Header */}
          <div className='bg-gray-800 px-4 py-2 flex items-center gap-2'>
            <div className='w-3 h-3 rounded-full bg-red-500'></div>
            <div className='w-3 h-3 rounded-full bg-yellow-500'></div>
            <div className='w-3 h-3 rounded-full bg-green-500'></div>
            <span className='ml-2 text-sm text-gray-400'>soul-os-terminal</span>
          </div>

          {/* Terminal Content */}
          <div className='p-4 font-mono text-sm'>
            {terminalLines.map((line, index) => (
              <div key={index} className='mb-1'>
                <span className='text-green-400'>➜</span>
                <span className='text-blue-400'> ~/soul-os</span>
                <span className='text-gray-400'> git:(</span>
                <span className='text-red-400'>404-not-found</span>
                <span className='text-gray-400'>)</span>
                <span className='ml-2'>{line}</span>
              </div>
            ))}
          </div>
        </div>

        {/* Error Content */}
        {showContent && (
          <div className='mt-8 text-center animate-fade-up'>
            <h1 className='text-4xl font-bold mb-4'>404: Page Not in This Reality</h1>
            <p className='text-gray-400 mb-8'>
              Looks like you&apos;ve ventured into uncharted territory. Our best developers are busy writing
              documentation
              instead of fixing this.
            </p>

            <div className='bg-gray-900/50 border border-gray-800 rounded-lg p-6 mb-8'>
              <h2 className='text-xl font-semibold mb-4'>Quick Debug Guide</h2>
              <div className='text-left space-y-3 text-gray-400'>
                <p>1. Did you try turning it off and on again? (Classic but gold)</p>
                <p>2. Check your spelling - we&apos;re developers, we&apos;re pedantic about these things</p>
                <p>3. Maybe it&apos;s a feature, not a bug? 🤔</p>
                <p>4. Have you tried using ChatGPT? (We definitely did)</p>
              </div>
            </div>

            <div className='flex gap-4 justify-center'>
              <Link
                href='/'
                className='px-6 py-3 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors'
              >
                Return to Safety
              </Link>
              <Link
                href={`${GITHUB_URL}/issues`}
                className='px-6 py-3 border border-gray-700 text-gray-300 rounded-lg hover:bg-white/5 transition-colors'
              >
                Report Bug
              </Link>
            </div>
          </div>
        )}
      </div>
    </main>
  );
}
