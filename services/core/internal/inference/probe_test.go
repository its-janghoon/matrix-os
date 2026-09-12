package inference

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestProbeReportsUpstreamReadiness drives both network backends against a
// server whose health it can flip, and asserts the probe follows it.
func TestProbeReportsUpstreamReadiness(t *testing.T) {
	t.Setenv("MATRIX_TEST_KEY", "test-key")

	var healthy atomic.Bool
	var probedPath atomic.Value
	healthy.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probedPath.Store(r.URL.Path)
		if !healthy.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer srv.Close()

	openAI, err := NewOpenAIBackend(OpenAIConfig{BaseURL: srv.URL, APIKeyEnv: "MATRIX_TEST_KEY"})
	if err != nil {
		t.Fatalf("NewOpenAIBackend: %v", err)
	}
	local, err := NewLocalHTTPBackend(LocalHTTPConfig{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewLocalHTTPBackend: %v", err)
	}

	for _, tc := range []struct {
		name     string
		backend  ProbeableBackend
		wantPath string
	}{
		{"openai", openAI, DefaultOpenAIProbePath},
		{"local-http", local, DefaultLocalHTTPProbePath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			healthy.Store(true)
			if err := tc.backend.Probe(context.Background()); err != nil {
				t.Fatalf("a healthy upstream should probe clean: %v", err)
			}
			if got := probedPath.Load(); got != tc.wantPath {
				t.Fatalf("probe path = %v, want %s", got, tc.wantPath)
			}

			// A non-2xx is not ready. This is the case that matters: the HTTP
			// server is answering, so a bare connection check would call it
			// healthy while it refuses every request.
			healthy.Store(false)
			if err := tc.backend.Probe(context.Background()); err == nil {
				t.Fatal("an upstream returning 503 must not probe clean")
			}
		})
	}
}

// TestProbeFailsWhenTheUpstreamIsGone is the crashed-model-server case, which is
// the whole reason the probe exists.
func TestProbeFailsWhenTheUpstreamIsGone(t *testing.T) {
	t.Setenv("MATRIX_TEST_KEY", "test-key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	backend, err := NewOpenAIBackend(OpenAIConfig{BaseURL: url, APIKeyEnv: "MATRIX_TEST_KEY"})
	if err != nil {
		t.Fatalf("NewOpenAIBackend: %v", err)
	}
	if err := backend.Probe(context.Background()); err == nil {
		t.Fatal("probing a dead upstream must fail")
	}
}

// TestConfiguredProbePathIsUsed covers the operator override, which is how a
// vLLM provider points the check at /health - a signal about the inference
// engine rather than only the HTTP layer in front of it.
func TestConfiguredProbePathIsUsed(t *testing.T) {
	t.Setenv("MATRIX_TEST_KEY", "test-key")

	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.URL.Path)
	}))
	defer srv.Close()

	// Written without the leading slash on purpose: a config that omits it must
	// not produce a URL with the path glued onto the host.
	backend, err := NewOpenAIBackend(OpenAIConfig{
		BaseURL:   srv.URL,
		APIKeyEnv: "MATRIX_TEST_KEY",
		ProbePath: "health",
	})
	if err != nil {
		t.Fatalf("NewOpenAIBackend: %v", err)
	}
	if err := backend.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got.Load() != "/health" {
		t.Fatalf("probe path = %v, want /health", got.Load())
	}
}

// TestProbeDoesNotWaitForTheRequestTimeout pins that the probe has a deadline of
// its own. A provider serving long completions may allow ten minutes for real
// work, and inheriting that would mean waiting ten minutes to learn a dead
// server is dead.
func TestProbeDoesNotWaitForTheRequestTimeout(t *testing.T) {
	t.Setenv("MATRIX_TEST_KEY", "test-key")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	backend, err := NewOpenAIBackend(OpenAIConfig{
		BaseURL:        srv.URL,
		APIKeyEnv:      "MATRIX_TEST_KEY",
		RequestTimeout: time.Hour,
	})
	if err != nil {
		t.Fatalf("NewOpenAIBackend: %v", err)
	}

	// The probe's own deadline is what must fire. Cap the test well under the
	// hour-long request timeout: if the probe inherited it, this would hang.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- backend.Probe(ctx) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a hanging upstream must not probe clean")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the probe inherited the request timeout instead of bounding itself")
	}
}
