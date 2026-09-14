package inference

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestRequestTimeoutBoundsTheBackends proves the configured per-request timeout
// actually reaches the HTTP client of both network backends, by making one that
// is too short fail against a slow upstream and a longer one succeed against the
// same upstream. Asserting on client.Timeout alone would pass even if the value
// were never consulted.
func TestRequestTimeoutBoundsTheBackends(t *testing.T) {
	const upstreamDelay = 150 * time.Millisecond

	openAIBody := `{"model":"m","choices":[{"message":{"role":"assistant","content":"ok"}}],` +
		`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	ollamaBody := `{"model":"m","message":{"role":"assistant","content":"ok"},` +
		`"prompt_eval_count":1,"eval_count":1}`

	slow := func(body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(upstreamDelay)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}))
	}

	t.Setenv("MATRIX_TEST_KEY", "test-key")

	for _, tc := range []struct {
		name string
		body string
		// build constructs the backend under test against baseURL.
		build func(baseURL string, timeout time.Duration) (Backend, error)
	}{
		{
			name: "openai",
			body: openAIBody,
			build: func(baseURL string, timeout time.Duration) (Backend, error) {
				return NewOpenAIBackend(OpenAIConfig{
					BaseURL:        baseURL,
					APIKeyEnv:      "MATRIX_TEST_KEY",
					RequestTimeout: timeout,
				})
			},
		},
		{
			name: "local-http",
			body: ollamaBody,
			build: func(baseURL string, timeout time.Duration) (Backend, error) {
				return NewLocalHTTPBackend(LocalHTTPConfig{
					BaseURL:        baseURL,
					RequestTimeout: timeout,
				})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := slow(tc.body)
			defer srv.Close()

			short, err := tc.build(srv.URL, upstreamDelay/10)
			if err != nil {
				t.Fatalf("build short-timeout backend: %v", err)
			}
			if _, err := short.Infer(context.Background(), InferenceRequest{Model: "m", Prompt: "hi"}); err == nil {
				t.Fatal("a timeout shorter than the upstream delay should have failed the request")
			}

			long, err := tc.build(srv.URL, 10*time.Second)
			if err != nil {
				t.Fatalf("build long-timeout backend: %v", err)
			}
			resp, err := long.Infer(context.Background(), InferenceRequest{Model: "m", Prompt: "hi"})
			if err != nil {
				t.Fatalf("a timeout longer than the upstream delay should have succeeded: %v", err)
			}
			if resp.Completion != "ok" {
				t.Fatalf("completion = %q, want %q", resp.Completion, "ok")
			}
		})
	}
}

// TestUnsetRequestTimeoutFallsBackToTheDefault pins the "zero means default"
// contract. A non-positive value must never mean "no deadline": a backend with
// no deadline lets one wedged runner hold a marketplace reservation forever.
func TestUnsetRequestTimeoutFallsBackToTheDefault(t *testing.T) {
	for _, configured := range []time.Duration{0, -time.Second} {
		if got := effectiveTimeout(configured); got != defaultHTTPTimeout {
			t.Fatalf("effectiveTimeout(%s) = %s, want %s", configured, got, defaultHTTPTimeout)
		}
	}

	t.Setenv("MATRIX_TEST_KEY", "test-key")
	openAI, err := NewOpenAIBackend(OpenAIConfig{BaseURL: "http://127.0.0.1:1", APIKeyEnv: "MATRIX_TEST_KEY"})
	if err != nil {
		t.Fatalf("NewOpenAIBackend: %v", err)
	}
	if openAI.client.Timeout != defaultHTTPTimeout {
		t.Fatalf("openai timeout = %s, want %s", openAI.client.Timeout, defaultHTTPTimeout)
	}
	local, err := NewLocalHTTPBackend(LocalHTTPConfig{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewLocalHTTPBackend: %v", err)
	}
	if local.client.Timeout != defaultHTTPTimeout {
		t.Fatalf("local-http timeout = %s, want %s", local.client.Timeout, defaultHTTPTimeout)
	}
}

// TestBackendConfigCarriesTheRequestTimeout covers the factory hop the node uses:
// a timeout set on the config-driven BackendConfig must survive into the built
// backend, which is the path `inference.backends[].request_timeout` takes.
func TestBackendConfigCarriesTheRequestTimeout(t *testing.T) {
	t.Setenv("MATRIX_TEST_KEY", "test-key")
	const want = 7 * time.Minute

	b, err := NewBackend(BackendConfig{
		Kind:           KindOpenAI,
		BaseURL:        "http://127.0.0.1:1",
		APIKeyEnv:      "MATRIX_TEST_KEY",
		RequestTimeout: want,
	})
	if err != nil {
		t.Fatalf("NewBackend(openai): %v", err)
	}
	if got := b.(*OpenAIBackend).client.Timeout; got != want {
		t.Fatalf("openai timeout = %s, want %s", got, want)
	}

	b, err = NewBackend(BackendConfig{
		Kind:           KindLocalHTTP,
		BaseURL:        "http://127.0.0.1:1",
		RequestTimeout: want,
	})
	if err != nil {
		t.Fatalf("NewBackend(local-http): %v", err)
	}
	if got := b.(*LocalHTTPBackend).client.Timeout; got != want {
		t.Fatalf("local-http timeout = %s, want %s", got, want)
	}
}
