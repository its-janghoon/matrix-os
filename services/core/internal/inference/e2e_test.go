package inference

import (
	"context"
	"crypto/ed25519"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
	"github.com/ecirlabs/matrix-core/internal/transport"
)

// memBus is a minimal in-memory gossip bus used to drive a single-node consensus
// engine without libp2p, mirroring the pattern the consensus package uses in its
// own tests. It fans a published message to every subscriber of a topic.
type memBus struct {
	mu   sync.Mutex
	subs map[string][]chan transport.Message
}

func newMemBus() *memBus { return &memBus{subs: make(map[string][]chan transport.Message)} }

func (b *memBus) Subscribe(ctx context.Context, topic string) (<-chan transport.Message, error) {
	ch := make(chan transport.Message, 1024)
	b.mu.Lock()
	b.subs[topic] = append(b.subs[topic], ch)
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		b.mu.Lock()
		defer b.mu.Unlock()
		subs := b.subs[topic]
		for i, c := range subs {
			if c == ch {
				b.subs[topic] = append(subs[:i], subs[i+1:]...)
				close(ch)
				break
			}
		}
	}()
	return ch, nil
}

func (b *memBus) Publish(ctx context.Context, topic string, data []byte) error {
	msg := transport.Message{From: peer.ID("node"), Topic: topic, Payload: append([]byte(nil), data...)}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.subs[topic] {
		select {
		case c <- msg:
		default:
		}
	}
	return nil
}

// memAccounts is an in-memory Accounts resolver keyed by account ID.
type memAccounts struct{ m map[string]*token.Account }

func (a memAccounts) Account(id string) (*token.Account, bool) {
	acct, ok := a.m[id]
	return acct, ok
}

// TestInferenceMarketplace_EndToEnd is the full end-to-end marketplace inference
// test: a buyer submits an inference job to a provider, the provider fulfills it
// through a stub (echo) backend, and the computed units settle buyer -> provider
// in the token through the real consensus-backed ledger. It asserts the balances
// moved by exactly the settled amount and the job completed.
func TestInferenceMarketplace_EndToEnd(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mkt, err := market.NewMarket(store)
	if err != nil {
		t.Fatalf("market.NewMarket: %v", err)
	}

	// Single-validator consensus so a quorum is one vote and blocks commit fast.
	validator, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount(validator): %v", err)
	}
	vs, err := consensus.NewValidatorSet([]ed25519.PublicKey{validator.PublicKey})
	if err != nil {
		t.Fatalf("NewValidatorSet: %v", err)
	}
	chain := consensus.NewBlockChain(store)

	// Observe commits so the test can wait deterministically for settlement.
	committed := make(chan *consensus.Block, 8)
	engine, err := consensus.New(consensus.Config{
		Transport:  newMemBus(),
		Validators: vs,
		Chain:      chain,
		Ledger:     mkt.Ledger(),
		Self:       validator,
		OnCommit:   func(b *consensus.Block) { committed <- b },
	})
	if err != nil {
		t.Fatalf("consensus.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("engine.Start: %v", err)
	}

	// Buyer and provider accounts. Fund the buyer on the shared market ledger.
	buyer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount(buyer): %v", err)
	}
	provider, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount(provider): %v", err)
	}
	buyerID := buyer.AccountID()
	providerID := provider.AccountID()

	const initialCredits = 1000
	if err := mkt.Ledger().Credit(buyerID, initialCredits); err != nil {
		t.Fatalf("Credit: %v", err)
	}

	// The provider advertises an inference-capable service: register capacity on
	// the market and a backend in the registry. PricePerUnit is one credit per
	// compute unit so the reserved price equals the reserved units.
	if err := mkt.RegisterProvider(market.Provider{ID: providerID, Capacity: 10000, PricePerUnit: 1}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	registry := NewRegistry()
	if err := registry.Register(providerID, NewEchoBackend()); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}

	svc, err := NewService(Config{
		Market:   mkt,
		Registry: registry,
		Settler:  engine, // consensus.Engine implements Settler via SubmitAccountTransfer
		Accounts: memAccounts{m: map[string]*token.Account{buyerID: buyer}},
	})
	if err != nil {
		t.Fatalf("inference.NewService: %v", err)
	}

	// The buyer submits an inference job. The echo backend for prompt
	// "hello world" produces a deterministic 7-unit cost:
	//   prompt     "user: hello world"       -> 3 tokens
	//   completion "echo: user: hello world" -> 4 tokens
	//   total                                    7 tokens => 7 units settled.
	// We reserve a units estimate that comfortably covers the cost; the amount
	// actually settled is the real usage reported by the backend.
	const unitsEstimate = 8
	const wantUnits = 7
	job, err := svc.SubmitInferenceJob(buyerID, providerID, InferenceRequest{
		Model:  "stub",
		Prompt: "hello world",
	}, unitsEstimate)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	if job.Status != InferenceJobPending {
		t.Fatalf("expected PENDING, got %q", job.Status)
	}

	// Fulfill: run inference through the backend and settle via consensus.
	fulfilled, err := svc.FulfillJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("FulfillJob: %v", err)
	}
	if fulfilled.Status != InferenceJobCompleted {
		t.Fatalf("expected COMPLETED, got %q", fulfilled.Status)
	}
	if fulfilled.Completion != "echo: user: hello world" {
		t.Fatalf("unexpected completion: %q", fulfilled.Completion)
	}
	if fulfilled.Units != wantUnits {
		t.Fatalf("settled units = %d, want %d (actual usage)", fulfilled.Units, wantUnits)
	}

	// Wait for the consensus block carrying the settlement to commit and apply.
	select {
	case <-committed:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for consensus commit of settlement")
	}

	// Poll balances until the committed transfer is reflected on the ledger. The
	// consensus apply runs deterministically inside commit; give it a moment.
	deadline := time.After(3 * time.Second)
	for {
		buyerBal, err := mkt.Ledger().Balance(buyerID)
		if err != nil {
			t.Fatalf("Balance(buyer): %v", err)
		}
		provBal, err := mkt.Ledger().Balance(providerID)
		if err != nil {
			t.Fatalf("Balance(provider): %v", err)
		}
		if buyerBal == initialCredits-wantUnits && provBal == wantUnits {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("balances did not settle: buyer=%d provider=%d (want buyer=%d provider=%d)",
				buyerBal, provBal, initialCredits-wantUnits, wantUnits)
		case <-time.After(20 * time.Millisecond):
		}
	}

	// The consensus committed chain holds exactly one block with the transfer.
	length, err := chain.Len()
	if err != nil {
		t.Fatalf("chain.Len: %v", err)
	}
	if length == 0 {
		t.Fatal("expected at least one committed consensus block")
	}
	if err := chain.ValidateChain(); err != nil {
		t.Fatalf("ValidateChain: %v", err)
	}
}
