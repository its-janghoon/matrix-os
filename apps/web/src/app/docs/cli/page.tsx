'use client';

import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';
import { FiCopy } from 'react-icons/fi';
import { toast } from 'sonner';

const copyCode = (code: string) => {
  navigator.clipboard.writeText(code);
  toast.success('Code copied to clipboard');
};

// Declared at module scope, not inside the page component. Defining it during
// render creates a new component type on every render, which throws away the
// subtree's state each time - react-hooks/static-components flags exactly this.
const CodeBlock = ({ label, code }: { label: string; code: string }) => (
  <div className='bg-black rounded-lg p-4'>
    <div className='flex justify-between items-center mb-2'>
      <span className='text-sm text-gray-100'>{label}</span>
      <button onClick={() => copyCode(code)} className='p-2 hover:bg-gray-800 rounded transition-colors'>
        <FiCopy className='w-4 h-4' />
      </button>
    </div>
    <pre className='text-sm text-gray-100 overflow-x-auto whitespace-pre-wrap break-words'>
      <code>{code}</code>
    </pre>
  </div>
);

export default function MatrixCliDocs() {
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
                  {/* Hero */}
                  <div className='bg-gradient-to-r from-blue-500/10 via-cyan-500/10 to-blue-500/10 rounded-xl p-8 mb-12 border border-blue-500/20'>
                    <h1 className='text-4xl font-bold text-white mb-4'>The matrix CLI</h1>
                    <p className='text-xl text-gray-100'>
                      <code className='text-gray-100 bg-gray-800 px-1.5 py-0.5 rounded'>matrix</code> is the command-line
                      operations tool for a Matrix OS node. It drives a running node over its gRPC market API
                      (<code className='text-gray-100 bg-gray-800 px-1.5 py-0.5 rounded'>matrix.market.v1.MarketService</code>,
                      default <code className='text-gray-100 bg-gray-800 px-1.5 py-0.5 rounded'>127.0.0.1:9091</code>):
                      check node health, manage compute providers and jobs, read balances and the token chain, and
                      manage an ed25519 wallet to sign and submit native MATRIX transfers.
                    </p>
                  </div>

                  <h2 className='text-3xl font-bold text-white mt-8 mb-6'>Install / build</h2>
                  <p className='text-gray-100 leading-relaxed mb-4'>
                    Build the binary from <code className='text-gray-100 bg-gray-800 px-1.5 py-0.5 rounded'>services/core</code>, then run{' '}
                    <code className='text-gray-100 bg-gray-800 px-1.5 py-0.5 rounded'>matrix --help</code> to see the full
                    command tree:
                  </p>
                  <div className='space-y-8'>
                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <CodeBlock
                        label='Build and explore the CLI'
                        code={`# from services/core
go build -o matrix ./cmd/matrix
# optionally install onto your PATH
go install ./cmd/matrix

# see the full command tree
matrix --help`}
                      />
                    </div>

                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>Global flags</h3>
                      <ul className='text-gray-100 space-y-2 list-disc pl-6 mb-0'>
                        <li>
                          <code className='text-gray-100 bg-gray-800 px-1.5 py-0.5 rounded'>--addr</code> (default{' '}
                          <code className='text-gray-100 bg-gray-800 px-1.5 py-0.5 rounded'>127.0.0.1:9091</code>): node
                          market gRPC endpoint.
                        </li>
                        <li>
                          <code className='text-gray-100 bg-gray-800 px-1.5 py-0.5 rounded'>--api-key</code>: API key for
                          nodes running with ACLs, sent as the <code className='text-gray-100 bg-gray-800 px-1.5 py-0.5 rounded'>authorization</code> gRPC
                          metadata header.
                        </li>
                        <li>
                          <code className='text-gray-100 bg-gray-800 px-1.5 py-0.5 rounded'>--timeout</code> (default{' '}
                          <code className='text-gray-100 bg-gray-800 px-1.5 py-0.5 rounded'>10s</code>): per-RPC timeout.
                        </li>
                        <li>
                          <code className='text-gray-100 bg-gray-800 px-1.5 py-0.5 rounded'>--json</code>: emit
                          machine-readable JSON instead of human-friendly tables.
                        </li>
                      </ul>
                    </div>
                  </div>

                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Common commands</h2>
                  <div className='space-y-8'>
                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>Node status and providers</h3>
                      <CodeBlock
                        label='Status, health, and provider management'
                        code={`matrix status                 # report serving status + endpoint
matrix health                 # gRPC health Check -> SERVING / NOT_SERVING

# Advertise local compute capacity on the order book.
matrix provider register --id provider-1 --capacity 100 --price 5

# List providers (add --include-remote for P2P-discovered providers).
matrix provider list --include-remote`}
                      />
                    </div>

                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>Jobs, balances, and transactions</h3>
                      <CodeBlock
                        label='Reserve, settle, and inspect'
                        code={`# Reserve provider capacity for a paid compute job.
matrix job submit --buyer <account-id> --provider provider-1 --units 10
matrix job complete --id <job-id>     # settle (transfers native MATRIX)
matrix job cancel --id <job-id>       # return reserved capacity

matrix balance --account <account-id>
matrix tx list --start 10 --limit 20`}
                      />
                    </div>

                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>Wallet</h3>
                      <p className='text-gray-100 leading-relaxed mb-4'>
                        The wallet is an ed25519 keypair stored at{' '}
                        <code className='text-gray-100 bg-gray-800 px-1.5 py-0.5 rounded'>~/.matrix/wallet.json</code>{' '}
                        with <code className='text-gray-100 bg-gray-800 px-1.5 py-0.5 rounded'>0600</code> permissions.
                        The private key is never printed or logged; transfers are signed locally and only the public
                        key, signature, and transfer fields are sent to the node.
                      </p>
                      <CodeBlock
                        label='Create a wallet and submit a signed transfer'
                        code={`matrix wallet create          # refuses to overwrite an existing file
matrix wallet show            # show the account ID / public key
matrix wallet balance

# Sign and submit a native MATRIX transfer.
matrix wallet transfer --to <recipient-account-id> --amount 200`}
                      />
                    </div>
                  </div>

                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mt-8 mb-8'>
                    <h3 className='text-xl font-bold text-white mb-3'>Note on inference</h3>
                    <p className='text-gray-100 leading-relaxed mb-0'>
                      The <code className='text-gray-100 bg-gray-800 px-1.5 py-0.5 rounded'>matrix.inference.v1</code>{' '}
                      InferenceService is not yet wired into the node, so the CLI does not expose an{' '}
                      <code className='text-gray-100 bg-gray-800 px-1.5 py-0.5 rounded'>inference</code> command.
                      Inference operations remain internal until the service is served over gRPC.
                    </p>
                  </div>

                  <h2 className='text-2xl font-bold text-white mb-4'>Next steps</h2>
                  <ul className='text-gray-100 space-y-3 list-disc pl-6 mb-0'>
                    <li>
                      <a href='/docs/quickstart' className='text-blue-400 hover:text-blue-300 underline'>
                        Quick Start Guide →
                      </a>
                    </li>
                    <li>
                      <a href='/docs/configuration' className='text-blue-400 hover:text-blue-300 underline'>
                        Configuration →
                      </a>
                    </li>
                    <li>
                      <a
                        href='https://github.com/ecirlabs/matrix-core/blob/main/services/core/cmd/matrix/README.md'
                        target='_blank'
                        rel='noopener noreferrer'
                        className='text-blue-400 hover:text-blue-300 underline'
                      >
                        Full matrix CLI README on GitHub →
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
