package consensus

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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

// futureBlock is a block for a height ahead of this node's, held until it gets
// there. It records how the block arrived, because that decides what happens on
// catch-up: a proposal is replayed as a proposal (and voted for, in the round it
// was made for), while a body backfilled by block sync is cached without a vote,
// exactly as when it arrived.
type futureBlock struct {
	block    *Block
	round    uint64
	polka    *PolkaCertificate
	fromSync bool
}

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
	// Evidence, when non-nil, persists proof of equivocation. Without it the
	// engine still detects and gossips an offence but forgets it on restart.
	Evidence *EvidenceStore
	// Sets, when non-nil, persists the validator set the chain has arrived at
	// and the changes waiting for an epoch boundary. Without it a node forgets
	// an admitted validator on restart and starts rejecting its votes, so a
	// deployment that allows set changes needs this.
	Sets *SetStore
	// EpochLength is how many committed blocks pass between set changes taking
	// effect. Zero means DefaultEpochLength.
	EpochLength uint64
	// ApprovedSetChanges is this operator's approval list, in the form
	// "add:<hex public key>" or "remove:<account id>".
	//
	// It is local policy, and deliberately not part of consensus: this node will
	// not PREVOTE for a block carrying a change that is not on its list, which
	// is how an operator vetoes. If the network commits it anyway, this node
	// applies it - a vote is a veto attempt and the committed chain is the fact.
	// An empty list means this node votes against every set change, which is the
	// safe default: without it, one validator could propose removing all the
	// others and the rest would vote for it without ever looking.
	ApprovedSetChanges []string
	// Stake, when non-nil, is the bonded-stake ledger. With it configured,
	// voting power is bonded stake rather than one vote per validator, admission
	// requires MinBond, and a proven offence takes the offender's bond.
	//
	// Without it the network runs unstaked: every validator has power 1, every
	// quorum is a headcount, and an ejected validator loses nothing but its
	// place. That is a coherent mode for operators who know each other, and the
	// only safe one for an open set is the other.
	Stake *StakeLedger
	// MinBond is the stake an account must have bonded before it may be admitted
	// to the validator set. Zero means DefaultMinBond; set it explicitly to zero
	// through ZeroMinBond to admit validators with nothing at risk.
	MinBond uint64
	// ZeroMinBond removes the minimum-bond requirement, which is only
	// appropriate on a network that is not using stake for security.
	ZeroMinBond bool
	// UnbondingPeriod is how many blocks after leaving the validator set an
	// account must wait before withdrawing its bond. Zero means
	// DefaultUnbondingPeriod.
	UnbondingPeriod uint64
	// TargetBond is how much this node should have bonded on its own consensus
	// account. When its bond is below this the node submits a bond for the
	// difference, and keeps doing so until the target is met.
	//
	// It is config rather than an RPC because bonding has to be signed by the
	// validator's own key, which lives inside the node - the same reason
	// approving a set change is config. Zero bonds nothing.
	TargetBond uint64
	// EjectEquivocators, when nil or true, has this node vote to REMOVE a
	// validator it holds proof equivocated, and offer that removal itself.
	//
	// Unlike an admission this needs no entry in ApprovedSetChanges, and that is
	// not a shortcut: equivocation evidence is self-proving. It is two votes for
	// different blocks at one height, round and phase, both signed by the
	// offender's own key, and every node verifies them itself rather than taking
	// a peer's word. An honest validator cannot produce such a pair, so there is
	// no operator judgement left to make - which is exactly what an admission,
	// where the judgement is the whole point, does not have.
	//
	// Set it false on a network where an operator would rather investigate an
	// offence than have the network eject the offender on its own.
	EjectEquivocators *bool
	// OnEquivocation, when non-nil, is called once per newly-discovered offence.
	// It is how an operator finds out, in addition to whatever the network does
	// about it.
	OnEquivocation func(eq *Equivocation)
}

// Engine is a fast, leader-based BFT consensus engine over a fixed validator
// set. It runs a driver goroutine that, per height, lets the round leader
// propose a block of pending transactions, collects prevotes, answers a prevote
// quorum with a precommit, and commits on a precommit quorum, then advances.
// Receive loops for proposals, votes, block sync and head announcements follow
// the transport goroutine + ctx-cancellation pattern used by marketexchange.
type Engine struct {
	transport Transport
	// validatorSet is the set in force right now, held atomically because it is
	// no longer fixed: a committed set change replaces it at an epoch boundary
	// while receive loops are reading it. Read it through vset().
	validatorSet atomic.Pointer[ValidatorSet]
	chain        *BlockChain
	ledger       *market.Ledger
	self         *token.Account
	selfID       string
	// isValidator is atomic because the epoch boundary flips it (a set change can
	// admit or eject this node) while the receive loops and the driver read it
	// outside e.mu. Read it with Load, never directly.
	isValidator atomic.Bool

	proposeInterval      time.Duration
	roundTimeout         time.Duration
	maxBlockTxs          int
	headAnnounceInterval time.Duration
	onCommit             CommitObserver
	evidence             *EvidenceStore
	onEquivocation       func(eq *Equivocation)
	sets                 *SetStore
	epochLength          uint64
	approvedChanges      map[string]struct{}
	// approvedSpecs is the same allow-list in parsed form. A node does not only
	// vote for the changes its operator approved, it also PROPOSES them: without
	// that, approving a change would have no effect until some other node
	// happened to propose exactly the same one, and the first operator to approve
	// an ejection would be waiting on the very validator they are ejecting.
	approvedSpecs []SetChange
	// ejectEquivocators is whether proven equivocation is grounds for this node
	// to vote for, and offer, the offender's removal.
	ejectEquivocators bool
	// stake, when non-nil, makes voting power bonded stake and membership cost
	// something. See Config.Stake.
	stake *StakeLedger
	// minBond gates admission; unbondingPeriod gates withdrawal.
	minBond         uint64
	unbondingPeriod uint64
	// targetBond is how much this node should have bonded; see Config.TargetBond.
	targetBond uint64
	// lastBondTop rate-limits the top-up check, and bondNonce keeps each top-up
	// a distinct transaction.
	lastBondTop time.Time
	bondNonce   uint64
	// stakeDirty is whether a bond or withdrawal has COMMITTED since the last
	// epoch boundary. It both asks for a re-weight and justifies producing empty
	// blocks to reach the boundary, because a bond the chain has accepted should
	// take effect within an epoch even if the network then goes quiet.
	//
	// It exists because re-weighting reads a balance per validator and writes to
	// the store, and doing that at every boundary put ledger and kv I/O on the
	// hot path under e.mu. On a chain with no stake activity it did that work to
	// arrive at the numbers it already had. With this flag the cost is paid when
	// a bond actually moves, which is the only time the answer changes.
	stakeDirty bool
	// stakeNeverWeighted is set until this node has weighted the set once, so a
	// store that already holds bonds is picked up at the first boundary reached
	// in the ordinary course of events.
	stakeNeverWeighted bool

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
	// prevotes and precommits hold the two voting phases for the current height,
	// keyed round -> blockHashHex -> voterID -> Vote. The nil marker is a
	// legitimate key in both: it records a validator that supported no block in
	// that round, which is what lets a round be concluded rather than hang.
	//
	// The whole signed vote is kept, not just the voter's identity, because the
	// votes ARE the protocol's evidence: a quorum of prevotes for one block at
	// one round is the polka a leader shows to unlock the others, and a quorum of
	// precommits is the certificate that proves a commit to a node that missed it.
	prevotes   map[uint64]map[string]map[string]Vote
	precommits map[uint64]map[string]map[string]Vote
	// lockedRound / lockedHash record the block this node PRECOMMITTED at the
	// highest round so far, and that round. This is the LOCK: once locked, the
	// node will not prevote a DIFFERENT block at this height unless shown a valid
	// PolkaCertificate proving that block reached a prevote quorum at a round >=
	// lockedRound. The lock is what makes leader rotation safe - two conflicting
	// blocks can never both gather a precommit quorum at one height.
	//
	// Crucially a lock is only ever taken after observing a polka, so every lock
	// in the network is backed by evidence some leader can collect and show. A
	// lock taken on a node's own single vote would not be, and validators could
	// end up locked on blocks that no quorum ever saw - a deadlock no certificate
	// could resolve.
	lockedRound uint64
	lockedHash  string
	locked      bool
	// validRound / validHash record the block that most recently reached a
	// prevote quorum at this height. A leader proposes THIS value rather than a
	// fresh one, which is what carries a partly-locked network to agreement:
	// every validator locked at a round <= validRound can verify the polka that
	// comes with it and release its lock.
	validRound uint64
	validHash  string
	hasValid   bool
	// committedThisHeight guards against double-commit at a height.
	committing bool
	// futureProposals stashes blocks for heights ahead of ours (this node is
	// lagging) so we can adopt them on catch-up. Keyed by "height:hash".
	futureProposals map[string]*futureBlock
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
	// lastSetChangeSubmit rate-limits re-offering the set changes this operator
	// approved, and setChangeNonce keeps each offer a distinct transaction so a
	// retry is not silently deduped against the offer that was voted down.
	lastSetChangeSubmit time.Time
	setChangeNonce      uint64
	// setChangeOffered records when each approved change was last put to the
	// network, keyed by its spec string. A change is offered again only after
	// several rounds, which is long enough for the previous offer to have
	// committed or been voted down: re-offering one that is still in flight would
	// commit the same change twice.
	setChangeOffered map[string]time.Time
	// seenEquivocations dedups reports when no evidence store is configured, so a
	// gossiped offence is not re-announced on every echo.
	seenEquivocations map[string]struct{}
	// pendingChanges are validator-set changes carried by committed blocks that
	// have not reached an epoch boundary yet.
	pendingChanges []SetChange
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
		chain:                cfg.Chain,
		ledger:               cfg.Ledger,
		self:                 cfg.Self,
		proposeInterval:      orDurationC(cfg.ProposeInterval, DefaultProposeInterval),
		roundTimeout:         orDurationC(cfg.RoundTimeout, DefaultRoundTimeout),
		maxBlockTxs:          orIntC(cfg.MaxBlockTxs, DefaultMaxBlockTxs),
		headAnnounceInterval: orDurationC(cfg.HeadAnnounceInterval, DefaultHeadAnnounceInterval),
		onCommit:             cfg.OnCommit,
		evidence:             cfg.Evidence,
		onEquivocation:       cfg.OnEquivocation,
		sets:                 cfg.Sets,
		ejectEquivocators:    cfg.EjectEquivocators == nil || *cfg.EjectEquivocators,
		stake:                cfg.Stake,
		// Weight once at the first boundary this node reaches: the store may
		// already hold bonds from before the process started. This does NOT
		// justify producing blocks to get there - a node that has just booted on
		// a quiet chain has no business emitting empty blocks - so it is a
		// separate flag from stakeDirty.
		stakeNeverWeighted: true,
		minBond:            stakeMinBond(cfg),
		unbondingPeriod:    orUint64C(cfg.UnbondingPeriod, DefaultUnbondingPeriod),
		targetBond:         cfg.TargetBond,
		epochLength:        orUint64C(cfg.EpochLength, DefaultEpochLength),
		approvedChanges:    make(map[string]struct{}, len(cfg.ApprovedSetChanges)),
		mempoolSet:         make(map[string]struct{}),
		committedTxs:       make(map[string]struct{}),
		appliedTxs:         make(map[string]bool),
		settleWaiters:      make(map[string][]chan struct{}),
		setChangeOffered:   make(map[string]time.Time),
		proposals:          make(map[string]*Block),
		prevotes:           make(map[uint64]map[string]map[string]Vote),
		precommits:         make(map[uint64]map[string]map[string]Vote),
		futureProposals:    make(map[string]*futureBlock),
		futureVotes:        make(map[uint64]map[string]map[string]Vote),
	}
	for _, c := range cfg.ApprovedSetChanges {
		spec, err := ParseChangeSpec(c)
		if err != nil {
			// Fail the node rather than ignore the entry: an operator who mistyped an
			// approval would otherwise believe they had agreed to a change their node
			// will in fact vote against.
			return nil, fmt.Errorf("consensus: approved set change %q: %w", c, err)
		}
		e.approvedChanges[strings.ToLower(spec.String())] = struct{}{}
		e.approvedSpecs = append(e.approvedSpecs, spec)
	}
	// Evidence already on disk is still evidence. A node that recorded an
	// offence, was restarted before the network ejected the offender, and then
	// forgot about it would silently go back to voting against the removal.
	if e.ejectEquivocators && cfg.Evidence != nil {
		records, err := cfg.Evidence.All()
		if err != nil {
			return nil, fmt.Errorf("consensus: read stored equivocation evidence: %w", err)
		}
		for i := range records {
			e.approveRemovalLocked(records[i].VoterID)
		}
	}
	e.validatorSet.Store(cfg.Validators)
	if cfg.Self != nil {
		e.selfID = cfg.Self.AccountID()
		e.isValidator.Store(cfg.Validators.Contains(e.selfID))
	}
	return e, nil
}

// vset returns the validator set in force. It is a method rather than a field
// read because the set changes underneath the receive loops.
func (e *Engine) vset() *ValidatorSet { return e.validatorSet.Load() }

func orDurationC(v, d time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return d
}

// stakeMinBond resolves the admission floor. Zero is ambiguous in a struct
// literal - it means both "unset" and "no minimum" - so ZeroMinBond says which
// one is meant, and the default applies otherwise.
func stakeMinBond(cfg Config) uint64 {
	if cfg.ZeroMinBond {
		return 0
	}
	return orUint64C(cfg.MinBond, DefaultMinBond)
}

func orUint64C(v, d uint64) uint64 {
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
	// Resume the set the CHAIN arrived at, not the one config was written with:
	// a node that had admitted a validator must not forget it on restart and
	// start rejecting that validator's votes.
	if e.sets != nil {
		saved, at, err := e.sets.LoadActive()
		if err != nil {
			e.mu.Unlock()
			return err
		}
		if saved != nil {
			e.validatorSet.Store(saved)
			e.isValidator.Store(e.selfID != "" && saved.Contains(e.selfID))
			fmt.Printf("consensus: resumed validator set of %d from height %d\n", saved.Len(), at)
		}
		pending, err := e.sets.LoadPending()
		if err != nil {
			e.mu.Unlock()
			return err
		}
		e.pendingChanges = pending
	}
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
	evidenceCh, err := e.transport.Subscribe(ctx, TopicEvidence)
	if err != nil {
		return fmt.Errorf("consensus: subscribe evidence: %w", err)
	}

	e.wg.Add(7)
	go e.runLoop(ctx, proposalCh, e.handleProposal)
	go e.runLoop(ctx, voteCh, e.handleVote)
	go e.runLoop(ctx, syncReqCh, e.handleSyncRequest)
	go e.runLoop(ctx, syncRespCh, e.handleSyncResponse)
	go e.runLoop(ctx, headCh, e.handleHeadAnnounce)
	go e.runLoop(ctx, evidenceCh, e.handleEvidence)
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
			e.maybeProposeApprovedChanges()
			e.maybeTopUpBond()
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

// tick performs one driver step under the lock, then publishes anything it
// produced outside it.
func (e *Engine) tick(ctx context.Context) {
	e.mu.Lock()

	// Round timeout: rotate the leader if this height has not committed in time.
	// Before leaving the round, put on record whatever this node did not say in
	// it - a nil prevote if it supported no block, a nil precommit if it
	// committed to none. Those votes are what let every node conclude the round
	// instead of waiting on a validator that will never speak, and they are the
	// reason a stalled height eventually moves rather than hanging on silence.
	//
	// The LOCK is deliberately preserved across the bump: a node still refuses to
	// prevote a conflicting block without a justifying polka.
	var pending []pendingVote
	height := e.height
	if time.Now().After(e.roundDeadline) {
		expired := e.round
		if e.isValidator.Load() {
			if _, voted := e.selfVoteAtLocked(VoteTypePrevote, expired); !voted {
				pending = append(pending, pendingVote{typ: VoteTypePrevote, round: expired})
			}
			if _, voted := e.selfVoteAtLocked(VoteTypePrecommit, expired); !voted {
				pending = append(pending, pendingVote{typ: VoteTypePrecommit, round: expired})
			}
		}
		e.round++
		e.roundDeadline = time.Now().Add(e.roundTimeoutForLocked(e.round))
	}

	// Only the leader proposes, only if this node is a validator with a key.
	if !e.isValidator.Load() || !e.vset().IsLeader(e.selfID, e.height, e.round) {
		e.mu.Unlock()
		e.flushPendingVotes(ctx, height, pending)
		return
	}
	block, polka := e.buildProposalLocked()
	round := e.round
	e.mu.Unlock()

	e.flushPendingVotes(ctx, height, pending)

	if block == nil {
		return
	}
	// Ingest our own proposal (so we tally our own prevote) and broadcast it.
	e.ingestProposal(ctx, block, round, polka)
}

// pendingVote is a nil vote a round timeout decided to cast, held until the
// engine lock is released.
type pendingVote struct {
	typ   VoteType
	round uint64
}

// flushPendingVotes casts the nil votes a round timeout produced. It drops them
// if the height moved on in the meantime: a vote must belong to the height it
// was decided at, and stamping it with a later one would inject evidence about
// a round that height never had.
func (e *Engine) flushPendingVotes(ctx context.Context, height uint64, pending []pendingVote) {
	for _, pv := range pending {
		e.mu.Lock()
		stale := e.height != height
		e.mu.Unlock()
		if stale {
			return
		}
		switch pv.typ {
		case VoteTypePrevote:
			e.castPrevote(ctx, nilVoteHash(), pv.round)
		case VoteTypePrecommit:
			e.castPrecommit(ctx, nilVoteHash(), pv.round)
		}
	}
}

// castNilVoteIf publishes a nil vote for a round this node left without voting.
// Callers must NOT hold e.mu.
func (e *Engine) castNilVoteIf(ctx context.Context, should bool, round uint64) {
	if !should {
		return
	}
	e.mu.Lock()
	height := e.height
	e.mu.Unlock()

	v := &Vote{
		Height:    height,
		Round:     round,
		BlockHash: nilVoteHash(),
		VoterID:   e.selfID,
		PublicKey: e.self.PublicKey,
	}
	if err := v.Sign(e.self.PrivateKey); err != nil {
		return
	}
	// Record it locally too: this node's own nil vote counts toward the evidence
	// it can later hand to a peer.
	e.tallyVote(v)
	if data, err := json.Marshal(v); err == nil {
		_ = e.transport.Publish(ctx, TopicVote, data)
	}
}

// buildProposalLocked constructs the proposal this node should make for the
// current height/round, together with the polka certificate that justifies it.
// Callers must hold e.mu. It returns nil when this node has nothing to propose.
//
// A leader does NOT get to choose freely. If any block has reached a prevote
// quorum at this height, that is the value it must put forward, carrying the
// polka with it: validators locked at a round <= that polka's round can verify
// it and release their locks, whereas a fresh value would be refused by every
// one of them and the round would be wasted. Only when no value has been
// polka'd is the leader free to batch pending transactions into a new block.
func (e *Engine) buildProposalLocked() (*Block, *PolkaCertificate) {
	if e.hasValid {
		if b, ok := e.proposals[e.validHash]; ok {
			return b, e.polkaCertificateLocked(e.validRound, e.validHash)
		}
		// We know a value was polka'd but do not hold its body. Proposing anything
		// else cannot commit (locked validators will refuse it), so stay quiet and
		// let the proposal gossip or block sync bring the body.
		if e.locked {
			return nil, nil
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
		// A set change that is no longer valid - a duplicate of one already in
		// force, or one from an account that has since left the set - would make
		// the whole block invalid. Leave it behind rather than poison a proposal
		// with it; pruneStaleSetChangesLocked clears it from the mempool.
		if IsSetChangeRecipient(e.mempool[i].To) {
			if err := e.verifySetChangeLocked(&e.mempool[i], e.height); err != nil {
				continue
			}
		}
		// A withdrawal whose unbonding period has not elapsed is not invalid
		// forever, only not yet: leaving it in the mempool is what makes it land
		// by itself once the delay passes, and proposing it now would make the
		// block invalid.
		if IsStakeRecipient(e.mempool[i].To) {
			if err := e.verifyStakeTxLocked(&e.mempool[i], e.height); err != nil {
				continue
			}
		}
		txs = append(txs, e.mempool[i])
	}
	if len(txs) == 0 && !e.mustAdvanceToEpochBoundaryLocked() {
		return nil, nil
	}

	b := &Block{
		Height:        e.height,
		Round:         e.round,
		PrevBlockHash: append([]byte(nil), e.headHash...),
		Txs:           txs,
		ProposerID:    e.selfID,
	}
	if err := b.Sign(e.self.PrivateKey); err != nil {
		return nil, nil
	}
	return b, nil
}

// polkaCertificateLocked builds the certificate for a specific (round, block)
// prevote quorum this node has observed, or nil when it holds no such quorum.
// Callers must hold e.mu.
func (e *Engine) polkaCertificateLocked(round uint64, hkey string) *PolkaCertificate {
	byVoter := e.prevotes[round][hkey]
	vs := e.vset()
	if vs.PowerOfVoters(byVoter) < vs.QuorumPower() {
		return nil
	}
	votes := make([]Vote, 0, len(byVoter))
	var blockHash []byte
	for _, v := range byVoter {
		votes = append(votes, v)
		blockHash = v.BlockHash
	}
	return &PolkaCertificate{
		Height:    e.height,
		Round:     round,
		BlockHash: append([]byte(nil), blockHash...),
		Votes:     votes,
	}
}

// ingestProposal records a locally-built proposal, publishes it, and prevotes
// for it. It is the leader-side path that mirrors what followers do on
// receiving a proposal in handleProposal.
func (e *Engine) ingestProposal(ctx context.Context, b *Block, round uint64, polka *PolkaCertificate) {
	support, err := e.acceptProposal(b, round, polka)
	if err != nil {
		return
	}
	// Broadcast the proposal envelope: the block exactly as it was built (or as
	// it was originally received, when re-proposing), signed for this round by
	// this node.
	prop := &Proposal{Block: *b, Round: round, ProposerID: e.selfID, Justify: polka}
	if err := prop.Sign(e.self.PrivateKey); err != nil {
		return
	}
	if data, err := json.Marshal(prop); err == nil {
		_ = e.transport.Publish(ctx, TopicProposal, data)
	}
	// A set change gets ONE proposal. If the network does not approve it, the
	// block will not reach a polka, and a leader that kept re-including the same
	// change would waste every round it leads - which is a stall a single
	// validator could cause on purpose. Re-proposing is then a deliberate act:
	// submit it again.
	e.dropSetChangesFromMempool(b)
	e.prevoteFor(ctx, b, round, support)
}

// dropSetChangesFromMempool forgets the set-change transactions a block we just
// proposed carried. If the block commits, the change is in the chain regardless;
// if it does not, the change is not retried automatically.
func (e *Engine) dropSetChangesFromMempool(b *Block) {
	var keys []string
	for i := range b.Txs {
		if IsSetChangeRecipient(b.Txs[i].To) {
			keys = append(keys, mempoolKey(&b.Txs[i]))
		}
	}
	if len(keys) == 0 {
		return
	}
	drop := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		drop[k] = struct{}{}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	kept := e.mempool[:0]
	for i := range e.mempool {
		k := mempoolKey(&e.mempool[i])
		if _, remove := drop[k]; remove {
			delete(e.mempoolSet, k)
			continue
		}
		kept = append(kept, e.mempool[i])
	}
	e.mempool = append([]token.Transaction(nil), kept...)
}

// handleProposal validates a received proposal and, if it is usable at the
// current height, caches it and prevotes - for the block when this node may
// support it, nil when its lock forbids that. Invalid or stale proposals are
// dropped silently (gossip is best-effort).
func (e *Engine) handleProposal(ctx context.Context, msg transport.Message) {
	var p Proposal
	if err := json.Unmarshal(msg.Payload, &p); err != nil {
		return
	}
	if err := e.verifyProposalEnvelope(&p); err != nil {
		return
	}
	b := p.Block
	support, err := e.acceptProposal(&b, p.Round, p.Justify)
	if err != nil {
		// We could not use the proposal at our current height. If it is for a
		// FUTURE height (we are lagging behind the network), stash it so we can
		// adopt it the moment we catch up, then re-gossip it so other lagging nodes
		// also receive it. This is the catch-up path that guarantees a node which
		// missed a committing proposal still obtains the block body.
		e.stashFutureProposal(ctx, &b, p.Round, p.Justify, msg.Payload)
		return
	}
	e.prevoteFor(ctx, &b, p.Round, support)
}

// prevoteFor casts this node's prevote for a round: for the block when it may
// support it, for nil when it may not.
//
// Voting nil rather than staying silent matters. A validator whose lock forbids
// the proposal still owes the round an answer, and a quorum of prevotes - for
// whatever mix of block and nil - is what lets every node conclude the round
// and move on instead of waiting out the timeout.
func (e *Engine) prevoteFor(ctx context.Context, b *Block, round uint64, support bool) {
	if support {
		e.castPrevote(ctx, b.Hash(), round)
		return
	}
	e.castPrevote(ctx, nilVoteHash(), round)
}

// verifyProposalEnvelope validates who is putting a block to the vote and for
// which round: the proposer must be a validator, must be the leader for the
// round it claims, must have signed the envelope, and cannot claim a round
// earlier than the block it carries was created at.
//
// The block inside is verified separately (acceptProposal). This check is what
// keeps a non-leader from filling every node's proposal cache with values at
// will; it does not decide anything about the block's validity.
func (e *Engine) verifyProposalEnvelope(p *Proposal) error {
	pub, ok := e.vset().PublicKey(p.ProposerID)
	if !ok {
		return ErrNotValidator
	}
	if !e.vset().IsLeader(p.ProposerID, p.Block.Height, p.Round) {
		return ErrWrongLeader
	}
	if err := p.VerifySignature(pub); err != nil {
		return err
	}
	if p.Round < p.Block.Round {
		return fmt.Errorf("%w: proposal round %d precedes block round %d",
			ErrInvalidMessage, p.Round, p.Block.Round)
	}
	return nil
}

// stashFutureProposal remembers a block for a height greater than ours so it
// can be replayed once we advance to that height, and re-gossips a received
// proposal once so the body keeps propagating to other lagging nodes. Blocks
// for heights we have already passed, or malformed ones, are ignored.
func (e *Engine) stashFutureProposal(ctx context.Context, b *Block, round uint64, polka *PolkaCertificate, raw []byte) {
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
	e.futureProposals[hkey] = &futureBlock{
		block:    &cp,
		round:    round,
		polka:    polka,
		fromSync: len(raw) == 0,
	}
	e.mu.Unlock()

	// Re-gossip once to help other lagging nodes obtain the body. A block handed
	// to us by block sync has no original proposal bytes to echo, and the peer
	// that served it is already answering requests, so there is nothing to
	// re-gossip in that case.
	if len(raw) > 0 {
		_ = e.transport.Publish(ctx, TopicProposal, raw)
	}
}

// drainFutureProposals adopts any stashed blocks that are now for the current
// height. Callers must NOT hold e.mu.
func (e *Engine) drainFutureProposals(ctx context.Context) {
	e.mu.Lock()
	h := e.height
	var ready []*futureBlock
	for k, fb := range e.futureProposals {
		if fb.block.Height < h {
			delete(e.futureProposals, k)
			continue
		}
		if fb.block.Height == h {
			ready = append(ready, fb)
			delete(e.futureProposals, k)
		}
	}
	e.mu.Unlock()
	for _, fb := range ready {
		if fb.fromSync {
			// A backfilled body: cache it so the precommits we buffered for this
			// height can commit it. It is settled history, not a proposal to endorse.
			_ = e.acceptSyncedBlock(fb.block)
			continue
		}
		support, err := e.acceptProposal(fb.block, fb.round, fb.polka)
		if err != nil {
			continue
		}
		e.prevoteFor(ctx, fb.block, fb.round, support)
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
	// Proposer must be a validator and the correct leader for the block's height
	// and round.
	pub, ok := e.vset().PublicKey(b.ProposerID)
	if !ok {
		return ErrNotValidator
	}
	if !e.vset().IsLeader(b.ProposerID, b.Height, b.Round) {
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
		if IsSetChangeRecipient(b.Txs[i].To) {
			if err := e.verifySetChangeLocked(&b.Txs[i], b.Height); err != nil {
				// Both sentinels are wrapped: callers match ErrInvalidMessage to
				// reject the block and ErrNotValidator (or the parse error) to say
				// why, so the reason is not flattened into a string.
				return fmt.Errorf("%w: tx %d: %w", ErrInvalidMessage, i, err)
			}
		}
		if IsStakeRecipient(b.Txs[i].To) {
			if err := e.verifyStakeTxLocked(&b.Txs[i], b.Height); err != nil {
				return fmt.Errorf("%w: tx %d: %w", ErrInvalidMessage, i, err)
			}
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

// verifySetChangeLocked checks a validator-set change carried by a block.
//
// These rules are part of consensus, so every node must reach the same verdict:
// the change has to parse, it has to come from a current validator, and the set
// it would produce has to be usable. Anything else makes the block invalid, not
// merely unpopular. Whether a well-formed change SHOULD pass is a separate,
// local question - see approvesChangesLocked. Callers must hold e.mu.
func (e *Engine) verifySetChangeLocked(tx *token.Transaction, height uint64) error {
	change, err := ParseSetChange(tx.To, height)
	if err != nil {
		return err
	}
	// Only a sitting validator may put a set change to the network. Without this
	// any account could fill blocks with proposals for the set.
	sender := tx.SenderID()
	if !e.vset().Contains(sender) {
		return fmt.Errorf("%w: set change submitted by %s, who is not a validator",
			ErrNotValidator, sender)
	}
	// A set change is not a transfer. Requiring zero value keeps the reserved
	// recipients from doubling as a way to move credits into an address no key
	// can ever spend from.
	if tx.Amount != 0 {
		return fmt.Errorf("%w: a validator set change must carry no value, got %d", ErrInvalidMessage, tx.Amount)
	}
	// The result must be a usable set: applying it together with everything
	// already pending must not empty the set or overflow it.
	combined := append(append([]SetChange(nil), e.pendingChanges...), change)
	next, err := e.vset().WithChanges(combined)
	if err != nil {
		return err
	}
	// An admission requires a bond. This is the rule that gives membership a
	// price: without it an account could be voted in with nothing at risk, and
	// the stake weighting below would hand it power 1 for free.
	//
	// It is checkable from committed state - the bond is a balance - so every
	// node validating this block at this height reaches the same answer.
	if change.Kind == SetChangeAdd && e.stake != nil && e.minBond > 0 {
		bonded, err := e.stake.Bonded(change.ValidatorID)
		if err != nil {
			return err
		}
		if bonded < e.minBond {
			return fmt.Errorf("%w: %s has %d bonded, the minimum is %d",
				ErrInsufficientBond, change.ValidatorID, bonded, e.minBond)
		}
	}
	// A change that alters nothing is invalid, not merely useless. Judge it
	// against the set that WILL be in force - the current set plus everything
	// already pending - so "remove X" followed by "add X" is still a real change
	// while a second copy of the same admission is not. Without this rule a
	// re-offered change could commit after the first copy had already taken
	// effect, and each no-op copy would put the chain through another epoch of
	// empty blocks to apply nothing.
	projected, err := e.vset().WithChanges(e.pendingChanges)
	if err != nil {
		// Unreachable: the pending changes were each validated against a usable
		// result when they committed. Fall back to the combined result, which is
		// known good.
		projected = next
	}
	if change.InForce(projected) {
		return fmt.Errorf("%w: %s is already in force", ErrInvalidMessage, change)
	}
	return nil
}

// verifyStakeTxLocked decides whether a stake transaction may be in a block at
// this height. Callers must hold e.mu.
//
// These are VALIDITY rules, not policy: a block carrying a stake transaction
// that breaks them is invalid on every node, because every node validating that
// block at that height sees the same committed prefix and so the same bonds,
// the same validator set and the same unbonding clocks. An honest leader will
// not propose one (buildProposalLocked skips it), and a malicious leader that
// does gets its block voted down.
func (e *Engine) verifyStakeTxLocked(tx *token.Transaction, height uint64) error {
	req, err := ParseStakeRecipient(tx.To)
	if err != nil {
		return err
	}
	sender := tx.SenderID()
	// You may only stake your OWN coins on your OWN account. Bonding into
	// someone else's account would be a gift of voting power, and withdrawing
	// from someone else's would be theft; neither is a thing this protocol
	// offers, and delegation is a feature with its own design rather than a side
	// effect of a missing check.
	if req.Account != sender {
		return fmt.Errorf("%w: a %s must name its own sender, not %s", ErrInvalidMessage, req.Op, req.Account)
	}

	switch req.Op {
	case StakeOpBond:
		if tx.Amount == 0 {
			return fmt.Errorf("%w: a bond must carry a non-zero amount", ErrInvalidMessage)
		}
		// Affordability is NOT checked here. A transfer the sender cannot afford
		// is deterministically skipped at apply time, which is how every other
		// transfer behaves, and making it a validity rule instead would let a
		// balance changing between proposal and commit invalidate a whole block.
		return nil

	case StakeOpWithdraw:
		if tx.Amount != 0 {
			return fmt.Errorf("%w: a withdrawal carries no amount; it returns the whole bond, got %d",
				ErrInvalidMessage, tx.Amount)
		}
		if e.stake == nil {
			return fmt.Errorf("%w: this network does not use bonded stake", ErrInvalidMessage)
		}
		at, allowed, err := e.stake.WithdrawableAt(sender, e.vset(), e.unbondingPeriod)
		if err != nil {
			return err
		}
		if !allowed {
			return fmt.Errorf("%w: %s is still a validator", ErrBondLocked, sender)
		}
		if height < at {
			return fmt.Errorf("%w: %s may withdraw from height %d, this block is height %d",
				ErrBondLocked, sender, at, height)
		}
		return nil

	default:
		return fmt.Errorf("%w: unknown stake operation %q", ErrInvalidMessage, req.Op)
	}
}

// setChangesInLocked extracts the changes a block carries. Callers must hold
// e.mu.
func (e *Engine) setChangesInLocked(b *Block) []SetChange {
	var out []SetChange
	for i := range b.Txs {
		if !IsSetChangeRecipient(b.Txs[i].To) {
			continue
		}
		change, err := ParseSetChange(b.Txs[i].To, b.Height)
		if err != nil {
			// Unreachable for a block that passed verification, and skipping is the
			// safe reading if it ever is reached.
			continue
		}
		out = append(out, change)
	}
	return out
}

// approvesChangesLocked reports whether this operator has approved every change
// in a block.
//
// This is the veto. It is local policy on purpose: the protocol cannot decide
// whether admitting a particular key is a good idea, so each operator lists the
// changes they will vote for and their node prevotes nil on anything else. A
// change therefore needs a quorum of operators to have listed it - which is
// what stops one validator from proposing the removal of all the others and
// having it wave through. Callers must hold e.mu.
func (e *Engine) approvesChangesLocked(changes []SetChange) bool {
	for _, c := range changes {
		if _, ok := e.approvedChanges[strings.ToLower(c.String())]; !ok {
			return false
		}
	}
	return true
}

// mayPrevoteLocked decides whether this node may prevote a block that differs
// from the one it has locked, given the polka a proposal carries. Callers must
// hold e.mu.
//
// The only acceptable proof is a quorum of PREVOTES for the proposed block at a
// round >= our locked round. That is what "the network moved on" means, and by
// quorum intersection it cannot exist for a block conflicting with one that has
// already committed: a commit is a quorum of precommits for one block at one
// round, its members locked on that block, and they will not prevote a
// conflicting one without this same proof - which no one can produce.
//
// A quorum of precommits is deliberately NOT accepted here: that is a commit
// certificate, and a node holding one commits the block rather than voting on
// it. PolkaCertificate.Verify rejects precommit votes for this reason.
func (e *Engine) mayPrevoteLocked(hkey string, polka *PolkaCertificate) error {
	if polka == nil {
		return fmt.Errorf("%w: locked on %s, proposal %s carries no polka certificate",
			ErrInvalidMessage, e.lockedHash, hkey)
	}
	if err := polka.Verify(e.vset()); err != nil {
		return fmt.Errorf("%w: invalid polka certificate: %v", ErrInvalidMessage, err)
	}
	if polka.Height != e.height {
		return fmt.Errorf("%w: polka certificate for wrong height", ErrInvalidMessage)
	}
	if polka.Round < e.lockedRound {
		return fmt.Errorf("%w: polka certificate round %d < locked round %d",
			ErrInvalidMessage, polka.Round, e.lockedRound)
	}
	// The certificate must endorse the block being proposed (a leader cannot wave
	// an unrelated certificate to unlock followers onto a third block).
	if fmt.Sprintf("%x", polka.BlockHash) != hkey {
		return fmt.Errorf("%w: polka certificate does not endorse the proposed block", ErrInvalidMessage)
	}
	return nil
}

// acceptProposal verifies a block for the current height and caches it,
// reporting whether this node may prevote FOR it.
//
// It returns an error - which the caller uses to drop or stash the message -
// only when the block cannot be used at this height at all. A block that is
// perfectly valid but conflicts with this node's lock is still cached and
// returns support=false: the node has to prevote nil for that round, and
// holding the body costs nothing and helps if the network later proves the
// block should win.
func (e *Engine) acceptProposal(b *Block, round uint64, polka *PolkaCertificate) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.verifyBlockForHeightLocked(b); err != nil {
		return false, err
	}

	hash := b.Hash()
	hkey := fmt.Sprintf("%x", hash)

	// Ingest the certificate's votes so we can build certificates ourselves and
	// see the polka even if we missed the raw prevote gossip. This is also how a
	// node learns which value it must propose when it becomes leader.
	if polka != nil {
		if err := polka.Verify(e.vset()); err == nil && polka.Height == e.height {
			for i := range polka.Votes {
				v := polka.Votes[i]
				e.recordVoteLocked(&v)
			}
		}
	}

	// Cache the block for this height so votes can be tallied against it.
	if _, seen := e.proposals[hkey]; !seen {
		cp := *b
		e.proposals[hkey] = &cp
	}
	if round > e.round {
		e.round = round
		e.roundDeadline = time.Now().Add(e.roundTimeoutForLocked(e.round))
	}

	// Voting discipline: a locked node prevotes only its locked block, unless the
	// proposal proves a quorum prevoted this one at a round >= the lock.
	if e.locked && hkey != e.lockedHash {
		if err := e.mayPrevoteLocked(hkey, polka); err != nil {
			return false, nil
		}
		e.locked = false
	}

	// The operator's veto on validator-set changes. The block is valid and
	// cached; this node just will not support it.
	if changes := e.setChangesInLocked(b); len(changes) > 0 && !e.approvesChangesLocked(changes) {
		for _, c := range changes {
			fmt.Printf("consensus: refusing to vote for a block that would %s "+
				"(not in consensus.approved_changes)\n", c)
		}
		return false, nil
	}
	return true, nil
}

// selfVoteAtLocked reports the block hash key this node itself voted for in one
// phase at one round of the current height, if it voted at all.
//
// Vote-once-per-round is checked against the vote record rather than against a
// "last vote" field, because a nil vote for a round the engine has just left
// can be cast after it has already voted in the new one. A single field would be
// rewound by that and could let a second, conflicting vote through in the round
// it had already spoken for - an equivocation, and the one thing an honest
// validator must never produce. The record cannot be rewound. Callers must hold
// e.mu.
func (e *Engine) selfVoteAtLocked(typ VoteType, round uint64) (string, bool) {
	byRound := e.prevotes
	if typ == VoteTypePrecommit {
		byRound = e.precommits
	}
	for hkey, byVoter := range byRound[round] {
		if _, ok := byVoter[e.selfID]; ok {
			return hkey, true
		}
	}
	return "", false
}

// recordVoteLocked files a signed vote for the current height in the phase it
// belongs to, so it can later back a certificate. Callers must hold e.mu. Votes
// for other heights are ignored.
func (e *Engine) recordVoteLocked(v *Vote) {
	e.recordVoteDetectingConflictLocked(v)
}

// recordVoteDetectingConflictLocked files a vote and reports a vote by the SAME
// validator, in the SAME phase of the SAME round, for a DIFFERENT block.
//
// That pair is equivocation, and this is the only place it can be noticed: an
// honest validator prevotes once and precommits once per round, so the engine
// used to file the second vote under a different block hash and count on
// without comment. Callers must hold e.mu.
func (e *Engine) recordVoteDetectingConflictLocked(v *Vote) *Vote {
	if v.Height != e.height {
		return nil
	}
	byRound := e.prevotes
	if v.Type == VoteTypePrecommit {
		byRound = e.precommits
	} else if v.Type != VoteTypePrevote {
		return nil
	}
	byHash := byRound[v.Round]
	if byHash == nil {
		byHash = make(map[string]map[string]Vote)
		byRound[v.Round] = byHash
	}
	hkey := fmt.Sprintf("%x", v.BlockHash)

	var conflict *Vote
	for otherHash, byVoter := range byHash {
		if otherHash == hkey {
			continue
		}
		if prior, ok := byVoter[v.VoterID]; ok {
			cp := prior
			conflict = &cp
			break
		}
	}

	set := byHash[hkey]
	if set == nil {
		set = make(map[string]Vote)
		byHash[hkey] = set
	}
	if _, ok := set[v.VoterID]; !ok {
		set[v.VoterID] = *v
	}
	return conflict
}

// castPrevote casts and broadcasts this node's prevote for a block hash (or the
// nil marker) at a round, then re-evaluates the round in case that prevote
// completed a polka.
func (e *Engine) castPrevote(ctx context.Context, hash []byte, round uint64) {
	e.castVoteMsg(ctx, VoteTypePrevote, hash, round)
}

// castPrecommit casts and broadcasts this node's precommit, which is also what
// takes the lock: from here on this node will not prevote a different block at
// this height without being shown a polka for it at a round >= this one.
func (e *Engine) castPrecommit(ctx context.Context, hash []byte, round uint64) {
	e.castVoteMsg(ctx, VoteTypePrecommit, hash, round)
}

// castVoteMsg signs, records and broadcasts one vote, then re-evaluates the
// height: a prevote may have completed a polka (which leads to a precommit) and
// a precommit may have completed a commit.
//
// The voting discipline is enforced here, under the lock, so no two goroutines
// can slip two votes past it:
//
//   - One prevote and one precommit per (height, round). A second, different
//     vote of the same phase in the same round is refused.
//   - A precommit for a block takes the lock at that round; the lock round only
//     advances, so a stale re-proposal cannot lower it.
//
// Together these guarantee an honest validator never contributes its precommit
// to two conflicting blocks at one height, which is exactly what quorum
// intersection needs to make divergence impossible.
func (e *Engine) castVoteMsg(ctx context.Context, typ VoteType, hash []byte, round uint64) {
	if !e.isValidator.Load() {
		return
	}
	hkey := fmt.Sprintf("%x", hash)
	nilVote := isNilVoteHash(hash)

	e.mu.Lock()
	// Never vote for a block whose body we do not hold: the vote would be
	// unverifiable for us and could not be justified to anyone else.
	if !nilVote {
		if _, ok := e.proposals[hkey]; !ok {
			e.mu.Unlock()
			return
		}
	}
	if typ != VoteTypePrevote && typ != VoteTypePrecommit {
		e.mu.Unlock()
		return
	}
	if prior, voted := e.selfVoteAtLocked(typ, round); voted && prior != hkey {
		e.mu.Unlock()
		return
	}
	if typ == VoteTypePrecommit && !nilVote && (!e.locked || round >= e.lockedRound) {
		e.locked = true
		e.lockedRound = round
		e.lockedHash = hkey
	}

	v := &Vote{
		Type:      typ,
		Height:    e.height,
		Round:     round,
		BlockHash: append([]byte(nil), hash...),
		VoterID:   e.selfID,
		PublicKey: e.self.PublicKey,
	}
	if err := v.Sign(e.self.PrivateKey); err != nil {
		e.mu.Unlock()
		return
	}
	// Record our own vote first so a single-validator (already quorate) set makes
	// progress without waiting for the network to echo it back.
	e.recordVoteLocked(v)
	e.mu.Unlock()

	if data, err := json.Marshal(v); err == nil {
		_ = e.transport.Publish(ctx, TopicVote, data)
	}
	e.onVotes(ctx)
}

// onVotes re-evaluates the height after new votes: a prevote quorum may need
// answering with a precommit, and a precommit quorum may be a commit.
func (e *Engine) onVotes(ctx context.Context) {
	e.processPrevoteQuorum(ctx)
	e.maybeCommit(ctx)
}

// processPrevoteQuorum reacts to prevote quorums at this height. It records the
// highest-round polka'd block as the value a leader must propose, and precommits
// it when this node is in that round - which is where the lock is taken.
//
// A quorum of NIL prevotes is answered with a nil precommit: the round agreed on
// nothing, and saying so lets the height move to the next round with everyone's
// position on the record instead of waiting out a timeout.
func (e *Engine) processPrevoteQuorum(ctx context.Context) {
	e.mu.Lock()
	vs := e.vset()
	quorum := vs.QuorumPower()
	var (
		polkaRound uint64
		polkaHash  string
		polkaFound bool
		nilRound   uint64
		nilFound   bool
	)
	for round, byHash := range e.prevotes {
		for hkey, voters := range byHash {
			if vs.PowerOfVoters(voters) < quorum {
				continue
			}
			if isNilVoteHashKey(hkey) {
				if !nilFound || round > nilRound {
					nilRound, nilFound = round, true
				}
				continue
			}
			if !polkaFound || round > polkaRound {
				polkaRound, polkaHash, polkaFound = round, hkey, true
			}
		}
	}
	if polkaFound && (!e.hasValid || polkaRound >= e.validRound) {
		e.validRound, e.validHash, e.hasValid = polkaRound, polkaHash, true
	}

	var (
		hash  []byte
		round uint64
		cast  bool
	)
	_, alreadyPrecommitted := e.selfVoteAtLocked(VoteTypePrecommit, e.round)
	switch {
	case polkaFound && polkaRound == e.round && !alreadyPrecommitted:
		if b, ok := e.proposals[polkaHash]; ok {
			hash, round, cast = b.Hash(), polkaRound, true
		}
	case nilFound && nilRound == e.round && !alreadyPrecommitted:
		hash, round, cast = nilVoteHash(), nilRound, true
	}
	e.mu.Unlock()

	if cast {
		e.castPrecommit(ctx, hash, round)
	}
}

// handleVote validates a received vote and files it, then re-evaluates the
// height (a prevote may complete a polka, a precommit may complete a commit).
func (e *Engine) handleVote(ctx context.Context, msg transport.Message) {
	var v Vote
	if err := json.Unmarshal(msg.Payload, &v); err != nil {
		return
	}
	if err := v.Verify(); err != nil {
		return
	}
	// The voter must be a validator (only validator votes count toward quorum).
	if !e.vset().Contains(v.VoterID) {
		return
	}
	e.tallyVote(&v)
	e.onVotes(ctx)
}

// tallyVote files a validator's vote: at the current height into the phase it
// belongs to, at a later height into the catch-up buffer. Votes for heights we
// have passed are dropped. Callers need not hold the lock; tallyVote takes it.
func (e *Engine) tallyVote(v *Vote) {
	e.mu.Lock()
	var pending *Equivocation
	defer func() {
		e.mu.Unlock()
		if pending != nil {
			// Outside the lock: recording touches the store and gossiping touches
			// the transport.
			e.reportEquivocation(context.Background(), pending, true)
		}
	}()
	switch {
	case v.Height == e.height:
		if conflict := e.recordVoteDetectingConflictLocked(v); conflict != nil {
			eq := NewEquivocation(*conflict, *v)
			pending = &eq
		}
	case v.Height > e.height && v.Height <= e.height+maxFutureStash:
		// We are lagging: buffer the vote so it counts the moment we reach that
		// height, so a node that fell behind still collects the quorum that let the
		// rest of the network commit.
		byHash := e.futureVotes[v.Height]
		if byHash == nil {
			byHash = make(map[string]map[string]Vote)
			e.futureVotes[v.Height] = byHash
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
}

// maybeCommit checks whether any block at the current height has reached a
// quorum of PRECOMMITS in one round and, if so, commits and applies it, then
// advances to the next height. It commits at most one block per call. The apply
// step runs deterministically so every honest node reaches identical balances.
//
// The quorum must come from a single round. Counting a block's precommits across
// rounds would commit on a quorum that existed at no single moment, and it would
// break the certificate a node hands to a peer that missed the block: the
// certificate is exactly this quorum, and a receiver checks it the same way.
func (e *Engine) maybeCommit(ctx context.Context) {
	e.mu.Lock()
	if e.committing {
		e.mu.Unlock()
		return
	}
	vs := e.vset()
	quorum := vs.QuorumPower()
	var (
		winner      *Block
		winnerRound uint64
		winnerKey   string
	)
	for round, byHash := range e.precommits {
		for hkey, voters := range byHash {
			if vs.PowerOfVoters(voters) < quorum || isNilVoteHashKey(hkey) {
				continue
			}
			b, ok := e.proposals[hkey]
			if !ok {
				// We have a quorum for a block we have not seen the body of yet; wait
				// for it to arrive (gossip may reorder, and block sync will ask for it).
				// It is re-evaluated on the next vote/tick.
				continue
			}
			if winner == nil || round > winnerRound {
				winner, winnerRound, winnerKey = b, round, hkey
			}
		}
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
	endorsements := e.endorsementsLocked(winnerRound, winnerKey)
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

	// Catch-up: adopt any block we buffered for the height we just entered, so a
	// node that fell behind commits the next block as soon as it has the body;
	// combined with the buffered votes seeded in advanceHeight this lets a lagging
	// node converge without waiting for new proposals.
	e.drainFutureProposals(ctx)

	// Re-check: buffered votes seeded for the new height may already form a
	// quorum for a block whose body we hold, allowing an immediate chained commit.
	e.onVotes(ctx)

	// Pipelining: immediately propose for the next height if we are its leader and
	// have pending work, rather than waiting for the next driver tick. This keeps
	// latency low across back-to-back blocks. tick takes its own locks.
	e.tick(ctx)
}

// endorsementsLocked collects the precommits for the given block hash at the
// given round - the quorum that committed it here. They are persisted with the
// block so this node can later prove the commit to a peer that missed the body,
// and being a single round's quorum they are a certificate in their own right.
// Callers must hold e.mu.
func (e *Engine) endorsementsLocked(round uint64, hkey string) []Vote {
	byVoter := e.precommits[round][hkey]
	out := make([]Vote, 0, len(byVoter))
	for _, v := range byVoter {
		out = append(out, v)
	}
	return out
}

// isNilVoteHashKey reports whether a hex hash key is the nil-vote marker.
func isNilVoteHashKey(hkey string) bool {
	for i := 0; i < len(hkey); i++ {
		if hkey[i] != '0' {
			return false
		}
	}
	return len(hkey) == 2*HashSize
}

// quorumWithoutBodyLocked reports whether some block at the current height has
// reached a precommit quorum while this node still lacks its body. That is the
// permanent-stall condition: nothing in the ordinary flow will ever deliver
// that body again. Callers must hold e.mu.
func (e *Engine) quorumWithoutBodyLocked() bool {
	vs := e.vset()
	quorum := vs.QuorumPower()
	for _, byHash := range e.precommits {
		for hkey, voters := range byHash {
			if vs.PowerOfVoters(voters) < quorum || isNilVoteHashKey(hkey) {
				continue
			}
			if _, ok := e.proposals[hkey]; !ok {
				return true
			}
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
	if !e.isValidator.Load() {
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

// reportEquivocation verifies, records and (when it is new to us) gossips proof
// that a validator voted two ways.
//
// It also acts on it, when EjectEquivocators is on: the offender's removal
// becomes a change this node votes for and offers. That is sound only because
// the evidence proves itself - both votes carry the offender's own signature,
// and this node checks them rather than trusting whoever sent them - so every
// honest node reaches the same conclusion from the same proof instead of some
// of them applying an enforcement rule the others do not.
//
// The removal still goes through consensus and still waits for an epoch
// boundary. Nothing here changes the set directly: a node that ejected a
// validator on its own authority would fork away from its peers.
func (e *Engine) reportEquivocation(ctx context.Context, eq *Equivocation, gossip bool) {
	if err := eq.Verify(e.vset()); err != nil {
		// Either not really equivocation, or not from a validator. Either way it
		// is not evidence.
		return
	}

	isNew := true
	if e.evidence != nil {
		recorded, err := e.evidence.Record(eq)
		if err != nil {
			fmt.Printf("consensus: could not store equivocation evidence against %s: %v\n", eq.VoterID, err)
		} else {
			isNew = recorded
		}
	} else {
		// With no store we cannot tell a repeat from a first sighting, so the
		// in-memory guard is the best available: one report per offence per run.
		e.mu.Lock()
		if e.seenEquivocations == nil {
			e.seenEquivocations = make(map[string]struct{})
		}
		if _, seen := e.seenEquivocations[eq.Key()]; seen {
			isNew = false
		} else {
			e.seenEquivocations[eq.Key()] = struct{}{}
		}
		e.mu.Unlock()
	}
	if !isNew {
		return
	}

	fmt.Printf("consensus: EQUIVOCATION by validator %s at height %d round %d (%s): "+
		"two signed votes for different blocks\n", eq.VoterID, eq.Height, eq.Round, eq.Type)

	// Act on it. The set is no longer fixed at startup, so the offence has a
	// remedy the protocol can carry: this node approves the offender's removal
	// and starts offering it, and every other node holding the same evidence
	// does too. A quorum of them commits the removal, and it takes effect at the
	// next epoch boundary like any other set change. The offender's own vote
	// against it is one vote.
	if e.ejectEquivocators {
		e.mu.Lock()
		e.approveRemovalLocked(eq.VoterID)
		e.mu.Unlock()
		what := "eject"
		if e.stake != nil {
			what = "slash and eject"
		}
		fmt.Printf("consensus: voting to %s %s; it takes effect at an epoch boundary "+
			"once a quorum of validators holding the same evidence has committed it\n", what, eq.VoterID)
	}

	if e.onEquivocation != nil {
		e.onEquivocation(eq)
	}
	if gossip {
		if data, err := json.Marshal(eq); err == nil {
			_ = e.transport.Publish(ctx, TopicEvidence, data)
		}
	}
}

// handleEvidence ingests equivocation proof from a peer.
//
// A stranger's report is actionable here only because it is self-proving: the
// record carries both signed votes, so this node checks them itself and never
// takes the sender's word. Evidence that does not verify is dropped in silence,
// exactly like any other malformed gossip.
func (e *Engine) handleEvidence(ctx context.Context, msg transport.Message) {
	var eq Equivocation
	if err := json.Unmarshal(msg.Payload, &eq); err != nil {
		return
	}
	// Re-gossip only what is new to us, which stops a permanent echo.
	e.reportEquivocation(ctx, &eq, true)
}

// Equivocations returns the offences this node has on record, oldest height
// first. It is how an operator or an API surfaces them.
func (e *Engine) Equivocations() ([]Equivocation, error) {
	if e.evidence == nil {
		return nil, nil
	}
	return e.evidence.All()
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

// applySyncedBlock ingests one served block: it files the accompanying
// precommits through the same verified path ordinary vote gossip takes, caches
// the body for the current height (or stashes it for a later one), and then
// re-runs the ordinary commit check.
//
// Nothing here is a new commit rule. The block commits only when the precommits
// reach the same single-round quorum a proposed block needs, over votes that
// each verified individually against the validator set and endorse this exact
// body. A response with no votes, too few, prevotes instead of precommits, or
// votes for a different block leaves this node exactly where it was. Caching a
// body is not voting for it, so this path cannot make a node contribute to a
// conflicting block - it can only let a node commit what a quorum has already
// committed.
func (e *Engine) applySyncedBlock(ctx context.Context, cb *CommittedBlock) {
	b := &cb.Block
	hash := b.Hash()

	for i := range cb.Votes {
		v := &cb.Votes[i]
		if v.Type != VoteTypePrecommit {
			continue
		}
		if v.Height != b.Height || !bytesEqual(v.BlockHash, hash) {
			continue
		}
		if !e.vset().Contains(v.VoterID) {
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
		e.stashFutureProposal(ctx, b, 0, nil, nil)
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
	// Withdrawals are collected here and performed after the transfer critical
	// section: StakeLedger.Withdraw takes the ledger lock itself, and calling it
	// from inside Atomically would deadlock. Their validity was already decided
	// when the block was verified at this height.
	var withdrawals []string
	var withdrawFor []string
	if err := e.ledger.Atomically(func(ltx market.LedgerTx) error {
		for i := range b.Txs {
			tx := &b.Txs[i]
			if IsStakeRecipient(tx.To) {
				// A BOND is an ordinary transfer into the reserved bond account, so
				// it falls through to the transfer path below and gets the same
				// affordability check and deterministic skip as any other transfer.
				// Only a WITHDRAWAL needs its own handling, because it moves the
				// whole bond back rather than an amount the transaction names.
				if req, err := ParseStakeRecipient(tx.To); err == nil && req.Op == StakeOpWithdraw {
					withdrawals = append(withdrawals, mempoolKey(tx))
					withdrawFor = append(withdrawFor, tx.SenderID())
					applied[mempoolKey(tx)] = true
					continue
				}
			}
			if IsSetChangeRecipient(tx.To) {
				// A set change carries no value (block validation enforces that) and its
				// recipient is a reserved marker, not an account. Transferring zero to it
				// would only bring a phantom balance key into existence, so the ledger is
				// left alone; the change itself is applied at the epoch boundary.
				applied[mempoolKey(tx)] = true
				continue
			}
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
	// Return the bonds of every withdrawal the block carried. Deterministic: the
	// whole bond moves, and the block was only valid at this height if the
	// withdrawal was permitted at this height, which every node evaluated
	// against the same committed prefix.
	for i, key := range withdrawals {
		id := withdrawFor[i]
		if e.stake == nil {
			// Unreachable: the validity rule refuses a withdrawal on a network
			// without a stake ledger. Guarded because a nil dereference in the
			// commit path would take the node down.
			applied[key] = false
			continue
		}
		returned, err := e.stake.Withdraw(id, e.vset(), e.unbondingPeriod, b.Height)
		if err != nil {
			// Unreachable for a block that passed verification. Record it as not
			// applied rather than failing the commit: the block is already in the
			// chain on every other node, so refusing it here would fork this node
			// off rather than protect it.
			fmt.Printf("consensus: withdrawal for %s in committed block %d did not apply: %v\n", id, b.Height, err)
			applied[key] = false
			continue
		}
		if returned > 0 {
			fmt.Printf("consensus: returned bond of %d to %s at height %d\n", returned, id, b.Height)
		}
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

	// A committed bond or withdrawal changes what the next boundary should weigh
	// the set by. Nothing else does, so nothing else makes the boundary do I/O.
	for i := range committed.Txs {
		if IsStakeRecipient(committed.Txs[i].To) {
			e.stakeDirty = true
			break
		}
	}

	// Collect any validator-set changes this block carried. They wait for the
	// next epoch boundary, so the set is stable for a run of heights and every
	// node switches at the same height.
	if changes := e.setChangesInLocked(committed); len(changes) > 0 {
		e.pendingChanges = append(e.pendingChanges, changes...)
		if err := e.sets.SavePending(e.pendingChanges); err != nil {
			fmt.Printf("consensus: could not persist pending set changes: %v\n", err)
		}
		for _, c := range changes {
			fmt.Printf("consensus: committed set change at height %d: %s (takes effect at the next epoch)\n",
				committed.Height, c)
		}
	}

	e.height = committed.Height + 1
	e.headHash = committed.Hash()
	e.round = 0
	e.roundDeadline = time.Now().Add(e.roundTimeoutForLocked(e.round))
	e.proposals = make(map[string]*Block)
	// Reset the per-height voting discipline state: a fresh height starts with no
	// vote cast, no lock, no polka'd value and no vote evidence.
	e.prevotes = make(map[uint64]map[string]map[string]Vote)
	e.precommits = make(map[uint64]map[string]map[string]Vote)
	e.lockedRound = 0
	e.lockedHash = ""
	e.locked = false
	e.validRound = 0
	e.validHash = ""
	e.hasValid = false
	// Seed the new height from any votes we buffered while lagging, so a node
	// that fell behind immediately counts the quorum the rest of the network
	// already produced for this height - and can prove that commit onward to
	// another lagging peer, even though it never saw those votes arrive live.
	if buffered, ok := e.futureVotes[e.height]; ok {
		for _, byVoter := range buffered {
			for _, v := range byVoter {
				vv := v
				e.recordVoteLocked(&vv)
			}
		}
		delete(e.futureVotes, e.height)
	}
	// Drop any stale buffered votes for heights we have now passed.
	for h := range e.futureVotes {
		if h < e.height {
			delete(e.futureVotes, h)
		}
	}
	e.applyEpochBoundaryLocked()
	// Now that the height, the pending changes and (at a boundary) the set are
	// all up to date, drop the set-change transactions the chain has overtaken.
	// Left in the mempool they would make every block carrying them invalid.
	e.pruneStaleSetChangesLocked()
	e.committing = false
}

// pruneStaleSetChangesLocked drops the set-change transactions in the mempool
// that can no longer commit: a duplicate of a change already in force or
// already pending, and one whose submitter has left the validator set. Such a
// transaction makes any block carrying it invalid, so it is not merely useless
// - left in place it would be proposed again and again by whichever node holds
// it. Callers must hold e.mu.
func (e *Engine) pruneStaleSetChangesLocked() {
	kept := e.mempool[:0]
	for i := range e.mempool {
		if IsSetChangeRecipient(e.mempool[i].To) {
			if err := e.verifySetChangeLocked(&e.mempool[i], e.height); err != nil {
				delete(e.mempoolSet, mempoolKey(&e.mempool[i]))
				continue
			}
		}
		kept = append(kept, e.mempool[i])
	}
	e.mempool = append([]token.Transaction(nil), kept...)
}

// applyEpochBoundaryLocked swaps in the new validator set when the height just
// entered starts an epoch. Callers must hold e.mu.
//
// Every node crosses the boundary at the same height and applies the same
// changes in the same order, because both come from the committed chain. That
// is the whole reason changes wait: applying them the moment they commit would
// have nodes switching sets at whatever moment each one happened to apply the
// block, and a leader schedule that differs by one height is a fork.
func (e *Engine) applyEpochBoundaryLocked() {
	if e.epochLength == 0 || e.height%e.epochLength != 0 {
		return
	}
	// A boundary with nothing pending still re-weights the set from bonded
	// stake, so a bond posted mid-epoch takes effect at the next boundary rather
	// than never - but only when a bond has actually moved since the last
	// boundary. Re-reading every balance to arrive at the numbers already in
	// hand is I/O under e.mu on the hot path, and at a short epoch length it is
	// most of what the driver does.
	reweight := e.stake != nil && (e.stakeDirty || e.stakeNeverWeighted)
	if len(e.pendingChanges) == 0 && !reweight {
		return
	}

	previous := e.vset()
	next, err := previous.WithChanges(e.pendingChanges)
	if err != nil {
		// Every change was checked when its block was validated, so this should be
		// unreachable. If it happens, keeping the current set is the only safe
		// move: dropping to an unusable set would end the chain.
		fmt.Printf("consensus: refusing an unusable validator set at height %d: %v\n", e.height, err)
		e.pendingChanges = nil
		if err := e.sets.SavePending(nil); err != nil {
			fmt.Printf("consensus: could not clear pending set changes: %v\n", err)
		}
		return
	}

	// Weight the new set by bonded stake, and take the bond of anyone this
	// boundary slashes. Both happen HERE rather than when the change committed,
	// so power changes at the same height on every node - a node weighting a
	// vote differently from its peers would compute a different quorum from the
	// same votes, which is a fork.
	if reweight || len(e.pendingChanges) > 0 {
		next = e.reweightLocked(next)
		e.stakeDirty = false
		e.stakeNeverWeighted = false
	}
	e.applyStakeSideEffectsLocked(previous, next)

	applied := e.pendingChanges
	e.pendingChanges = nil
	e.validatorSet.Store(next)
	wasValidator := e.isValidator.Load()
	nowValidator := e.selfID != "" && next.Contains(e.selfID)
	e.isValidator.Store(nowValidator)

	if err := e.sets.SaveActive(next, e.height); err != nil {
		fmt.Printf("consensus: could not persist the new validator set: %v\n", err)
	}
	if err := e.sets.SavePending(nil); err != nil {
		fmt.Printf("consensus: could not clear pending set changes: %v\n", err)
	}

	for _, c := range applied {
		fmt.Printf("consensus: validator set change in force at height %d: %s\n", e.height, c)
	}
	fmt.Printf("consensus: validator set is now %d members, total power %d, quorum %d\n",
		next.Len(), next.TotalPower(), next.QuorumPower())
	if wasValidator && !nowValidator {
		fmt.Printf("consensus: this node is no longer a validator; it will follow and apply blocks but not vote\n")
	}
	if !wasValidator && nowValidator {
		fmt.Printf("consensus: this node is now a validator and will propose and vote\n")
	}

	// The per-height voting state was reset by the caller for the new height, but
	// the round-robin schedule just changed under us, so the current round's
	// leader is a different validator. Nothing else to do: the driver picks that
	// up on its next tick.
}

// reweightLocked returns vs with voting power taken from bonded stake, or vs
// unchanged on a network without stake. Callers must hold e.mu.
func (e *Engine) reweightLocked(vs *ValidatorSet) *ValidatorSet {
	if e.stake == nil {
		return vs
	}
	bonds, err := e.stake.BondedFor(vs.IDs())
	if err != nil {
		// Reading a balance failed. Keeping the set as it is - equal power, or
		// whatever it carried over - is wrong in the same way on every node only
		// if every node fails, which is not something we can rely on, so say so
		// loudly rather than silently diverging.
		fmt.Printf("consensus: could not read bonded stake at height %d, leaving voting power unchanged: %v\n",
			e.height, err)
		return vs
	}

	// ALL OR NOTHING. If any member has bonded nothing, the whole set stays at
	// equal power.
	//
	// Weighting a partly-bonded set is the dangerous case, not the safe one: an
	// unbonded member counts 1, so the first validator to bond anything at all
	// holds essentially the entire voting power and the rest of the network
	// cannot outvote it - it could not even be slashed, because a slash needs a
	// quorum it now controls. A network turning stake on would hand itself to
	// whoever bonded first.
	//
	// Staying at headcount until every member has bonded makes the transition
	// atomic: the set flips to stake weighting at one boundary, when the last
	// validator has posted its bond. A validator that refuses to bond holds the
	// network at headcount, which is the status quo and something its peers can
	// answer by removing it - a far smaller problem than the alternative.
	var unbonded []string
	for _, id := range vs.IDs() {
		if bonds[id] == 0 {
			unbonded = append(unbonded, id)
		}
	}
	if len(unbonded) > 0 {
		fmt.Printf("consensus: %d of %d validators have bonded nothing, so voting power stays equal "+
			"(weighting a partly-bonded set would hand the network to whoever bonded first); "+
			"first unbonded: %s\n", len(unbonded), vs.Len(), unbonded[0])
		return vs
	}

	weighted, err := vs.WithPower(bonds)
	if err != nil {
		fmt.Printf("consensus: could not weight the validator set at height %d: %v\n", e.height, err)
		return vs
	}
	return weighted
}

// applyStakeSideEffectsLocked runs the stake bookkeeping a set change implies:
// it starts the unbonding clock for validators that just left, stops it for
// those that just joined, and takes the bond of anyone this boundary slashed.
// Callers must hold e.mu.
func (e *Engine) applyStakeSideEffectsLocked(previous, next *ValidatorSet) {
	if e.stake == nil {
		return
	}
	for _, id := range previous.IDs() {
		if next.Contains(id) {
			continue
		}
		// Left the set. The unbonding clock starts now, so the bond stays
		// slashable for UnbondingPeriod blocks after the departure - which is what
		// stops a validator equivocating and withdrawing before the evidence
		// lands.
		if err := e.stake.RecordLeftSet(id, e.height); err != nil {
			fmt.Printf("consensus: %v\n", err)
		}
	}
	for _, id := range next.IDs() {
		if previous.Contains(id) {
			continue
		}
		// Joined (or rejoined) the set: it is a validator, so no unbonding clock
		// is running and any earlier departure is stale.
		if err := e.stake.ClearLeftSet(id); err != nil {
			fmt.Printf("consensus: %v\n", err)
		}
	}
	for _, c := range e.pendingChanges {
		if c.Kind != SetChangeSlash {
			continue
		}
		taken, err := e.stake.Slash(c.ValidatorID)
		if err != nil {
			fmt.Printf("consensus: %v\n", err)
			continue
		}
		fmt.Printf("consensus: SLASHED %s at height %d: %d native base units moved from its bond to the reward pool\n",
			c.ValidatorID, e.height, taken)
	}
}

// maybeProposeApprovedChanges submits the validator-set changes this operator
// approved and that have not taken effect yet, so an approval is an action and
// not just a vote.
//
// It re-submits on a slow cadence because a change only commits once a QUORUM of
// operators has approved it, and operators do not edit their configs
// simultaneously. The first node to approve an ejection proposes a block the
// others vote down; it keeps offering the change, and the moment enough peers
// have approved it too, the next proposal carries it through. Without the
// retry, the change would be lost to whoever approved it first.
//
// A change already in force, already waiting for the epoch boundary, or already
// sitting in this node's mempool is skipped, so a spec left in the config after
// it has been applied costs nothing.
func (e *Engine) maybeProposeApprovedChanges() {
	if !e.isValidator.Load() || e.self == nil {
		return
	}

	e.mu.Lock()
	if len(e.approvedSpecs) == 0 {
		e.mu.Unlock()
		return
	}
	// One attempt per round timeout: often enough that an approval lands within a
	// few blocks of the quorum reaching it, rare enough that a change nobody else
	// has approved does not fill every proposal.
	if !e.lastSetChangeSubmit.IsZero() && time.Since(e.lastSetChangeSubmit) < e.roundTimeout {
		e.mu.Unlock()
		return
	}
	e.lastSetChangeSubmit = time.Now()

	vs := e.vset()
	pending := make(map[string]struct{}, len(e.pendingChanges))
	for _, c := range e.pendingChanges {
		pending[c.String()] = struct{}{}
	}
	inMempool := make(map[string]struct{}, len(e.mempool))
	for i := range e.mempool {
		if IsSetChangeRecipient(e.mempool[i].To) {
			inMempool[e.mempool[i].To] = struct{}{}
		}
	}

	// A proposal is dropped from the mempool after one attempt (see
	// dropSetChangesFromMempool), so an offer that was voted down leaves no trace
	// to dedup against. Wait several rounds before offering again: long enough
	// that the previous offer has either committed or lost its vote.
	const offerBackoffRounds = 4
	var todo []SetChange
	for _, spec := range e.approvedSpecs {
		key := spec.String()
		if spec.InForce(vs) {
			continue
		}
		if _, waiting := pending[key]; waiting {
			continue
		}
		if _, queued := inMempool[spec.Recipient()]; queued {
			continue
		}
		if last, offered := e.setChangeOffered[key]; offered && time.Since(last) < offerBackoffRounds*e.roundTimeout {
			continue
		}
		e.setChangeOffered[key] = time.Now()
		todo = append(todo, spec)
	}
	nonce := e.setChangeNonce
	e.setChangeNonce += uint64(len(todo))
	e.mu.Unlock()

	for i, spec := range todo {
		// Zero value: a set change moves no credits, and block validation refuses
		// one that tries to.
		if _, err := e.SubmitAccountTransfer(e.self, spec.Recipient(), 0, nonce+uint64(i)); err != nil {
			fmt.Printf("consensus: could not offer the approved set change %s: %v\n", spec, err)
		}
	}
}

// mustAdvanceToEpochBoundaryLocked reports whether the chain has to keep
// producing blocks even with nothing to put in them. Callers must hold e.mu.
//
// A committed set change - or a committed bond - takes effect at the next epoch
// boundary, and a height only exists once a block commits. On an idle chain no
// blocks are produced, so without this a change would commit and then wait
// forever for a boundary the chain never reaches: an admitted validator that
// never joins, an equivocating one that is never ejected, and a bond that never
// becomes voting power, purely because the network is quiet.
//
// The exception is bounded: at most one empty block per height between the
// commit and the boundary, and the boundary clears the pending changes (even
// when applying them fails), so this can never become a permanent stream of
// empty blocks.
func (e *Engine) mustAdvanceToEpochBoundaryLocked() bool {
	// A committed bond that has not been weighted yet counts too, for the same
	// reason: an operator who bonded on a quiet network would otherwise see
	// their stake do nothing at all until unrelated traffic happened to arrive.
	// Bounded the same way - the boundary clears the flag.
	return len(e.pendingChanges) > 0 || (e.stake != nil && e.stakeDirty)
}

// approveRemovalLocked makes the ejection of validatorID a change this node
// will vote for and offer, exactly as if the operator had listed it under
// consensus.approved_changes. It is idempotent. Callers must hold e.mu - except
// in New, before any goroutine exists.
//
// On a network with bonded stake this approves a SLASH rather than a plain
// removal: the offence is provable, so taking the bond is the point. Without
// stake there is no bond to take and the offender simply loses its place.
func (e *Engine) approveRemovalLocked(validatorID string) {
	kind := SetChangeRemove
	if e.stake != nil {
		kind = SetChangeSlash
	}
	change := SetChange{Kind: kind, ValidatorID: validatorID}
	key := strings.ToLower(change.String())
	if _, already := e.approvedChanges[key]; already {
		return
	}
	e.approvedChanges[key] = struct{}{}
	e.approvedSpecs = append(e.approvedSpecs, change)
}

// maybeTopUpBond submits a bond for the shortfall between what this node has
// bonded and what its operator configured, so bonding is something a node does
// rather than something an operator has to find an RPC for.
//
// Idempotent and self-healing: it bonds the DIFFERENCE, so a partial top-up
// completes on the next attempt and a node funded gradually still reaches its
// target. It bonds nothing it cannot afford, and nothing at all once the target
// is met.
func (e *Engine) maybeTopUpBond() {
	if e.stake == nil || e.self == nil || e.targetBond == 0 {
		return
	}

	e.mu.Lock()
	if !e.lastBondTop.IsZero() && time.Since(e.lastBondTop) < bondTopUpInterval {
		e.mu.Unlock()
		return
	}
	e.lastBondTop = time.Now()
	// A bond already waiting to commit would be counted twice, because the
	// ledger does not reflect it until its block lands.
	for i := range e.mempool {
		if IsStakeRecipient(e.mempool[i].To) && e.mempool[i].SenderID() == e.selfID {
			e.mu.Unlock()
			return
		}
	}
	nonce := e.bondNonce
	e.bondNonce++
	e.mu.Unlock()

	bonded, err := e.stake.Bonded(e.selfID)
	if err != nil {
		fmt.Printf("consensus: could not read this node's bond: %v\n", err)
		return
	}
	if bonded >= e.targetBond {
		return
	}
	shortfall := e.targetBond - bonded

	spendable, err := e.ledger.Balance(e.selfID)
	if err != nil {
		fmt.Printf("consensus: could not read this node's balance: %v\n", err)
		return
	}
	if spendable == 0 {
		fmt.Printf("consensus: this node wants %d more bonded but its consensus account (%s) holds nothing; "+
			"fund that account before it can validate on a staked network\n", shortfall, e.selfID)
		return
	}
	amount := shortfall
	if amount > spendable {
		amount = spendable
	}

	if _, err := e.SubmitBond(e.self, amount, nonce); err != nil {
		fmt.Printf("consensus: could not submit a bond of %d: %v\n", amount, err)
		return
	}
	fmt.Printf("consensus: bonding %d native base units (bonded %d, target %d)\n", amount, bonded, e.targetBond)
}

// bondTopUpInterval is how often a node checks whether its bond is short. Slow
// on purpose: it corrects a config drift, it is not a hot path.
const bondTopUpInterval = 5 * time.Second

// PendingSetChanges returns the changes waiting for the next epoch boundary.
func (e *Engine) PendingSetChanges() []SetChange {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]SetChange(nil), e.pendingChanges...)
}

// EpochLength is how many committed blocks pass between set changes taking
// effect.
func (e *Engine) EpochLength() uint64 { return e.epochLength }

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
		e.prevotes = make(map[uint64]map[string]map[string]Vote)
		e.precommits = make(map[uint64]map[string]map[string]Vote)
		e.lockedRound = 0
		e.lockedHash = ""
		e.locked = false
		e.validRound = 0
		e.validHash = ""
		e.hasValid = false
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
func (e *Engine) ValidatorSet() *ValidatorSet { return e.vset() }
