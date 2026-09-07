package node

import (
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/inference"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
)

// backendTestNode builds the minimum a node needs to install configured
// inference backends: a config and an order book. registerConfiguredInference-
// Backends is exercised directly rather than through Start so a failure points
// at the registration and not at everything else a node brings up.
func backendTestNode(t *testing.T, cfg InferenceConfig) (*Node, *inference.Registry) {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m, err := market.NewMarket(store)
	if err != nil {
		t.Fatalf("market.NewMarket: %v", err)
	}
	n := &Node{market: m, config: &Config{}}
	n.config.Inference = cfg
	return n, inference.NewRegistry()
}

func TestConfiguredBackendJoinsBothTheRegistryAndTheOrderBook(t *testing.T) {
	n, registry := backendTestNode(t, InferenceConfig{
		Backends: []InferenceBackendConfig{{
			ID:           "my-gpu-box",
			Kind:         "local-http",
			BaseURL:      "http://127.0.0.1:8000",
			Models:       []string{"Llama-3.3-70B", "qwen-2.5-72b"},
			Capacity:     1000,
			PricePerUnit: 3,
		}},
	})

	if err := n.registerConfiguredInferenceBackends(registry); err != nil {
		t.Fatalf("registerConfiguredInferenceBackends: %v", err)
	}

	// Both registrations are required. A backend with no order-book entry is the
	// mistake the demo provider made once: submit failed with "provider not
	// found" even though the backend was installed.
	if _, err := registry.Backend("my-gpu-box"); err != nil {
		t.Fatalf("backend not in the inference registry: %v", err)
	}
	prov, ok := n.market.GetProvider("my-gpu-box")
	if !ok {
		t.Fatal("provider not on the order book")
	}
	if prov.Capacity != 1000 || prov.PricePerUnit != 3 {
		t.Fatalf("provider = capacity %d, price %d; want 1000, 3", prov.Capacity, prov.PricePerUnit)
	}

	// And it is routable by model, which is the whole point of declaring them.
	routed := n.market.ProvidersForModel("llama-3.3-70b")
	if len(routed) != 1 || routed[0].ID != "my-gpu-box" {
		t.Fatalf("ProvidersForModel = %+v, want the configured backend", routed)
	}
}

func TestAConfiguredBackendIsNotReRegisteredOverPendingReservations(t *testing.T) {
	cfg := InferenceConfig{
		Backends: []InferenceBackendConfig{{
			ID: "gpu", Kind: "echo", Models: []string{"m"}, Capacity: 10, PricePerUnit: 1,
		}},
	}
	n, registry := backendTestNode(t, cfg)
	if err := n.registerConfiguredInferenceBackends(registry); err != nil {
		t.Fatalf("first registration: %v", err)
	}

	if err := n.market.Ledger().Credit("buyer", 1_000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if _, err := n.market.SubmitJob("buyer", "gpu", 4); err != nil {
		t.Fatalf("submit: %v", err)
	}

	// A restart re-runs registration. RegisterProvider resets Available to
	// Capacity, so re-registering would forget the 4 units a pending job holds
	// and let the provider oversubscribe itself.
	if err := n.registerConfiguredInferenceBackends(inference.NewRegistry()); err != nil {
		t.Fatalf("second registration: %v", err)
	}
	prov, _ := n.market.GetProvider("gpu")
	if prov.Available != 6 {
		t.Fatalf("Available = %d after a restart, want 6 (the reservation survives)", prov.Available)
	}
}

func TestConfiguredBackendRejectsBadDeclarations(t *testing.T) {
	tests := []struct {
		name    string
		backend InferenceBackendConfig
		wantIn  string
	}{
		{
			name:    "no id",
			backend: InferenceBackendConfig{Kind: "echo", Capacity: 1, PricePerUnit: 1},
			wantIn:  "id must not be empty",
		},
		{
			name:    "no capacity",
			backend: InferenceBackendConfig{ID: "a", Kind: "echo", PricePerUnit: 1},
			wantIn:  "capacity must be > 0",
		},
		{
			name:    "no price",
			backend: InferenceBackendConfig{ID: "a", Kind: "echo", Capacity: 1},
			wantIn:  "price_per_unit must be > 0",
		},
		{
			name:    "unknown kind",
			backend: InferenceBackendConfig{ID: "a", Kind: "telepathy", Capacity: 1, PricePerUnit: 1},
			wantIn:  "unknown kind",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, registry := backendTestNode(t, InferenceConfig{
				Backends: []InferenceBackendConfig{tt.backend},
			})
			err := n.registerConfiguredInferenceBackends(registry)
			if err == nil {
				t.Fatal("want an error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Fatalf("error = %q, want it to mention %q", err, tt.wantIn)
			}
		})
	}
}

func TestConfiguredBackendRejectsADuplicateProviderID(t *testing.T) {
	n, registry := backendTestNode(t, InferenceConfig{
		EchoProvider: "demo",
		Backends: []InferenceBackendConfig{
			{ID: "gpu", Kind: "echo", Capacity: 1, PricePerUnit: 1},
			{ID: "gpu", Kind: "echo", Capacity: 2, PricePerUnit: 2},
		},
	})

	// Two backends under one provider ID would silently install whichever came
	// last while the order book kept the first one's capacity and price.
	err := n.registerConfiguredInferenceBackends(registry)
	if err == nil || !strings.Contains(err.Error(), "declared twice") {
		t.Fatalf("error = %v, want a duplicate-id rejection", err)
	}
}

func TestConfiguredBackendRejectsCollidingWithTheEchoProvider(t *testing.T) {
	n, registry := backendTestNode(t, InferenceConfig{
		EchoProvider: "demo-inference-provider",
		Backends: []InferenceBackendConfig{
			{ID: "demo-inference-provider", Kind: "local-http", Capacity: 1, PricePerUnit: 1},
		},
	})

	// The echo provider is registered before this runs, so a colliding backend
	// would replace it in the registry while the order book kept the demo's
	// capacity and price.
	err := n.registerConfiguredInferenceBackends(registry)
	if err == nil || !strings.Contains(err.Error(), "echo_provider") {
		t.Fatalf("error = %v, want a collision with inference.echo_provider", err)
	}
}

func TestABackendMayDeclareNoModelAndStaysReachableByID(t *testing.T) {
	n, registry := backendTestNode(t, InferenceConfig{
		Backends: []InferenceBackendConfig{{
			ID: "unlisted", Kind: "echo", Capacity: 5, PricePerUnit: 2,
		}},
	})

	if err := n.registerConfiguredInferenceBackends(registry); err != nil {
		t.Fatalf("registerConfiguredInferenceBackends: %v", err)
	}

	if _, ok := n.market.GetProvider("unlisted"); !ok {
		t.Fatal("a backend with no declared model should still be on the order book")
	}
	if got := n.market.ProvidersForModel("anything"); len(got) != 0 {
		t.Fatalf("ProvidersForModel = %+v, want nothing: it advertises no model", got)
	}
}
