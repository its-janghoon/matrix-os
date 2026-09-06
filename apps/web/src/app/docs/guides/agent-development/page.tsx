'use client';

import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';
import { FiCopy } from 'react-icons/fi';
import { toast } from 'sonner';

export default function AgentDevelopmentGuide() {
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
                    <h1 className='text-4xl font-bold text-white mb-4'>Agent Development Guide</h1>
                    <p className='text-xl text-gray-100'>
                      Learn how to create, deploy, and manage intelligent agents in the Matrix OS ecosystem.
                    </p>
                  </div>

                  {/* Basic Agent Structure */}
                  <h2 className='text-3xl font-bold text-white mt-8 mb-6'>Basic Agent Structure</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      Create your first agent with this basic structure:
                    </p>
                    <div className='bg-black rounded-lg p-4'>
                      <div className='flex justify-between items-center mb-2'>
                        <span className='text-sm text-gray-100'>Basic Agent Template</span>
                        <button
                          onClick={() =>
                            copyCode(`import { Agent, AgentContext } from '@matrix-os/core';

@Agent({
  name: 'my-agent',
  version: '1.0.0',
  description: 'My first Matrix OS agent'
})
export class MyAgent {
  constructor(private context: AgentContext) {}

  async onStart() {
    this.context.logger.info('Agent started');
  }

  async onMessage(message: any) {
    this.context.logger.info('Received message:', message);
    // Handle incoming messages
  }

  async onStop() {
    this.context.logger.info('Agent stopped');
  }
}`)
                          }
                          className='p-2 hover:bg-gray-800 rounded transition-colors'
                        >
                          <FiCopy className='w-4 h-4' />
                        </button>
                      </div>
                      <pre className='text-sm text-gray-100 overflow-x-auto whitespace-pre-wrap break-words'>
                        <code>{`import { Agent, AgentContext } from '@matrix-os/core';

@Agent({
  name: 'my-agent',
  version: '1.0.0',
  description: 'My first Matrix OS agent'
})
export class MyAgent {
  constructor(private context: AgentContext) {}

  async onStart() {
    this.context.logger.info('Agent started');
  }

  async onMessage(message: any) {
    this.context.logger.info('Received message:', message);
    // Handle incoming messages
  }

  async onStop() {
    this.context.logger.info('Agent stopped');
  }
}`}</code>
                      </pre>
                    </div>
                  </div>

                  {/* Agent Lifecycle */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Agent Lifecycle</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                    <h3 className='text-xl font-bold text-white mb-3'>Lifecycle Events</h3>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      Understand and handle different agent lifecycle events:
                    </p>
                    <ul className='text-gray-100 space-y-3 list-disc pl-6'>
                      <li>Initialization and configuration</li>
                      <li>Start-up sequence</li>
                      <li>Runtime operation</li>
                      <li>Graceful shutdown</li>
                    </ul>
                  </div>

                  {/* Agent Capabilities */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Agent Capabilities</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                    <div className='grid grid-cols-1 md:grid-cols-2 gap-6'>
                      <div>
                        <h4 className='text-lg font-semibold text-white mb-2'>Core Capabilities</h4>
                        <ul className='text-gray-100 space-y-2 list-disc pl-6'>
                          <li>Message handling</li>
                          <li>State management</li>
                          <li>Resource access</li>
                          <li>Error handling</li>
                        </ul>
                      </div>
                      <div>
                        <h4 className='text-lg font-semibold text-white mb-2'>Advanced Features</h4>
                        <ul className='text-gray-100 space-y-2 list-disc pl-6'>
                          <li>AI integration</li>
                          <li>Network communication</li>
                          <li>Data persistence</li>
                          <li>Security features</li>
                        </ul>
                      </div>
                    </div>
                  </div>

                  {/* Best Practices */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Best Practices</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                    <h3 className='text-xl font-bold text-white mb-3'>Development Guidelines</h3>
                    <ul className='text-gray-100 space-y-3 list-disc pl-6'>
                      <li>Follow the principle of single responsibility</li>
                      <li>Implement proper error handling and logging</li>
                      <li>Use type-safe message formats</li>
                      <li>Optimize resource usage</li>
                      <li>Write comprehensive tests</li>
                    </ul>
                  </div>

                  {/* Next Steps */}
                  <div className='bg-blue-500/10 rounded-xl p-6 mt-8 border border-blue-500/20'>
                    <h2 className='text-2xl font-bold text-white mb-4'>Next Steps</h2>
                    <p className='text-gray-100 leading-relaxed mb-4'>Continue to the next section:</p>
                    <ul className='text-gray-100 space-y-3 list-disc pl-6 mb-0'>
                      <li>
                        <a href='/docs/guides/network-setup' className='text-blue-400 hover:text-blue-300 underline'>
                          Next: Network Setup Guide →
                        </a>
                      </li>
                    </ul>
                  </div>
                </article>
              </div>
            </main>
          </div>
        </div>
      </div>
    </>
  );
}
