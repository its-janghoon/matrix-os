'use client';

import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';
import { FiCopy } from 'react-icons/fi';
import { toast } from 'sonner';

export default function NetworkSetupGuide() {
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
                    <h1 className='text-4xl font-bold text-white mb-4'>Network Setup Guide</h1>
                    <p className='text-xl text-gray-100'>
                      Configure and optimize your Matrix OS network for secure and efficient communication between nodes
                      and agents.
                    </p>
                  </div>

                  {/* Network Configuration */}
                  <h2 className='text-3xl font-bold text-white mt-8 mb-6'>Network Configuration</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      Basic network configuration in your matrix.config.json:
                    </p>
                    <div className='bg-black rounded-lg p-4'>
                      <div className='flex justify-between items-center mb-2'>
                        <span className='text-sm text-gray-100'>Network Configuration</span>
                        <button
                          onClick={() =>
                            copyCode(`{
  "network": {
    "host": "0.0.0.0",
    "port": 9000,
    "protocol": "matrix-v1",
    "discovery": {
      "enabled": true,
      "interval": 60,
      "bootstrapNodes": [
        "/ip4/1.2.3.4/tcp/9000/p2p/QmBootstrapNode1",
        "/ip4/5.6.7.8/tcp/9000/p2p/QmBootstrapNode2"
      ]
    },
    "security": {
      "encryption": "enabled",
      "allowedPeers": ["QmTrustedPeer1", "QmTrustedPeer2"]
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
                        <code>{`{
  "network": {
    "host": "0.0.0.0",
    "port": 9000,
    "protocol": "matrix-v1",
    "discovery": {
      "enabled": true,
      "interval": 60,
      "bootstrapNodes": [
        "/ip4/1.2.3.4/tcp/9000/p2p/QmBootstrapNode1",
        "/ip4/5.6.7.8/tcp/9000/p2p/QmBootstrapNode2"
      ]
    },
    "security": {
      "encryption": "enabled",
      "allowedPeers": ["QmTrustedPeer1", "QmTrustedPeer2"]
    }
  }
}`}</code>
                      </pre>
                    </div>
                  </div>

                  {/* Network Topology */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Network Topology</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                    <h3 className='text-xl font-bold text-white mb-3'>Topology Types</h3>
                    <div className='grid grid-cols-1 md:grid-cols-2 gap-6'>
                      <div>
                        <h4 className='text-lg font-semibold text-white mb-2'>Mesh Network</h4>
                        <ul className='text-gray-100 space-y-2 list-disc pl-6'>
                          <li>Full peer-to-peer connectivity</li>
                          <li>Decentralized architecture</li>
                          <li>Automatic peer discovery</li>
                          <li>Resilient to node failures</li>
                        </ul>
                      </div>
                      <div>
                        <h4 className='text-lg font-semibold text-white mb-2'>Hub and Spoke</h4>
                        <ul className='text-gray-100 space-y-2 list-disc pl-6'>
                          <li>Centralized coordination</li>
                          <li>Simplified management</li>
                          <li>Efficient resource usage</li>
                          <li>Better control and monitoring</li>
                        </ul>
                      </div>
                    </div>
                  </div>

                  {/* Network Security */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Network Security</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                    <h3 className='text-xl font-bold text-white mb-3'>Security Configuration</h3>
                    <div className='bg-black rounded-lg p-4'>
                      <div className='flex justify-between items-center mb-2'>
                        <span className='text-sm text-gray-100'>Security Settings</span>
                        <button
                          onClick={() =>
                            copyCode(`{
  "security": {
    "encryption": {
      "type": "aes-256-gcm",
      "keyRotation": "7d"
    },
    "authentication": {
      "type": "ed25519",
      "challenge": true
    },
    "firewall": {
      "enabled": true,
      "rules": [
        {"port": 9000, "allow": "all"},
        {"port": "range:1024:65535", "allow": "none"}
      ]
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
                        <code>{`{
  "security": {
    "encryption": {
      "type": "aes-256-gcm",
      "keyRotation": "7d"
    },
    "authentication": {
      "type": "ed25519",
      "challenge": true
    },
    "firewall": {
      "enabled": true,
      "rules": [
        {"port": 9000, "allow": "all"},
        {"port": "range:1024:65535", "allow": "none"}
      ]
    }
  }
}`}</code>
                      </pre>
                    </div>
                  </div>

                  {/* Performance Optimization */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Performance Optimization</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                    <h3 className='text-xl font-bold text-white mb-3'>Optimization Techniques</h3>
                    <ul className='text-gray-100 space-y-3 list-disc pl-6'>
                      <li>Configure appropriate buffer sizes</li>
                      <li>Optimize peer connection limits</li>
                      <li>Implement message compression</li>
                      <li>Use efficient routing strategies</li>
                      <li>Monitor network metrics</li>
                    </ul>
                  </div>

                  {/* Next Steps */}
                  <div className='bg-blue-500/10 rounded-xl p-6 mt-8 border border-blue-500/20'>
                    <h2 className='text-2xl font-bold text-white mb-4'>Next Steps</h2>
                    <p className='text-gray-100 leading-relaxed mb-4'>Return to the start:</p>
                    <ul className='text-gray-100 space-y-3 list-disc pl-6 mb-0'>
                      <li>
                        <a href='/docs/introduction' className='text-blue-400 hover:text-blue-300 underline'>
                          Back to Introduction →
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
