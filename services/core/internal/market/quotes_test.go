package market

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestProviderQuoteDefaultsAndFreshnessValidation(t *testing.T) {
	m := newTestMarket(t, newTestStore(t))
	before := time.Now().UTC()
	if err := m.RegisterProvider(Provider{ID: "gpu", Capacity: 10, PricePerUnit: 7}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	got, ok := m.GetProvider("gpu")
	if !ok {
		t.Fatal("provider missing after registration")
	}
	if got.QuoteID == "" || got.QuoteVersion != 1 {
		t.Fatalf("generated quote identity = %q/%d, want nonempty/1", got.QuoteID, got.QuoteVersion)
	}
	if got.ObservedAt.Before(before) || !got.ValidUntil.Equal(got.ObservedAt.Add(DefaultQuoteTTL)) {
		t.Fatalf("generated times = observed %s valid %s", got.ObservedAt, got.ValidUntil)
	}

	now := time.Now().UTC()
	for _, tc := range []struct {
		name string
		p    Provider
		want error
	}{
		{
			name: "expired",
			p:    Provider{ID: "old", Capacity: 1, PricePerUnit: 1, ObservedAt: now.Add(-2 * time.Hour), ValidUntil: now.Add(-time.Hour)},
			want: ErrStaleQuote,
		},
		{
			name: "future observation",
			p:    Provider{ID: "future", Capacity: 1, PricePerUnit: 1, ObservedAt: now.Add(MaxQuoteClockSkew + time.Minute), ValidUntil: now.Add(time.Hour)},
			want: ErrInvalidProvider,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := m.RegisterProvider(tc.p); !errors.Is(err, tc.want) {
				t.Fatalf("RegisterProvider error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestUpdateProviderQuotePreservesCapacityReservationsAndModels(t *testing.T) {
	m := newTestMarket(t, newTestStore(t))
	if err := m.RegisterProvider(Provider{
		ID: "gpu", Capacity: 10, PricePerUnit: 3, QuoteID: "quote-1", QuoteVersion: 4,
		Models: []string{"Model-B", "model-a"},
	}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	if err := m.Ledger().Credit("buyer", 1_000); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	job, err := m.SubmitJob("buyer", "gpu", 4)
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}

	if err := m.UpdateProviderQuote("gpu", Provider{
		PricePerUnit: 9, CostPerUnit: 6, MarkupBasisPoints: 250,
		QuoteID: "quote-2", QuoteVersion: 5,
	}); err != nil {
		t.Fatalf("UpdateProviderQuote: %v", err)
	}
	updated, _ := m.GetProvider("gpu")
	if updated.Capacity != 10 || updated.Available != 6 {
		t.Fatalf("capacity/available = %d/%d, want 10/6", updated.Capacity, updated.Available)
	}
	if len(updated.Models) != 2 || updated.Models[0] != "model-a" || updated.Models[1] != "model-b" {
		t.Fatalf("models changed during quote update: %v", updated.Models)
	}
	if updated.PricePerUnit != 9 || updated.CostPerUnit != 6 || updated.QuoteID != "quote-2" || updated.QuoteVersion != 5 {
		t.Fatalf("updated quote = %+v", updated)
	}

	// Repeating the refresh through the defaulting path must advance the quote
	// again without releasing the four units still reserved by the pending job.
	if err := m.UpdateProviderQuote("gpu", Provider{PricePerUnit: 11}); err != nil {
		t.Fatalf("UpdateProviderQuote(second): %v", err)
	}
	refreshed, _ := m.GetProvider("gpu")
	if refreshed.Capacity != 10 || refreshed.Available != 6 {
		t.Fatalf("capacity/available after second refresh = %d/%d, want 10/6", refreshed.Capacity, refreshed.Available)
	}
	if len(refreshed.Models) != 2 || refreshed.Models[0] != "model-a" || refreshed.Models[1] != "model-b" {
		t.Fatalf("models changed during second quote update: %v", refreshed.Models)
	}
	if refreshed.PricePerUnit != 11 || refreshed.QuoteID == "" || refreshed.QuoteID == "quote-2" || refreshed.QuoteVersion != 6 {
		t.Fatalf("second refreshed quote = %+v", refreshed)
	}

	// The pending job keeps the exact quote accepted at reservation and settles
	// at that old amount even after the live provider quote changes.
	snapshot, _ := m.GetJob(job.ID)
	if snapshot.PricePerUnit != 3 || snapshot.Price != 12 || snapshot.QuoteID != "quote-1" || snapshot.QuoteVersion != 4 {
		t.Fatalf("job snapshot changed after update: %+v", snapshot)
	}
	if err := m.CompleteJob(job.ID); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}
	balance, _ := m.Ledger().Balance("gpu")
	if balance != 12 {
		t.Fatalf("provider settled %d, want snapshotted 12", balance)
	}
}

func TestProviderQuoteVersionAndPriceOverflowFailClosed(t *testing.T) {
	m := newTestMarket(t, newTestStore(t))
	if err := m.RegisterProvider(Provider{ID: "version", Capacity: 1, PricePerUnit: 1, QuoteVersion: math.MaxUint64}); err != nil {
		t.Fatalf("RegisterProvider(version): %v", err)
	}
	if err := m.UpdateProviderQuote("version", Provider{PricePerUnit: 2}); !errors.Is(err, ErrPriceOverflow) {
		t.Fatalf("UpdateProviderQuote error = %v, want ErrPriceOverflow", err)
	}

	if err := m.RegisterProvider(Provider{ID: "price", Capacity: 2, PricePerUnit: math.MaxUint64}); err != nil {
		t.Fatalf("RegisterProvider(price): %v", err)
	}
	if _, err := m.SubmitJob("buyer", "price", 2); !errors.Is(err, ErrPriceOverflow) {
		t.Fatalf("SubmitJob error = %v, want ErrPriceOverflow", err)
	}
}

func TestListProvidersExcludesExpiredLocalQuotes(t *testing.T) {
	store := newTestStore(t)
	if err := store.Put([]byte(providerKeyPrefix+"expired"), []byte(`{"id":"expired","capacity":2,"price_per_unit":5,"quote_id":"expired-q","quote_version":1,"observed_at":"2020-01-01T00:00:00Z","valid_until":"2020-01-02T00:00:00Z","available":2,"models":["m"]}`)); err != nil {
		t.Fatalf("persist expired provider: %v", err)
	}
	m := newTestMarket(t, store)

	if got := m.ListProviders(); len(got) != 0 {
		t.Fatalf("expired provider must not be listed to buyers: %+v", got)
	}
	if _, ok := m.GetProvider("expired"); !ok {
		t.Fatal("expired provider must remain addressable by ID for refresh")
	}
	if err := m.UpdateProviderQuote("expired", Provider{PricePerUnit: 6}); err != nil {
		t.Fatalf("UpdateProviderQuote: %v", err)
	}
	listed := m.ListProviders()
	if len(listed) != 1 || listed[0].ID != "expired" || listed[0].PricePerUnit != 6 {
		t.Fatalf("refreshed provider listing = %+v", listed)
	}
}

func TestPersistedLegacyQuoteRecordsAreHiddenButCannotSettle(t *testing.T) {
	store := newTestStore(t)
	// These are exact pre-quote JSON shapes. Do not generate replacement quote
	// identity during load: no node can know what a historical buyer accepted.
	if err := store.Put([]byte(providerKeyPrefix+"legacy"), []byte(`{"id":"legacy","capacity":10,"price_per_unit":5,"available":9,"models":["m"]}`)); err != nil {
		t.Fatalf("persist legacy provider: %v", err)
	}
	if err := store.Put([]byte(jobKeyPrefix+"legacy-job"), []byte(`{"id":"legacy-job","buyer":"buyer","provider":"legacy","units":1,"price":5,"price_per_unit":5,"status":"pending","created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z"}`)); err != nil {
		t.Fatalf("persist legacy job: %v", err)
	}
	m := newTestMarket(t, store)

	providers := m.ListProviders()
	if len(providers) != 0 {
		t.Fatalf("legacy provider must be hidden from buyer listing: %+v", providers)
	}
	if _, ok := m.GetProvider("legacy"); !ok {
		t.Fatal("legacy provider must remain addressable by ID for administrative refresh")
	}
	if got := m.ProvidersForModel("m"); len(got) != 0 {
		t.Fatalf("legacy provider must be stale/ineligible, got %+v", got)
	}
	if err := m.ValidateJobQuoteForSettlement("legacy-job"); !errors.Is(err, ErrStaleQuote) {
		t.Fatalf("ValidateJobQuoteForSettlement error = %v, want ErrStaleQuote", err)
	}
	loadedLegacy, _ := m.GetJob("legacy-job")
	if loadedLegacy.PricePerUnit != 0 || loadedLegacy.Status != JobQuoteIncomplete {
		t.Fatalf("legacy incomplete quote marker = price %d status %q, want 0/%q",
			loadedLegacy.PricePerUnit, loadedLegacy.Status, JobQuoteIncomplete)
	}
	if err := m.CompleteJob("legacy-job"); !errors.Is(err, ErrStaleQuote) {
		t.Fatalf("CompleteJob error = %v, want ErrStaleQuote", err)
	}
	// Inference completion prices actual usage through CheckedMul using the job's
	// snapshotted PricePerUnit. A pre-snapshot zero therefore fails with the same
	// explicit stale-quote sentinel rather than constructing a zero transfer.
	if _, err := CheckedMul(1, 0); !errors.Is(err, ErrStaleQuote) {
		t.Fatalf("legacy inference pricing error = %v, want ErrStaleQuote", err)
	}
	if err := m.CancelJob("legacy-job"); err != nil {
		t.Fatalf("CancelJob must remain available: %v", err)
	}
	cancelled, _ := m.GetJob("legacy-job")
	if cancelled.Status != JobCancelled {
		t.Fatalf("legacy job status = %q, want cancelled", cancelled.Status)
	}
}

func TestProviderAndJobQuoteSnapshotsRehydrate(t *testing.T) {
	store := newTestStore(t)
	first := newTestMarket(t, store)
	observed := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	valid := observed.Add(time.Hour)
	if err := first.RegisterProvider(Provider{
		ID: "gpu", Capacity: 3, PricePerUnit: 7, CostPerUnit: 5, MarkupBasisPoints: 200,
		QuoteID: "persisted-quote", QuoteVersion: 9, ObservedAt: observed, ValidUntil: valid,
	}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	if err := first.Ledger().Credit("buyer", 100); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	job, err := first.SubmitJob("buyer", "gpu", 2)
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}

	second := newTestMarket(t, store)
	provider, _ := second.GetProvider("gpu")
	if provider.CostPerUnit != 5 || provider.MarkupBasisPoints != 200 || provider.QuoteID != "persisted-quote" ||
		provider.QuoteVersion != 9 || !provider.ObservedAt.Equal(observed) || !provider.ValidUntil.Equal(valid) {
		t.Fatalf("rehydrated provider quote = %+v", provider)
	}
	rehydrated, _ := second.GetJob(job.ID)
	if rehydrated.PricePerUnit != 7 || rehydrated.QuoteID != "persisted-quote" || rehydrated.QuoteVersion != 9 ||
		!rehydrated.QuoteObservedAt.Equal(observed) || !rehydrated.QuoteValidUntil.Equal(valid) {
		t.Fatalf("rehydrated job snapshot = %+v", rehydrated)
	}
}

func TestReserveRemoteJobRehydratesIdempotentAcceptedSnapshot(t *testing.T) {
	store := newTestStore(t)
	first := newTestMarket(t, store)
	now := time.Now().UTC().Truncate(time.Millisecond)
	provider := Provider{
		ID:           "remote-provider",
		Capacity:     2,
		PricePerUnit: 13,
		QuoteID:      "remote-quote",
		QuoteVersion: 4,
		ObservedAt:   now.Add(-time.Minute),
		ValidUntil:   now.Add(time.Hour),
	}
	if err := first.RegisterProvider(provider); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	if err := first.Ledger().Credit("remote-buyer", 100); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	accepted := AcceptedQuote{
		PricePerUnit: provider.PricePerUnit,
		QuoteID:      provider.QuoteID,
		QuoteVersion: provider.QuoteVersion,
		ObservedAt:   provider.ObservedAt,
		ValidUntil:   provider.ValidUntil,
		Total:        26,
	}
	job, err := first.ReserveRemoteJob("remote-buyer", provider.ID, 2, 9, "request-digest", accepted, now)
	if err != nil {
		t.Fatalf("ReserveRemoteJob: %v", err)
	}

	second := newTestMarket(t, store)
	replayed, err := second.ReserveRemoteJob("remote-buyer", provider.ID, 2, 9, "request-digest", accepted, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("replay after rehydrate: %v", err)
	}
	if replayed.ID != job.ID || replayed.RemoteRequestDigest != "request-digest" || replayed.Price != 26 {
		t.Fatalf("rehydrated replay = %+v, want original %+v", replayed, job)
	}
	storedProvider, _ := second.GetProvider(provider.ID)
	if storedProvider.Available != 0 || len(second.ListJobs()) != 1 {
		t.Fatalf("rehydrated replay double-reserved: provider %+v jobs %d", storedProvider, len(second.ListJobs()))
	}

	if _, err := second.ReserveRemoteJob("remote-buyer", provider.ID, 2, 9, "different-digest", accepted, now.Add(time.Minute)); !errors.Is(err, ErrRemoteRequestConflict) {
		t.Fatalf("conflicting replay error = %v, want ErrRemoteRequestConflict", err)
	}
}
