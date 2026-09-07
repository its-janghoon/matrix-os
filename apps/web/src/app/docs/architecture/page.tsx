import { CopyButton } from '@/components/CopyButton';
import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';
import { NodeStack } from '@/components/diagrams';

export const metadata = {
  title: 'Architecture',
  description:
    'What runs inside a matrixd node: the libp2p host, the compute marketplace, the BFT consensus engine, the inference backends and the Pebble store they all share.',
};

/**
 * This page used to describe a different product. It had a "Kernel" exposing
 * `@KernelModule` decorators imported from an `@matrix-os/core` npm package, an
 * agent runtime configured with container isolation, and a DHT - none of which
 * exist anywhere in this repository. What does exist is one Go daemon with a
 * libp2p host, a marketplace, a consensus engine, pluggable inference backends
 * and a Pebble store, reachable over gRPC. That is what is documented here, and
 * the diagram carries the shape so the text can stop restating it.
 */
const CONFIG = `network:
  listen_addr: /ip4/0.0.0.0/tcp/9000
  bootstrap_peers: []
storage:
  engine: pebble
  path: ~/.matrix/data
admin:
  addr: 0.0.0.0:9090      # gRPC: deploy, health, logs
market:
  addr: 0.0.0.0:9091      # gRPC: matrix.market.v1.MarketService
inference:
  addr: 0.0.0.0:9092      # gRPC: matrix.inference.v1.InferenceService
  echo_provider: local    # GPU-free backend, for trying the flow
connect:
  addr: 0.0.0.0:9093      # HTTP/JSON for the same services; "off" disables it
  allowed_origins: ["*"]  # browser origins allowed to call it
consensus:
  validators: []          # hex account IDs; empty = solo validator`;

const subsystems = [
  {
    name: 'libp2p host and transport',
    body: 'Peer connections and the gossip topics the marketplace and consensus publish on. Discovery is peer-to-peer; there is no broker and no registry server.',
  },
  {
    name: 'Compute marketplace',
    body: 'The provider order book and the job records. Registering announces capacity and a price; submitting a job reserves that capacity.',
  },
  {
    name: 'Consensus engine',
    body: 'An ed25519 validator set, a round-robin leader, and two voting phases per height. The set is chain state: a change rides in a committed block and takes effect at an epoch boundary, so every node switches at the same height. Committed blocks are hash-linked, and a node that misses one fetches it from a peer with the precommit quorum that committed it.',
  },
  {
    name: 'Inference backends',
    body: 'An Ollama-style local runner, an OpenAI-compatible proxy whose key comes from the environment, or a deterministic echo backend that needs no GPU.',
  },
  {
    name: 'Pebble store',
    body: 'One embedded key-value store per node, holding the market ledger, the per-node token chain and the committed block chain under disjoint key prefixes.',
  },
  {
    name: 'gRPC services',
    body: 'Market, inference and admin, each on its own port, gated by the same authentication when ACLs are enabled.',
  },
  {
    name: 'HTTP endpoint',
    body: 'The same market and inference services over plain HTTP, as JSON, on port 9093. Raw gRPC needs HTTP/2 trailers that no browser can produce, so this is the surface a web app, a dApp front end or the Console in a WebView actually calls. Same objects, same auth.',
  },
];

export default function ArchitecturePage() {
  return (
    <>
      <Navigation />
      <div className='min-h-screen bg-black'>
        <div className='pt-16'>
          <div className='flex flex-col lg:flex-row'>
            <DocSidebar />

            <main className='min-w-0 flex-1 p-4 sm:p-6 lg:ml-64 lg:p-8'>
              <div className='mx-auto max-w-4xl'>
                <article className='text-gray-100'>
                  <div className='mb-10 rounded-xl border border-primary-400/20 bg-gradient-to-r from-primary-400/10 via-accent-300/10 to-primary-400/10 p-8'>
                    <h1 className='mb-4 text-4xl font-bold text-white'>Architecture</h1>
                    <p className='text-xl text-gray-100'>
                      One process, one store. Here is what a node is made of and how you reach it.
                    </p>
                  </div>

                  <NodeStack />

                  <h2 className='mb-6 mt-12 text-3xl font-bold text-white'>Subsystems</h2>
                  <dl className='grid gap-4 sm:grid-cols-2'>
                    {subsystems.map((s) => (
                      <div key={s.name} className='rounded-xl border border-gray-800 bg-gray-900/50 p-5'>
                        <dt className='text-base font-semibold text-white'>{s.name}</dt>
                        <dd className='mt-2 text-sm leading-relaxed text-gray-300'>{s.body}</dd>
                      </div>
                    ))}
                  </dl>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>How it is configured</h2>
                  <p className='mb-4 text-gray-300'>
                    One YAML file, written by <code className='text-white'>matrix init</code> and read at startup.
                    Every field below is real; see{' '}
                    <a href='/docs/configuration' className='text-accent-200 underline hover:text-accent-100'>
                      configuration
                    </a>{' '}
                    for the rest.
                  </p>
                  <div className='rounded-lg border border-gray-800 bg-black p-4'>
                    <div className='mb-2 flex items-center justify-between'>
                      <span className='text-sm text-gray-400'>~/.matrix/config.yaml</span>
                      <CopyButton value={CONFIG} label='Copy' />
                    </div>
                    <pre className='overflow-x-auto text-sm text-gray-100'>
                      <code>{CONFIG}</code>
                    </pre>
                  </div>

                  <div className='mt-10 rounded-xl border border-primary-400/20 bg-primary-400/10 p-6'>
                    <h2 className='mb-4 text-2xl font-bold text-white'>Next</h2>
                    <ul className='mb-0 list-disc space-y-3 pl-6 text-gray-100'>
                      <li>
                        <a href='/docs/matrix-protocol' className='text-accent-200 underline hover:text-accent-100'>
                          The Matrix Protocol
                        </a>{' '}
                        - the wire messages between nodes
                      </li>
                      <li>
                        <a href='/docs/compute-marketplace' className='text-accent-200 underline hover:text-accent-100'>
                          The compute marketplace
                        </a>{' '}
                        - providers, jobs and settlement
                      </li>
                      <li>
                        <a href='/docs/configuration' className='text-accent-200 underline hover:text-accent-100'>
                          Configuration
                        </a>{' '}
                        - every field in the file above
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
