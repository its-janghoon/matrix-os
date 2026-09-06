'use client';

import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';
import { FiCopy } from 'react-icons/fi';
import { toast } from 'sonner';

export default function MatrixOsQuickstart() {
  const copyCode = (code: string) => {
    navigator.clipboard.writeText(code);
    toast.success('Code copied to clipboard');
  };

  return (
    <>
      <Navigation />
      <div className='min-h-screen bg-black'>
        <div className='pt-16'>
          <div className='flex'>
            <DocSidebar />

            {/* Main Content */}
            <main className='flex-1 ml-64 p-8'>
              <div className='max-w-4xl mx-auto'>
                <article className='text-gray-100'>
                  {/* Hero Section */}
                  <div className='bg-gradient-to-r from-blue-500/10 via-purple-500/10 to-blue-500/10 rounded-xl p-8 mb-12 border border-blue-500/20'>
                    <h1 className='text-4xl font-bold text-white mb-4'>Quick Start Guide</h1>
                    <p className='text-xl text-gray-100'>
                      Create your first Matrix OS agent in minutes. This guide will walk you through the basics of
                      setting up and running an autonomous agent.
                    </p>
                  </div>

                  <h2 className='text-3xl font-bold text-white mt-8 mb-6'>Prerequisites</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <ul className='text-gray-100 space-y-3 list-disc pl-6 mb-0'>
                      <li>
                        Matrix OS installed (see{' '}
                        <a href='/docs/installation' className='text-blue-400 hover:text-blue-300 underline'>
                          Installation Guide
                        </a>
                        )
                      </li>
                      <li>Basic understanding of command line interfaces</li>
                      <li>Text editor of your choice</li>
                      <li>Terminal or command prompt</li>
                    </ul>
                  </div>

                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Creating Your First Agent</h2>

                  <div className='space-y-8'>
                    {/* Project Setup */}
                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>1. Project Setup</h3>
                      <p className='text-gray-100 leading-relaxed mb-4'>
                        First, create a new directory for your agent project and initialize it:
                      </p>
                      <div className='bg-black rounded-lg p-4'>
                        <div className='flex justify-between items-center mb-2'>
                          <span className='text-sm text-gray-100'>Create and initialize project</span>
                          <button
                            onClick={() =>
                              copyCode(`# Create project directory
mkdir my-first-agent
cd my-first-agent

# Initialize Matrix OS project
matrix-os init`)
                            }
                            className='p-2 hover:bg-gray-800 rounded transition-colors'
                          >
                            <FiCopy className='w-4 h-4' />
                          </button>
                        </div>
                        <pre className='text-sm text-gray-100 overflow-x-auto whitespace-pre-wrap break-words'>
                          <code>{`# Create project directory
mkdir my-first-agent
cd my-first-agent

# Initialize Matrix OS project
matrix-os init`}</code>
                        </pre>
                      </div>
                      <p className='text-gray-100 mt-4 mb-0'>
                        The initialization wizard will guide you through setting up your project with sensible defaults.
                      </p>
                    </div>

                    {/* Agent Configuration */}
                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>2. Configure Your Agent</h3>
                      <p className='text-gray-100 leading-relaxed mb-4'>
                        Open the generated{' '}
                        <code className='text-gray-100 bg-gray-800 px-1.5 py-0.5 rounded'>agent.config.ts</code> file
                        and customize your agent&apos;s behavior:
                      </p>
                      <div className='bg-black rounded-lg p-4'>
                        <div className='flex justify-between items-center mb-2'>
                          <span className='text-sm text-gray-100'>agent.config.ts</span>
                          <button
                            onClick={() =>
                              copyCode(`import { defineAgent } from '@matrix-os/core';

export default defineAgent({
  name: 'my-first-agent',
  description: 'A simple demo agent',
  capabilities: ['compute', 'network'],
  
  // Define agent's behavior
  behavior: {
    onStart: async (context) => {
      context.log.info('Agent started!');
    },
    onMessage: async (message, context) => {
      context.log.info('Received message:', message);
      return { status: 'received' };
    }
  }
});`)
                            }
                            className='p-2 hover:bg-gray-800 rounded transition-colors'
                          >
                            <FiCopy className='w-4 h-4' />
                          </button>
                        </div>
                        <pre className='text-sm text-gray-100 overflow-x-auto whitespace-pre-wrap break-words'>
                          <code>{`import { defineAgent } from '@matrix-os/core';

export default defineAgent({
  name: 'my-first-agent',
  description: 'A simple demo agent',
  capabilities: ['compute', 'network'],
  
  // Define agent's behavior
  behavior: {
    onStart: async (context) => {
      context.log.info('Agent started!');
    },
    onMessage: async (message, context) => {
      context.log.info('Received message:', message);
      return { status: 'received' };
    }
  }
});`}</code>
                        </pre>
                      </div>
                    </div>

                    {/* Running the Agent */}
                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>3. Run Your Agent</h3>
                      <p className='text-gray-100 leading-relaxed mb-4'>
                        Start your agent in development mode with live-reload enabled:
                      </p>
                      <div className='bg-black rounded-lg p-4'>
                        <div className='flex justify-between items-center mb-2'>
                          <span className='text-sm text-gray-100'>Start development mode</span>
                          <button
                            onClick={() => copyCode('matrix-os dev')}
                            className='p-2 hover:bg-gray-800 rounded transition-colors'
                          >
                            <FiCopy className='w-4 h-4' />
                          </button>
                        </div>
                        <pre className='text-sm text-gray-100 overflow-x-auto whitespace-pre-wrap break-words'>
                          <code>matrix-os dev</code>
                        </pre>
                      </div>
                      <p className='text-gray-100 mt-4 mb-0'>
                        Your agent is now running! The development mode includes:
                      </p>
                      <ul className='text-gray-100 mt-2 space-y-2 list-disc pl-6 mb-0'>
                        <li>Live-reload on code changes</li>
                        <li>Enhanced logging and debugging</li>
                        <li>Performance monitoring</li>
                      </ul>
                    </div>

                    {/* Testing the Agent */}
                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>4. Test Your Agent</h3>
                      <p className='text-gray-100 leading-relaxed mb-4'>
                        Send a test message to your agent using the Matrix OS CLI:
                      </p>
                      <div className='bg-black rounded-lg p-4'>
                        <div className='flex justify-between items-center mb-2'>
                          <span className='text-sm text-gray-100'>Send test message</span>
                          <button
                            onClick={() => copyCode('matrix-os message my-first-agent "Hello, Agent!"')}
                            className='p-2 hover:bg-gray-800 rounded transition-colors'
                          >
                            <FiCopy className='w-4 h-4' />
                          </button>
                        </div>
                        <pre className='text-sm text-gray-100 overflow-x-auto whitespace-pre-wrap break-words'>
                          <code>matrix-os message my-first-agent &quot;Hello, Agent!&quot;</code>
                        </pre>
                      </div>
                    </div>
                  </div>

                  <h2 className='text-2xl font-bold text-white mb-4'>Next Steps</h2>
                  <p className='text-gray-100 leading-relaxed mb-4'>Continue your journey with Matrix OS:</p>
                  <ul className='text-gray-100 space-y-3 list-disc pl-6 mb-0'>
                    <li>
                      <a href='/docs/architecture' className='text-blue-400 hover:text-blue-300 underline'>
                        Next: Architecture Overview →
                      </a>
                    </li>
                    <li>
                      <a href='/docs/guides/agent-development' className='text-blue-400 hover:text-blue-300 underline'>
                        Jump to Agent Development Guide →
                      </a>
                    </li>
                  </ul>
                </article>
              </div>
            </main>
          </div>
        </div>
      </div>
    </>
  );
}
