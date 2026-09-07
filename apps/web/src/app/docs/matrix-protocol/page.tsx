import { CodeSample } from '@/components/CodeSample';
import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';

export const metadata = {
  title: 'Matrix Protocol',
  description:
    'What Matrix OS nodes actually say to each other: the gossip topics, the consensus messages, and the certificates that make a commit provable.',
};

/**
 * This page used to show `new MatrixProtocol({...})` imported from an
 * `@matrix-os/core` npm package that does not exist. The protocol is not a
 * JavaScript object; it is a set of gossip topics and signed messages. That is
 * what is documented here, from the code in
 * services/core/internal/consensus and internal/marketexchange.
 */
const CONSENSUS_TOPICS = `matrix.consensus.v2/proposal        a leader putting a block to the vote
matrix.consensus.v2/vote            a validator's prevote or precommit
matrix.consensus.v2/sync-request    "I am at height H, send me what I missed"
matrix.consensus.v2/sync-response   committed blocks plus the quorum that committed them
matrix.consensus.v2/head            a validator's committed height, once a second`;

const MARKET_TOPICS = `matrix/market/announce/v1   provider capacity and price
matrix/market/jobs/v1      job records
matrix/market/settle/v1    settlement`;

const BLOCK = `Block {
  height          uint64   // position in the committed chain
  round           uint64   // the round this value first appeared at
  prev_block_hash bytes    // links to the committed head
  txs             []Transaction
  proposer_id     string   // the validator that created it
  signature       bytes    // by that validator, over the canonical bytes
}`;

const PROPOSAL = `Proposal {
  block        Block
  round        uint64            // the round being proposed FOR, >= block.round
  proposer_id  string            // the leader of that round
  signature    bytes
  justify      PolkaCertificate  // present when locked validators must switch
}`;

const VOTE = `Vote {
  type        PREVOTE | PRECOMMIT
  height      uint64
  round       uint64
  block_hash  bytes   // 32 zero bytes means "no block this round"
  voter_id    string
  public_key  bytes
  signature   bytes
}`;

const CERT = `PolkaCertificate {
  height      uint64
  round       uint64
  block_hash  bytes
  votes       []Vote   // a quorum of PREVOTES, each verified independently
}`;

export default function MatrixProtocolPage() {
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
                    <h1 className='mb-4 text-4xl font-bold text-white'>Matrix Protocol</h1>
                    <p className='text-xl text-gray-100'>
                      What nodes say to each other. Gossip topics, signed messages, and the certificates that make a
                      commit something you can check rather than something you are told.
                    </p>
                  </div>

                  <h2 className='mb-4 mt-8 text-3xl font-bold text-white'>Transport</h2>
                  <p className='mb-4 text-gray-300'>
                    libp2p, with gossipsub topics. There is no broker and no registry server: a node discovers peers
                    and subscribes. Messages are JSON on the wire and every one that can move value carries an
                    ed25519 signature over a canonical, length-prefixed encoding, so no two distinct field
                    combinations can produce the same signed bytes.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>Topics</h2>
                  <CodeSample label='consensus' code={CONSENSUS_TOPICS} />
                  <p className='mt-4 text-gray-300'>
                    The version in the topic name is load-bearing. v2 introduced two voting phases and the proposal
                    envelope, and a v1 node cannot act on a v2 message: sharing a topic across that boundary would
                    look like a network fault rather than a version boundary, so the versions simply do not meet.
                  </p>
                  <CodeSample className='mt-6' label='marketplace' code={MARKET_TOPICS} />

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>Messages</h2>

                  <h3 className='mb-3 mt-8 text-xl font-bold text-white'>Block</h3>
                  <p className='mb-4 text-gray-300'>
                    Immutable once built, and that matters: round, proposer and signature are all part of its hash, so
                    a validator that re-proposes a block at a later round has to forward these exact bytes. Re-signing
                    would change the hash, which would make it a different block, which every validator locked on the
                    original would correctly refuse.
                  </p>
                  <CodeSample label='Block' code={BLOCK} />

                  <h3 className='mb-3 mt-8 text-xl font-bold text-white'>Proposal</h3>
                  <p className='mb-4 text-gray-300'>
                    The envelope around a block. It exists so the same value can be proposed more than once: the
                    round being proposed for and the leader proposing it live here, not in the block.
                  </p>
                  <CodeSample label='Proposal' code={PROPOSAL} />

                  <h3 className='mb-3 mt-8 text-xl font-bold text-white'>Vote</h3>
                  <p className='mb-4 text-gray-300'>
                    Two phases. A prevote says what a validator sees this round; a quorum of prevotes for one block is
                    a polka. On seeing a polka a validator precommits that block, and the precommit is what locks it.
                    A quorum of precommits commits. A vote for 32 zero bytes is a nil vote - "I support no block this
                    round" - which is how a round concludes on evidence instead of on a timeout.
                  </p>
                  <CodeSample label='Vote' code={VOTE} />

                  <h3 className='mb-3 mt-8 text-xl font-bold text-white'>PolkaCertificate</h3>
                  <p className='mb-4 text-gray-300'>
                    The evidence a leader shows to unlock the others. A validator locked on one block will prevote a
                    different one only when shown a quorum of prevotes for it at a round at least as high as its lock.
                    A precommit quorum is deliberately not accepted here: that is a commit certificate, and a node
                    holding one commits the block rather than voting on it.
                  </p>
                  <CodeSample label='PolkaCertificate' code={CERT} />

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>Catching up</h2>
                  <p className='mb-4 text-gray-300'>
                    Gossip is best effort, so a node can miss the one proposal its height committed on. The votes
                    keep arriving and can even reach a quorum, but a quorum without the body cannot commit, and no
                    leader re-proposes a committed block. That is why <code className='text-white'>sync-request</code>{' '}
                    and <code className='text-white'>sync-response</code> exist: a peer serves the committed block
                    together with the precommit quorum that committed it, and the receiver verifies those votes
                    itself. A served block commits under exactly the same rule as a proposed one, so the sync path
                    adds no new trust.
                  </p>
                  <p className='mb-4 text-gray-300'>
                    <code className='text-white'>head</code> announcements exist for the quiet case: a node that was
                    down while blocks committed, coming back to a network with no traffic, would otherwise never
                    learn it was behind. Only validators announce, so the cost tracks the validator set rather than
                    the size of the network.
                  </p>

                  <div className='mt-10 rounded-xl border border-primary-400/20 bg-primary-400/10 p-6'>
                    <h2 className='mb-4 text-2xl font-bold text-white'>Next</h2>
                    <ul className='mb-0 list-disc space-y-3 pl-6 text-gray-100'>
                      <li>
                        <a href='/products/consensus' className='text-accent-200 underline hover:text-accent-100'>
                          Consensus
                        </a>{' '}
                        - the same protocol as a picture
                      </li>
                      <li>
                        <a href='/docs/architecture' className='text-accent-200 underline hover:text-accent-100'>
                          Architecture
                        </a>{' '}
                        - which subsystem owns which topic
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
