package consensus

import (
	"context"
	"github.com/ecirlabs/matrix-core/internal/market"
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/ecirlabs/matrix-core/internal/transport"
)

// memBus is an in-memory gossip bus that fans a published message out to every
// subscriber of a topic across all connected nodes. It lets a test wire N
// consensus engines together without libp2p, exactly as the marketexchange tests
// drive the Transport interface with a fake network. It is safe for concurrent
// use.
type memBus struct {
	mu   sync.Mutex
	subs map[string][]*memSub
	// deliver, when non-nil, decides whether a message published by `from`
	// reaches the subscriber owned by `to` on `topic`. Returning false drops it,
	// which is how a test models the partial connectivity that gossip really
	// has: a node that misses one message on one topic, or one that is cut off
	// entirely for a while. Drops are the reason block sync exists, so a test
	// that cannot drop a message cannot exercise it.
	deliver func(from, to peer.ID, topic string) bool
	// seeded records the credits every node in this cluster was given OUTSIDE
	// the ordered log, which is what a real network's genesis file is - and the
	// POSITION in the log each one is applied at.
	//
	// The position is the whole point. The state root makes out-of-band ledger
	// writes visible, so a credit written to four ledgers one at a time leaves
	// the four holding different balances for the length of the loop, and that
	// is not a cosmetic window: a block landing inside it is applied against
	// different balances on different nodes, so a transfer can be afforded on
	// one and skipped as unaffordable on another. Skipped is committed, and
	// committed is never retried, so the two ledgers never come back together.
	// Production forbids the equivalent - FundAccount is refused on a
	// multi-validator node for exactly this reason - so pinning each credit to a
	// log position is modelling the rule rather than working around it.
	seeded []seedCredit
}

// seedCredit is one out-of-band credit, and the height every node applies it at.
type seedCredit struct {
	// height is the block this credit lands immediately BEFORE. A node whose
	// next block is this height has applied it; one still below has not.
	height  uint64
	account string
	amount  uint64
}

// fileSeed records a credit every node applies just before the block at
// `height`.
//
// Callers pick a height NO node has committed yet, so every node necessarily
// crosses it: see mintAll.
func (b *memBus) fileSeed(height uint64, account string, amount uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seeded = append(b.seeded, seedCredit{height: height, account: account, amount: amount})
}

// filedSeeds returns a snapshot of every credit filed so far.
func (b *memBus) filedSeeds() []seedCredit {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]seedCredit(nil), b.seeded...)
}

// seedApplier applies the cluster's filed credits to ONE node's ledger, each
// exactly once, at the log position it was filed against.
//
// Exactly-once is what lets two different callers drive it without coordinating:
// the commit path applies whatever the block it just committed made due, and
// mintAll applies a credit directly to a node that is already standing at the
// height it was filed against. Both go through here, so a node reached by both
// is credited once.
type seedApplier struct {
	bus    *memBus
	ledger *market.Ledger
	mu     sync.Mutex
	// applied is indexed by position in the bus's seed list, which is
	// append-only, so an index names the same credit forever.
	applied map[int]bool
}

func newSeedApplier(bus *memBus, ledger *market.Ledger) *seedApplier {
	return &seedApplier{bus: bus, ledger: ledger, applied: map[int]bool{}}
}

// appliedAt reports whether every credit filed against `height` has been applied
// to this node's ledger.
func (s *seedApplier) appliedAt(height uint64) bool {
	seeds := s.bus.filedSeeds()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, seed := range seeds {
		if seed.height == height && !s.applied[i] {
			return false
		}
	}
	return true
}

// beforeBlock applies every credit filed against a height at or below `height`,
// which is what a node whose NEXT block is `height` owes.
//
// At or below, rather than exactly at: a node catching up by block sync jumps
// heights, and it still has to arrive at the same balances as the nodes that
// walked there one block at a time.
func (s *seedApplier) beforeBlock(height uint64) error {
	seeds := s.bus.filedSeeds()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, seed := range seeds {
		if seed.height > height || s.applied[i] {
			continue
		}
		if err := s.ledger.Credit(seed.account, seed.amount); err != nil {
			return err
		}
		s.applied[i] = true
	}
	return nil
}

type memSub struct {
	owner  peer.ID
	ch     chan transport.Message
	ctx    context.Context
	closed bool
}

func newMemBus() *memBus {
	return &memBus{subs: make(map[string][]*memSub)}
}

// setDeliveryFilter installs (or with nil clears) the delivery predicate.
func (b *memBus) setDeliveryFilter(f func(from, to peer.ID, topic string) bool) {
	b.mu.Lock()
	b.deliver = f
	b.mu.Unlock()
}

// endpoint is one node's view of the shared bus. Each engine gets its own
// endpoint (with a distinct peer ID) so a node can be identified as the source
// of a message and, like real gossipsub, does not need special-casing for its
// own publishes (the engine tallies its own votes locally regardless).
type endpoint struct {
	bus  *memBus
	self peer.ID
}

func (b *memBus) endpoint(self peer.ID) *endpoint {
	return &endpoint{bus: b, self: self}
}

// Subscribe registers a channel for topic that receives every message published
// to that topic (including this node's own publishes, matching gossipsub, which
// the engine tolerates). The channel closes when ctx is cancelled.
func (e *endpoint) Subscribe(ctx context.Context, topic string) (<-chan transport.Message, error) {
	sub := &memSub{owner: e.self, ch: make(chan transport.Message, 1024), ctx: ctx}
	e.bus.mu.Lock()
	e.bus.subs[topic] = append(e.bus.subs[topic], sub)
	e.bus.mu.Unlock()

	go func() {
		<-ctx.Done()
		e.bus.mu.Lock()
		if !sub.closed {
			sub.closed = true
			close(sub.ch)
		}
		e.bus.mu.Unlock()
	}()
	return sub.ch, nil
}

// Publish delivers data to every subscriber of topic on a best-effort basis. A
// full subscriber buffer drops the message (as gossip may), which the engine's
// re-proposal/re-vote and round timeouts tolerate.
func (e *endpoint) Publish(ctx context.Context, topic string, data []byte) error {
	msg := transport.Message{From: e.self, Topic: topic, Payload: append([]byte(nil), data...)}

	// Hold the bus lock across the closed-check and the send so it cannot race
	// with the ctx-cancel goroutine that sets closed and closes the channel. The
	// send is non-blocking (buffered channel + default) so holding the lock never
	// blocks on a slow consumer.
	e.bus.mu.Lock()
	defer e.bus.mu.Unlock()
	for _, s := range e.bus.subs[topic] {
		if s.closed {
			continue
		}
		if e.bus.deliver != nil && !e.bus.deliver(e.self, s.owner, topic) {
			continue
		}
		select {
		case s.ch <- msg:
		default:
			// Drop on a full buffer; consensus tolerates dropped gossip.
		}
	}
	return nil
}
