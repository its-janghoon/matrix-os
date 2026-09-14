package node

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/inference"
)

// waitFor polls until cond holds or the deadline passes. The supervisor is a
// goroutine on a ticker, so a test has to wait for an effect rather than assert
// immediately after triggering it.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestHealthCheckTakesADeadBackendOffTheMarketAndBringsItBack is the end-to-end
// behaviour: the node probes the configured backend, suspends its order-book
// listing when the model server stops answering, and restores it when it
// recovers. Before this, a crashed model server produced a provider that kept
// winning routing decisions and failing the reservations it took.
func TestHealthCheckTakesADeadBackendOffTheMarketAndBringsItBack(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()

	n, registry := backendTestNode(t, InferenceConfig{
		Backends: []InferenceBackendConfig{{
			ID:                  "gpu-box-1",
			Kind:                "local-http",
			BaseURL:             srv.URL,
			HealthCheckInterval: 10 * time.Millisecond,
			Models:              []string{"qwen3-32b"},
			Capacity:            1000,
			PricePerUnit:        2,
		}},
	})
	if err := n.registerConfiguredInferenceBackends(registry); err != nil {
		t.Fatalf("registerConfiguredInferenceBackends: %v", err)
	}
	if len(n.inferenceHealthChecks) != 1 {
		t.Fatalf("health checks collected = %d, want 1", len(n.inferenceHealthChecks))
	}

	ctx, cancel := context.WithCancel(context.Background())
	n.ctx = ctx
	defer cancel()
	n.startInferenceHealthChecks()

	routable := func() bool { return len(n.market.ProvidersForModel("qwen3-32b")) == 1 }
	waitFor(t, "a healthy backend to be routable", routable)

	healthy.Store(false)
	waitFor(t, "a dead backend to be taken off the market", func() bool { return !routable() })

	p, ok := n.market.GetProvider("gpu-box-1")
	if !ok || !p.Suspended {
		t.Fatalf("provider = %+v, ok=%v; want suspended", p, ok)
	}

	healthy.Store(true)
	waitFor(t, "a recovered backend to return to the market", routable)
}

// TestHealthCheckIsOnByDefaultAndCanBeTurnedOff covers the switch. Default ON is
// deliberate: a provider that did not think about this gets the protection. The
// switch exists because "openai" also points at paid third-party vendors, where
// every probe is a billed request against a rate limit.
func TestHealthCheckIsOnByDefaultAndCanBeTurnedOff(t *testing.T) {
	off := false
	on := true
	for _, tc := range []struct {
		name        string
		flag        *bool
		interval    time.Duration
		wantEnabled bool
		wantEvery   time.Duration
	}{
		{"unset means on at the default interval", nil, 0, true, defaultInferenceHealthCheckInterval},
		{"explicit true means on", &on, 0, true, defaultInferenceHealthCheckInterval},
		{"a configured interval is honoured", nil, 5 * time.Second, true, 5 * time.Second},
		{"explicit false means off", &off, 5 * time.Second, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			every, enabled := inferenceHealthCheckInterval(InferenceBackendConfig{
				HealthCheck:         tc.flag,
				HealthCheckInterval: tc.interval,
			})
			if enabled != tc.wantEnabled {
				t.Fatalf("enabled = %v, want %v", enabled, tc.wantEnabled)
			}
			if enabled && every != tc.wantEvery {
				t.Fatalf("interval = %s, want %s", every, tc.wantEvery)
			}
		})
	}
}

// TestDisabledHealthCheckStartsNoSupervisor proves the switch reaches the
// registration path, not just the helper.
func TestDisabledHealthCheckStartsNoSupervisor(t *testing.T) {
	off := false
	n, registry := backendTestNode(t, InferenceConfig{
		Backends: []InferenceBackendConfig{{
			ID:           "resold-credits",
			Kind:         "local-http",
			BaseURL:      "http://127.0.0.1:11434",
			HealthCheck:  &off,
			Capacity:     10,
			PricePerUnit: 1,
		}},
	})
	if err := n.registerConfiguredInferenceBackends(registry); err != nil {
		t.Fatalf("registerConfiguredInferenceBackends: %v", err)
	}
	if len(n.inferenceHealthChecks) != 0 {
		t.Fatalf("health checks collected = %d, want none", len(n.inferenceHealthChecks))
	}
}

// TestEchoBackendIsNeverProbed pins that a backend with no upstream is left
// alone rather than guessed about. The echo backend cannot be down.
func TestEchoBackendIsNeverProbed(t *testing.T) {
	if _, ok := any(inference.NewEchoBackend()).(inference.ProbeableBackend); ok {
		t.Fatal("the echo backend has no upstream and must not claim to be probeable")
	}
}
