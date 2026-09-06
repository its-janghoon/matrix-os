'use client';

import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';
import { FiCopy } from 'react-icons/fi';
import { toast } from 'sonner';

export default function MatrixOsArchitecture() {
  const copyCode = (code: string) => {
    navigator.clipboard.writeText(code);
    toast.success('Code copied to clipboard');
  };

  return (
    <>
      <Navigation />
      <div className='min-h-screen bg-black'>
        <div className='pt-16'>
          <div className='flex flex-col lg:flex-row'>
            <DocSidebar />

            {/* Main Content */}
            <main className='min-w-0 flex-1 p-4 sm:p-6 lg:ml-64 lg:p-8'>
              <div className='max-w-4xl mx-auto'>
                <article className='text-gray-100'>
                  {/* Hero Section */}
                  <div className='bg-gradient-to-r from-blue-500/10 via-purple-500/10 to-blue-500/10 rounded-xl p-8 mb-12 border border-blue-500/20'>
                    <h1 className='text-4xl font-bold text-white mb-4'>Matrix OS Architecture</h1>
                    <p className='text-xl text-gray-100'>
                      Understand the core architecture of Matrix OS, its components, and how they work together to
                      create a powerful distributed operating system.
                    </p>
                  </div>

                  {/* System Overview */}
                  <h2 className='text-3xl font-bold text-white mt-8 mb-6'>System Overview</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      Matrix OS is built on a layered architecture that combines distributed systems, blockchain
                      technology, and AI capabilities:
                    </p>
                    <ul className='text-gray-100 space-y-3 list-disc pl-6'>
                      <li>Core System Layer - Handles fundamental OS operations</li>
                      <li>Network Layer - Manages peer-to-peer communications</li>
                      <li>Consensus Layer - Ensures distributed agreement</li>
                      <li>Agent Runtime Layer - Executes autonomous agents</li>
                      <li>Application Layer - Hosts user applications and services</li>
                    </ul>
                  </div>

                  {/* Core Components */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Core Components</h2>

                  <div className='space-y-8'>
                    {/* Kernel */}
                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>Kernel</h3>
                      <p className='text-gray-100 leading-relaxed mb-4'>
                        The Matrix OS kernel manages system resources and provides core services:
                      </p>
                      <div className='bg-black rounded-lg p-4'>
                        <div className='flex justify-between items-center mb-2'>
                          <span className='text-sm text-gray-100'>Kernel Architecture</span>
                          <button
                            onClick={() =>
                              copyCode(`// Matrix OS Kernel Component Example
import { KernelModule } from '@matrix-os/core';

@KernelModule({
  name: 'resource-manager',
  priority: 'high'
})
export class ResourceManager {
  async allocateResources(request: ResourceRequest): Promise<ResourceAllocation> {
    // Resource allocation logic
    return this.optimizer.allocate(request);
  }
}`)
                            }
                            className='p-2 hover:bg-gray-800 rounded transition-colors'
                          >
                            <FiCopy className='w-4 h-4' />
                          </button>
                        </div>
                        <pre className='text-sm text-gray-100 overflow-x-auto whitespace-pre-wrap break-words'>
                          <code>{`// Matrix OS Kernel Component Example
import { KernelModule } from '@matrix-os/core';

@KernelModule({
  name: 'resource-manager',
  priority: 'high'
})
export class ResourceManager {
  async allocateResources(request: ResourceRequest): Promise<ResourceAllocation> {
    // Resource allocation logic
    return this.optimizer.allocate(request);
  }
}`}</code>
                        </pre>
                      </div>
                    </div>

                    {/* Network Stack */}
                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>Network Stack</h3>
                      <p className='text-gray-100 leading-relaxed mb-4'>
                        The network stack enables peer-to-peer communication and distributed consensus:
                      </p>
                      <ul className='text-gray-100 space-y-3 list-disc pl-6'>
                        <li>P2P Discovery Protocol</li>
                        <li>Secure Message Transport</li>
                        <li>Distributed Hash Table (DHT)</li>
                        <li>Network State Synchronization</li>
                      </ul>
                    </div>

                    {/* Agent Runtime */}
                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>Agent Runtime</h3>
                      <p className='text-gray-100 leading-relaxed mb-4'>
                        The agent runtime provides the execution environment for autonomous agents:
                      </p>
                      <div className='bg-black rounded-lg p-4'>
                        <div className='flex justify-between items-center mb-2'>
                          <span className='text-sm text-gray-100'>Agent Runtime Example</span>
                          <button
                            onClick={() =>
                              copyCode(`// Agent Runtime Configuration
{
  "runtime": {
    "isolation": "container",
    "resources": {
      "cpu": "dynamic",
      "memory": "2GB"
    },
    "security": {
      "sandboxing": true,
      "permissions": ["network", "storage"]
    }
  }
}`)
                            }
                            className='p-2 hover:bg-gray-800 rounded transition-colors'
                          >
                            <FiCopy className='w-4 h-4' />
                          </button>
                        </div>
                        <pre className='text-sm text-gray-100 overflow-x-auto whitespace-pre-wrap break-words'>
                          <code>{`// Agent Runtime Configuration
{
  "runtime": {
    "isolation": "container",
    "resources": {
      "cpu": "dynamic",
      "memory": "2GB"
    },
    "security": {
      "sandboxing": true,
      "permissions": ["network", "storage"]
    }
  }
}`}</code>
                        </pre>
                      </div>
                    </div>
                  </div>

                  {/* System Flow */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>System Flow</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      Understanding how components interact in Matrix OS:
                    </p>
                    <ol className='text-gray-100 space-y-3 list-decimal pl-6'>
                      <li>System initialization and boot sequence</li>
                      <li>Network discovery and peer connection</li>
                      <li>Agent deployment and execution</li>
                      <li>Resource management and optimization</li>
                      <li>State synchronization across the network</li>
                    </ol>
                  </div>

                  {/* Next Steps */}
                  <div className='bg-blue-500/10 rounded-xl p-6 mt-8 border border-blue-500/20'>
                    <h2 className='text-2xl font-bold text-white mb-4'>Next Steps</h2>
                    <p className='text-gray-100 leading-relaxed mb-4'>To dive deeper into Matrix OS architecture:</p>
                    <ul className='text-gray-100 space-y-3 list-disc pl-6 mb-0'>
                      <li>
                        <a href='/docs/matrix-protocol' className='text-blue-400 hover:text-blue-300 underline'>
                          Learn about the Matrix Protocol
                        </a>
                      </li>
                      <li>
                        <a href='/docs/configuration' className='text-blue-400 hover:text-blue-300 underline'>
                          Explore configuration options
                        </a>
                      </li>
                      <li>
                        <a href='/docs/soul-protocol' className='text-blue-400 hover:text-blue-300 underline'>
                          Understand the Soul Protocol
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
