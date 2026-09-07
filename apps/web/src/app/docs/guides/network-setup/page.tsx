import { CodeSample } from '@/components/CodeSample';
import { ValidatorSetChange } from '@/components/diagrams';
import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';

export const metadata = {
  title: 'Network setup',
  description:
    'Running more than one Matrix OS node: the two things that must match, how the validator set changes while the network is running, and what turning on bonded stake does.',
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
    - 4b1d0c9a...                                                        # B
  epoch_length: 100`;

const SET_CHANGE = `# on node A, and on enough other nodes to make a quorum:
consensus:
  approved_changes:
    - add:9f2c1e4b...        # C's consensus identity, from its own startup log

# and to eject one:
consensus:
  approved_changes:
    - remove:4b1d0c9a...`;

const SET_CHANGE_LOG = `consensus: committed set change at height 41: add:9f2c1e4b... (takes effect at the next epoch)
consensus: validator set change in force at height 100: add:9f2c1e4b...
consensus: validator set is now 3 members, quorum 3`;

const VETO_LOG = `consensus: refusing to vote for a block that would add:9f2c1e4b... (not in consensus.approved_changes)`;

const STAKE = `consensus:
  stake:
    enabled: true
    min_bond: 1000000000000000    # 1,000,000 MATRIX at 9 decimals; null = this default
    unbonding_period: 1000        # blocks to wait after leaving before withdrawing
    bond: 2000000000000000        # what THIS node keeps bonded from its own account`;

const STAKE_LOG = `Consensus identity: 0c2265316242995a96890c698af0e1959264bb6b1f805ed7352fe6174f6fcabb
Consensus: bonded stake is ON. Voting power is bonded MATRIX; a validator must bond at least
  1000 base units to be admitted, and waits 4 blocks after leaving to withdraw.
Consensus: this node will keep 5000 base units bonded from its own account 0c226531...

# before its account is funded:
consensus: this node wants 5000 more bonded but its consensus account (0c226531...) holds
  nothing; fund that account before it can validate on a staked network

# after:
consensus: bonding 5000 native base units (bonded 0, target 5000)
consensus: validator set is now 1 members, total power 5000, quorum 3334`;

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

                  <p className='mt-4 text-gray-300'>
                    <code className='text-white'>epoch_length</code> must match too. It is how many blocks pass
                    between validator-set changes taking effect, and nodes that disagree about it would switch sets
                    at different heights - see below.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>4. Changing the set later</h2>
                  <p className='mb-4 text-gray-300'>
                    The list above is the <em>genesis</em> set. Once the chain has changed the set, each node resumes
                    the set the chain arrived at and ignores this field, so a config that still says the set is empty
                    is not a problem. Adding or removing a validator does not need a coordinated restart:
                  </p>

                  <ValidatorSetChange />

                  <p className='mb-4 text-gray-300'>
                    An operator lists a change in their own config. There is no CLI step - the node offers the
                    changes its operator has approved and keeps re-offering them until they commit, so operators can
                    edit their configs one at a time:
                  </p>
                  <CodeSample label='config.yaml' code={SET_CHANGE} />
                  <p className='mb-4 mt-4 text-gray-300'>
                    On a node that has approved it, the change commits and then waits:
                  </p>
                  <CodeSample label='node log' code={SET_CHANGE_LOG} />
                  <p className='mt-4 text-gray-300'>
                    Two properties are worth being explicit about. First, the approval list is a{' '}
                    <strong className='text-white'>veto</strong>: a node prevotes nil on a block carrying a change it
                    has not approved, so a change needs a quorum of <em>operators</em> to have approved it. One
                    validator cannot propose the removal of all the others and have it wave through.
                  </p>
                  <CodeSample label='node log' code={VETO_LOG} />
                  <p className='mt-4 text-gray-300'>
                    Second, a committed change takes effect at an <strong className='text-white'>epoch boundary</strong>,
                    a height that is a multiple of <code className='text-white'>epoch_length</code>, not at the
                    moment it commits. That is what keeps every node&apos;s leader schedule identical: applying a
                    change as each node happened to reach the block would have nodes disagreeing about who may
                    propose, which is a fork. It also means a change is not instant - with the default{' '}
                    <code className='text-white'>epoch_length: 100</code> it lands within 100 blocks.
                  </p>
                  <p className='mt-4 text-gray-300'>
                    A validator caught <strong className='text-white'>equivocating</strong> - two votes for different
                    blocks at one height, round and phase, both signed by its own key - is removed the same way, but
                    without any config entry. The evidence proves itself, so every node verifies it rather than
                    trusting the peer that relayed it, and there is no operator judgement left to make. Set{' '}
                    <code className='text-white'>consensus.eject_equivocators: false</code> if you would rather
                    investigate an offence yourself; detection, recording and gossip carry on either way.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>5. Bonded stake</h2>
                  <p className='mb-4 text-gray-300'>
                    Off by default. With it on, three things change: voting power becomes an account&apos;s bonded
                    native MATRIX, admission requires a minimum bond, and a validator proven to have equivocated
                    loses that bond instead of only its place.
                  </p>
                  <CodeSample label='config.yaml' code={STAKE} />
                  <p className='mt-4 text-gray-300'>
                    A bond is a balance in a reserved account,{' '}
                    <code className='text-white'>consensus/stake/bond/&lt;account id&gt;</code>, on the same ledger
                    everything else settles on. So bonding conserves supply, bonded coins leave the spendable
                    balance, and &quot;how much is bonded&quot; is a balance you can read. Bonding has to be signed
                    by the validator&apos;s own key, which lives inside the node, which is why{' '}
                    <code className='text-white'>bond</code> is a config field rather than a command: the node bonds
                    the shortfall itself and keeps topping it up. Fund its consensus account first - the id is the
                    one it prints at startup - or it says so and does nothing:
                  </p>
                  <CodeSample label='node log' code={STAKE_LOG} />

                  <p className='mt-4 text-gray-300'>
                    Why weight the quorum at all: a quorum counted in HEADS can be bought for the price of a few
                    minimum bonds under a few identities, because identities are free and only the bond is not.
                    Weighting by stake prices an attack at two thirds of everything bonded however many identities it
                    is spread across.
                  </p>

                  <div className='my-8 rounded-xl border border-semantic-processing/40 bg-semantic-processing/10 p-6'>
                    <h3 className='mb-2 text-lg font-bold text-white'>Everyone bonds, or nobody is weighted</h3>
                    <p className='mb-0 text-gray-100'>
                      While ANY validator has bonded nothing, the whole set stays at equal power and the node says
                      so. That is deliberate: an unbonded member counts as 1, so weighting a partly-bonded set would
                      hand essentially the entire voting power to whoever bonded first - and it could not even be
                      slashed, because a slash needs a quorum it would then control. Staying at headcount until the
                      last validator has posted its bond makes the switch atomic. A validator that refuses to bond
                      holds the network at headcount, which is the status quo and something its peers can answer by
                      removing it.
                    </p>
                  </div>

                  <p className='mt-4 text-gray-300'>
                    Withdrawing is gated: a sitting validator may not withdraw at all, and after leaving the set it
                    waits <code className='text-white'>unbonding_period</code> blocks. Without that delay a validator
                    could equivocate, be ejected, and pull its bond out before the network committed the slash - so
                    the delay has to be long enough for evidence to be gossiped, voted on and committed. A withdrawal
                    submitted early is not rejected, it simply waits in the mempool and lands by itself.
                  </p>

                  <div className='my-8 rounded-xl border border-semantic-processing/40 bg-semantic-processing/10 p-6'>
                    <h3 className='mb-2 text-lg font-bold text-white'>What stake does not make this</h3>
                    <p className='mb-0 text-gray-100'>
                      Membership is still a decision a quorum of operators makes, not one anyone can buy into: a bond
                      is necessary to be admitted and it is not sufficient. There are no rewards for staking either -
                      nothing pays a validator per block - so a bond earns nothing and only stands to be lost. That
                      is the honest state of it: stake here is a deterrent, not yet a yield.
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
