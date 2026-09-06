'use client';

import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';
import { FiCopy } from 'react-icons/fi';
import { toast } from 'sonner';

export default function MatrixProtocol() {
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
                    <h1 className='text-4xl font-bold text-white mb-4'>Matrix Protocol</h1>
                    <p className='text-xl text-gray-100'>
                      Understand the Matrix Protocol, the foundation of Matrix OS&apos;s distributed communication and
                      consensus system.
                    </p>
                  </div>

                  {/* Protocol Overview */}
                  <h2 className='text-3xl font-bold text-white mt-8 mb-6'>Protocol Overview</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      The Matrix Protocol is a peer-to-peer protocol designed for distributed systems and AI agent
                      communication. Key features include:
                    </p>
                    <ul className='text-gray-100 space-y-3 list-disc pl-6'>
                      <li>Decentralized peer discovery and routing</li>
                      <li>Secure message transport layer</li>
                      <li>Distributed consensus mechanism</li>
                      <li>State synchronization</li>
                      <li>Agent communication framework</li>
                    </ul>
                  </div>

                  {/* Message Format */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Message Format</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                    <h3 className='text-xl font-bold text-white mb-3'>Protocol Messages</h3>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      Matrix Protocol messages follow a standardized format:
                    </p>
                    <div className='bg-black rounded-lg p-4'>
                      <div className='flex justify-between items-center mb-2'>
                        <span className='text-sm text-gray-100'>Message Format Example</span>
                        <button
                          onClick={() =>
                            copyCode(`{
  "header": {
    "version": "1.0",
    "messageType": "AGENT_COMMUNICATION",
    "sender": "QmSenderPeerId",
    "recipient": "QmRecipientPeerId",
    "timestamp": 1634567890
  },
  "payload": {
    "type": "request",
    "action": "compute",
    "data": {
      // Action-specific data
    }
  },
  "signature": "base64-encoded-signature"
}`)
                          }
                          className='p-2 hover:bg-gray-800 rounded transition-colors'
                        >
                          <FiCopy className='w-4 h-4' />
                        </button>
                      </div>
                      <pre className='text-sm text-gray-100 overflow-x-auto whitespace-pre-wrap break-words'>
                        <code>{`{
  "header": {
    "version": "1.0",
    "messageType": "AGENT_COMMUNICATION",
    "sender": "QmSenderPeerId",
    "recipient": "QmRecipientPeerId",
    "timestamp": 1634567890
  },
  "payload": {
    "type": "request",
    "action": "compute",
    "data": {
      // Action-specific data
    }
  },
  "signature": "base64-encoded-signature"
}`}</code>
                      </pre>
                    </div>
                  </div>

                  {/* Protocol Implementation */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Protocol Implementation</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                    <h3 className='text-xl font-bold text-white mb-3'>Using the Protocol</h3>
                    <p className='text-gray-100 leading-relaxed mb-4'>Implement Matrix Protocol in your agents:</p>
                    <div className='bg-black rounded-lg p-4'>
                      <div className='flex justify-between items-center mb-2'>
                        <span className='text-sm text-gray-100'>Protocol Implementation</span>
                        <button
                          onClick={() =>
                            copyCode(`import { MatrixProtocol } from '@matrix-os/core';

class MyAgent extends MatrixProtocol {
  async onMessage(message) {
    if (message.header.messageType === 'AGENT_COMMUNICATION') {
      const { type, action, data } = message.payload;
      
      // Handle the message
      switch(action) {
        case 'compute':
          return this.handleCompute(data);
        case 'store':
          return this.handleStore(data);
        default:
          throw new Error('Unknown action');
      }
    }
  }

  async sendRequest(recipient, action, data) {
    return this.send({
      messageType: 'AGENT_COMMUNICATION',
      recipient,
      payload: {
        type: 'request',
        action,
        data
      }
    });
  }
}`)
                          }
                          className='p-2 hover:bg-gray-800 rounded transition-colors'
                        >
                          <FiCopy className='w-4 h-4' />
                        </button>
                      </div>
                      <pre className='text-sm text-gray-100 overflow-x-auto whitespace-pre-wrap break-words'>
                        <code>{`import { MatrixProtocol } from '@matrix-os/core';

class MyAgent extends MatrixProtocol {
  async onMessage(message) {
    if (message.header.messageType === 'AGENT_COMMUNICATION') {
      const { type, action, data } = message.payload;
      
      // Handle the message
      switch(action) {
        case 'compute':
          return this.handleCompute(data);
        case 'store':
          return this.handleStore(data);
        default:
          throw new Error('Unknown action');
      }
    }
  }

  async sendRequest(recipient, action, data) {
    return this.send({
      messageType: 'AGENT_COMMUNICATION',
      recipient,
      payload: {
        type: 'request',
        action,
        data
      }
    });
  }
}`}</code>
                      </pre>
                    </div>
                  </div>

                  {/* Protocol Security */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Protocol Security</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                    <h3 className='text-xl font-bold text-white mb-3'>Security Features</h3>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      The Matrix Protocol includes several security features:
                    </p>
                    <ul className='text-gray-100 space-y-3 list-disc pl-6'>
                      <li>End-to-end encryption for all messages</li>
                      <li>Digital signatures for message authenticity</li>
                      <li>Peer authentication and verification</li>
                      <li>Transport layer security</li>
                    </ul>
                  </div>

                  {/* Next Steps */}
                  <div className='bg-blue-500/10 rounded-xl p-6 mt-8 border border-blue-500/20'>
                    <h2 className='text-2xl font-bold text-white mb-4'>Next Steps</h2>
                    <p className='text-gray-100 leading-relaxed mb-4'>To learn more about the Matrix Protocol:</p>
                    <ul className='text-gray-100 space-y-3 list-disc pl-6 mb-0'>
                      <li>
                        <a href='/docs/soul-protocol' className='text-blue-400 hover:text-blue-300 underline'>
                          Explore the Soul Protocol
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
