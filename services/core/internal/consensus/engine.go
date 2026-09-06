package consensus

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
	"github.com/ecirlabs/matrix-core/internal/transport"
)

// Transport is the subset of internal/transport.Transport the Engine needs. It
// is an interface so tests can wire N engines to an in-memory gossip bus while
// production passes the real gossipsub transport. It mirrors the abstraction
// internal/marketexchange uses.
type Transport interface {
	// Subscribe joins topic and returns a channel of received messages. The
	// channel is closed when ctx is cancelled.
	Subscribe(ctx context.Context, topic string) (<-chan transport.Message, error)
	// Publish sends data to topic. The topic must have been joined (via
	// Subscribe) first, matching the real transport's join-before-publish rule.
	Publish(ctx context.Context, topic string, data []byte) error
}

// Default timings. Speed is the priority, so the round period is short: a leader
// proposes promptly and, on collecting a quorum, commits immediately (there is
// no separate commit round). RoundTimeout bounds how long a node waits for a
// height to commit before advancing the round, which rotates the leader so
// progress continues even if the current leader is silent.
const (
	// DefaultProposeInterval is how often the leader (re)proposes / the driver
	// re-evaluates the current height. Kept small for low latency.
	DefaultProposeInterval = 15 * time.Millisecond
	// DefaultRoundTimeout is how long a node waits at a height before bumping the
	// round (rotating the leader). It is a small multiple of the propose interval.
	DefaultRoundTimeout = 150 * time.Millisecond
	// DefaultMaxBlockTxs bounds how many transactions a single block carries.
	DefaultMaxBlockTxs = 512
	// DefaultHeadAnnounceInterval is how often a node announces its committed
	// chain length so peers that are behind can notice and ask for the
	// difference. It is far longer than a round: this is a slow background
	// heartbeat, not part of the commit path.
	DefaultHeadAnnounceInterval = time.Second
	// maxRoundTimeoutFactor caps how far the per-round timeout backoff can
	// stretch the base timeout. Without a cap a height that keeps failing would
	// eventually stop making progress at all; without a backoff it never settles
	// (see roundTimeoutForLocked).
	maxRoundTimeoutFactor = 20
	// maxFutureStash bounds how many heights ahead a lagging node buffers
	// proposals/votes for, so a slow node's catch-up buffers cannot grow
	// unbounded.
	maxFutureStash = 64
)

// CommitObserver is notified after each block is committed and applied. It is
// optional and used mainly by tests to observe progress deterministically.
type CommitObserver func(b *Block)

// Config configures an Engine.
type Config struct {
	// Transport is the gossip transport (required).
	Transport Transport
	// Validators is the fixed validator set (required).
	Validators *ValidatorSet
	// Chain is the committed-block ledger (required).
	Chain *BlockChain
	// Ledger is the market ledger committed transactions are applied to
	// (required). Applying moves credits deterministically per committed block.
	Ledger *market.Ledger
	// Self is this node's validator account. When non-nil and its ID is in the
	// validator set, the node proposes (when leader) and votes. When nil the node
	// is a passive follower: it still receives proposals/votes, commits on quorum,
	// and applies blocks, but never proposes or votes itself.
	Self *token.Account
	// ProposeInterval overrides DefaultProposeInterval when > 0.
	ProposeInterval time.Duration
	// RoundTimeout overrides DefaultRoundTimeout when > 0.
	RoundTimeout time.Duration
	// MaxBlockTxs overrides DefaultMaxBlockTxs when > 0.
	MaxBlockTxs int
	// HeadAnnounceInterval overrides DefaultHeadAnnounceInterval when > 0.
	HeadAnnounceInterval time.Duration
	// OnCommit, when non-nil, is invoked after each block commits+applies.
	OnCommit CommitObserver
}

// Engine is a fast, leader-based BFT consensus engine over a fixed validator
// set. It runs a driver goroutine that, per height, lets the round leader
// propose a block of pending transactions, collects votes, and commits on
// quorum, then advances. Receive loops for proposals and votes follow the
// transport goroutine + ctx-cancellation pattern used by marketexchange.
type Engine struct {
	transport   Transport
	validators  *ValidatorSet
	chain       *BlockChain
	ledger      *market.Ledger
	self        *token.Account
	selfID      string
	isValidator bool

	proposeInterval      time.Duration
	roundTimeout         time.Duration
	maxBlockTxs          int
	headAnnounceInterval time.Duration
	onCommit             CommitObserver

	mu sync.Mutex
	// mempool holds submitted-but-not-yet-committed transactions in submission
	// order, keyed by a stable dedup key so a duplicate submission is ignored.
	mempool    []token.Transaction
	mempoolSet map[string]struct{}
	// committedTxs remembers the dedup key of every transaction already committed
	// to a block, providing permanent replay protection: a re-submitted or
	// leader-replayed transaction that already committed is rejected/skipped so it
	// can never be applied twice. It is the consensus analogue of token.Chain's
	// per-sender nonce gate.
	committedTxs map[string]struct{}
	// appliedTxs records, per committed transaction dedup key, whether the
	// transfer actually MOVED credits (true) or was deterministically skipped at
	// apply time because the sender could not afford it (false). It lets a caller
	// distinguish "committed and paid" from "committed but skipped", which is what
	// a settlement API needs to avoid reporting an unfunded payment as complete.
	appliedTxs map[string]bool
	// settleWaiters are channels to close when a specific committed tx key becomes
	// known (applied or skipped), so a caller can block until its settlement is
	// finalised rather than polling. Keyed by tx dedup key.
	settleWaiters map[string][]chan struct{}
	// height is the next height to commit (equals committed chain length).
	height uint64
	// round is the current round for the current height. It starts at 0 for each
	// height and increments on RoundTimeout to rotate the leader.
	round uint64
	// headHash is the committed head hash the next block links to.
	headHash []byte
	// roundDeadline is when the current (height,round) times out and the round is
	// bumped.
	roundDeadline time.Time
	// proposals caches the block a node has seen for the current height, keyed by
	// block hash (hex), so late votes for a known block can be tallied and
	// committed.
	proposals map[string]*Block
	// votes tallies distinct voter IDs per block hash (hex) for the current
	// height, aggregated across all rounds. A block keeps the same hash when
	// re-proposed at a higher round, so votes accumulate toward one quorum per
	// block regardless of the round they were cast in.
	votes map[string]map[string]struct{}
	// roundVotes retains the actual signed Vote messages seen for the current
	// height, keyed by round -> blockHashHex -> voterID -> Vote. It is the
	// evidence a leader draws on to build a PolkaCertificate justifying a
	// higher-round proposal, and lets the engine detect when a block reached a
	// quorum (a "polka") at a specific round.
	roundVotes map[uint64]map[string]map[string]Vote
	// votedRound / votedHash record the (round, block hash) this node last voted
	// for at the current height. They enforce the vote-once-per-round rule (never
	// cast two votes in the same round) together with the lock below.
	votedRound uint64
	votedHash  string
	hasVoted   bool
	// lockedRound / lockedHash record the highest round at which this node voted
	// for a block at the current height, and that block's hash. This is the LOCK:
	// once locked, the node will not vote for a DIFFERENT block at the same height
	// unless it sees a valid PolkaCertificate proving that different block reached
	// a quorum at a round >= lockedRound (i.e. the network provably moved on). The
	// lock is what makes leader rotation safe: two conflicting blocks can never
	// both gather a quorum at the same height.
	lockedRound uint64
	lockedHash  string
	locked      bool
	// committedThisHeight guards against double-commit at a height.
	committing bool
	// futureProposals stashes proposals for heights ahead of ours (this node is
	// lagging) so we can adopt them on catch-up. Keyed by "height:hash".
	futureProposals map[string]*Block
	// futureVotes stashes votes for heights ahead of ours so we can tally them the
	// moment we reach that height. Keyed by height -> blockHashHex -> voterID ->
	// Vote. The whole signed vote is kept, not just the voter's identity, so a
	// node that commits a height purely from buffered votes can still hand that
	// evidence to a peer asking for the same block (see CommittedBlock).
	futureVotes map[uint64]map[string]map[string]Vote
	// lastSyncRequest is when this node last broadcast a BlockSyncRequest. It
	// rate-limits the ask to one per round timeout so a stalled node is
	// persistent without flooding the topic.
	lastSyncRequest time.Time
	// lastHeadAnnounce is when this node last announced its committed height.
	lastHeadAnnounce time.Time
	// peerHeight is the greatest committed height any peer has announced. Above
	// our own height it is direct evidence that we are behind, which the other
	// signals only reveal while the network is busy.
	peerHeight uint64

	wg      sync.WaitGroup
	started bool
}

// New constructs an Engine from cfg. It does not start goroutines; call Start.
func New(cfg Config) (*Engine, error) {
	if cfg.Transport == nil {
		return nil, fmt.Errorf("consensus: transport is required")
	}
	if cfg.Validators == nil || cfg.Validators.Len() == 0 {
		return nil, fmt.Errorf("consensus: %w", ErrEmptyValidatorSet)
	}
	if cfg.Chain == nil {
		return nil, fmt.Errorf("consensus: chain is required")
	}
	if cfg.Ledger == nil {
		return nil, fmt.Errorf("consensus: ledger is required")
	}
	e := &Engine{
		transport:            cfg.Transport,
		validators:           cfg.Validators,
		chain:                cfg.Chain,
		ledger:               cfg.Ledger,
		self:                 cfg.Self,
		proposeInterval:      orDurationC(cfg.ProposeInterval, DefaultProposeInterval),
		roundTimeout:         orDurationC(cfg.RoundTimeout, DefaultRoundTimeout),
		maxBlockTxs:          orIntC(cfg.MaxBlockTxs, DefaultMaxBlockTxs),
		headAnnounceInterval: orDurationC(cfg.HeadAnnounceInterval, DefaultHeadAnnounceInterval),
		onCommit:             cfg.OnCommit,
		mempoolSet:           make(map[string]struct{}),
		committedTxs:         make(map[string]struct{}),
		appliedTxs:           make(map[string]bool),
		settleWaiters:        make(map[string][]chan struct{}),
		proposals:            make(map[string]*Block),
		votes:                make(map[string]map[string]struct{}),
		roundVotes:           make(map[uint64]map[string]map[string]Vote),
		futureProposals:      make(map[string]*Block),
		futureVotes:          make(map[uint64]map[string]map[string]Vote),
	}
	if cfg.Self != nil {
		e.selfID = cfg.Self.AccountID()
		e.isValidator = cfg.Validators.Contains(e.selfID)
	}
	return e, nil
}

func orDurationC(v, d time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return d
}

func orIntC(v, d int) int {
	if v > 0 {
		return v
	}
	return d
}

// Start subscribes to the proposal and vote topics, launches a receive loop per
// topic, and starts the driver loop. The provided ctx governs every goroutine:
// cancelling ctx closes the subscription channels and ends all loops, so a
// caller only needs to cancel ctx (and optionally Wait) to shut down.
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return fmt.Errorf("consensus: already started")
	}
	e.started = true
	// Initialise height/head from the persisted committed chain so a restarted
	// engine resumes cleanly.
	head, length, err := e.chain.Head()
	if err != nil {
		e.mu.Unlock()
		return err
	}
	e.height = length
	e.headHash = head
	e.round = 0
	e.roundDeadline = time.Now().Add(e.roundTimeoutForLocked(e.round))
	e.mu.Unlock()

	// Rehydrate the committed-tx dedup set from the persisted chain so a restarted
	// engine still rejects replays of transactions committed before the restart.
	for h := uint64(0); h < length; h++ {
		b, err := e.chain.BlockAt(h)
		if err != nil {
			return err
		}
		e.mu.Lock()
		for i := range b.Txs {
			e.committedTxs[mempoolKey(&b.Txs[i])] = struct{}{}
		}
		e.mu.Unlock()
	}

	proposalCh, err := e.transport.Subscribe(ctx, TopicProposal)
	if err != nil {
		return fmt.Errorf("consensus: subscribe proposal: %w", err)
	}
	voteCh, err := e.transport.Subscribe(ctx, TopicVote)
	if err != nil {
		return fmt.Errorf("consensus: subscribe vote: %w", err)
	}
	syncReqCh, err := e.transport.Subscribe(ctx, TopicSyncRequest)
	if err != nil {
		return fmt.Errorf("consensus: subscribe sync request: %w", err)
	}
	syncRespCh, err := e.transport.Subscribe(ctx, TopicSyncResponse)
	if err != nil {
		return fmt.Errorf("consensus: subscribe sync response: %w", err)
	}
	headCh, err := e.transport.Subscribe(ctx, TopicHead)
	if err != nil {
		return fmt.Errorf("consensus: subscribe head: %w", err)
	}

	e.wg.Add(6)
	go e.runLoop(ctx, proposalCh, e.handleProposal)
	go e.runLoop(ctx, voteCh, e.handleVote)
	go e.runLoop(ctx, syncReqCh, e.handleSyncRequest)
	go e.runLoop(ctx, syncRespCh, e.handleSyncResponse)
	go e.runLoop(ctx, headCh, e.handleHeadAnnounce)
	go e.driver(ctx)
	return nil
}

// Wait blocks until all loops have exited (after the Start ctx is cancelled).
func (e *Engine) Wait() { e.wg.Wait() }

// runLoop consumes messages until ctx is cancelled or ch is closed, mirroring
// the marketexchange cancellation pattern.
func (e *Engine) runLoop(ctx context.Context, ch <-chan transport.Message, handle func(context.Context, transport.Message)) {
	defer e.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			handle(ctx, msg)
		}
	}
}

// Submit adds a signed transaction to the mempool so it can be included in a
// future block. It verifies the signature up front so a badly-signed tx is
// rejected at the door and never reaches consensus. Duplicate submissions (same
// sender + nonce + signature) are ignored. Any node may accept submissions; the
// tx propagates to the leader implicitly because clients submit to nodes and,
// for a multi-node deployment, tx gossip or client fan-out feeds leaders. In the
// in-process test bus, transactions are submitted directly to whichever node(s)
// the test chooses, and the leader among them proposes them.
func (e *Engine) Submit(tx *token.Transaction) error {
	if err := tx.Verify(); err != nil {
		return err
	}
	if tx.To == "" {
		return fmt.Errorf("%w: recipient must not be empty", ErrInvalidMessage)
	}
	key := mempoolKey(tx)
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.committedTxs[key]; ok {
		// Already committed: a replay of a finalized transaction. Ignore silently
		// so a well-meaning re-submit is a no-op while a malicious replay cannot
		// double-apply.
		return nil
	}
	if _, ok := e.mempoolSet[key]; ok {
		return nil
	}
	e.mempoolSet[key] = struct{}{}
	e.mempool = append(e.mempool, *tx)
	return nil
}

// mempoolKey is a stable dedup key for a transaction: sender + nonce +
// signature hex. Two submissions of the same signed tx collapse to one.
func mempoolKey(tx *token.Transaction) string {
	return fmt.Sprintf("%s:%d:%x", tx.SenderID(), tx.Nonce, tx.Signature)
}

// driver is the round engine. It ticks at the propose interval; on each tick it
// (a) if this node is the leader for the current height/round and has pending
// transactions, proposes a block, and (b) if the round has timed out without a
// commit, bumps the round to rotate the leader. Commits happen in handleVote
// when a quorum is reached; the driver's job is to propose and to keep rounds
// advancing so a silent leader cannot stall the chain.
func (e *Engine) driver(ctx context.Context) {
	defer e.wg.Done()
	ticker := time.NewTicker(e.proposeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.maybeAnnounceHead(ctx)
			e.maybeRequestSync(ctx)
			e.tick(ctx)
		}
	}
}

// roundTimeoutForLocked returns how long to wait at the given round before
// rotating the leader: the base timeout plus half of it per round elapsed,
// capped at maxRoundTimeoutFactor times the base.
//
// A FIXED timeout livelocks under load. Every round rotates the leader, and a
// new leader proposes a new block, so if the timeout is shorter than the time a
// proposal actually needs to reach a quorum - which is what happens when the
// machine is loaded, the network is slow, or the block is large - each round is
// abandoned before it can commit and votes scatter across a succession of
// competing blocks. The cluster then rotates forever without committing
// anything, at full speed. Backing the timeout off per round is the standard
// answer (Tendermint's timeoutPropose + timeoutProposeDelta * round): the
// rounds keep rotating while the leader is genuinely silent, but as soon as the
// problem is slowness rather than a dead leader, the window grows past what a
// commit needs and the height settles.
//
// This only ever affects liveness. Safety across rotation is the lock rule's
// job, and it does not depend on any timing.
func (e *Engine) roundTimeoutForLocked(round uint64) time.Duration {
	if round > maxRoundTimeoutFactor {
		round = maxRoundTimeoutFactor
	}
	backoff := e.roundTimeout + time.Duration(round)*(e.roundTimeout/2)
	if max := maxRoundTimeoutFactor * e.roundTimeout; backoff > max {
		backoff = max
	}
	return backoff
}

// tick performs one driver step under the lock, then publishes any produced
// proposal outside the lock.
func (e *Engine) tick(ctx context.Context) {
	e.mu.Lock()

	// Round timeout: rotate the leader if this height has not committed in time.
	// Bumping the round permits one fresh vote in the new round (vote-once is
	// per-round); the LOCK is deliberately preserved across the bump so a node
	// still refuses to vote for a conflicting block without a justifying polka.
	if time.Now().After(e.roundDeadline) {
		e.round++
		e.roundDeadline = time.Now().Add(e.roundTimeoutForLocked(e.round))
		e.hasVoted = false
	}

	// Only the leader proposes, only if this node is a validator with a key.
	if !e.isValidator || !e.validators.IsLeader(e.selfID, e.round) {
		e.mu.Unlock()
		return
	}
	// Nothing to do without pending transactions (empty blocks add no value and
	// only churn the chain; speed-first means we commit real work promptly and
	// stay quiet otherwise).
	if len(e.mempool) == 0 {
		e.mu.Unlock()
		return
	}
	// Avoid re-proposing an identical block we already proposed for this
	// height/round: if we already have a proposal cached for the current height
	// authored by us this round, skip.
	block := e.buildBlockLocked()
	e.mu.Unlock()

	if block == nil {
		return
	}
	// Ingest our own proposal (so we tally our own vote) and broadcast it, then
	// vote for it.
	e.ingestProposal(ctx, block)
}

// buildBlockLocked constructs a signed block proposal for the current
// height/round from the mempool. Callers must hold e.mu. It returns nil when
// this node cannot propose. The proposal is signed by e.self.
//
// Voting-discipline rule for the leader: if this node is LOCKED on a block at
// the current height (it voted for it in an earlier round), it MUST re-propose
// that exact locked block rather than a fresh one, unless it can attach a polka
// certificate for a different block at a round >= its lock (proving the network
// moved on). This keeps rotation safe: a leader cannot orphan a value that a
// quorum may already have locked. When re-proposing the locked block at a higher
// round it re-signs it for the new round and attaches the best certificate it
// holds so locked followers can verify and vote.
func (e *Engine) buildBlockLocked() *Block {
	// If locked, prefer re-proposing the locked block (unless a higher polka lets
	// us switch — handled by bestPolkaLocked returning a certificate for another
	// block at a round >= lockedRound).
	if e.locked {
		best := e.bestPolkaLocked()
		if best == nil || best.Round < e.lockedRound || string(best.BlockHash) == e.lockedHash {
			// No justification to switch away from the locked block: re-propose it.
			if lb, ok := e.proposals[e.lockedHash]; ok {
				reproposed := *lb
				reproposed.Round = e.round
				reproposed.ProposerID = e.selfID
				reproposed.Justify = e.bestPolkaLocked()
				if err := reproposed.Sign(e.self.PrivateKey); err != nil {
					return nil
				}
				return &reproposed
			}
		}
	}

	txs := make([]token.Transaction, 0, len(e.mempool))
	for i := range e.mempool {
		if len(txs) >= e.maxBlockTxs {
			break
		}
		// Never propose a transaction that already committed (defensive: the
		// mempool should not contain it, but guard against races).
		if _, done := e.committedTxs[mempoolKey(&e.mempool[i])]; done {
			continue
		}
		txs = append(txs, e.mempool[i])
	}
	if len(txs) == 0 {
		return nil
	}

	b := &Block{
		Height:        e.height,
		Round:         e.round,
		PrevBlockHash: append([]byte(nil), e.headHash...),
		Txs:           txs,
		ProposerID:    e.selfID,
	}
	// A round > 0 proposal for a fresh block must justify why it is safe to
	// abandon any earlier-round value: attach the highest polka certificate we
	// hold. If we hold none but are proposing a fresh block at round > 0, the
	// proposal is unjustified and locked followers will not vote for it; that is
	// acceptable (it simply will not reach quorum) and preserves safety.
	if e.round > 0 {
		b.Justify = e.bestPolkaLocked()
	}
	if err := b.Sign(e.self.PrivateKey); err != nil {
		return nil
	}
	return b
}

// bestPolkaLocked returns a PolkaCertificate for the highest round at the
// current height at which some block reached a quorum of votes, or nil if no
// block has been polka'd yet. Callers must hold e.mu. The certificate carries
// the actual signed votes so a receiver can verify it independently.
func (e *Engine) bestPolkaLocked() *PolkaCertificate {
	quorum := e.validators.Quorum()
	var (
		bestRound = uint64(0)
		bestHash  string
		found     bool
	)
	for round, byHash := range e.roundVotes {
		for hkey, voters := range byHash {
			if len(voters) < quorum {
				continue
			}
			if !found || round > bestRound {
				bestRound = round
				bestHash = hkey
				found = true
			}
		}
	}
	if !found {
		return nil
	}
	voters := e.roundVotes[bestRound][bestHash]
	votes := make([]Vote, 0, len(voters))
	var blockHash []byte
	for _, v := range voters {
		votes = append(votes, v)
		blockHash = v.BlockHash
	}
	return &PolkaCertificate{
		Height:    e.height,
		Round:     bestRound,
		BlockHash: append([]byte(nil), blockHash...),
		Votes:     votes,
	}
}

// ingestProposal records a locally-built proposal, publishes it, and casts this
// node's vote for it. It is the leader-side path that mirrors what followers do
// on receiving a proposal in handleProposal.
func (e *Engine) ingestProposal(ctx context.Context, b *Block) {
	if err := e.acceptProposal(b); err != nil {
		return
	}
	// Broadcast the proposal.
	if data, err := json.Marshal(&Proposal{Block: *b}); err == nil {
		_ = e.transport.Publish(ctx, TopicProposal, data)
	}
	// Vote for our own proposal.
	e.castVote(ctx, b)
}

// handleProposal validates a received proposal and, if valid for the current
// height, caches it and casts this node's vote. Invalid or stale proposals are
// dropped silently (gossip is best-effort).
func (e *Engine) handleProposal(ctx context.Context, msg transport.Message) {
	var p Proposal
	if err := json.Unmarshal(msg.Payload, &p); err != nil {
		return
	}
	b := p.Block
	if err := e.acceptProposal(&b); err != nil {
		// We could not use the proposal at our current height. If it is for a
		// FUTURE height (we are lagging behind the network), stash it so we can
		// adopt it the moment we catch up, then re-gossip it so other lagging nodes
		// also receive it. This is the catch-up path that guarantees a node which
		// missed a committing proposal still obtains the block body.
		e.stashFutureProposal(ctx, &b, msg.Payload)
		return
	}
	e.castVote(ctx, &b)
}

// stashFutureProposal remembers a proposal for a height greater than ours so it
// can be replayed once we advance to that height, and re-gossips it once so the
// body keeps propagating to other lagging nodes. Proposals for heights we have
// already passed, or malformed ones, are ignored.
func (e *Engine) stashFutureProposal(ctx context.Context, b *Block, raw []byte) {
	e.mu.Lock()
	if b.Height <= e.height || b.Height > e.height+maxFutureStash {
		e.mu.Unlock()
		return
	}
	hkey := fmt.Sprintf("%d:%x", b.Height, b.Hash())
	if _, seen := e.futureProposals[hkey]; seen {
		e.mu.Unlock()
		return
	}
	cp := *b
	e.futureProposals[hkey] = &cp
	e.mu.Unlock()

	// Re-gossip once to help other lagging nodes obtain the body. A block handed
	// to us by block sync has no original proposal bytes to echo, and the peer
	// that served it is already answering requests, so there is nothing to
	// re-gossip in that case.
	if len(raw) > 0 {
		_ = e.transport.Publish(ctx, TopicProposal, raw)
	}
}

// drainFutureProposals adopts any stashed proposals that are now for the current
// height, casting a vote for each. Callers must NOT hold e.mu.
func (e *Engine) drainFutureProposals(ctx context.Context) {
	e.mu.Lock()
	h := e.height
	var ready []*Block
	for k, b := range e.futureProposals {
		if b.Height < h {
			delete(e.futureProposals, k)
			continue
		}
		if b.Height == h {
			ready = append(ready, b)
			delete(e.futureProposals, k)
		}
	}
	e.mu.Unlock()
	for _, b := range ready {
		if err := e.acceptProposal(b); err == nil {
			e.castVote(ctx, b)
		}
	}
}

// verifyBlockForHeightLocked checks everything about a block that does not
// depend on this node's voting state: it is for the height we are trying to
// commit, it links to our committed head, its proposer is the validator who
// leads its round, its proposer signature verifies, and every transaction in it
// is individually valid, unique within the block and not a replay of one
// already committed.
//
// It is shared by the two ways a body can arrive - a leader's proposal and a
// block-sync response - so both are held to the same standard. What it
// deliberately does NOT do is touch the lock/justification rules: those govern
// whether this node may VOTE, which a synced block never asks it to do.
// Callers must hold e.mu.
func (e *Engine) verifyBlockForHeightLocked(b *Block) error {
	// Only consider blocks for the height we are currently trying to commit.
	if b.Height != e.height {
		return fmt.Errorf("%w: block height %d != current %d", ErrInvalidMessage, b.Height, e.height)
	}
	// Prev-hash must link to our committed head.
	if len(b.PrevBlockHash) != HashSize || string(b.PrevBlockHash) != string(e.headHash) {
		return ErrPrevHashMismatch
	}
	// Proposer must be a validator and the correct leader for the block's round.
	pub, ok := e.validators.PublicKey(b.ProposerID)
	if !ok {
		return ErrNotValidator
	}
	if !e.validators.IsLeader(b.ProposerID, b.Round) {
		return ErrWrongLeader
	}
	if err := b.VerifySignature(pub); err != nil {
		return err
	}
	// Every transaction must be individually validly signed. A single bad tx
	// invalidates the whole block, so a malicious leader cannot smuggle a forged
	// transfer past honest voters.
	seenInBlock := make(map[string]struct{}, len(b.Txs))
	for i := range b.Txs {
		if err := b.Txs[i].Verify(); err != nil {
			return fmt.Errorf("%w: tx %d: %v", ErrInvalidMessage, i, err)
		}
		if b.Txs[i].To == "" {
			return fmt.Errorf("%w: tx %d empty recipient", ErrInvalidMessage, i)
		}
		key := mempoolKey(&b.Txs[i])
		// Reject a block that replays an already-committed transaction: an honest
		// validator will not vote for it, so a malicious leader cannot double-apply
		// a finalized transfer.
		if _, done := e.committedTxs[key]; done {
			return fmt.Errorf("%w: tx %d already committed (replay)", ErrInvalidMessage, i)
		}
		// Reject a block that contains the same transaction twice.
		if _, dup := seenInBlock[key]; dup {
			return fmt.Errorf("%w: tx %d duplicated within block", ErrInvalidMessage, i)
		}
		seenInBlock[key] = struct{}{}
	}
	return nil
}

// acceptProposal verifies a block for the current height and caches it. It
// returns an error (which the caller uses to drop the message) when the block is
// not acceptable. Verification: proposer is a validator, proposer is the correct
// leader for the block's round, proposer signature verifies, the block links to
// our committed head, and every transaction is individually validly signed.
func (e *Engine) acceptProposal(b *Block) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.verifyBlockForHeightLocked(b); err != nil {
		return err
	}

	hash := b.Hash()
	hkey := fmt.Sprintf("%x", hash)

	// Voting-discipline / lock rule. This is the heart of the safety fix: an
	// honest validator that has locked a block at the current height will NOT
	// accept (and therefore will not vote for) a proposal for a DIFFERENT block at
	// this height unless the proposal carries a valid PolkaCertificate proving
	// that different block reached a quorum at a round >= our locked round. That
	// certificate is only obtainable if a quorum voted for the other block, which
	// (by quorum intersection) cannot happen for a block conflicting with one that
	// already committed. So two conflicting blocks can never both reach quorum at
	// one height, and no node commits a block another node has ruled out.
	if e.locked && hkey != e.lockedHash {
		if b.Justify == nil {
			return fmt.Errorf("%w: locked on %s, proposal %s carries no justification", ErrInvalidMessage, e.lockedHash, hkey)
		}
		if err := b.Justify.Verify(e.validators); err != nil {
			return fmt.Errorf("%w: invalid justification: %v", ErrInvalidMessage, err)
		}
		if b.Justify.Height != e.height {
			return fmt.Errorf("%w: justification for wrong height", ErrInvalidMessage)
		}
		if b.Justify.Round < e.lockedRound {
			return fmt.Errorf("%w: justification round %d < locked round %d", ErrInvalidMessage, b.Justify.Round, e.lockedRound)
		}
		// The justification must endorse the block being proposed (a leader cannot
		// wave an unrelated certificate to unlock followers onto a third block).
		if fmt.Sprintf("%x", b.Justify.BlockHash) != hkey {
			return fmt.Errorf("%w: justification does not endorse the proposed block", ErrInvalidMessage)
		}
		// Valid, higher-or-equal-round polka for this different block: release the
		// lock so we may vote for it. (advanceHeight resets the lock per height.)
		e.locked = false
	}

	// If a proposal carries a justification, ingest its votes so we too can build
	// certificates and detect polkas across rounds even if we missed the raw vote
	// gossip.
	if b.Justify != nil {
		if err := b.Justify.Verify(e.validators); err == nil && b.Justify.Height == e.height {
			for i := range b.Justify.Votes {
				e.recordRoundVoteLocked(&b.Justify.Votes[i])
			}
		}
	}

	// Cache the block for this height so votes can be tallied against it. Adopt
	// the proposal's round so our own subsequent votes/leadership checks align
	// with the block we are voting for.
	if _, seen := e.proposals[hkey]; !seen {
		cp := *b
		cp.Justify = nil // do not retain certificates in the cached body
		e.proposals[hkey] = &cp
	}
	if b.Round > e.round {
		e.round = b.Round
		e.roundDeadline = time.Now().Add(e.roundTimeoutForLocked(e.round))
		// A new round permits one fresh vote; clear the per-round guard while
		// keeping the lock intact.
		e.hasVoted = false
	}
	return nil
}

// recordRoundVoteLocked stores a signed vote in the per-round evidence map for
// the current height so it can later back a PolkaCertificate. Callers must hold
// e.mu. Votes for other heights are ignored.
func (e *Engine) recordRoundVoteLocked(v *Vote) {
	if v.Height != e.height {
		return
	}
	byHash := e.roundVotes[v.Round]
	if byHash == nil {
		byHash = make(map[string]map[string]Vote)
		e.roundVotes[v.Round] = byHash
	}
	hkey := fmt.Sprintf("%x", v.BlockHash)
	set := byHash[hkey]
	if set == nil {
		set = make(map[string]Vote)
		byHash[hkey] = set
	}
	if _, ok := set[v.VoterID]; !ok {
		set[v.VoterID] = *v
	}
}

// castVote signs and broadcasts this node's vote for block b, and also tallies
// the vote locally (which may itself trigger a commit if quorum is already
// present, e.g. in a single-validator set). A non-validator node does not vote
// but still tallies received votes toward a commit.
//
// The voting discipline is enforced here, under the lock, so no two goroutines
// can slip two votes past it:
//
//   - Vote-once-per-round: at most one vote per (height, round). A second vote
//     in the same round is refused.
//   - Lock: a node never votes for a block that differs from the one it is
//     locked on at the current height. acceptProposal only releases the lock when
//     it has verified a quorum polka for the new block at a round >= the lock, so
//     by the time we get here the block is safe to vote for. Casting the vote
//     (re)locks the node onto this block at this round.
//
// Together these guarantee an honest validator never contributes its vote to two
// conflicting blocks at the same height, which is exactly what quorum
// intersection needs to make double-commit (divergence) impossible.
func (e *Engine) castVote(ctx context.Context, b *Block) {
	if !e.isValidator {
		return
	}
	hash := b.Hash()
	hkey := fmt.Sprintf("%x", hash)

	e.mu.Lock()
	if b.Height != e.height {
		e.mu.Unlock()
		return
	}
	// Vote-once-per-round: refuse a second, distinct vote within the same round.
	if e.hasVoted && e.votedRound == b.Round {
		if e.votedHash == hkey {
			// Idempotent re-vote for the same block/round: allow the tally + publish
			// (gossip may need the echo) but do not change lock state.
		} else {
			e.mu.Unlock()
			return
		}
	}
	// Lock: never vote for a block different from the one we are locked on. The
	// lock is only released in acceptProposal after verifying a higher-or-equal
	// round polka for the new block, so reaching here with a different hash while
	// locked should not happen; guard defensively.
	if e.locked && hkey != e.lockedHash {
		e.mu.Unlock()
		return
	}

	v := &Vote{
		Height:    b.Height,
		Round:     b.Round,
		BlockHash: hash,
		VoterID:   e.selfID,
		PublicKey: e.self.PublicKey,
	}
	if err := v.Sign(e.self.PrivateKey); err != nil {
		e.mu.Unlock()
		return
	}
	// Record that we voted, and (re)lock onto this block at this round. The lock
	// round only advances, so a stale re-proposal cannot lower it.
	e.hasVoted = true
	e.votedRound = b.Round
	e.votedHash = hkey
	if !e.locked || b.Round >= e.lockedRound {
		e.locked = true
		e.lockedRound = b.Round
		e.lockedHash = hkey
	}
	e.mu.Unlock()

	// Tally our own vote first so a single-node / already-quorate set commits
	// without waiting for the network to echo our vote back.
	e.tallyVote(v)
	if data, err := json.Marshal(v); err == nil {
		_ = e.transport.Publish(ctx, TopicVote, data)
	}
	e.maybeCommit(ctx)
}

// handleVote validates a received vote and tallies it, committing if the tally
// reaches quorum.
func (e *Engine) handleVote(ctx context.Context, msg transport.Message) {
	var v Vote
	if err := json.Unmarshal(msg.Payload, &v); err != nil {
		return
	}
	if err := v.Verify(); err != nil {
		return
	}
	// The voter must be a validator (only validator votes count toward quorum).
	if !e.validators.Contains(v.VoterID) {
		return
	}
	e.tallyVote(&v)
	e.maybeCommit(ctx)
}

// tallyVote records a distinct validator vote for a block hash at the current
// height. Votes for other heights are ignored. Callers need not hold the lock;
// tallyVote takes it.
func (e *Engine) tallyVote(v *Vote) {
	e.mu.Lock()
	defer e.mu.Unlock()
	hkey := fmt.Sprintf("%x", v.BlockHash)
	switch {
	case v.Height == e.height:
		set := e.votes[hkey]
		if set == nil {
			set = make(map[string]struct{})
			e.votes[hkey] = set
		}
		set[v.VoterID] = struct{}{}
		// Retain the raw vote per round so a leader can later assemble a
		// PolkaCertificate proving this block reached a quorum at this round.
		e.recordRoundVoteLocked(v)
	case v.Height > e.height && v.Height <= e.height+maxFutureStash:
		// We are lagging: buffer the vote so it counts the moment we reach that
		// height, so a node that fell behind still collects the quorum that let the
		// rest of the network commit.
		byHash := e.futureVotes[v.Height]
		if byHash == nil {
			byHash = make(map[string]map[string]Vote)
			e.futureVotes[v.Height] = byHash
		}
		set := byHash[hkey]
		if set == nil {
			set = make(map[string]Vote)
			byHash[hkey] = set
		}
		if _, ok := set[v.VoterID]; !ok {
			set[v.VoterID] = *v
		}
	}
}

// maybeCommit checks whether any known block for the current height has reached
// the vote quorum and, if so, commits and applies it, then advances to the next
// height. It commits at most one block per call. The apply step runs
// deterministically so every honest node reaches identical balances.
func (e *Engine) maybeCommit(ctx context.Context) {
	e.mu.Lock()
	if e.committing {
		e.mu.Unlock()
		return
	}
	quorum := e.validators.Quorum()
	var winner *Block
	for hkey, voters := range e.votes {
		if len(voters) < quorum {
			continue
		}
		b, ok := e.proposals[hkey]
		if !ok {
			// We have quorum for a block we have not seen the body of yet; wait for
			// the proposal to arrive (gossip may reorder). It will be re-evaluated on
			// the next vote/tick.
			continue
		}
		winner = b
		break
	}
	if winner == nil {
		// A quorum for a block whose body we never received is the stall block
		// sync exists for: the votes will keep arriving but the body never will,
		// because the rest of the network has moved on and will not re-propose it.
		// Ask for it.
		stuck := e.quorumWithoutBodyLocked()
		e.mu.Unlock()
		if stuck {
			e.maybeRequestSync(ctx)
		}
		return
	}
	e.committing = true
	block := *winner
	endorsements := e.endorsementsLocked(fmt.Sprintf("%x", block.Hash()))
	e.mu.Unlock()

	// Commit to the hash-linked chain and apply to the ledger. Both are done
	// while NOT holding e.mu (they take their own locks) to avoid lock-ordering
	// issues, but committing is serialised by the committing flag so no two
	// commits race at the same height.
	if err := e.commitAndApply(&block, endorsements); err != nil {
		// A commit failure (e.g. height/prev-hash mismatch because another path
		// already advanced) simply means this height is already handled; clear the
		// committing flag and reconcile state to the chain head.
		e.reconcileToChainHead()
		return
	}

	e.advanceHeight(&block)

	if e.onCommit != nil {
		e.onCommit(&block)
	}

	// Catch-up: adopt any proposal we buffered for the height we just entered and
	// vote for it, so a node that fell behind commits the next block as soon as it
	// has the body; combined with the buffered votes seeded in advanceHeight this
	// lets a lagging node converge to the same chain without a separate sync
	// protocol.
	e.drainFutureProposals(ctx)

	// Re-check: buffered votes seeded for the new height may already form a quorum
	// for a block whose body we already hold, allowing an immediate chained commit.
	e.maybeCommit(ctx)

	// Pipelining: immediately propose for the next height if we are its leader and
	// have pending work, rather than waiting for the next driver tick. This keeps
	// latency low across back-to-back blocks. tick takes its own locks.
	e.tick(ctx)
}

// endorsementsLocked collects the distinct signed votes this node holds for the
// given block hash at the current height, across every round. They are the
// evidence that carried the block to quorum here, and are persisted with it so
// this node can later prove the commit to a peer that missed the body. Callers
// must hold e.mu.
func (e *Engine) endorsementsLocked(hkey string) []Vote {
	seen := make(map[string]struct{})
	var out []Vote
	for _, byHash := range e.roundVotes {
		for _, v := range byHash[hkey] {
			if _, dup := seen[v.VoterID]; dup {
				continue
			}
			seen[v.VoterID] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

// quorumWithoutBodyLocked reports whether some block at the current height has
// reached the vote quorum while this node still lacks its body. That is the
// permanent-stall condition: nothing in the ordinary flow will ever deliver
// that body again. Callers must hold e.mu.
func (e *Engine) quorumWithoutBodyLocked() bool {
	quorum := e.validators.Quorum()
	for hkey, voters := range e.votes {
		if len(voters) < quorum {
			continue
		}
		if _, ok := e.proposals[hkey]; !ok {
			return true
		}
	}
	return false
}

// maybeRequestSync broadcasts a request for the committed block at this node's
// height when there is evidence the network has moved past it: votes or
// proposals buffered for later heights, or a quorum at our own height whose
// body never arrived. It is rate limited to one request per round timeout, so a
// stalled node keeps asking until it recovers without flooding the topic.
//
// A node that is simply idle (nothing buffered, no quorum outstanding) never
// asks, so a healthy network carries no sync traffic at all.
func (e *Engine) maybeRequestSync(ctx context.Context) {
	e.mu.Lock()
	behind := e.peerHeight > e.height ||
		len(e.futureVotes) > 0 ||
		len(e.futureProposals) > 0 ||
		e.quorumWithoutBodyLocked()
	if !behind {
		e.mu.Unlock()
		return
	}
	if !e.lastSyncRequest.IsZero() && time.Since(e.lastSyncRequest) < e.roundTimeout {
		e.mu.Unlock()
		return
	}
	e.lastSyncRequest = time.Now()
	req := BlockSyncRequest{Height: e.height, RequesterID: e.selfID}
	e.mu.Unlock()

	if data, err := json.Marshal(&req); err == nil {
		_ = e.transport.Publish(ctx, TopicSyncRequest, data)
	}
}

// maybeAnnounceHead publishes this node's committed height once per announce
// interval, so a peer that is behind on an idle network can tell.
//
// Only validators announce. Every node listens and every node answers sync
// requests, but the announcement is a broadcast heartbeat, so letting the whole
// network emit one would scale its cost with the number of participants rather
// than with the fixed validator set. A follower learns it is behind from the
// validators' announcements, which is the same information.
func (e *Engine) maybeAnnounceHead(ctx context.Context) {
	if !e.isValidator {
		return
	}
	e.mu.Lock()
	if !e.lastHeadAnnounce.IsZero() && time.Since(e.lastHeadAnnounce) < e.headAnnounceInterval {
		e.mu.Unlock()
		return
	}
	e.lastHeadAnnounce = time.Now()
	ann := HeadAnnounce{Height: e.height, NodeID: e.selfID}
	e.mu.Unlock()

	if data, err := json.Marshal(&ann); err == nil {
		_ = e.transport.Publish(ctx, TopicHead, data)
	}
}

// handleHeadAnnounce records a peer's committed height and, when it is ahead of
// ours, asks for the blocks we are missing.
//
// The announcement is not trusted for anything but the decision to ask: a peer
// claiming a huge height cannot move this node's chain, because every block it
// then serves has to verify and reach a quorum on its own merits. The worst a
// liar achieves is making us send requests, which the rate limit bounds.
func (e *Engine) handleHeadAnnounce(ctx context.Context, msg transport.Message) {
	var ann HeadAnnounce
	if err := json.Unmarshal(msg.Payload, &ann); err != nil {
		return
	}
	e.mu.Lock()
	if ann.Height > e.peerHeight {
		e.peerHeight = ann.Height
	}
	ahead := ann.Height > e.height
	e.mu.Unlock()

	if ahead {
		e.maybeRequestSync(ctx)
	}
}

// handleSyncRequest answers a peer asking for committed blocks from a height we
// have. It serves a batch starting at the requested height, each block with the
// votes that endorsed it, bounded by MaxSyncBatch and MaxSyncResponseBytes. A
// node that has nothing to offer stays silent rather than answering emptily.
//
// Every node that holds the height answers, which is deliberate: the requester
// only needs one response to arrive, and on a lossy network the redundancy is
// what makes recovery reliable. Duplicate responses are cheap to discard - a
// block below the receiver's height is dropped without work.
func (e *Engine) handleSyncRequest(ctx context.Context, msg transport.Message) {
	var req BlockSyncRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return
	}
	// Our own broadcast echoes back to us; there is nothing to serve ourselves.
	if req.RequesterID != "" && req.RequesterID == e.selfID {
		return
	}
	length, err := e.chain.Len()
	if err != nil || req.Height >= length {
		return
	}

	var resp BlockSyncResponse
	size := 0
	for h := req.Height; h < length && len(resp.Blocks) < MaxSyncBatch; h++ {
		b, err := e.chain.BlockAt(h)
		if err != nil {
			break
		}
		votes, err := e.chain.CommitVotes(h)
		if err != nil {
			// The body alone is still useful to a peer that holds the votes.
			votes = nil
		}
		cb := CommittedBlock{Block: *b, Votes: votes}
		enc, err := json.Marshal(&cb)
		if err != nil {
			break
		}
		if len(resp.Blocks) > 0 && size+len(enc) > MaxSyncResponseBytes {
			break
		}
		resp.Blocks = append(resp.Blocks, cb)
		size += len(enc)
	}
	if len(resp.Blocks) == 0 {
		return
	}
	if data, err := json.Marshal(&resp); err == nil {
		_ = e.transport.Publish(ctx, TopicSyncResponse, data)
	}
}

// handleSyncResponse applies a served batch in ascending height order.
func (e *Engine) handleSyncResponse(ctx context.Context, msg transport.Message) {
	var resp BlockSyncResponse
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		return
	}
	if len(resp.Blocks) > MaxSyncBatch {
		return
	}
	for i := range resp.Blocks {
		e.applySyncedBlock(ctx, &resp.Blocks[i])
	}
}

// applySyncedBlock ingests one served block: it tallies the accompanying votes
// through the same verified path ordinary vote gossip takes, caches the body for
// the current height (or stashes it for a later one), and then re-runs the
// ordinary commit check.
//
// Nothing here is a new commit rule. The block commits only when the tally
// reaches the same quorum a proposed block needs, over votes that each verified
// individually against the validator set and endorse this exact body. A
// response with no votes, too few votes, or votes for a different block leaves
// this node exactly where it was. Caching a body is not voting for it, so this
// path cannot make a node contribute to a conflicting block - it can only let a
// node commit what a quorum has already endorsed.
func (e *Engine) applySyncedBlock(ctx context.Context, cb *CommittedBlock) {
	b := &cb.Block
	hash := b.Hash()

	for i := range cb.Votes {
		v := &cb.Votes[i]
		if v.Height != b.Height || !bytesEqual(v.BlockHash, hash) {
			continue
		}
		if !e.validators.Contains(v.VoterID) {
			continue
		}
		if err := v.Verify(); err != nil {
			continue
		}
		e.tallyVote(v)
	}

	if err := e.acceptSyncedBlock(b); err != nil {
		// Not for our current height (or not valid against it). If it is ahead of
		// us, stash it so the catch-up path adopts it once we get there.
		e.stashFutureProposal(ctx, b, nil)
		return
	}
	e.maybeCommit(ctx)
}

// acceptSyncedBlock verifies a block served by block sync against the current
// height and caches its body so a quorum can commit it.
//
// It runs the same validation a proposal gets but casts no vote and touches no
// lock state: a synced block is already-agreed history being backfilled, not a
// proposal asking for this node's endorsement. In particular a node LOCKED on a
// different block at this height still accepts the body, because refusing it
// would be refusing to learn what the network committed - the lock exists to
// stop this node voting for a conflicting block, which caching never does.
func (e *Engine) acceptSyncedBlock(b *Block) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.verifyBlockForHeightLocked(b); err != nil {
		return err
	}
	hkey := fmt.Sprintf("%x", b.Hash())
	if _, seen := e.proposals[hkey]; !seen {
		cp := *b
		cp.Justify = nil
		e.proposals[hkey] = &cp
	}
	return nil
}

// commitAndApply persists the block to the committed chain and deterministically
// applies its ordered transactions to the market ledger. Each transaction moves
// credits from sender to recipient; a transaction whose sender cannot afford it
// is skipped (it committed to the ordered log but moves no credits), so every
// node applies the identical deterministic result. Because the transaction set
// and order are fixed by the committed block, every honest node computes the
// same balances.
func (e *Engine) commitAndApply(b *Block, endorsements []Vote) error {
	if _, err := e.chain.Commit(b, endorsements); err != nil {
		return err
	}
	// Apply transfers atomically as one critical section on the ledger, matching
	// token.SettledLedger's safe pattern (affordability check + transfer under the
	// ledger write lock). Determinism: iterate in block order; skip unaffordable
	// transfers uniformly. Record per-tx whether the transfer actually applied so
	// a settlement caller can tell "paid" from "skipped".
	applied := make(map[string]bool, len(b.Txs))
	if err := e.ledger.Atomically(func(ltx market.LedgerTx) error {
		for i := range b.Txs {
			tx := &b.Txs[i]
			sender := tx.SenderID()
			bal, err := ltx.Balance(sender)
			if err != nil {
				return err
			}
			if bal < tx.Amount {
				// Deterministically skip: insufficient funds at apply time. Every node
				// sees the same prior balances (same committed prefix), so every node
				// makes the same skip decision.
				applied[mempoolKey(tx)] = false
				continue
			}
			if err := ltx.Transfer(sender, tx.To, tx.Amount); err != nil {
				return err
			}
			applied[mempoolKey(tx)] = true
		}
		return nil
	}); err != nil {
		return err
	}
	// Publish the applied/skipped result and wake any settlement waiters. Done
	// after the ledger critical section so observers only see finalised state.
	e.recordApplied(applied)
	return nil
}

// recordApplied stores the applied/skipped outcome for each committed tx and
// wakes any waiters blocked on those txs. It takes e.mu.
func (e *Engine) recordApplied(applied map[string]bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for key, ok := range applied {
		e.appliedTxs[key] = ok
		if waiters, present := e.settleWaiters[key]; present {
			for _, ch := range waiters {
				close(ch)
			}
			delete(e.settleWaiters, key)
		}
	}
}

// WaitForSettlement blocks until the given transaction is committed to a block
// and its apply outcome is known, or until ctx is done. It returns whether the
// transfer was committed and whether it actually applied (moved credits). This
// is what an API caller uses to avoid reporting a settled/complete job for a
// payment that has only been submitted to the mempool and may yet be skipped.
// The primitive return signature (no shared struct) lets consumers in other
// packages depend on the Engine without importing a result type from here.
func (e *Engine) WaitForSettlement(ctx context.Context, tx *token.Transaction) (committed bool, applied bool, err error) {
	key := mempoolKey(tx)
	e.mu.Lock()
	if a, ok := e.appliedTxs[key]; ok {
		e.mu.Unlock()
		return true, a, nil
	}
	ch := make(chan struct{})
	e.settleWaiters[key] = append(e.settleWaiters[key], ch)
	e.mu.Unlock()

	select {
	case <-ctx.Done():
		// Deregister this waiter so a transaction that never commits (dropped from
		// the mempool, or a submit whose block never lands) does not leave a
		// permanent entry in settleWaiters. recordApplied may have closed ch and
		// removed the whole key concurrently between ctx firing and us taking the
		// lock; in that case the key is simply absent and the filter is a no-op.
		e.mu.Lock()
		if waiters, present := e.settleWaiters[key]; present {
			kept := waiters[:0]
			for _, w := range waiters {
				if w != ch {
					kept = append(kept, w)
				}
			}
			if len(kept) == 0 {
				delete(e.settleWaiters, key)
			} else {
				e.settleWaiters[key] = kept
			}
		}
		e.mu.Unlock()
		return false, false, ctx.Err()
	case <-ch:
		e.mu.Lock()
		a := e.appliedTxs[key]
		e.mu.Unlock()
		return true, a, nil
	}
}

// advanceHeight resets per-height consensus state after a commit and moves the
// head/height forward. Callers must NOT hold e.mu.
func (e *Engine) advanceHeight(committed *Block) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Remove committed transactions from the mempool by dedup key.
	committedKeys := make(map[string]struct{}, len(committed.Txs))
	for i := range committed.Txs {
		committedKeys[mempoolKey(&committed.Txs[i])] = struct{}{}
	}
	for k := range committedKeys {
		e.committedTxs[k] = struct{}{}
	}
	kept := e.mempool[:0]
	for i := range e.mempool {
		k := mempoolKey(&e.mempool[i])
		if _, done := committedKeys[k]; done {
			delete(e.mempoolSet, k)
			continue
		}
		kept = append(kept, e.mempool[i])
	}
	e.mempool = append([]token.Transaction(nil), kept...)

	e.height = committed.Height + 1
	e.headHash = committed.Hash()
	e.round = 0
	e.roundDeadline = time.Now().Add(e.roundTimeoutForLocked(e.round))
	e.proposals = make(map[string]*Block)
	// Reset the per-height voting discipline state: a fresh height starts with no
	// vote cast, no lock, and no round-vote evidence.
	e.roundVotes = make(map[uint64]map[string]map[string]Vote)
	e.votedRound = 0
	e.votedHash = ""
	e.hasVoted = false
	e.lockedRound = 0
	e.lockedHash = ""
	e.locked = false
	// Seed the new height's vote tally from any votes we buffered while lagging,
	// so a node that fell behind immediately counts the quorum the rest of the
	// network already produced for this height. The raw votes are also seeded
	// into the per-round evidence map, so this node can prove the commit to a
	// peer even though it never saw the votes arrive at the current height.
	e.votes = make(map[string]map[string]struct{})
	if buffered, ok := e.futureVotes[e.height]; ok {
		for hkey, byVoter := range buffered {
			set := make(map[string]struct{}, len(byVoter))
			for voter, v := range byVoter {
				set[voter] = struct{}{}
				vv := v
				e.recordRoundVoteLocked(&vv)
			}
			e.votes[hkey] = set
		}
		delete(e.futureVotes, e.height)
	}
	// Drop any stale buffered votes for heights we have now passed.
	for h := range e.futureVotes {
		if h < e.height {
			delete(e.futureVotes, h)
		}
	}
	e.committing = false
}

// reconcileToChainHead resets in-memory height/head to the persisted committed
// chain head. It is used when a commit attempt failed because the chain already
// advanced past this engine's view.
func (e *Engine) reconcileToChainHead() {
	head, length, err := e.chain.Head()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.committing = false
	if err != nil {
		return
	}
	if length >= e.height {
		e.height = length
		e.headHash = head
		e.round = 0
		e.roundDeadline = time.Now().Add(e.roundTimeoutForLocked(e.round))
		e.proposals = make(map[string]*Block)
		e.votes = make(map[string]map[string]struct{})
		e.roundVotes = make(map[uint64]map[string]map[string]Vote)
		e.votedRound = 0
		e.votedHash = ""
		e.hasVoted = false
		e.lockedRound = 0
		e.lockedHash = ""
		e.locked = false
	}
}

// Height returns the next height to commit (equivalently, the committed chain
// length).
func (e *Engine) Height() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.height
}

// MempoolLen returns the number of pending (uncommitted) transactions.
func (e *Engine) MempoolLen() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.mempool)
}

// Chain returns the committed-block ledger for inspection.
func (e *Engine) Chain() *BlockChain { return e.chain }

// Ledger returns the market ledger consensus applies committed transactions to.
func (e *Engine) Ledger() *market.Ledger { return e.ledger }

// ValidatorSet returns the fixed validator set.
func (e *Engine) ValidatorSet() *ValidatorSet { return e.validators }
