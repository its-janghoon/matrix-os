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

const JOIN_CONFIG = `# on the JOINING node, before it starts:
network:
  listen_addr: /ip4/0.0.0.0/tcp/9000
  bootstrap_peers:
    - /ip4/<a running peer>/tcp/9000/p2p/<its peer id>
consensus:
  validators:                 # the GENESIS ids, NOT the set in force now
    - 689cf718481d3c13cf4e370526d5228d758e0c2c9b6f5eb3bbc0540c20eede31
    - 4b1d0c9a...
  epoch_length: 100           # must match every node
genesis:                      # must match the network's config EXACTLY
  reward_pool: 1000000000000000000
  # allocations: ...the same named allocations the network started with...`;

const JOIN_ADMIT = `# the joining node's startup log:
Consensus identity: 9f2c1e4b...   # <- give this hex id to the operators

# an existing operator, in their own config, once a quorum of them agree:
consensus:
  approved_changes:
    - add:9f2c1e4b...`;

const STAKE = `consensus:
  membership_mode: bonded-open
  participate_in_open_set: true
  stake:
    enabled: true
    min_bond: 1000000000000000    # positive admission floor; must match every node
    unbonding_period: 1000
    bond: 1000000000000000        # fund THIS node's printed consensus account`;

const ECONOMICS = `consensus:
  fee_basis_points: 100           # 1%, the code cap
  maintainer_account: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" # EXAMPLE; replace
  maintainer_fee_share_basis_points: 5000 # 50% of the fee, not 50% of transferred value
  rewards:
    per_block: 0                  # public launch: no provider token emissions`;

const ECONOMICS_LOG = `Consensus: a protocol fee of 100 basis points (1.00%) is taken from every value transfer.
Consensus: maintainer receives 5000 basis points of that fee (0.50% of transferred value).
Consensus: provider rewards are OFF; providers earn only user-paid MATRIX.

# a job priced at 10000, settled through consensus:
#   provider    +9900   (quoted amount less the 1% protocol fee)
#   maintainer    +50   (half of the fee)
#   validators    +50   (remainder, pro rata by voting power)
#   emissions       0`;

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

                  <div className='my-8 rounded-xl border border-semantic-processing/40 bg-semantic-processing/10 p-6'>
                    <h2 className='mb-2 text-xl font-bold text-white'>Generated baseline versus public overlay</h2>
                    <p className='mb-0 text-gray-100'>
                      <code className='text-white'>matrixd -init</code> creates a secure starting point, not a turnkey
                      production profile. Public operators must still set identical genesis and economics, exact
                      HTTPS CORS origins (never a wildcard), public reads, signed writes, positive 600/120 request
                      limits, a real 64-hex maintainer account, and the verified Base bridge target. Base Sepolia
                      rehearsal is chain 84532; Base production is 8453. See the configuration page for the overlay.
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
                  <p className='mt-4 text-gray-300'>
                    The peer id is <strong>stable across restarts</strong>. It is a second identity, separate from
                    the consensus one above, and it is persisted under{' '}
                    <code className='text-white'>p2p/identity/peer_key</code> in the same store. That is worth
                    knowing because it used to be generated fresh on every start: every multiaddr you had pasted
                    into another node&apos;s config went stale the moment this one restarted, and libp2p verifies the
                    id it dialed, so the only symptom was{' '}
                    <code className='text-white'>all dials failed</code> - indistinguishable from the typo above.
                    Keeping <code className='text-white'>storage.path</code> keeps both identities; deleting it
                    makes the node a different validator AND a different peer.
                  </p>
                  <p className='mt-4 text-gray-300'>
                    Bootstrap peers are also re-dialled, not dialled once. A node checks every 10 seconds whether
                    each configured peer is still connected and re-dials the ones that are not, so restarting one
                    node does not leave the others permanently disconnected from it. The node prints the exact
                    multiaddrs to give other operators on startup, so there is nothing to assemble by hand.
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

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>4. Changing an operator-approved set</h2>
                  <p className='mb-4 text-gray-300'>
                    This section describes <code className='text-white'>membership_mode: operator-approved</code>.
                    There the genesis set changes only after operators approve it. The generated and public launch
                    profile instead uses <code className='text-white'>bonded-open</code>: candidates self-sign
                    admission after their positive bond commits, and active validators self-sign voluntary exits.
                    In either mode, the set in force is committed chain state and resumes from history.
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

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>5. Joining a running network</h2>
                  <p className='mb-4 text-gray-300'>
                    Everything above starts nodes together from the same genesis. That is not how a network grows. A
                    node that arrives late has an <strong className='text-white'>empty store</strong>: it downloads
                    the chain from its bootstrap peers and replays it from height zero. Two fields in its config have
                    to be exactly right or it never gets off the ground, and the mistakes are quiet ones.
                  </p>
                  <CodeSample label='config.yaml' code={JOIN_CONFIG} />

                  <h3 className='mb-2 mt-8 text-xl font-bold text-white'>
                    consensus.validators must be the GENESIS set, not the current one
                  </h3>
                  <p className='mb-4 text-gray-300'>
                    This is the counterintuitive one. To accept the block at each height a replaying node checks that
                    its proposer was the leader for <em>that</em> height, evaluated against the validator set as it
                    stood <em>then</em>. So it needs the set the chain started from - the{' '}
                    <strong className='text-white'>genesis</strong> set - to validate early history. It then replays
                    the committed set changes and arrives at the current set by itself, exactly as an existing node
                    does across a restart.
                  </p>
                  <p className='mb-4 text-gray-300'>
                    Hand it the set <em>in force now</em> instead and it computes the wrong leader for height 0: the
                    proposer that actually led block 0 is not the leader under today&apos;s larger set, so it refuses
                    block 0, nothing after it can link, and the node sits at height 0 forever. There is no error that
                    names the real cause - it just never catches up. Configure the genesis ids and let the node
                    derive the rest.
                  </p>

                  <h3 className='mb-2 mt-8 text-xl font-bold text-white'>
                    genesis: must match the network&apos;s exactly
                  </h3>
                  <p className='mb-4 text-gray-300'>
                    The chain carries <strong className='text-white'>transactions</strong>, never the balances they
                    started from. Genesis allocations and the reward pool are applied from{' '}
                    <em>the node&apos;s own config</em> at first start, once, and the node then applies the
                    downloaded transactions on top. So a node whose <code className='text-white'>genesis</code>{' '}
                    differs from the network&apos;s ends up with <strong className='text-white'>the same blocks and
                    different balances</strong>: it agrees on history and disagrees about money, which is the worst
                    kind of disagreement because nothing looks broken until a balance is read. Copy the network&apos;s
                    genesis config verbatim - the same allocations and the same{' '}
                    <code className='text-white'>reward_pool</code>.
                  </p>

                  <h3 className='mb-2 mt-8 text-xl font-bold text-white'>Getting admitted as a validator</h3>
                  <p className='mb-4 text-gray-300'>
                    A correctly configured joining node first follows without voting while it replays history. In
                    bonded-open mode, fund its printed consensus account and set a bond at or above the network&apos;s
                    positive minimum; the candidate signs and gossips its own admission transaction after that bond
                    commits. No operator allow-list vote is required. In operator-approved mode, use the
                    approved-change flow from section 4 instead.
                  </p>
                  <CodeSample label='operator-approved join (legacy/private networks)' code={JOIN_ADMIT} />
                  <p className='mt-4 text-gray-300'>
                    <code className='text-white'>epoch_length</code> must match every node here too, for the reason
                    in section 3: it is when set changes take effect, and nodes that disagree about it would switch
                    sets at different heights and fork.
                  </p>

                  <div className='my-8 rounded-xl border border-semantic-processing/40 bg-semantic-processing/10 p-6'>
                    <h3 className='mb-2 text-lg font-bold text-white'>On a staked network, bond before admission</h3>
                    <p className='mb-0 text-gray-100'>
                      In bonded-open mode, a joining node cannot be admitted until its self-funded positive minimum
                      bond has committed. Fund its consensus account - the id it prints at startup - then set{' '}
                      <code className='text-white'>consensus.stake.bond</code>; the node signs the bond and admission
                      itself. In operator-approved mode the bond is still necessary when stake is enabled, and the
                      separate operator approval remains necessary too.
                    </p>
                  </div>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>6. Bonded stake</h2>
                  <p className='mb-4 text-gray-300'>
                    The generated bonded-open baseline turns this on. Voting power becomes an account&apos;s bonded
                    native MATRIX, admission requires a positive minimum bond, and a validator proven to have
                    equivocated loses that bond instead of only its place. A private operator-approved network can
                    choose equal voting power instead.
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
                    <h3 className='mb-2 text-lg font-bold text-white'>What bonded-open means</h3>
                    <p className='mb-0 text-gray-100'>
                      Admission and voluntary exit are permissionless only after the candidate&apos;s positive bond and
                      self-signed membership transaction commit. That prices identities but does not make EVM bridge
                      attestors dynamic: WrappedMatrix keeps the fixed committee selected at contract deployment,
                      independently of native validator joins and exits.
                    </p>
                  </div>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>7. Launch economics: fee, no emissions</h2>
                  <p className='mb-4 text-gray-300'>
                    The public overlay charges a 100-basis-point protocol fee, gives the configured maintainer 5000
                    basis points of that fee, and sets <code className='text-white'>rewards.per_block: 0</code>.
                    Providers publish MATRIX-denominated quotes and earn user payments; launch supply does not grow
                    through provider emissions.
                  </p>
                  <CodeSample label='config.yaml' code={ECONOMICS} />

                  <h3 className='mb-2 mt-8 text-xl font-bold text-white'>The protocol fee</h3>
                  <p className='mb-4 text-gray-300'>
                    A cut of every value transfer a committed block carries, paid to the validator set pro rata by
                    voting power. It is what makes a bond worth posting: without it, stake is a pure cost and no
                    third party has a reason to put capital at risk. Turn the two on together.
                  </p>
                  <p className='mb-4 text-gray-300'>
                    It comes <strong className='text-white'>out of the amount</strong>, so a transfer of 10,000 at 1%
                    credits the recipient 9,900 and the validators 100. Adding it to the sender instead would make a
                    transfer that was affordable when it was proposed unaffordable when it was applied, so a job
                    priced at exactly the buyer&apos;s balance would commit and then be skipped. Providers price for
                    the cut the way they would on any marketplace that takes one.
                  </p>
                  <p className='mb-4 text-gray-300'>
                    The rate is capped at <strong className='text-white'>100 basis points in code</strong>, not in
                    config validation. A build refuses to start on a higher number, so the worst a mistyped{' '}
                    <code className='text-white'>1000</code> can do is fail loudly rather than take ten times the
                    intended cut. Every node must agree on the rate: a node charging differently would compute
                    different balances from the same block.
                  </p>
                  <p className='mb-4 text-gray-300'>
                    It is paid to the set in force, not to the validators whose votes carried the block. That is not
                    a preference - the commit certificate a node observes is node-specific, so a split that depended
                    on it would give two honest nodes different balances. The cost of the choice is real and worth
                    knowing: it pays for stake rather than for participation, so a validator that never votes still
                    earns, and the answer to that is to remove it.
                  </p>

                  <h3 className='mb-2 mt-8 text-xl font-bold text-white'>Provider emissions are zero at launch</h3>
                  <p className='mb-4 text-gray-300'>
                    matrixd supports a fixed, supply-tracked reward-pool schedule for networks that explicitly choose
                    one, but the public overlay does not: <code className='text-white'>per_block: 0</code>. Providers
                    manually quote their final MATRIX price (or a MATRIX-denominated observed cost plus markup), and
                    a buyer&apos;s job snapshots that quote. Manual quotes expire and must be refreshed with{' '}
                    <code className='text-white'>matrix provider quote-update --id &lt;provider&gt; --price &lt;price&gt;</code>;
                    run or schedule it before expiry. Do not re-register to refresh: quote-update preserves available
                    capacity and active reservations. There is no stablecoin peg and no DEX oracle silently repricing
                    jobs.
                  </p>
                  <p className='mb-4 text-gray-300'>
                    A reservation keeps the exact price-per-unit, quote id/version, observation time and validity
                    window accepted by the buyer. A later provider refresh changes future jobs, not an existing
                    snapshot, and stale quotes are rejected before work starts.
                  </p>
                  <CodeSample label='node log' code={ECONOMICS_LOG} />

                  <div className='my-8 rounded-xl border border-semantic-processing/40 bg-semantic-processing/10 p-6'>
                    <h3 className='mb-2 text-lg font-bold text-white'>
                      The fee applies to every consensus-settled value transfer
                    </h3>
                    <p className='mb-0 text-gray-100'>
                      All value now moves through consensus. Marketplace settlement - a compute job, an inference
                      job - and a plain{' '}
                      <code className='text-white'>matrix wallet transfer</code> are all submitted as signed
                      transfers that a quorum orders into a committed block and every node applies to the same
                      ledger. So each of them pays the protocol fee the same way, and two nodes agree on the
                      resulting balances. There is no longer a path that moves MATRIX on one node without a quorum
                      or that escapes the fee.
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
