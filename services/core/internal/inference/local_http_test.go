package inference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestLocalHTTPBackend_Infer drives the local-runner backend against an
// httptest.Server emulating an Ollama /api/chat non-streaming response,
// asserting the request path, that streaming is disabled, and that the runner's
// token counts map onto Usage/units.
func TestLocalHTTPBackend_Infer(t *testing.T) {
	var gotPath string
	var gotBody ollamaChatRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "llama3",
			"message": {"role": "assistant", "content": "Hello from the local runner."},
			"prompt_eval_count": 5,
			"eval_count": 6
		}`))
	}))
	defer srv.Close()

	backend, err := NewLocalHTTPBackend(LocalHTTPConfig{BaseURL: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("NewLocalHTTPBackend: %v", err)
	}
	if backend.Name() != "local-http" {
		t.Fatalf("name = %q, want local-http", backend.Name())
	}

	resp, err := backend.Infer(context.Background(), InferenceRequest{Model: "llama3", Prompt: "hi"})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}

	if gotPath != "/api/chat" {
		t.Fatalf("path = %q, want /api/chat", gotPath)
	}
	if gotBody.Stream {
		t.Fatalf("stream = true, want false (non-streaming)")
	}
	if gotBody.Model != "llama3" {
		t.Fatalf("request model = %q, want llama3", gotBody.Model)
	}

	if resp.Completion != "Hello from the local runner." {
		t.Fatalf("completion = %q", resp.Completion)
	}
	if resp.Usage.PromptTokens != 5 || resp.Usage.CompletionTokens != 6 || resp.Usage.TotalTokens != 11 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
	if resp.Units != 11 {
		t.Fatalf("units = %d, want 11", resp.Units)
	}
}

// TestNewBackend_Factory asserts the config-driven factory builds each kind and
// that an unknown kind is rejected.
func TestNewBackend_Factory(t *testing.T) {
	b, err := NewBackend(BackendConfig{Kind: KindEcho})
	if err != nil || b.Name() != "echo" {
		t.Fatalf("echo backend: %v / %v", b, err)
	}

	t.Setenv("MATRIX_TEST_FACTORY_KEY", "k")
	b, err = NewBackend(BackendConfig{Kind: KindOpenAI, APIKeyEnv: "MATRIX_TEST_FACTORY_KEY", BaseURL: "http://example"})
	if err != nil || b.Name() != "openai" {
		t.Fatalf("openai backend: %v / %v", b, err)
	}

	b, err = NewBackend(BackendConfig{Kind: KindLocalHTTP, BaseURL: "http://127.0.0.1:11434"})
	if err != nil || b.Name() != "local-http" {
		t.Fatalf("local-http backend: %v / %v", b, err)
	}

	if _, err := NewBackend(BackendConfig{Kind: "bogus"}); err == nil {
		t.Fatalf("expected error for unknown kind")
	}
}

// TestRegistry asserts provider->backend registration and lookup.
func TestRegistry(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Backend("nope"); err == nil {
		t.Fatalf("expected ErrBackendNotFound for unknown provider")
	}
	if err := r.Register("prov-1", NewEchoBackend()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	b, err := r.Backend("prov-1")
	if err != nil || b.Name() != "echo" {
		t.Fatalf("Backend lookup: %v / %v", b, err)
	}
	if got := r.Providers(); len(got) != 1 || got[0] != "prov-1" {
		t.Fatalf("Providers = %v", got)
	}
}
