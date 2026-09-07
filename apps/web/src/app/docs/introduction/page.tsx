import { CodeSample } from '@/components/CodeSample';
import DocSidebar from '@/components/DocSidebar';
import { MarketFlow } from '@/components/diagrams';
import Navigation from '@/components/Navigation';
import { GITHUB_URL } from '@/lib/releases';
import Link from 'next/link';

export const metadata = {
  title: 'Introduction',
  description:
    'What Matrix OS is, what it does today, and what it does not do: a peer-to-peer market for compute and LLM inference, settled in one coin on its own BFT chain.',
};

/**
 * This page used to advertise "zero-knowledge proofs for privacy-preserving
 * operations", "secure enclaves for sensitive computations", "native support
 * for machine learning models, neural networks", and agents that "learn and
 * adapt from their environment" and "collaborate with other agents". None of
 * that exists: there is no ZK anything and no enclave anything in the tree, and
 * the agent runtime exposes exactly four host functions - log, send,
 * get_memory, set_memory. It also sent readers to the quickstart to "create
 * your first agent", which is not what the quickstart does.
 *
 * An introduction is the page a reader trusts most, so it is the worst place to
 * put a wish list. What follows is the system as it is.
 */
const LIST_PROVIDERS = `import { MatrixClient } from 'matrix-os-sdk';

const matrix = new MatrixClient({ endpoint: 'http://127.0.0.1:9093' });

for (const p of await matrix.listProviders({ includeRemote: true })) {
  console.log(\`\${p.id}: \${p.available}/\${p.capacity} units at \${p.pricePerUnit} each\`);
}`;

const REAL = [
  {
    title: 'A market for compute',
    body: 'A node advertises capacity at a price. A buyer reserves units, the work is done, and the buyer pays the provider. Providers found over libp2p appear alongside local ones.',
  },
  {
    title: 'LLM inference on that market',
    body: 'An inference job is a compute job with a prompt attached. The backend is pluggable: a local HTTP model server, an OpenAI-compatible API, or the GPU-free echo backend a fresh node registers so the path works before you have a model.',
  },
  {
    title: 'One coin, one ledger',
    body: 'Native MATRIX, 9 decimals, capped at a billion. Both marketplaces settle in it through consensus, so a balance is a fact every node agrees on rather than one node’s opinion.',
  },
  {
    title: 'Its own BFT chain',
    body: 'An ed25519 validator set, a leader that rotates every commit, two voting phases, and a quorum of more than two thirds. Committed blocks are hash-linked; a node that misses one fetches it with the quorum that committed it.',
  },
  {
    title: 'Bonded stake, optionally',
    body: 'Turn it on and voting power becomes an account’s bonded MATRIX, so a quorum costs two thirds of what is bonded rather than two thirds of the identities. Admission requires a minimum bond, and a validator proven to have equivocated loses that bond to the reward pool.',
  },
  {
    title: 'A fee, and an emission',
    body: 'A capped cut of each committed transfer pays the validator set, so a bond earns as well as risks. A fixed per-block budget from the genesis pool pays the registered providers a block paid, halving on a schedule until it is spent. Both off unless an operator turns them on.',
  },
  {
    title: 'A wrapped ERC-20 mirror',
    body: 'Native MATRIX locks into escrow and a threshold of validator attestations authorizes minting wMATRIX; burning it releases the escrow, and on a validator set that release needs a quorum of attestations too, so neither direction rests on one node. Against local and test networks only - it is not deployed to public Ethereum.',
  },
  {
    title: 'A WebAssembly agent runtime',
    body: 'A node embeds wazero and runs a module against four host functions - log, send, get_memory, set_memory - under a memory ceiling and a per-call deadline, so a guest that never returns is torn down rather than holding the node.',
  },
];

const NOT_YET = [
  'A permissionless validator set is still ahead. Bonded stake, a fee that pays it and an emission that pays providers all exist, but they are off unless an operator turns them on, and the set is still one a quorum of operators admits you to rather than one you buy into.',
  'No zero-knowledge proofs and no secure enclaves. A provider sees the work it runs.',
  'No agent deployment path over the network, and no agent-to-agent collaboration primitive. The runtime runs a module you hand it.',
  'The bridge is not on public Ethereum, and the SDK is not on npm.',
];

export default function MatrixOsIntroduction() {
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
                    <h1 className='mb-4 text-4xl font-bold text-white'>Introduction</h1>
                    <p className='text-xl text-gray-100'>
                      Matrix OS is a peer-to-peer marketplace for compute and LLM inference, settled in one coin on
                      its own fast BFT Layer 1. You run a node, it sells your spare capacity or buys someone
                      else&apos;s, and payment is a transaction a quorum of validators has committed.
                    </p>
                  </div>

                  <MarketFlow />

                  <h2 className='mb-6 mt-12 text-3xl font-bold text-white'>What it does today</h2>
                  <div className='grid grid-cols-1 gap-6 md:grid-cols-2'>
                    {REAL.map((item) => (
                      <div key={item.title} className='rounded-xl border border-gray-800 bg-gray-900/50 p-6'>
                        <h3 className='mb-2 text-lg font-bold text-white'>{item.title}</h3>
                        <p className='mb-0 leading-relaxed text-gray-300'>{item.body}</p>
                      </div>
                    ))}
                  </div>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>What it does not do</h2>
                  <p className='mb-4 text-gray-300'>
                    Stated because an introduction is the page a reader trusts most, and because these gaps change
                    what the system is suitable for:
                  </p>
                  <ul className='list-disc space-y-3 pl-6 text-gray-300'>
                    {NOT_YET.map((item) => (
                      <li key={item}>{item}</li>
                    ))}
                  </ul>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>A first call</h2>
                  <p className='mb-4 text-gray-300'>
                    A node serves its marketplace and inference APIs over HTTP as well as gRPC, so a browser, a dApp
                    front end or a script can drive it directly:
                  </p>
                  <CodeSample label='TypeScript' code={LIST_PROVIDERS} />
                  <p className='mt-4 text-gray-300'>
                    The SDK lives in <code className='text-white'>packages/sdk</code> in the repository and is not on
                    npm yet, so add it with{' '}
                    <code className='text-white'>yarn add file:/path/to/matrix-os/packages/sdk</code>. Amounts are{' '}
                    <code className='text-white'>bigint</code>: MATRIX has 9 decimals and a cap of a billion coins, so
                    a balance can exceed what a JavaScript number holds exactly.
                  </p>

                  <div className='mt-10 rounded-xl border border-primary-400/20 bg-primary-400/10 p-6'>
                    <h2 className='mb-4 text-2xl font-bold text-white'>Where to go next</h2>
                    <ul className='mb-0 list-disc space-y-3 pl-6 text-gray-100'>
                      <li>
                        <Link href='/docs/quickstart' className='text-accent-200 underline hover:text-accent-100'>
                          Quickstart
                        </Link>{' '}
                        - a node, a funded wallet and a settled compute job, in about a minute
                      </li>
                      <li>
                        <Link href='/docs/architecture' className='text-accent-200 underline hover:text-accent-100'>
                          Architecture
                        </Link>{' '}
                        - what is inside the process you just started
                      </li>
                      <li>
                        <Link
                          href='/docs/guides/network-setup'
                          className='text-accent-200 underline hover:text-accent-100'
                        >
                          Network setup
                        </Link>{' '}
                        - two nodes agreeing on one ledger
                      </li>
                      <li>
                        <a
                          href={GITHUB_URL}
                          target='_blank'
                          rel='noopener noreferrer'
                          className='text-accent-200 underline hover:text-accent-100'
                        >
                          The repository
                        </a>{' '}
                        - the code every claim on this page refers to
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
