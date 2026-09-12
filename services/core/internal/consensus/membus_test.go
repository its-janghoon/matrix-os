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
	// the ordered log, which is what a real network's genesis file is.
	//
	// It exists because the state root makes out-of-band ledger writes visible:
	// a node that did not receive the same seed reaches different balances from
	// the same blocks and correctly refuses to vote. Production forbids the
	// equivalent - FundAccount is refused on a multi-validator node for exactly
	// this reason - so a joiner replaying this seed is modelling the rule rather
	// than working around it.
	seeded []seedCredit
}

// seedCredit is one out-of-band credit applied to every node in a cluster.
type seedCredit struct {
	account string
	amount  uint64
}

// recordSeed notes a credit given to the whole cluster.
func (b *memBus) recordSeed(account string, amount uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seeded = append(b.seeded, seedCredit{account: account, amount: amount})
}

// applySeed replays the cluster's seed into a ledger.
func (b *memBus) applySeed(ledger *market.Ledger) error {
	b.mu.Lock()
	seeds := append([]seedCredit(nil), b.seeded...)
	b.mu.Unlock()
	for _, s := range seeds {
		if err := ledger.Credit(s.account, s.amount); err != nil {
			return err
		}
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
