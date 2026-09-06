package inference

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// fakeSettler is a fully controllable Settler for unit-testing the Service's
// charge computation and settlement-confirmation semantics without a real
// consensus engine. It records the amount it was asked to settle and lets a test
// dictate the committed/applied outcome WaitForSettlement reports.
type fakeSettler struct {
	mu sync.Mutex

	lastAmount uint64
	lastNonce  uint64
	submitted  int

	// outcome controls what WaitForSettlement reports.
	committed bool
	applied   bool
	waitErr   error
	submitErr error
}

func (f *fakeSettler) SubmitAccountTransfer(from *token.Account, recipient string, amount, nonce uint64) (*token.Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.submitErr != nil {
		return nil, f.submitErr
	}
	f.lastAmount = amount
	f.lastNonce = nonce
	f.submitted++
	// Return a minimally-valid transaction; the fake never inspects it beyond
	// identity, and WaitForSettlement below is keyed on the fake's own outcome.
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

// newTestService builds a Service over a fresh in-memory market with the given
// settler, registering an echo backend and a provider at the given price, and
// funding the buyer. It returns the service plus the buyer/provider IDs.
func newTestService(t *testing.T, settler Settler, pricePerUnit, buyerCredits uint64) (*Service, string, string) {
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
	buyer, _ := token.GenerateAccount()
	provider, _ := token.GenerateAccount()
	buyerID := buyer.AccountID()
	providerID := provider.AccountID()
	if err := mkt.Ledger().Credit(buyerID, buyerCredits); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if err := mkt.RegisterProvider(market.Provider{ID: providerID, Capacity: 100000, PricePerUnit: pricePerUnit}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	registry := NewRegistry()
	if err := registry.Register(providerID, NewEchoBackend()); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}
	svc, err := NewService(Config{
		Market:   mkt,
		Registry: registry,
		Settler:  settler,
		Accounts: memAccounts{m: map[string]*token.Account{buyerID: buyer}},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, buyerID, providerID
}

// TestFulfillJob_PriceScaling verifies the settled amount is scaled by the
// provider's PricePerUnit (not the raw token count). With price 3 and the echo
// backend's deterministic 7-unit usage, the charge must be 7 * 3 = 21.
func TestFulfillJob_PriceScaling(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	// unitsEstimate 8 reserves price 8*3 = 24; buyer must afford it.
	svc, buyerID, providerID := newTestService(t, fs, 3, 1000)

	job, err := svc.SubmitInferenceJob(buyerID, providerID, InferenceRequest{Prompt: "hello world"}, 8)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	fulfilled, err := svc.FulfillJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("FulfillJob: %v", err)
	}
	if fulfilled.Status != InferenceJobCompleted {
		t.Fatalf("status = %q, want completed", fulfilled.Status)
	}
	// 7 tokens * price 3 = 21.
	if got := fs.amount(); got != 21 {
		t.Fatalf("settled amount = %d, want 21 (7 units * price 3)", got)
	}
	if fulfilled.Units != 21 {
		t.Fatalf("job.Units = %d, want 21", fulfilled.Units)
	}
}

// TestFulfillJob_ClampsToEstimate verifies that when the backend reports MORE
// units than were reserved, the charge is clamped to the reserved estimate and
// scaled by price, never exceeding the reserved (affordability-checked) price.
func TestFulfillJob_ClampsToEstimate(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	// Reserve only 3 units at price 2 => reserved price 6. The echo backend for
	// "hello world" reports 7 units, which exceeds the estimate and must be
	// clamped to 3, giving a charge of 3 * 2 = 6 (== reserved price), not 7 * 2.
	svc, buyerID, providerID := newTestService(t, fs, 2, 1000)

	job, err := svc.SubmitInferenceJob(buyerID, providerID, InferenceRequest{Prompt: "hello world"}, 3)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	fulfilled, err := svc.FulfillJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("FulfillJob: %v", err)
	}
	if got := fs.amount(); got != 6 {
		t.Fatalf("settled amount = %d, want 6 (clamped 3 units * price 2)", got)
	}
	if fulfilled.Units != 6 {
		t.Fatalf("job.Units = %d, want 6", fulfilled.Units)
	}
	// The charge must never exceed the reserved price.
	mjob, _ := svc.market.GetJob(job.MarketJobID)
	if fs.amount() > mjob.Price {
		t.Fatalf("charge %d exceeded reserved price %d", fs.amount(), mjob.Price)
	}
}

// TestFulfillJob_UnaffordableSettlementNotCompleted verifies Issue #3: when the
// settlement commits but is SKIPPED at apply time (buyer could not afford it),
// the job is reported FAILED, never COMPLETED, and an error is returned. No
// silent "completed" payment that never landed.
func TestFulfillJob_UnaffordableSettlementNotCompleted(t *testing.T) {
	// committed but NOT applied => the transfer was skipped as unaffordable.
	fs := &fakeSettler{committed: true, applied: false}
	svc, buyerID, providerID := newTestService(t, fs, 1, 1000)

	job, err := svc.SubmitInferenceJob(buyerID, providerID, InferenceRequest{Prompt: "hello world"}, 8)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	fulfilled, err := svc.FulfillJob(context.Background(), job.ID)
	if err == nil {
		t.Fatal("expected an error when settlement is skipped as unaffordable")
	}
	if fulfilled == nil {
		t.Fatal("expected the job record to be returned even on unaffordable settlement")
	}
	if fulfilled.Status == InferenceJobCompleted {
		t.Fatal("SAFETY: job must NOT be COMPLETED when its payment did not apply")
	}
	if fulfilled.Status != InferenceJobFailed {
		t.Fatalf("status = %q, want failed", fulfilled.Status)
	}
	// The reserved capacity must have been released back to the provider.
	prov, _ := svc.market.GetProvider(providerID)
	if prov.Available != prov.Capacity {
		t.Fatalf("capacity not released: available=%d capacity=%d", prov.Available, prov.Capacity)
	}
}

// TestFulfillJob_SettlingWhenUnconfirmed verifies that when settlement cannot be
// confirmed within the wait (WaitForSettlement returns an error, e.g. timeout),
// the job is left in the honest SETTLING state rather than COMPLETED, and no
// error masks a possibly-still-pending payment.
func TestFulfillJob_SettlingWhenUnconfirmed(t *testing.T) {
	fs := &fakeSettler{waitErr: context.DeadlineExceeded}
	svc, buyerID, providerID := newTestService(t, fs, 1, 1000)

	job, err := svc.SubmitInferenceJob(buyerID, providerID, InferenceRequest{Prompt: "hello world"}, 8)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	fulfilled, err := svc.FulfillJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("FulfillJob returned error for unconfirmed settlement: %v", err)
	}
	if fulfilled.Status != InferenceJobSettling {
		t.Fatalf("status = %q, want settling (unconfirmed settlement)", fulfilled.Status)
	}
	// The completion and billed units are populated even while settling.
	if fulfilled.Units != 7 {
		t.Fatalf("job.Units = %d, want 7", fulfilled.Units)
	}
	if fulfilled.Completion == "" {
		t.Fatal("completion should be populated in SETTLING state")
	}
}

// TestFulfillJob_SubmitError verifies a settlement submit failure marks the job
// failed and returns an error (no completion claimed).
func TestFulfillJob_SubmitError(t *testing.T) {
	fs := &fakeSettler{submitErr: errors.New("boom")}
	svc, buyerID, providerID := newTestService(t, fs, 1, 1000)

	job, err := svc.SubmitInferenceJob(buyerID, providerID, InferenceRequest{Prompt: "hello world"}, 8)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	if _, err := svc.FulfillJob(context.Background(), job.ID); err == nil {
		t.Fatal("expected error on submit failure")
	}
	got, _ := svc.GetJob(job.ID)
	if got.Status != InferenceJobFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
}
