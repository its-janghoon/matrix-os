import { CodeSample } from '@/components/CodeSample';
import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';
import { GITHUB_URL, REPO } from '@/lib/releases';

export const metadata = {
  title: 'Quickstart',
  description:
    'Zero to a settled compute job on a local Matrix OS node: initialize, start, and run matrix quickstart. Every command here is real.',
};

/**
 * This page used to describe `matrix-os init`, an `agent.config.ts`, and a
 * `defineAgent` helper imported from an `@matrix-os/core` npm package. The
 * binary is `matrix`, there is no such config file, and that package does not
 * exist. What follows is the sequence that actually works, with the output a
 * real run prints.
 */
const START_NODE = `matrixd -init          # writes config.yaml (0600) with a generated admin key
matrixd                # starts the node`;

const RUN_QUICKSTART = `# the key is under security.api_keys in the config matrixd -init wrote
matrix --api-key <key> quickstart`;

const QUICKSTART_OUTPUT = `1. wallet created at ~/.matrix/wallet.json
   buyer account: c5b097824f2e2222a278af3dbe4b519fa653a96e44ff08891668b3a2a708d5d9

2. funded buyer with 1000000 from the reward pool
   buyer balance: 1000000

3. registered provider "quickstart-provider" (capacity 100, price 5/unit)

4. submitted job 807b0adf...: 10 units @ 5 = 50 (status pending)

5. completed job 807b0adf... (status completed)

done. native MATRIX moved buyer -> provider:
  buyer    c5b09782...  999950
  provider quickstart-provider  50
  job 807b0adf... settled for 50`;

const BY_HAND = `matrix wallet create                 # an ed25519 keypair at ~/.matrix/wallet.json
matrix wallet show                   # your account id

matrix --api-key <key> fund --account <your-account> --amount 1000000

matrix provider register --id gpu-1 --capacity 100 --price 5
matrix provider list

matrix job submit --buyer <your-account> --provider gpu-1 --units 10
matrix job complete --id <job-id>

matrix balance --account <your-account>
matrix tx list --limit 5`;

const FROM_CODE = `import { MatrixClient } from 'matrix-os-sdk';

const matrix = new MatrixClient({
  endpoint: 'http://127.0.0.1:9093',
  apiKey: process.env.MATRIX_API_KEY,
});

const provider = await matrix.registerProvider({ id: 'gpu-1', capacity: 100n, pricePerUnit: 5n });
const job = await matrix.submitJob({ buyer: myAccount, provider: provider.id, units: 10n });
const settled = await matrix.completeJob(job.id);

console.log(settled.status, settled.price); // JOB_STATUS_COMPLETED 50n`;

const INFERENCE = `matrix inference submit \\
  --buyer <your-account> \\
  --provider demo-inference-provider \\
  --model demo \\
  --prompt "one sentence about peer-to-peer compute"`;

export default function QuickstartPage() {
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
                    <h1 className='mb-4 text-4xl font-bold text-white'>Quickstart</h1>
                    <p className='text-xl text-gray-100'>
                      A node, a funded wallet, and a settled compute job. Three commands, about a minute.
                    </p>
                  </div>

                  <h2 className='mb-4 mt-8 text-3xl font-bold text-white'>Before you start</h2>
                  <p className='mb-4 text-gray-300'>
                    You need the two binaries, <code className='text-white'>matrixd</code> (the node) and{' '}
                    <code className='text-white'>matrix</code> (the CLI). Get them from{' '}
                    <a href='/download' className='text-accent-200 underline hover:text-accent-100'>
                      a release
                    </a>{' '}
                    or build from source with the{' '}
                    <a href='/docs/installation' className='text-accent-200 underline hover:text-accent-100'>
                      installation guide
                    </a>
                    . Nothing else: no GPU, no model server, no account with us.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>1. Start a node</h2>
                  <CodeSample label='shell' code={START_NODE} />
                  <p className='mt-4 text-gray-300'>
                    <code className='text-white'>-init</code> writes a config with access control on and one
                    generated admin key inside it, which is why the file is mode 0600. It also seeds a genesis reward
                    pool, so there is native MATRIX to fund an account with - the coins are moved out of that pool,
                    never minted.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>2. Zero to a settled job</h2>
                  <p className='mb-4 text-gray-300'>In another shell:</p>
                  <CodeSample label='shell' code={RUN_QUICKSTART} />
                  <p className='mb-4 mt-4 text-gray-300'>What it prints:</p>
                  <CodeSample label='output' code={QUICKSTART_OUTPUT} />
                  <p className='mt-4 text-gray-300'>
                    Five steps: a wallet, funding from the reward pool, a provider advertising capacity at a price, a
                    job that reserves that capacity, and a completion that moves 50 native MATRIX from buyer to
                    provider. Re-running is safe - the wallet is reused.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>3. The same thing, one step at a time</h2>
                  <p className='mb-4 text-gray-300'>
                    Every step of the demo is a command you can run yourself:
                  </p>
                  <CodeSample label='shell' code={BY_HAND} />
                  <p className='mt-4 text-gray-300'>
                    <code className='text-white'>matrix --help</code> lists the rest:{' '}
                    <code className='text-white'>job cancel</code>, <code className='text-white'>tx get</code>,{' '}
                    <code className='text-white'>status</code>, <code className='text-white'>health</code>, and the
                    wallet commands that sign a transfer locally so the node never sees a private key.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>4. Ask for an inference</h2>
                  <p className='mb-4 text-gray-300'>
                    A freshly initialized node registers a GPU-free echo backend as{' '}
                    <code className='text-white'>demo-inference-provider</code>, so the inference path works before
                    you have any models:
                  </p>
                  <CodeSample label='shell' code={INFERENCE} />

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>From code</h2>
                  <p className='mb-4 text-gray-300'>
                    The node serves the same marketplace and inference APIs over HTTP on port 9093, so a script, a
                    web app or a dApp front end can drive it directly:
                  </p>
                  <CodeSample label='TypeScript' code={FROM_CODE} />
                  <p className='mt-4 text-gray-300'>
                    The SDK is{' '}
                    <a
                      href={`${GITHUB_URL}/tree/main/packages/sdk`}
                      className='text-accent-200 underline hover:text-accent-100'
                      target='_blank'
                      rel='noopener noreferrer'
                    >
                      <code className='text-white'>packages/sdk</code>
                    </a>{' '}
                    in the {REPO} repository. It is not published to npm yet, so add it from a checkout with{' '}
                    <code className='text-white'>yarn add file:/path/to/matrix-os/packages/sdk</code>.
                  </p>

                  <div className='mt-10 rounded-xl border border-primary-400/20 bg-primary-400/10 p-6'>
                    <h2 className='mb-4 text-2xl font-bold text-white'>Next</h2>
                    <ul className='mb-0 list-disc space-y-3 pl-6 text-gray-100'>
                      <li>
                        <a href='/docs/architecture' className='text-accent-200 underline hover:text-accent-100'>
                          Architecture
                        </a>{' '}
                        - what is inside the node you just started
                      </li>
                      <li>
                        <a href='/docs/compute-marketplace' className='text-accent-200 underline hover:text-accent-100'>
                          Compute marketplace
                        </a>{' '}
                        - how a job is priced, reserved and settled
                      </li>
                      <li>
                        <a href='/docs/configuration' className='text-accent-200 underline hover:text-accent-100'>
                          Configuration
                        </a>{' '}
                        - every field in the file <code className='text-white'>-init</code> wrote
                      </li>
                      <li>
                        <a href='/docs/cli' className='text-accent-200 underline hover:text-accent-100'>
                          matrix CLI
                        </a>{' '}
                        - the full command surface
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
