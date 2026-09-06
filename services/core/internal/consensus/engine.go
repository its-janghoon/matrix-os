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

	proposeInterval time.Duration
	roundTimeout    time.Duration
	maxBlockTxs     int
	onCommit        CommitObserver

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
	// height.
	votes map[string]map[string]struct{}
	// committedThisHeight guards against double-commit at a height.
	committing bool
	// futureProposals stashes proposals for heights ahead of ours (this node is
	// lagging) so we can adopt them on catch-up. Keyed by "height:hash".
	futureProposals map[string]*Block
	// futureVotes stashes votes for heights ahead of ours so we can tally them the
	// moment we reach that height. Keyed by height -> blockHashHex -> voterID set.
	futureVotes map[uint64]map[string]map[string]struct{}

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
		transport:       cfg.Transport,
		validators:      cfg.Validators,
		chain:           cfg.Chain,
		ledger:          cfg.Ledger,
		self:            cfg.Self,
		proposeInterval: orDurationC(cfg.ProposeInterval, DefaultProposeInterval),
		roundTimeout:    orDurationC(cfg.RoundTimeout, DefaultRoundTimeout),
		maxBlockTxs:     orIntC(cfg.MaxBlockTxs, DefaultMaxBlockTxs),
		onCommit:        cfg.OnCommit,
		mempoolSet:      make(map[string]struct{}),
		committedTxs:    make(map[string]struct{}),
		proposals:       make(map[string]*Block),
		votes:           make(map[string]map[string]struct{}),
		futureProposals: make(map[string]*Block),
		futureVotes:     make(map[uint64]map[string]map[string]struct{}),
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
	e.roundDeadline = time.Now().Add(e.roundTimeout)
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

	e.wg.Add(3)
	go e.runLoop(ctx, proposalCh, e.handleProposal)
	go e.runLoop(ctx, voteCh, e.handleVote)
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
			e.tick(ctx)
		}
	}
}

// tick performs one driver step under the lock, then publishes any produced
// proposal outside the lock.
func (e *Engine) tick(ctx context.Context) {
	e.mu.Lock()

	// Round timeout: rotate the leader if this height has not committed in time.
	if time.Now().After(e.roundDeadline) {
		e.round++
		e.roundDeadline = time.Now().Add(e.roundTimeout)
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
func (e *Engine) buildBlockLocked() *Block {
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
	if err := b.Sign(e.self.PrivateKey); err != nil {
		return nil
	}
	return b
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

	// Re-gossip once to help other lagging nodes obtain the body.
	_ = e.transport.Publish(ctx, TopicProposal, raw)
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

// acceptProposal verifies a block for the current height and caches it. It
// returns an error (which the caller uses to drop the message) when the block is
// not acceptable. Verification: proposer is a validator, proposer is the correct
// leader for the block's round, proposer signature verifies, the block links to
// our committed head, and every transaction is individually validly signed.
func (e *Engine) acceptProposal(b *Block) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Only consider proposals for the height we are currently trying to commit.
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

	// Cache the block for this height so votes can be tallied against it. Adopt
	// the proposal's round so our own subsequent votes/leadership checks align
	// with the block we are voting for.
	hash := b.Hash()
	hkey := fmt.Sprintf("%x", hash)
	if _, seen := e.proposals[hkey]; !seen {
		cp := *b
		e.proposals[hkey] = &cp
	}
	if b.Round > e.round {
		e.round = b.Round
		e.roundDeadline = time.Now().Add(e.roundTimeout)
	}
	return nil
}

// castVote signs and broadcasts this node's vote for block b, and also tallies
// the vote locally (which may itself trigger a commit if quorum is already
// present, e.g. in a single-validator set). A non-validator node does not vote
// but still tallies received votes toward a commit.
func (e *Engine) castVote(ctx context.Context, b *Block) {
	if !e.isValidator {
		return
	}
	hash := b.Hash()
	v := &Vote{
		Height:    b.Height,
		Round:     b.Round,
		BlockHash: hash,
		VoterID:   e.selfID,
		PublicKey: e.self.PublicKey,
	}
	if err := v.Sign(e.self.PrivateKey); err != nil {
		return
	}
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
	case v.Height > e.height && v.Height <= e.height+maxFutureStash:
		// We are lagging: buffer the vote so it counts the moment we reach that
		// height, so a node that fell behind still collects the quorum that let the
		// rest of the network commit.
		byHash := e.futureVotes[v.Height]
		if byHash == nil {
			byHash = make(map[string]map[string]struct{})
			e.futureVotes[v.Height] = byHash
		}
		set := byHash[hkey]
		if set == nil {
			set = make(map[string]struct{})
			byHash[hkey] = set
		}
		set[v.VoterID] = struct{}{}
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
		e.mu.Unlock()
		return
	}
	e.committing = true
	block := *winner
	e.mu.Unlock()

	// Commit to the hash-linked chain and apply to the ledger. Both are done
	// while NOT holding e.mu (they take their own locks) to avoid lock-ordering
	// issues, but committing is serialised by the committing flag so no two
	// commits race at the same height.
	if err := e.commitAndApply(&block); err != nil {
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

// commitAndApply persists the block to the committed chain and deterministically
// applies its ordered transactions to the market ledger. Each transaction moves
// credits from sender to recipient; a transaction whose sender cannot afford it
// is skipped (it committed to the ordered log but moves no credits), so every
// node applies the identical deterministic result. Because the transaction set
// and order are fixed by the committed block, every honest node computes the
// same balances.
func (e *Engine) commitAndApply(b *Block) error {
	if _, err := e.chain.Commit(b); err != nil {
		return err
	}
	// Apply transfers atomically as one critical section on the ledger, matching
	// token.SettledLedger's safe pattern (affordability check + transfer under the
	// ledger write lock). Determinism: iterate in block order; skip unaffordable
	// transfers uniformly.
	return e.ledger.Atomically(func(ltx market.LedgerTx) error {
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
				continue
			}
			if err := ltx.Transfer(sender, tx.To, tx.Amount); err != nil {
				return err
			}
		}
		return nil
	})
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
	e.roundDeadline = time.Now().Add(e.roundTimeout)
	e.proposals = make(map[string]*Block)
	// Seed the new height's vote tally from any votes we buffered while lagging,
	// so a node that fell behind immediately counts the quorum the rest of the
	// network already produced for this height.
	if buffered, ok := e.futureVotes[e.height]; ok {
		e.votes = buffered
		delete(e.futureVotes, e.height)
	} else {
		e.votes = make(map[string]map[string]struct{})
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
		e.roundDeadline = time.Now().Add(e.roundTimeout)
		e.proposals = make(map[string]*Block)
		e.votes = make(map[string]map[string]struct{})
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
