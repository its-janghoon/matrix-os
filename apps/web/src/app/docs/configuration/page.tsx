'use client';

import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';
import { FiCopy } from 'react-icons/fi';
import { toast } from 'sonner';

export default function MatrixOsConfiguration() {
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
                    <h1 className='text-4xl font-bold text-white mb-4'>Configuring Matrix OS</h1>
                    <p className='text-xl text-gray-100'>
                      Learn how to configure Matrix OS to match your needs, from basic settings to advanced
                      customization options.
                    </p>
                  </div>

                  {/* Basic Configuration */}
                  <h2 className='text-3xl font-bold text-white mt-8 mb-6'>Basic Configuration</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      The main configuration file for Matrix OS is{' '}
                      <code className='bg-black px-2 py-1 rounded'>matrix.config.json</code>. Here&apos;s a basic
                      example:
                    </p>
                    <div className='bg-black rounded-lg p-4'>
                      <div className='flex justify-between items-center mb-2'>
                        <span className='text-sm text-gray-100'>Basic Configuration</span>
                        <button
                          onClick={() =>
                            copyCode(`{
  "node": {
    "name": "my-matrix-node",
    "type": "full",
    "network": {
      "port": 9000,
      "host": "0.0.0.0"
    }
  },
  "storage": {
    "path": "./data",
    "maxSize": "10GB"
  },
  "logging": {
    "level": "info",
    "output": "console"
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
  "node": {
    "name": "my-matrix-node",
    "type": "full",
    "network": {
      "port": 9000,
      "host": "0.0.0.0"
    }
  },
  "storage": {
    "path": "./data",
    "maxSize": "10GB"
  },
  "logging": {
    "level": "info",
    "output": "console"
  }
}`}</code>
                      </pre>
                    </div>
                  </div>

                  {/* Network Configuration */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Network Configuration</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                    <h3 className='text-xl font-bold text-white mb-3'>P2P Network Settings</h3>
                    <p className='text-gray-100 leading-relaxed mb-4'>Configure peer-to-peer networking parameters:</p>
                    <div className='bg-black rounded-lg p-4'>
                      <div className='flex justify-between items-center mb-2'>
                        <span className='text-sm text-gray-100'>Network Configuration</span>
                        <button
                          onClick={() =>
                            copyCode(`{
  "network": {
    "p2p": {
      "maxPeers": 50,
      "bootstrapNodes": [
        "/ip4/1.2.3.4/tcp/9000/p2p/QmHash1",
        "/ip4/5.6.7.8/tcp/9000/p2p/QmHash2"
      ],
      "discovery": {
        "mdns": true,
        "bootstrap": true
      }
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
    "p2p": {
      "maxPeers": 50,
      "bootstrapNodes": [
        "/ip4/1.2.3.4/tcp/9000/p2p/QmHash1",
        "/ip4/5.6.7.8/tcp/9000/p2p/QmHash2"
      ],
      "discovery": {
        "mdns": true,
        "bootstrap": true
      }
    }
  }
}`}</code>
                      </pre>
                    </div>
                  </div>

                  {/* Security Configuration */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Security Configuration</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                    <h3 className='text-xl font-bold text-white mb-3'>Security Settings</h3>
                    <p className='text-gray-100 leading-relaxed mb-4'>Configure security parameters and permissions:</p>
                    <div className='bg-black rounded-lg p-4'>
                      <div className='flex justify-between items-center mb-2'>
                        <span className='text-sm text-gray-100'>Security Configuration</span>
                        <button
                          onClick={() =>
                            copyCode(`{
  "security": {
    "encryption": {
      "type": "aes-256-gcm",
      "keyRotation": "7d"
    },
    "permissions": {
      "agents": ["network", "storage", "compute"],
      "users": {
        "admin": ["all"],
        "user": ["read", "execute"]
      }
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
    "permissions": {
      "agents": ["network", "storage", "compute"],
      "users": {
        "admin": ["all"],
        "user": ["read", "execute"]
      }
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

                  {/* Advanced Configuration */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Advanced Configuration</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                    <h3 className='text-xl font-bold text-white mb-3'>Performance Tuning</h3>
                    <p className='text-gray-100 leading-relaxed mb-4'>Fine-tune Matrix OS performance:</p>
                    <ul className='text-gray-100 space-y-3 list-disc pl-6'>
                      <li>Resource allocation and limits</li>
                      <li>Network optimization</li>
                      <li>Storage configuration</li>
                      <li>Caching strategies</li>
                    </ul>
                  </div>

                  {/* Environment Variables */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Environment Variables</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      Matrix OS supports configuration through environment variables:
                    </p>
                    <div className='bg-black rounded-lg p-4'>
                      <pre className='text-sm text-gray-100 overflow-x-auto whitespace-pre-wrap break-words'>
                        <code>{`# Network configuration
MATRIX_NODE_PORT=9000
MATRIX_NODE_HOST=0.0.0.0

# Security settings
MATRIX_SECURITY_ENCRYPTION_KEY=your-secret-key
MATRIX_SECURITY_ENABLE_FIREWALL=true

# Storage configuration
MATRIX_STORAGE_PATH=/path/to/data
MATRIX_STORAGE_MAX_SIZE=10GB`}</code>
                      </pre>
                    </div>
                  </div>

                  {/* Next Steps */}
                  <div className='bg-blue-500/10 rounded-xl p-6 mt-8 border border-blue-500/20'>
                    <h2 className='text-2xl font-bold text-white mb-4'>Next Steps</h2>
                    <p className='text-gray-100 leading-relaxed mb-4'>After configuring Matrix OS:</p>
                    <ul className='text-gray-100 space-y-3 list-disc pl-6 mb-0'>
                      <li>
                        <a href='/docs/architecture' className='text-blue-400 hover:text-blue-300 underline'>
                          Learn about the system architecture
                        </a>
                      </li>
                      <li>
                        <a href='/docs/matrix-protocol' className='text-blue-400 hover:text-blue-300 underline'>
                          Explore the Matrix Protocol
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
