package market

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/kv"
)

func TestMarket_RegisterProvider(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
		wantErr  error
	}{
		{
			name:     "valid provider",
			provider: Provider{ID: "gpu-1", Capacity: 100, PricePerUnit: 5},
			wantErr:  nil,
		},
		{
			name:     "empty id rejected",
			provider: Provider{ID: "", Capacity: 100, PricePerUnit: 5},
			wantErr:  ErrInvalidProvider,
		},
		{
			name:     "zero capacity rejected",
			provider: Provider{ID: "gpu-2", Capacity: 0, PricePerUnit: 5},
			wantErr:  ErrInvalidProvider,
		},
		{
			name:     "zero price rejected",
			provider: Provider{ID: "gpu-3", Capacity: 100, PricePerUnit: 0},
			wantErr:  ErrInvalidProvider,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMarket(t, newTestStore(t))
			err := m.RegisterProvider(tt.provider)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RegisterProvider() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				if _, ok := m.GetProvider(tt.provider.ID); ok {
					t.Errorf("invalid provider %q should not be stored", tt.provider.ID)
				}
				return
			}
			stored, ok := m.GetProvider(tt.provider.ID)
			if !ok {
				t.Fatalf("provider %q not found after registration", tt.provider.ID)
			}
			if stored.Available != tt.provider.Capacity {
				t.Errorf("Available = %d, want %d (= Capacity)", stored.Available, tt.provider.Capacity)
			}
		})
	}
}

// newTestMarket constructs a Market over the given store, failing the test on
// any construction (rehydration) error.
func newTestMarket(t *testing.T, store *kv.Store) *Market {
	t.Helper()
	m, err := NewMarket(store)
	if err != nil {
		t.Fatalf("NewMarket() error = %v", err)
	}
	return m
}

// setupMarket registers one provider and credits a buyer, returning the market.
func setupMarket(t *testing.T, capacity, pricePerUnit, buyerCredits uint64) *Market {
	t.Helper()
	m := newTestMarket(t, newTestStore(t))
	if err := m.RegisterProvider(Provider{ID: "prov", Capacity: capacity, PricePerUnit: pricePerUnit}); err != nil {
		t.Fatalf("RegisterProvider() error = %v", err)
	}
	if buyerCredits > 0 {
		if err := m.Ledger().Credit("buyer", buyerCredits); err != nil {
			t.Fatalf("Credit() error = %v", err)
		}
	}
	return m
}

func TestMarket_SubmitJob_HappyPath(t *testing.T) {
	// capacity 100, price 5/unit, buyer has 1000 credits.
	m := setupMarket(t, 100, 5, 1000)

	job, err := m.SubmitJob("buyer", "prov", 10)
	if err != nil {
		t.Fatalf("SubmitJob() error = %v", err)
	}

	if job.Status != JobPending {
		t.Errorf("job status = %q, want %q", job.Status, JobPending)
	}
	if job.Price != 50 { // 10 units * 5 per unit
		t.Errorf("job price = %d, want 50", job.Price)
	}
	if job.ID == "" {
		t.Error("job ID should be generated")
	}

	// Available capacity decremented.
	prov, _ := m.GetProvider("prov")
	if prov.Available != 90 {
		t.Errorf("provider Available = %d, want 90", prov.Available)
	}

	// No credits move at submit time: buyer still holds full balance.
	bal, _ := m.Ledger().Balance("buyer")
	if bal != 1000 {
		t.Errorf("buyer balance = %d, want 1000 (nothing debited until completion)", bal)
	}
}

func TestMarket_SubmitJob_UnknownProvider(t *testing.T) {
	m := setupMarket(t, 100, 5, 1000)
	_, err := m.SubmitJob("buyer", "does-not-exist", 10)
	if !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("SubmitJob() error = %v, want ErrProviderNotFound", err)
	}
}

func TestMarket_SubmitJob_InsufficientCapacity(t *testing.T) {
	m := setupMarket(t, 5, 5, 1000)
	_, err := m.SubmitJob("buyer", "prov", 10)
	if !errors.Is(err, ErrInsufficientCapacity) {
		t.Fatalf("SubmitJob() error = %v, want ErrInsufficientCapacity", err)
	}
	// Capacity must be unchanged and no job created.
	prov, _ := m.GetProvider("prov")
	if prov.Available != 5 {
		t.Errorf("provider Available = %d, want 5 (unchanged)", prov.Available)
	}
	if len(m.ListJobs()) != 0 {
		t.Errorf("no job should be created, got %d", len(m.ListJobs()))
	}
}

func TestMarket_SubmitJob_InsufficientFunds(t *testing.T) {
	// price would be 10*5 = 50, buyer only has 40.
	m := setupMarket(t, 100, 5, 40)
	_, err := m.SubmitJob("buyer", "prov", 10)
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("SubmitJob() error = %v, want ErrInsufficientFunds", err)
	}
	// Capacity unchanged and no persisted job.
	prov, _ := m.GetProvider("prov")
	if prov.Available != 100 {
		t.Errorf("provider Available = %d, want 100 (unchanged)", prov.Available)
	}
	if len(m.ListJobs()) != 0 {
		t.Errorf("no job should be created, got %d", len(m.ListJobs()))
	}
}

func TestMarket_CompleteJob(t *testing.T) {
	m := setupMarket(t, 100, 5, 1000)
	job, err := m.SubmitJob("buyer", "prov", 10)
	if err != nil {
		t.Fatalf("SubmitJob() error = %v", err)
	}

	if err := m.CompleteJob(job.ID); err != nil {
		t.Fatalf("CompleteJob() error = %v", err)
	}

	completed, ok := m.GetJob(job.ID)
	if !ok {
		t.Fatalf("job %q not found", job.ID)
	}
	if completed.Status != JobCompleted {
		t.Errorf("job status = %q, want %q", completed.Status, JobCompleted)
	}

	// Credits transferred buyer->provider by exactly the price (50).
	buyerBal, _ := m.Ledger().Balance("buyer")
	provBal, _ := m.Ledger().Balance("prov")
	if buyerBal != 950 {
		t.Errorf("buyer balance = %d, want 950 (1000 - 50)", buyerBal)
	}
	if provBal != 50 {
		t.Errorf("provider balance = %d, want 50", provBal)
	}
}

func TestMarket_CompleteJob_Errors(t *testing.T) {
	m := setupMarket(t, 100, 5, 1000)

	t.Run("unknown job", func(t *testing.T) {
		if err := m.CompleteJob("no-such-job"); !errors.Is(err, ErrJobNotFound) {
			t.Fatalf("CompleteJob() error = %v, want ErrJobNotFound", err)
		}
	})

	t.Run("double completion rejected", func(t *testing.T) {
		job, err := m.SubmitJob("buyer", "prov", 4)
		if err != nil {
			t.Fatalf("SubmitJob() error = %v", err)
		}
		if err := m.CompleteJob(job.ID); err != nil {
			t.Fatalf("first CompleteJob() error = %v", err)
		}
		if err := m.CompleteJob(job.ID); !errors.Is(err, ErrInvalidJobState) {
			t.Fatalf("second CompleteJob() error = %v, want ErrInvalidJobState", err)
		}
	})
}

func TestMarket_CancelJob(t *testing.T) {
	m := setupMarket(t, 100, 5, 1000)
	job, err := m.SubmitJob("buyer", "prov", 10)
	if err != nil {
		t.Fatalf("SubmitJob() error = %v", err)
	}

	if err := m.CancelJob(job.ID); err != nil {
		t.Fatalf("CancelJob() error = %v", err)
	}

	cancelled, _ := m.GetJob(job.ID)
	if cancelled.Status != JobCancelled {
		t.Errorf("job status = %q, want %q", cancelled.Status, JobCancelled)
	}

	// Capacity restored.
	prov, _ := m.GetProvider("prov")
	if prov.Available != 100 {
		t.Errorf("provider Available = %d, want 100 (restored)", prov.Available)
	}

	// No credits transferred.
	buyerBal, _ := m.Ledger().Balance("buyer")
	provBal, _ := m.Ledger().Balance("prov")
	if buyerBal != 1000 {
		t.Errorf("buyer balance = %d, want 1000 (unchanged)", buyerBal)
	}
	if provBal != 0 {
		t.Errorf("provider balance = %d, want 0 (no transfer)", provBal)
	}
}

func TestMarket_SubmitJob_SelfDealingRejected(t *testing.T) {
	// A provider registered under the same account the buyer uses must not be
	// able to buy from itself: completion would transfer credits to the same
	// account. Register a provider whose ID equals the buyer and submit against
	// it.
	m := newTestMarket(t, newTestStore(t))
	if err := m.RegisterProvider(Provider{ID: "self", Capacity: 100, PricePerUnit: 5}); err != nil {
		t.Fatalf("RegisterProvider() error = %v", err)
	}
	if err := m.Ledger().Credit("self", 1000); err != nil {
		t.Fatalf("Credit() error = %v", err)
	}

	_, err := m.SubmitJob("self", "self", 10)
	if !errors.Is(err, ErrSelfDealing) {
		t.Fatalf("SubmitJob() error = %v, want ErrSelfDealing", err)
	}

	// No job created and capacity untouched.
	if len(m.ListJobs()) != 0 {
		t.Errorf("no job should be created, got %d", len(m.ListJobs()))
	}
	prov, _ := m.GetProvider("self")
	if prov.Available != 100 {
		t.Errorf("provider Available = %d, want 100 (unchanged)", prov.Available)
	}
}

func TestMarket_RehydrateAfterRestart(t *testing.T) {
	// Persist providers and jobs through one Market, then build a second Market
	// over the same store (simulating a node restart) and assert the durable
	// state is visible via the read APIs rather than lost.
	store := newTestStore(t)

	first := newTestMarket(t, store)
	if err := first.RegisterProvider(Provider{ID: "prov-a", Capacity: 100, PricePerUnit: 5}); err != nil {
		t.Fatalf("RegisterProvider() error = %v", err)
	}
	if err := first.RegisterProvider(Provider{ID: "prov-b", Capacity: 40, PricePerUnit: 2}); err != nil {
		t.Fatalf("RegisterProvider() error = %v", err)
	}
	if err := first.Ledger().Credit("buyer", 1000); err != nil {
		t.Fatalf("Credit() error = %v", err)
	}

	// One pending job (reserves capacity) and one completed job.
	pending, err := first.SubmitJob("buyer", "prov-a", 10) // reserves 10, price 50
	if err != nil {
		t.Fatalf("SubmitJob() error = %v", err)
	}
	completed, err := first.SubmitJob("buyer", "prov-b", 5) // price 10
	if err != nil {
		t.Fatalf("SubmitJob() error = %v", err)
	}
	if err := first.CompleteJob(completed.ID); err != nil {
		t.Fatalf("CompleteJob() error = %v", err)
	}

	// Simulate a restart: a fresh Market over the same store must rehydrate.
	second := newTestMarket(t, store)

	// Providers rehydrated with their reserved capacity intact.
	provA, ok := second.GetProvider("prov-a")
	if !ok {
		t.Fatalf("prov-a not found after restart")
	}
	if provA.Available != 90 {
		t.Errorf("prov-a Available = %d, want 90 (10 reserved by pending job)", provA.Available)
	}
	if provA.Capacity != 100 || provA.PricePerUnit != 5 {
		t.Errorf("prov-a = %+v, want Capacity 100 PricePerUnit 5", provA)
	}
	provB, ok := second.GetProvider("prov-b")
	if !ok {
		t.Fatalf("prov-b not found after restart")
	}
	if provB.Available != 35 {
		t.Errorf("prov-b Available = %d, want 35 (5 consumed by completed job)", provB.Available)
	}

	if got := len(second.ListProviders()); got != 2 {
		t.Errorf("ListProviders() len = %d, want 2", got)
	}

	// Jobs rehydrated with their statuses.
	if got := len(second.ListJobs()); got != 2 {
		t.Errorf("ListJobs() len = %d, want 2", got)
	}
	rehydratedPending, ok := second.GetJob(pending.ID)
	if !ok {
		t.Fatalf("pending job %q not found after restart", pending.ID)
	}
	if rehydratedPending.Status != JobPending {
		t.Errorf("pending job status = %q, want %q", rehydratedPending.Status, JobPending)
	}
	rehydratedCompleted, ok := second.GetJob(completed.ID)
	if !ok {
		t.Fatalf("completed job %q not found after restart", completed.ID)
	}
	if rehydratedCompleted.Status != JobCompleted {
		t.Errorf("completed job status = %q, want %q", rehydratedCompleted.Status, JobCompleted)
	}

	// The rehydrated pending job is still completable, proving reserved state is
	// usable and not just cosmetic.
	if err := second.CompleteJob(pending.ID); err != nil {
		t.Fatalf("CompleteJob() after restart error = %v", err)
	}
	provBal, _ := second.Ledger().Balance("prov-a")
	if provBal != 50 {
		t.Errorf("prov-a balance = %d, want 50 after completing rehydrated job", provBal)
	}
}

func TestMarket_FreshStoreStartsEmpty(t *testing.T) {
	// Rehydration must not change behavior over a brand-new store.
	m := newTestMarket(t, newTestStore(t))
	if got := len(m.ListProviders()); got != 0 {
		t.Errorf("ListProviders() len = %d, want 0 on fresh store", got)
	}
	if got := len(m.ListJobs()); got != 0 {
		t.Errorf("ListJobs() len = %d, want 0 on fresh store", got)
	}
}

// recordingObserver captures observer callbacks for assertions in tests.
type recordingObserver struct {
	providerCount int
	activeJobs    int
	jobsCompleted int
	creditsTotal  uint64
}

func (o *recordingObserver) ProviderCountChanged(count int) { o.providerCount = count }
func (o *recordingObserver) ActiveJobsChanged(count int)    { o.activeJobs = count }
func (o *recordingObserver) JobCompleted(credits uint64) {
	o.jobsCompleted++
	o.creditsTotal += credits
}

func TestMarket_ObserverDrivenByActivity(t *testing.T) {
	obs := &recordingObserver{}
	m := newTestMarket(t, newTestStore(t))
	m.SetObserver(obs)

	if err := m.RegisterProvider(Provider{ID: "prov", Capacity: 100, PricePerUnit: 5}); err != nil {
		t.Fatalf("RegisterProvider() error = %v", err)
	}
	if obs.providerCount != 1 {
		t.Errorf("providerCount = %d, want 1 after registration", obs.providerCount)
	}

	if err := m.Ledger().Credit("buyer", 1000); err != nil {
		t.Fatalf("Credit() error = %v", err)
	}
	job, err := m.SubmitJob("buyer", "prov", 10) // price 50
	if err != nil {
		t.Fatalf("SubmitJob() error = %v", err)
	}
	if obs.activeJobs != 1 {
		t.Errorf("activeJobs = %d, want 1 after submit", obs.activeJobs)
	}

	if err := m.CompleteJob(job.ID); err != nil {
		t.Fatalf("CompleteJob() error = %v", err)
	}
	if obs.activeJobs != 0 {
		t.Errorf("activeJobs = %d, want 0 after completion", obs.activeJobs)
	}
	if obs.jobsCompleted != 1 {
		t.Errorf("jobsCompleted = %d, want 1", obs.jobsCompleted)
	}
	if obs.creditsTotal != 50 {
		t.Errorf("creditsTotal = %d, want 50", obs.creditsTotal)
	}
}

func TestMarket_JobTimestampsAndOrdering(t *testing.T) {
	m := setupMarket(t, 100, 1, 1000)

	first, err := m.SubmitJob("buyer", "prov", 1)
	if err != nil {
		t.Fatalf("SubmitJob() error = %v", err)
	}
	if first.CreatedAt.IsZero() || first.UpdatedAt.IsZero() {
		t.Fatalf("job timestamps should be set, got CreatedAt=%v UpdatedAt=%v", first.CreatedAt, first.UpdatedAt)
	}

	// Ensure a distinct creation time for deterministic ordering.
	time.Sleep(2 * time.Millisecond)
	second, err := m.SubmitJob("buyer", "prov", 1)
	if err != nil {
		t.Fatalf("SubmitJob() error = %v", err)
	}

	jobs := m.ListJobs()
	if len(jobs) != 2 {
		t.Fatalf("ListJobs() len = %d, want 2", len(jobs))
	}
	if jobs[0].ID != first.ID || jobs[1].ID != second.ID {
		t.Errorf("ListJobs() order = [%s, %s], want chronological [%s, %s]",
			jobs[0].ID, jobs[1].ID, first.ID, second.ID)
	}

	// Completing a job advances UpdatedAt but preserves CreatedAt.
	if err := m.CompleteJob(first.ID); err != nil {
		t.Fatalf("CompleteJob() error = %v", err)
	}
	done, _ := m.GetJob(first.ID)
	if !done.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("CreatedAt changed on completion: got %v, want %v", done.CreatedAt, first.CreatedAt)
	}
	if !done.UpdatedAt.After(first.UpdatedAt) {
		t.Errorf("UpdatedAt should advance on completion: got %v, was %v", done.UpdatedAt, first.UpdatedAt)
	}
}

func TestMarket_ConcurrentSubmitCancelNoDeadlock(t *testing.T) {
	// Regression test for the lock-order inversion between SubmitJob (providersMu
	// -> jobsMu) and CancelJob (previously jobsMu -> providersMu). Under the old
	// ordering, concurrent SubmitJob/CancelJob calls could each hold one mutex
	// and block forever on the other, so this test would hang (and time out).
	// With both paths taking providersMu before jobsMu it completes.
	//
	// Run with `go test -race` for data-race coverage; the primary value is
	// driving both paths concurrently from many goroutines with enough
	// interleaving to reliably schedule the AB/BA hazard.
	m := setupMarket(t, 1_000_000, 1, 1_000_000)

	const (
		workers = 16
		// Pebble fsyncs every persisted provider/job change. Twenty iterations
		// per worker still schedules hundreds of overlapping Submit/Cancel pairs
		// and catches the AB/BA lock inversion, without turning this lock-order
		// test into a storage-throughput benchmark on slower CI disks.
		perWorker = 20
	)

	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < perWorker; i++ {
					job, err := m.SubmitJob("buyer", "prov", 1)
					if err != nil {
						// Capacity/funds are sized generously; any error here is
						// unexpected and worth surfacing.
						t.Errorf("SubmitJob() error = %v", err)
						return
					}
					// Cancel the job we just submitted so SubmitJob and CancelJob
					// run concurrently across workers against the same provider.
					if err := m.CancelJob(job.ID); err != nil {
						t.Errorf("CancelJob() error = %v", err)
						return
					}
				}
			}()
		}
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Completed without deadlocking.
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent SubmitJob/CancelJob did not complete: likely lock-order deadlock")
	}

	// Every submitted job was cancelled, so all reserved capacity is restored.
	prov, _ := m.GetProvider("prov")
	if prov.Available != prov.Capacity {
		t.Errorf("provider Available = %d, want %d (all capacity restored)", prov.Available, prov.Capacity)
	}
}

func TestMarket_FullFlow(t *testing.T) {
	// End-to-end: register, submit, complete; verify balances and capacity.
	m := newTestMarket(t, newTestStore(t))
	if err := m.RegisterProvider(Provider{ID: "node-a", Capacity: 50, PricePerUnit: 3}); err != nil {
		t.Fatalf("RegisterProvider() error = %v", err)
	}
	if err := m.Ledger().Credit("shopper", 500); err != nil {
		t.Fatalf("Credit() error = %v", err)
	}

	job, err := m.SubmitJob("shopper", "node-a", 20) // price = 60
	if err != nil {
		t.Fatalf("SubmitJob() error = %v", err)
	}
	if got := len(m.ListProviders()); got != 1 {
		t.Errorf("ListProviders() len = %d, want 1", got)
	}
	if got := len(m.ListJobs()); got != 1 {
		t.Errorf("ListJobs() len = %d, want 1", got)
	}

	if err := m.CompleteJob(job.ID); err != nil {
		t.Fatalf("CompleteJob() error = %v", err)
	}

	shopperBal, _ := m.Ledger().Balance("shopper")
	nodeBal, _ := m.Ledger().Balance("node-a")
	if shopperBal != 440 {
		t.Errorf("shopper balance = %d, want 440 (500 - 60)", shopperBal)
	}
	if nodeBal != 60 {
		t.Errorf("node-a balance = %d, want 60", nodeBal)
	}
}
