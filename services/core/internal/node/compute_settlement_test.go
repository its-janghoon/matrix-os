package node

import (
	"context"
	"crypto/ed25519"
	"errors"
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
// engine without libp2p, mirroring the pattern the consensus and inference
// packages use in their own tests. It fans a published message out to every
// subscriber of a topic.
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

func (b *memBus) Publish(_ context.Context, topic string, data []byte) error {
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

// fakeSettler is a controllable ComputeSettler for unit-testing the
// coordinator's confirmation semantics without a real consensus engine.
type fakeSettler struct {
	mu sync.Mutex

	lastRecipient string
	lastAmount    uint64
	lastNonce     uint64
	submitted     int

	committed bool
	applied   bool
	waitErr   error
	submitErr error
}

func (f *fakeSettler) SubmitAccountTransfer(_ *token.Account, recipient string, amount, nonce uint64) (*token.Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.submitErr != nil {
		return nil, f.submitErr
	}
	f.lastRecipient = recipient
	f.lastAmount = amount
	f.lastNonce = nonce
	f.submitted++
	return &token.Transaction{To: recipient, Amount: amount, Nonce: nonce}, nil
}

func (f *fakeSettler) WaitForSettlement(_ context.Context, _ *token.Transaction) (bool, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.committed, f.applied, f.waitErr
}

func (f *fakeSettler) amount() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastAmount
}

// newSettledMarket builds a market over a fresh store and seeds the buyer's
// balance honestly through the FEAT-002 Treasury reward-pool/genesis path so the
// balance is real native MATRIX (issued at genesis and moved from the reward
// pool), not an ad-hoc ledger credit. It registers a provider at pricePerUnit and
// returns the market, buyer and provider accounts.
func newSettledMarket(t *testing.T, pricePerUnit, buyerFunds uint64) (*market.Market, *token.Account, *token.Account) {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mkt, err := market.NewMarket(store)
	if err != nil {
		t.Fatalf("market.NewMarket: %v", err)
	}

	buyer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount(buyer): %v", err)
	}
	provider, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount(provider): %v", err)
	}

	// Seed the buyer's balance via honest native issuance: allocate a genesis
	// reward pool, then fund the buyer out of it (conserves supply, no ad-hoc
	// Credit). This proves the balances moved by settlement are real native
	// MATRIX.
	treasury := token.NewTreasury(mkt.Ledger(), store)
	if err := treasury.ApplyGenesis(nil, token.NativeMaxSupply); err != nil {
		t.Fatalf("ApplyGenesis: %v", err)
	}
	if err := treasury.FundFromRewardPool(buyer.AccountID(), buyerFunds); err != nil {
		t.Fatalf("FundFromRewardPool: %v", err)
	}

	if err := mkt.RegisterProvider(market.Provider{ID: provider.AccountID(), Capacity: 100000, PricePerUnit: pricePerUnit}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	return mkt, buyer, provider
}

// newConsensusEngine starts a single-validator consensus engine over an
// in-memory bus, wired to the given market ledger. It returns the engine and the
// committed-block channel for deterministic waiting.
func newConsensusEngine(t *testing.T, ctx context.Context, mkt *market.Market, store *kv.Store) (*consensus.Engine, <-chan *consensus.Block) {
	t.Helper()
	validator, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount(validator): %v", err)
	}
	vs, err := consensus.NewValidatorSet([]ed25519.PublicKey{validator.PublicKey})
	if err != nil {
		t.Fatalf("NewValidatorSet: %v", err)
	}
	committed := make(chan *consensus.Block, 8)
	engine, err := consensus.New(consensus.Config{
		Transport:  newMemBus(),
		Validators: vs,
		Chain:      consensus.NewBlockChain(store),
		Ledger:     mkt.Ledger(),
		Self:       validator,
		OnCommit:   func(b *consensus.Block) { committed <- b },
	})
	if err != nil {
		t.Fatalf("consensus.New: %v", err)
	}
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("engine.Start: %v", err)
	}
	return engine, committed
}

// TestComputeSettlement_EndToEndThroughConsensus proves a compute job moves REAL
// native-MATRIX balances via consensus: the buyer is debited exactly the job
// price and the provider is credited exactly the price, settled through the
// consensus-committed block chain, and the job ends COMPLETED with capacity
// restored. It exercises the same path node.ComputeSettlementCoordinator wires in
// production.
func TestComputeSettlement_EndToEndThroughConsensus(t *testing.T) {
	const (
		pricePerUnit = 5
		units        = 10
		buyerFunds   = 1000
		wantPrice    = units * pricePerUnit // 50
	)

	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mkt, err := market.NewMarket(store)
	if err != nil {
		t.Fatalf("market.NewMarket: %v", err)
	}

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

	// Honest native issuance: genesis reward pool -> fund buyer.
	treasury := token.NewTreasury(mkt.Ledger(), store)
	if err := treasury.ApplyGenesis(nil, token.NativeMaxSupply); err != nil {
		t.Fatalf("ApplyGenesis: %v", err)
	}
	if err := treasury.FundFromRewardPool(buyerID, buyerFunds); err != nil {
		t.Fatalf("FundFromRewardPool: %v", err)
	}
	if err := mkt.RegisterProvider(market.Provider{ID: providerID, Capacity: 100000, PricePerUnit: pricePerUnit}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	engine, committed := newConsensusEngine(t, ctx, mkt, store)

	coord, err := NewComputeSettlementCoordinator(mkt, engine, memAccounts{m: map[string]*token.Account{buyerID: buyer}})
	if err != nil {
		t.Fatalf("NewComputeSettlementCoordinator: %v", err)
	}

	job, err := mkt.SubmitJob(buyerID, providerID, units)
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	if job.Price != wantPrice {
		t.Fatalf("job price = %d, want %d", job.Price, wantPrice)
	}

	settled, err := coord.SettleAndCompleteJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("SettleAndCompleteJob: %v", err)
	}
	if settled.Status != market.JobCompleted {
		t.Fatalf("job status = %q, want completed", settled.Status)
	}

	// A consensus block carrying the settlement must have committed.
	select {
	case <-committed:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for consensus commit of compute settlement")
	}

	// Real native-MATRIX balances moved by exactly the price, through consensus.
	buyerBal, _ := mkt.Ledger().Balance(buyerID)
	provBal, _ := mkt.Ledger().Balance(providerID)
	if buyerBal != buyerFunds-wantPrice {
		t.Errorf("buyer balance = %d, want %d (funded %d - price %d)", buyerBal, buyerFunds-wantPrice, buyerFunds, wantPrice)
	}
	if provBal != wantPrice {
		t.Errorf("provider balance = %d, want %d", provBal, wantPrice)
	}

	// Capacity restored (the reservation was released, not double-spent).
	prov, _ := mkt.GetProvider(providerID)
	if prov.Available != prov.Capacity {
		t.Errorf("provider available = %d, want %d (reservation released)", prov.Available, prov.Capacity)
	}
}

// TestComputeSettlement_ChargedExactlyOnce drives a settlement through a real
// consensus engine and asserts the buyer is charged exactly once: the total
// debited equals a single job price even though both a consensus apply and a
// reservation release occur.
func TestComputeSettlement_ChargedExactlyOnce(t *testing.T) {
	const (
		pricePerUnit = 3
		units        = 4
		buyerFunds   = 500
		wantPrice    = units * pricePerUnit // 12
	)

	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mkt, err := market.NewMarket(store)
	if err != nil {
		t.Fatalf("market.NewMarket: %v", err)
	}
	buyer, _ := token.GenerateAccount()
	provider, _ := token.GenerateAccount()
	buyerID := buyer.AccountID()
	providerID := provider.AccountID()

	treasury := token.NewTreasury(mkt.Ledger(), store)
	if err := treasury.ApplyGenesis(nil, token.NativeMaxSupply); err != nil {
		t.Fatalf("ApplyGenesis: %v", err)
	}
	if err := treasury.FundFromRewardPool(buyerID, buyerFunds); err != nil {
		t.Fatalf("FundFromRewardPool: %v", err)
	}
	if err := mkt.RegisterProvider(market.Provider{ID: providerID, Capacity: 100000, PricePerUnit: pricePerUnit}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	engine, committed := newConsensusEngine(t, ctx, mkt, store)
	coord, err := NewComputeSettlementCoordinator(mkt, engine, memAccounts{m: map[string]*token.Account{buyerID: buyer}})
	if err != nil {
		t.Fatalf("NewComputeSettlementCoordinator: %v", err)
	}

	job, err := mkt.SubmitJob(buyerID, providerID, units)
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	if _, err := coord.SettleAndCompleteJob(ctx, job.ID); err != nil {
		t.Fatalf("SettleAndCompleteJob: %v", err)
	}
	select {
	case <-committed:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for consensus commit")
	}

	buyerBal, _ := mkt.Ledger().Balance(buyerID)
	if buyerBal != buyerFunds-wantPrice {
		t.Fatalf("buyer charged %d, want exactly one price %d (charged once)", buyerFunds-buyerBal, wantPrice)
	}
}

// TestComputeSettlement_UnaffordableNoPhantomCompletion verifies the honest
// failure path with a real consensus engine: a buyer who cannot afford the job
// price has the settlement SKIPPED at consensus apply time, so the job is NOT
// reported completed, no phantom balance moves, and the reservation is released.
func TestComputeSettlement_UnaffordableNoPhantomCompletion(t *testing.T) {
	const (
		pricePerUnit = 100
		units        = 10
		buyerFunds   = 50 // far less than price 1000
		wantPrice    = units * pricePerUnit
	)

	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mkt, err := market.NewMarket(store)
	if err != nil {
		t.Fatalf("market.NewMarket: %v", err)
	}
	buyer, _ := token.GenerateAccount()
	provider, _ := token.GenerateAccount()
	buyerID := buyer.AccountID()
	providerID := provider.AccountID()

	treasury := token.NewTreasury(mkt.Ledger(), store)
	if err := treasury.ApplyGenesis(nil, token.NativeMaxSupply); err != nil {
		t.Fatalf("ApplyGenesis: %v", err)
	}
	// Fund the buyer with enough to PASS the submit-time affordability check
	// (which is checked against the price), then drain it so the consensus apply
	// finds the account unaffordable. This isolates the "committed but skipped at
	// apply" path that a phantom-completion bug would mishandle.
	if err := treasury.FundFromRewardPool(buyerID, wantPrice); err != nil {
		t.Fatalf("FundFromRewardPool: %v", err)
	}
	if err := mkt.RegisterProvider(market.Provider{ID: providerID, Capacity: 100000, PricePerUnit: pricePerUnit}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}

	job, err := mkt.SubmitJob(buyerID, providerID, units)
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}

	// Now drain the buyer below the price so the settlement cannot apply. Move the
	// funds to a sink account through the ledger primitive (this is not the path
	// under test; it just sets up the unaffordable condition).
	if err := mkt.Ledger().Transfer(buyerID, "sink", buyerFunds+1); err == nil {
		// buyer had exactly wantPrice; move most of it away leaving < price.
	}
	// Ensure buyer now holds less than the price.
	if bal, _ := mkt.Ledger().Balance(buyerID); bal >= wantPrice {
		// Drain the remainder to be safe.
		_ = mkt.Ledger().Transfer(buyerID, "sink", bal-1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	engine, _ := newConsensusEngine(t, ctx, mkt, store)
	coord, err := NewComputeSettlementCoordinator(mkt, engine, memAccounts{m: map[string]*token.Account{buyerID: buyer}})
	if err != nil {
		t.Fatalf("NewComputeSettlementCoordinator: %v", err)
	}

	settled, err := coord.SettleAndCompleteJob(ctx, job.ID)
	if !errors.Is(err, ErrSettlementNotApplied) {
		t.Fatalf("SettleAndCompleteJob error = %v, want ErrSettlementNotApplied", err)
	}
	if settled != nil {
		t.Fatalf("expected no completed job on unaffordable settlement, got %+v", settled)
	}

	// SAFETY: the job must NOT be completed and the provider must not have been
	// paid a phantom balance.
	got, _ := mkt.GetJob(job.ID)
	if got.Status == market.JobCompleted {
		t.Fatal("SAFETY: job must NOT be COMPLETED when its payment did not apply")
	}
	provBal, _ := mkt.Ledger().Balance(providerID)
	if provBal != 0 {
		t.Errorf("provider balance = %d, want 0 (no phantom payment)", provBal)
	}
	// The reservation was released back to the provider.
	prov, _ := mkt.GetProvider(providerID)
	if prov.Available != prov.Capacity {
		t.Errorf("provider available = %d, want %d (reservation released)", prov.Available, prov.Capacity)
	}
}

// TestComputeSettlement_UnaffordableUnit uses the fake settler to assert the
// coordinator's semantics directly: a committed-but-not-applied outcome yields
// ErrSettlementNotApplied, no completion, and a released reservation, without a
// running consensus engine.
func TestComputeSettlement_UnaffordableUnit(t *testing.T) {
	mkt, buyer, provider := newSettledMarket(t, 5, 1000)
	buyerID := buyer.AccountID()
	providerID := provider.AccountID()

	job, err := mkt.SubmitJob(buyerID, providerID, 10)
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}

	fs := &fakeSettler{committed: true, applied: false}
	coord, err := NewComputeSettlementCoordinator(mkt, fs, memAccounts{m: map[string]*token.Account{buyerID: buyer}})
	if err != nil {
		t.Fatalf("NewComputeSettlementCoordinator: %v", err)
	}
	settled, err := coord.SettleAndCompleteJob(context.Background(), job.ID)
	if !errors.Is(err, ErrSettlementNotApplied) {
		t.Fatalf("error = %v, want ErrSettlementNotApplied", err)
	}
	if settled != nil {
		t.Fatalf("expected nil job, got %+v", settled)
	}
	if fs.amount() != job.Price {
		t.Errorf("submitted amount = %d, want job price %d", fs.amount(), job.Price)
	}
	got, _ := mkt.GetJob(job.ID)
	if got.Status == market.JobCompleted {
		t.Fatal("job must not be completed on unapplied settlement")
	}
	prov, _ := mkt.GetProvider(providerID)
	if prov.Available != prov.Capacity {
		t.Errorf("provider available = %d, want %d (reservation released)", prov.Available, prov.Capacity)
	}
}

// TestComputeSettlement_UnconfirmedLeavesJobReserved verifies that when the
// settlement cannot be confirmed (WaitForSettlement returns an error), the job
// is left reserved and uncompleted and the context error is surfaced, so the
// caller can retry rather than seeing a phantom completion.
func TestComputeSettlement_UnconfirmedLeavesJobReserved(t *testing.T) {
	mkt, buyer, provider := newSettledMarket(t, 5, 1000)
	buyerID := buyer.AccountID()
	providerID := provider.AccountID()

	job, err := mkt.SubmitJob(buyerID, providerID, 10)
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	reservedProv, _ := mkt.GetProvider(providerID)

	fs := &fakeSettler{waitErr: context.DeadlineExceeded}
	coord, err := NewComputeSettlementCoordinator(mkt, fs, memAccounts{m: map[string]*token.Account{buyerID: buyer}})
	if err != nil {
		t.Fatalf("NewComputeSettlementCoordinator: %v", err)
	}
	if _, err := coord.SettleAndCompleteJob(context.Background(), job.ID); err == nil {
		t.Fatal("expected an error when settlement cannot be confirmed")
	}

	got, _ := mkt.GetJob(job.ID)
	if got.Status != market.JobPending {
		t.Errorf("job status = %q, want pending (reservation intact)", got.Status)
	}
	prov, _ := mkt.GetProvider(providerID)
	if prov.Available != reservedProv.Available {
		t.Errorf("provider available = %d, want %d (reservation unchanged)", prov.Available, reservedProv.Available)
	}
}

// TestComputeSettlement_MissingSigningAccount verifies a job whose buyer has no
// resolvable signing account fails with ErrNoSigningAccount before any transfer
// is submitted.
func TestComputeSettlement_MissingSigningAccount(t *testing.T) {
	mkt, buyer, provider := newSettledMarket(t, 5, 1000)
	buyerID := buyer.AccountID()
	providerID := provider.AccountID()

	job, err := mkt.SubmitJob(buyerID, providerID, 10)
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	fs := &fakeSettler{committed: true, applied: true}
	// Empty resolver: the buyer's key is unknown.
	coord, err := NewComputeSettlementCoordinator(mkt, fs, memAccounts{m: map[string]*token.Account{}})
	if err != nil {
		t.Fatalf("NewComputeSettlementCoordinator: %v", err)
	}
	if _, err := coord.SettleAndCompleteJob(context.Background(), job.ID); !errors.Is(err, ErrNoSigningAccount) {
		t.Fatalf("error = %v, want ErrNoSigningAccount", err)
	}
	if fs.submitted != 0 {
		t.Errorf("no transfer should be submitted without a signing account, got %d", fs.submitted)
	}
}
