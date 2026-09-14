package market

import (
	"errors"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/kv"
)

func suspendTestMarket(t *testing.T) *Market {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m, err := NewMarket(store)
	if err != nil {
		t.Fatalf("NewMarket: %v", err)
	}
	return m
}

func registerSuspendable(t *testing.T, m *Market) {
	t.Helper()
	now := time.Now().UTC()
	if err := m.RegisterProvider(Provider{
		ID:           "gpu-box-1",
		Capacity:     1000,
		PricePerUnit: 2,
		Models:       []string{"qwen3-32b"},
		ObservedAt:   now,
		ValidUntil:   now.Add(DefaultQuoteTTL),
	}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	if err := m.ledger.Credit("buyer-1", 1_000_000); err != nil {
		t.Fatalf("credit buyer: %v", err)
	}
}

// TestSuspendedProviderIsOffTheMarket covers the whole point of the flag: a
// suspended provider is not routed to and cannot be reserved against, and both
// reverse the moment it is resumed.
func TestSuspendedProviderIsOffTheMarket(t *testing.T) {
	m := suspendTestMarket(t)
	registerSuspendable(t, m)

	if got := m.ProvidersForModel("qwen3-32b"); len(got) != 1 {
		t.Fatalf("healthy provider should be routable, got %d candidates", len(got))
	}

	changed, err := m.SetProviderSuspended("gpu-box-1", true)
	if err != nil {
		t.Fatalf("SetProviderSuspended: %v", err)
	}
	if !changed {
		t.Fatal("suspending a serving provider should report a change")
	}

	if got := m.ProvidersForModel("qwen3-32b"); len(got) != 0 {
		t.Fatalf("a suspended provider must not be routed to, got %d candidates", len(got))
	}
	_, err = m.SubmitJob("buyer-1", "gpu-box-1", 10)
	if !errors.Is(err, ErrProviderSuspended) {
		t.Fatalf("SubmitJob against a suspended provider = %v, want ErrProviderSuspended", err)
	}

	// Re-suspending is not a transition; a poller must be able to tell the two
	// apart so it logs a state change rather than every tick.
	changed, err = m.SetProviderSuspended("gpu-box-1", true)
	if err != nil {
		t.Fatalf("re-suspend: %v", err)
	}
	if changed {
		t.Fatal("re-suspending an already suspended provider should report no change")
	}

	if _, err := m.SetProviderSuspended("gpu-box-1", false); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got := m.ProvidersForModel("qwen3-32b"); len(got) != 1 {
		t.Fatalf("a resumed provider should be routable again, got %d candidates", len(got))
	}
	if _, err := m.SubmitJob("buyer-1", "gpu-box-1", 10); err != nil {
		t.Fatalf("SubmitJob after resume: %v", err)
	}
}

// TestSuspensionSurvivesAQuoteRefresh is the restart case. A node re-applies its
// config quote on every start; if that cleared the flag, a provider whose
// backend is still down would be put back on the market by a restart and would
// take reservations until the first probe caught up.
func TestSuspensionSurvivesAQuoteRefresh(t *testing.T) {
	m := suspendTestMarket(t)
	registerSuspendable(t, m)
	if _, err := m.SetProviderSuspended("gpu-box-1", true); err != nil {
		t.Fatalf("SetProviderSuspended: %v", err)
	}

	now := time.Now().UTC()
	if err := m.UpdateProviderQuote("gpu-box-1", Provider{
		ID:           "gpu-box-1",
		Capacity:     1000,
		PricePerUnit: 5,
		ObservedAt:   now,
		ValidUntil:   now.Add(DefaultQuoteTTL),
	}); err != nil {
		t.Fatalf("UpdateProviderQuote: %v", err)
	}

	p, ok := m.GetProvider("gpu-box-1")
	if !ok {
		t.Fatal("provider disappeared")
	}
	if !p.Suspended {
		t.Fatal("a quote refresh must not silently put a suspended provider back on the market")
	}
	if p.PricePerUnit != 5 {
		t.Fatalf("price = %d, want the refreshed 5", p.PricePerUnit)
	}
}

// TestSuspendingDoesNotDisturbLiveReservations pins the deliberate limit of the
// flag: what stops is NEW reservations. A probe cannot tell a dead backend from
// one busy finishing a real completion, so cancelling in-flight work on that
// evidence would be the more expensive mistake.
func TestSuspendingDoesNotDisturbLiveReservations(t *testing.T) {
	m := suspendTestMarket(t)
	registerSuspendable(t, m)

	job, err := m.SubmitJob("buyer-1", "gpu-box-1", 100)
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	if _, err := m.SetProviderSuspended("gpu-box-1", true); err != nil {
		t.Fatalf("SetProviderSuspended: %v", err)
	}

	before, _ := m.GetProvider("gpu-box-1")
	if before.Available != 900 {
		t.Fatalf("available = %d, want the reservation still held at 900", before.Available)
	}
	got, ok := m.GetJob(job.ID)
	if !ok || got.Status != JobPending {
		t.Fatalf("live job = %+v, ok=%v; suspension must not cancel it", got, ok)
	}

	if _, err := m.ReleaseAsCompleted(job.ID, 100); err != nil {
		t.Fatalf("ReleaseAsCompleted: %v", err)
	}
	after, _ := m.GetProvider("gpu-box-1")
	if after.Available != 1000 {
		t.Fatalf("available = %d, want the reservation returned at 1000", after.Available)
	}
	// The returned capacity must NOT read as "back on the market". This is the
	// reason suspension is a flag and not Available = 0.
	if !after.Suspended {
		t.Fatal("releasing a reservation must not clear the suspension")
	}
	if got := m.ProvidersForModel("qwen3-32b"); len(got) != 0 {
		t.Fatalf("still suspended, so it must not be routable; got %d candidates", len(got))
	}
}
