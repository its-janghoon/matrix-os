package market

import (
	"reflect"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/kv"
)

func newModelMarket(t *testing.T) *Market {
	t.Helper()
	return newTestMarket(t, newTestStore(t))
}

func TestRegisterProviderNormalizesModels(t *testing.T) {
	m := newModelMarket(t)

	if err := m.RegisterProvider(Provider{
		ID:           "gpu",
		Capacity:     10,
		PricePerUnit: 3,
		// Mixed case, padding, a duplicate under a different case, and a blank.
		Models: []string{"Llama-3.3-70B", " qwen-2.5-72b ", "llama-3.3-70b", "  "},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	got, ok := m.GetProvider("gpu")
	if !ok {
		t.Fatal("provider not found after registering it")
	}
	want := []string{"llama-3.3-70b", "qwen-2.5-72b"}
	if !reflect.DeepEqual(got.Models, want) {
		t.Fatalf("models = %v, want %v", got.Models, want)
	}
}

func TestAComputeOnlyProviderAdvertisesNoModel(t *testing.T) {
	m := newModelMarket(t)

	if err := m.RegisterProvider(Provider{ID: "cpu", Capacity: 4, PricePerUnit: 1}); err != nil {
		t.Fatalf("register: %v", err)
	}

	got, _ := m.GetProvider("cpu")
	if got.Models != nil {
		t.Fatalf("models = %v, want nil for a compute-only provider", got.Models)
	}
	if got.ServesModel("llama-3.3-70b") {
		t.Fatal("a provider that advertises no model must not serve one")
	}
	// And it is never selected by model, which is what keeps compute-only
	// capacity out of an inference route.
	if provs := m.ProvidersForModel("llama-3.3-70b"); len(provs) != 0 {
		t.Fatalf("ProvidersForModel returned %d providers, want 0", len(provs))
	}
}

func TestProvidersForModelIsCheapestFirstAndCaseInsensitive(t *testing.T) {
	m := newModelMarket(t)

	for _, p := range []Provider{
		{ID: "expensive", Capacity: 10, PricePerUnit: 9, Models: []string{"llama-3.3-70b"}},
		{ID: "cheap", Capacity: 10, PricePerUnit: 2, Models: []string{"llama-3.3-70b"}},
		{ID: "other-model", Capacity: 10, PricePerUnit: 1, Models: []string{"qwen-2.5-72b"}},
	} {
		if err := m.RegisterProvider(p); err != nil {
			t.Fatalf("register %s: %v", p.ID, err)
		}
	}

	// The caller passes the model in whatever case it arrived in.
	got := m.ProvidersForModel("Llama-3.3-70B")
	if len(got) != 2 {
		t.Fatalf("got %d providers, want 2: %+v", len(got), got)
	}
	if got[0].ID != "cheap" || got[1].ID != "expensive" {
		t.Fatalf("order = %s, %s; want cheap, expensive", got[0].ID, got[1].ID)
	}
}

func TestProvidersForModelTiesBreakOnIDSoRoutingIsDeterministic(t *testing.T) {
	m := newModelMarket(t)

	for _, id := range []string{"c", "a", "b"} {
		if err := m.RegisterProvider(Provider{
			ID: id, Capacity: 10, PricePerUnit: 5, Models: []string{"m"},
		}); err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
	}

	// Map iteration order is random, so the same query must not answer
	// differently on two calls: a router that picked the head would otherwise
	// scatter jobs at random across equally-priced providers.
	for i := 0; i < 8; i++ {
		got := m.ProvidersForModel("m")
		if len(got) != 3 || got[0].ID != "a" || got[1].ID != "b" || got[2].ID != "c" {
			t.Fatalf("call %d: order = %+v, want a, b, c", i, got)
		}
	}
}

func TestProvidersForModelSkipsAProviderWithNoCapacityLeft(t *testing.T) {
	m := newModelMarket(t)

	if err := m.RegisterProvider(Provider{
		ID: "full", Capacity: 1, PricePerUnit: 1, Models: []string{"m"},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := m.RegisterProvider(Provider{
		ID: "free", Capacity: 5, PricePerUnit: 4, Models: []string{"m"},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Fund the buyer so the reservation is not refused for affordability, then
	// consume the cheaper provider's only unit.
	if err := m.Ledger().Credit("buyer", 1_000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if _, err := m.SubmitJob("buyer", "full", 1); err != nil {
		t.Fatalf("submit: %v", err)
	}

	got := m.ProvidersForModel("m")
	if len(got) != 1 || got[0].ID != "free" {
		t.Fatalf("got %+v, want only the provider with capacity left", got)
	}
}

func TestModelsSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	store, err := kv.New(kv.Config{Path: dir})
	if err != nil {
		t.Fatalf("open kv: %v", err)
	}
	m := newTestMarket(t, store)
	if err := m.RegisterProvider(Provider{
		ID: "gpu", Capacity: 10, PricePerUnit: 3, Models: []string{"llama-3.3-70b"},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// A provider whose models were not persisted would come back unroutable, so
	// a restart would silently take it off every model route it advertised.
	reopened, err := kv.New(kv.Config{Path: dir})
	if err != nil {
		t.Fatalf("reopen kv: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	m2 := newTestMarket(t, reopened)

	got := m2.ProvidersForModel("llama-3.3-70b")
	if len(got) != 1 || got[0].ID != "gpu" {
		t.Fatalf("after restart got %+v, want the gpu provider", got)
	}
}
