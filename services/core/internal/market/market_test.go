package market

import (
	"errors"
	"testing"
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
			m := NewMarket(newTestStore(t))
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

// setupMarket registers one provider and credits a buyer, returning the market.
func setupMarket(t *testing.T, capacity, pricePerUnit, buyerCredits uint64) *Market {
	t.Helper()
	m := NewMarket(newTestStore(t))
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

func TestMarket_FullFlow(t *testing.T) {
	// End-to-end: register, submit, complete; verify balances and capacity.
	m := NewMarket(newTestStore(t))
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
