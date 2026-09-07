import { CodeSample } from '@/components/CodeSample';
import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';

export const metadata = {
  title: 'Network setup',
  description:
    'Running more than one Matrix OS node: the two things that must match, how to find a node peer id and validator id, and what a set change costs today.',
};

/**
 * This page used to document a JSON config with network.discovery.interval, an
 * aes-256-gcm encryption block and a firewall rule list, none of which exist.
 * What follows is what it actually takes to get two nodes agreeing on one
 * ledger, including the part that is awkward.
 */
const NODE_A = `# node A
matrixd -init -config a.yaml
# edit a.yaml:
#   network.listen_addr: /ip4/0.0.0.0/tcp/9000
matrixd -config a.yaml`;

const A_OUTPUT = `Genesis applied: 0 named allocation(s), reward pool 1000000000000000000 native base units.
Consensus identity: 689cf718481d3c13cf4e370526d5228d758e0c2c9b6f5eb3bbc0540c20eede31
...`;

const NODE_B = `# node B, on another machine (or another port on this one)
matrixd -init -config b.yaml
# edit b.yaml:
#   network.listen_addr: /ip4/0.0.0.0/tcp/9001
#   network.bootstrap_peers:
#     - /ip4/<A's address>/tcp/9000/p2p/<A's peer id>
matrixd -config b.yaml`;

const VALIDATORS = `# in BOTH a.yaml and b.yaml, identically:
consensus:
  validators:
    - 689cf718481d3c13cf4e370526d5228d758e0c2c9b6f5eb3bbc0540c20eede31   # A
    - 4b1d0c9a...                                                        # B`;

const PORTS = `9000  libp2p        peers. Must be reachable by other nodes.
9090  gRPC admin    deploy, health, logs.
9091  gRPC market   providers, jobs, balances, transfers.
9092  gRPC inference
9093  HTTP          the same market and inference services, as JSON, for browsers.`;

export default function NetworkSetupPage() {
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
                    <h1 className='mb-4 text-4xl font-bold text-white'>Network setup</h1>
                    <p className='text-xl text-gray-100'>
                      Two things have to match for two nodes to agree on one ledger: they must be able to reach each
                      other, and they must list the same validator set.
                    </p>
                  </div>

                  <h2 className='mb-4 mt-8 text-3xl font-bold text-white'>1. Start the first node</h2>
                  <CodeSample label='shell' code={NODE_A} />
                  <p className='mb-4 mt-4 text-gray-300'>
                    Two lines of its output matter. The node prints its consensus identity, which is the id you put in
                    every node&apos;s validator list:
                  </p>
                  <CodeSample label='output' code={A_OUTPUT} />
                  <p className='mt-4 text-gray-300'>
                    That identity is generated on first start and persisted in the store at{' '}
                    <code className='text-white'>storage.path</code>. Keep that directory and the node keeps its
                    place in the set; delete it and the node becomes a different validator.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>2. Point the second node at the first</h2>
                  <CodeSample label='shell' code={NODE_B} />
                  <p className='mt-4 text-gray-300'>
                    A bootstrap entry is a full libp2p multiaddr including the peer id, which is the{' '}
                    <code className='text-white'>/p2p/...</code> suffix. A failed dial is logged and does not stop
                    the node, so a typo here looks like silence rather than an error - check the log.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>3. Give both the same validator set</h2>
                  <CodeSample label='config.yaml' code={VALIDATORS} />
                  <p className='mt-4 text-gray-300'>
                    This is the step that decides whether you have one network or two. Every node derives its
                    round-robin leader schedule from this list, so nodes with different lists disagree about who may
                    propose and will never commit each other&apos;s blocks - even though they are connected and
                    gossiping. A node&apos;s own identity is always added to its set, which is why an empty list is a
                    working single-validator node.
                  </p>
                  <p className='mt-4 text-gray-300'>
                    A quorum is more than two thirds of the set. Three validators tolerate none failing, four
                    tolerate one; the useful sizes start at four.
                  </p>

                  <div className='my-8 rounded-xl border border-semantic-processing/40 bg-semantic-processing/10 p-6'>
                    <h3 className='mb-2 text-lg font-bold text-white'>The set is static today</h3>
                    <p className='mb-0 text-gray-100'>
                      Changing it means editing every node&apos;s config and restarting them. There is no on-chain
                      validator set, no staking and no eviction, so adding a validator is an operational task
                      coordinated out of band, and a compromised validator is removed by an operator rather than by
                      the protocol. That is the next substantial piece of work on the consensus layer; until it
                      lands, treat the validator set as a deployment decision rather than a runtime one.
                    </p>
                  </div>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>Ports</h2>
                  <CodeSample label='listeners' code={PORTS} />
                  <p className='mt-4 text-gray-300'>
                    Only 9000 needs to be reachable by other nodes. The four API ports are for operators and clients:
                    expose them deliberately, and with{' '}
                    <code className='text-white'>security.enable_acls</code> on - with it off, anyone who can reach
                    9091 can spend the balances this node holds keys for.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>Checking it worked</h2>
                  <ul className='list-disc space-y-3 pl-6 text-gray-300'>
                    <li>
                      <code className='text-white'>matrix --addr &lt;node&gt;:9091 --api-key &lt;key&gt; status</code>{' '}
                      on each node.
                    </li>
                    <li>
                      Submit and complete a job against one node, then read the balance from the other. If both agree,
                      they are applying the same committed ledger. If one lags, it is catching up - a node that
                      missed a block fetches it from a peer along with the quorum that committed it.
                    </li>
                    <li>
                      <code className='text-white'>matrix tx list</code> from each node should return the same
                      transactions in the same order.
                    </li>
                  </ul>

                  <div className='mt-10 rounded-xl border border-primary-400/20 bg-primary-400/10 p-6'>
                    <h2 className='mb-4 text-2xl font-bold text-white'>Next</h2>
                    <ul className='mb-0 list-disc space-y-3 pl-6 text-gray-100'>
                      <li>
                        <a href='/docs/configuration' className='text-accent-200 underline hover:text-accent-100'>
                          Configuration
                        </a>{' '}
                        - every field in both files
                      </li>
                      <li>
                        <a href='/products/consensus' className='text-accent-200 underline hover:text-accent-100'>
                          Consensus
                        </a>{' '}
                        - what the quorum is actually doing
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
