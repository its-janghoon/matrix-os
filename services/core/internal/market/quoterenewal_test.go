package market

import (
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/kv"
)

func renewalTestMarket(t *testing.T) *Market {
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

// registerAt puts a provider on the book with a window starting at observedAt.
func registerAt(t *testing.T, m *Market, id string, observedAt time.Time, window time.Duration) Provider {
	t.Helper()
	if err := m.RegisterProvider(Provider{
		ID:                id,
		Capacity:          1000,
		PricePerUnit:      7,
		CostPerUnit:       4,
		MarkupBasisPoints: 250,
		Models:            []string{"qwen3-32b"},
		ObservedAt:        observedAt,
		ValidUntil:        observedAt.Add(window),
	}); err != nil {
		t.Fatalf("RegisterProvider(%s): %v", id, err)
	}
	p, ok := m.GetProvider(id)
	if !ok {
		t.Fatalf("provider %s missing right after registering it", id)
	}
	return p
}

// A seller that has simply stayed up for a day used to disappear from its own
// order book. This is that day, without the restart that always hid it.
func TestASellerThatStaysUpPastItsWindowIsStillOnTheBook(t *testing.T) {
	m := renewalTestMarket(t)
	registered := time.Now().UTC()
	registerAt(t, m, "gpu-box-1", registered, DefaultQuoteTTL)

	// Nothing has happened except time. The backend answers, the node is up,
	// the operator changed nothing.
	aDayLater := registered.Add(DefaultQuoteTTL + time.Minute)

	if got := m.RenewProviderQuotes(aDayLater); len(got) != 1 || got[0] != "gpu-box-1" {
		t.Fatalf("RenewProviderQuotes renewed %v, want [gpu-box-1]", got)
	}

	p, ok := m.GetProvider("gpu-box-1")
	if !ok {
		t.Fatal("provider vanished from the order book")
	}
	if err := validateProviderQuoteForUse(p, aDayLater); err != nil {
		t.Fatalf("a renewed quote is still unusable: %v", err)
	}
	if !p.ValidUntil.After(aDayLater) {
		t.Fatalf("renewed quote expires at %s, which is not after %s", p.ValidUntil, aDayLater)
	}
}

// Renewal restates the offer. It must never author a different one.
func TestRenewalMovesTheWindowAndNothingElse(t *testing.T) {
	m := renewalTestMarket(t)
	registered := time.Now().UTC()
	before := registerAt(t, m, "gpu-box-1", registered, DefaultQuoteTTL)

	halfwayPlus := registered.Add(DefaultQuoteTTL/2 + time.Second)
	if got := m.RenewProviderQuotes(halfwayPlus); len(got) != 1 {
		t.Fatalf("RenewProviderQuotes renewed %v, want one entry", got)
	}
	after, _ := m.GetProvider("gpu-box-1")

	if after.PricePerUnit != before.PricePerUnit {
		t.Errorf("price moved: %d -> %d", before.PricePerUnit, after.PricePerUnit)
	}
	if after.CostPerUnit != before.CostPerUnit {
		t.Errorf("cost moved: %d -> %d", before.CostPerUnit, after.CostPerUnit)
	}
	if after.MarkupBasisPoints != before.MarkupBasisPoints {
		t.Errorf("markup moved: %d -> %d", before.MarkupBasisPoints, after.MarkupBasisPoints)
	}
	if after.QuoteID != before.QuoteID {
		t.Errorf("quote identity moved: %q -> %q", before.QuoteID, after.QuoteID)
	}
	if after.Capacity != before.Capacity || after.Available != before.Available {
		t.Errorf("capacity moved: %d/%d -> %d/%d", before.Capacity, before.Available, after.Capacity, after.Available)
	}

	// A listener accepts a moved window only under a strictly newer version.
	if after.QuoteVersion <= before.QuoteVersion {
		t.Errorf("quote version %d is not newer than %d, so no other node would take the renewal",
			after.QuoteVersion, before.QuoteVersion)
	}
	// Same length, so an operator's configured quote_ttl survives renewal.
	if got, want := after.ValidUntil.Sub(after.ObservedAt), DefaultQuoteTTL; got != want {
		t.Errorf("renewed window is %s, want %s", got, want)
	}
	if !after.ObservedAt.Equal(halfwayPlus) {
		t.Errorf("renewed observation is %s, want %s", after.ObservedAt, halfwayPlus)
	}
}

// Renewal is not free: a reservation names the exact window it accepted, so
// renewing more often than needed refuses buyers who read the directory a
// moment too early.
func TestAWindowStillMostlyAheadIsLeftAlone(t *testing.T) {
	m := renewalTestMarket(t)
	registered := time.Now().UTC()
	before := registerAt(t, m, "gpu-box-1", registered, DefaultQuoteTTL)

	justUnderHalfway := registered.Add(DefaultQuoteTTL/2 - time.Second)
	if got := m.RenewProviderQuotes(justUnderHalfway); len(got) != 0 {
		t.Fatalf("renewed %v with half the window still ahead of it", got)
	}
	after, _ := m.GetProvider("gpu-box-1")
	if after.QuoteVersion != before.QuoteVersion || !after.ValidUntil.Equal(before.ValidUntil) {
		t.Errorf("record changed without being due: version %d->%d, valid until %s->%s",
			before.QuoteVersion, after.QuoteVersion, before.ValidUntil, after.ValidUntil)
	}
}

// Suspension is the health check's word that the backend stopped answering.
// Falling out of the directory is the correct outcome, and the one case the old
// behaviour got right.
func TestASuspendedProviderIsNotRenewed(t *testing.T) {
	m := renewalTestMarket(t)
	registered := time.Now().UTC()
	registerAt(t, m, "gpu-box-1", registered, DefaultQuoteTTL)
	if _, err := m.SetProviderSuspended("gpu-box-1", true); err != nil {
		t.Fatalf("SetProviderSuspended: %v", err)
	}

	if got := m.RenewProviderQuotes(registered.Add(DefaultQuoteTTL + time.Minute)); len(got) != 0 {
		t.Fatalf("renewed %v, but its backend is not answering", got)
	}
}

// An operator's configured window is what gets restated, not the default.
func TestRenewalKeepsAShortConfiguredWindowShort(t *testing.T) {
	m := renewalTestMarket(t)
	registered := time.Now().UTC()
	const window = 20 * time.Minute
	registerAt(t, m, "short", registered, window)

	if got := m.RenewProviderQuotes(registered.Add(window)); len(got) != 1 {
		t.Fatalf("renewed %v, want [short]", got)
	}
	after, _ := m.GetProvider("short")
	if got := after.ValidUntil.Sub(after.ObservedAt); got != window {
		t.Errorf("renewed window is %s, want the configured %s", got, window)
	}
}

// Renewal survives a restart, because the store is what a restart reads.
func TestARenewedWindowIsPersisted(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	defer func() { _ = store.Close() }()
	m, err := NewMarket(store)
	if err != nil {
		t.Fatalf("NewMarket: %v", err)
	}
	registered := time.Now().UTC()
	registerAt(t, m, "gpu-box-1", registered, DefaultQuoteTTL)
	renewedAt := registered.Add(DefaultQuoteTTL + time.Minute)
	if got := m.RenewProviderQuotes(renewedAt); len(got) != 1 {
		t.Fatalf("renewed %v, want one entry", got)
	}

	reopened, err := NewMarket(store)
	if err != nil {
		t.Fatalf("NewMarket (reopen): %v", err)
	}
	p, ok := reopened.GetProvider("gpu-box-1")
	if !ok {
		t.Fatal("provider did not survive the reopen")
	}
	if err := validateProviderQuoteForUse(p, renewedAt); err != nil {
		t.Fatalf("the reopened record is unusable, so the renewal was only in memory: %v", err)
	}
}

// A record with no complete quote identity is deliberately ineligible, and
// inventing one for it would misrepresent what a buyer is being offered.
func TestAnIncompleteQuoteIsNotGivenAnIdentityByRenewal(t *testing.T) {
	now := time.Now().UTC()
	for name, p := range map[string]Provider{
		"no quote id": {ID: "a", PricePerUnit: 7, QuoteVersion: 1, ObservedAt: now, ValidUntil: now.Add(time.Hour)},
		"no version":  {ID: "b", PricePerUnit: 7, QuoteID: "q", ObservedAt: now, ValidUntil: now.Add(time.Hour)},
		"no price":    {ID: "c", QuoteID: "q", QuoteVersion: 1, ObservedAt: now, ValidUntil: now.Add(time.Hour)},
		"no window":   {ID: "d", PricePerUnit: 7, QuoteID: "q", QuoteVersion: 1},
	} {
		if _, ok := quoteDueForRenewal(p, now.Add(2*time.Hour)); ok {
			t.Errorf("%s: renewal claimed an incomplete record", name)
		}
	}
}
