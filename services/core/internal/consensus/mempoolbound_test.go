package consensus

import (
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
	"github.com/libp2p/go-libp2p/core/peer"
)

// newSubmitOnlyEngine builds an engine that is never Started, so no driver runs
// and nothing commits. That is what makes the mempool cap observable: on a live
// cluster blocks drain the mempool faster than a test can fill it, so the cap
// never binds and a test that only watched a running cluster would prove
// nothing about it.
func newSubmitOnlyEngine(t *testing.T, maxMempool int) *Engine {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	self, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	vs, err := NewValidatorSet([]ed25519.PublicKey{self.PublicKey})
	if err != nil {
		t.Fatalf("NewValidatorSet: %v", err)
	}
	bus := newMemBus()
	eng, err := New(Config{
		Transport:       bus.endpoint(peer.ID("submit-only")),
		Validators:      vs,
		Chain:           NewBlockChain(store),
		Ledger:          market.NewLedger(store),
		Self:            self,
		ProposeInterval: time.Hour,
		RoundTimeout:    time.Hour,
		Sets:            NewSetStore(store),
		ZeroMinBond:     true,
		MaxMempoolTxs:   maxMempool,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return eng
}

// TestTheMempoolIsBounded
//
// THE ATTACK. Engine.Submit checks the signature, a non-empty recipient, the
// nonce rules and the reserved-recipient rules. It does NOT check that the
// sender can afford the transfer - affordability is decided at apply time,
// where an unaffordable transfer is deterministically skipped - and the mempool
// was a plain slice that grew with every accepted transaction.
//
// So a keypair holding ZERO balance signs transfers at nonce 0, 1, 2, ... Each
// is a distinct (sender, nonce), each passes every check, each is appended, and
// each is held in memory and gossiped. The cost to the attacker is a signature;
// the cost to every node is memory.
func TestTheMempoolIsBounded(t *testing.T) {
	const cap = 64
	eng := newSubmitOnlyEngine(t, cap)

	// A pauper: a valid keypair with no balance at all.
	pauper, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}

	accepted, refused := 0, 0
	var lastErr error
	for nonce := uint64(0); nonce < uint64(cap)*4; nonce++ {
		err := eng.Submit(signedTransfer(t, pauper, "spam", 1, nonce))
		if err == nil {
			accepted++
			continue
		}
		refused++
		lastErr = err
	}

	eng.mu.Lock()
	held := len(eng.mempool)
	setSize := len(eng.mempoolSet)
	nonceSize := len(eng.mempoolNonces)
	eng.mu.Unlock()

	if held > cap {
		t.Fatalf("the mempool holds %d transactions with a cap of %d: an account with no balance "+
			"can grow it without bound", held, cap)
	}
	if refused == 0 {
		t.Fatalf("all %d transfers from a zero-balance account were accepted; the mempool is unbounded",
			accepted)
	}
	// The refusal has to be legible: an honest sender hitting a congested node
	// must be able to tell "retry later" from "your transfer was rejected".
	if !errors.Is(lastErr, ErrMempoolFull) {
		t.Fatalf("refusal error = %v, want ErrMempoolFull", lastErr)
	}
	// The bookkeeping maps must not outgrow the mempool either - a cap on the
	// slice alone would still leak memory through them.
	if setSize > cap || nonceSize > cap {
		t.Fatalf("mempool is capped at %d but its index maps hold %d/%d entries",
			cap, setSize, nonceSize)
	}
}

// TestABoundedMempoolStillAcceptsWorkAfterDraining. A cap that never releases
// would be its own denial of service: once full, no honest transfer could ever
// be submitted again. Committing must free room.
func TestABoundedMempoolStillAcceptsWorkAfterDraining(t *testing.T) {
	const cap = 8
	eng := newSubmitOnlyEngine(t, cap)

	pauper, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	for nonce := uint64(0); nonce < uint64(cap)*2; nonce++ {
		_ = eng.Submit(signedTransfer(t, pauper, "spam", 1, nonce))
	}

	honest, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	blocked := eng.Submit(signedTransfer(t, honest, "recipient", 100, 0))
	if !errors.Is(blocked, ErrMempoolFull) {
		t.Fatalf("expected the full mempool to refuse, got %v", blocked)
	}

	// Drain it the way a commit does.
	eng.mu.Lock()
	drained := make([]token.Transaction, len(eng.mempool))
	copy(drained, eng.mempool)
	eng.mempool = nil
	for i := range drained {
		delete(eng.mempoolSet, mempoolKey(&drained[i]))
		if nk, checked := nonceKey(&drained[i]); checked {
			delete(eng.mempoolNonces, nk)
		}
	}
	eng.mu.Unlock()

	if err := eng.Submit(signedTransfer(t, honest, "recipient", 100, 0)); err != nil {
		t.Fatalf("an honest transfer was refused after the mempool drained: %v", err)
	}
}

// TestALiveClusterNeverExceedsTheMempoolCap checks the same bound on a running
// cluster, where the interesting part is that it holds while blocks commit
// concurrently rather than that it ever fills.
func TestALiveClusterNeverExceedsTheMempoolCap(t *testing.T) {
	const cap = 64
	nodes, stop := newCluster(t, 1, func(c *Config) { c.MaxMempoolTxs = cap })
	defer stop()
	nd := nodes[0]

	pauper, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	peak := 0
	for nonce := uint64(0); nonce < uint64(cap)*8; nonce++ {
		_ = nd.engine.Submit(signedTransfer(t, pauper, "spam", 1, nonce))
		nd.engine.mu.Lock()
		if n := len(nd.engine.mempool); n > peak {
			peak = n
		}
		nd.engine.mu.Unlock()
	}
	if peak > cap {
		t.Fatalf("the mempool reached %d with a cap of %d while blocks were committing", peak, cap)
	}
}
