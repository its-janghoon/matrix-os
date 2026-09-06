package inference

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestOpenAIBackend_Infer drives the OpenAI-compatible backend against an
// httptest.Server returning a canned chat completion, asserting the request
// shape (path, method, Authorization bearer header, and JSON body) and that the
// standard response is parsed into the completion and usage-derived units.
func TestOpenAIBackend_Infer(t *testing.T) {
	const apiKey = "test-secret-key"
	t.Setenv("MATRIX_TEST_OPENAI_KEY", apiKey)

	var gotAuth, gotPath, gotMethod, gotContentType string
	var gotBody chatCompletionRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")

		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "gpt-4o-mini",
			"choices": [
				{"message": {"role": "assistant", "content": "Paris is the capital of France."}}
			],
			"usage": {"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 20}
		}`))
	}))
	defer srv.Close()

	backend, err := NewOpenAIBackend(OpenAIConfig{
		BaseURL:    srv.URL,
		APIKeyEnv:  "MATRIX_TEST_OPENAI_KEY",
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenAIBackend: %v", err)
	}
	if backend.Name() != "openai" {
		t.Fatalf("name = %q, want openai", backend.Name())
	}

	resp, err := backend.Infer(context.Background(), InferenceRequest{
		Model:  "gpt-4o-mini",
		Prompt: "What is the capital of France?",
	})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}

	// Request shape assertions.
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path = %q, want /v1/chat/completions", gotPath)
	}
	if gotContentType != "application/json" {
		t.Fatalf("content-type = %q, want application/json", gotContentType)
	}
	if gotAuth != "Bearer "+apiKey {
		t.Fatalf("authorization header = %q, want %q", gotAuth, "Bearer "+apiKey)
	}
	if gotBody.Model != "gpt-4o-mini" {
		t.Fatalf("request model = %q, want gpt-4o-mini", gotBody.Model)
	}
	if len(gotBody.Messages) != 1 || gotBody.Messages[0].Role != "user" ||
		gotBody.Messages[0].Content != "What is the capital of France?" {
		t.Fatalf("unexpected request messages: %+v", gotBody.Messages)
	}

	// Response parsing assertions.
	if resp.Completion != "Paris is the capital of France." {
		t.Fatalf("completion = %q", resp.Completion)
	}
	if resp.Usage.PromptTokens != 12 || resp.Usage.CompletionTokens != 8 || resp.Usage.TotalTokens != 20 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
	if resp.Units != 20 {
		t.Fatalf("units = %d, want 20", resp.Units)
	}
	if resp.Model != "gpt-4o-mini" {
		t.Fatalf("model = %q, want gpt-4o-mini", resp.Model)
	}
}

// TestOpenAIBackend_MissingAPIKey asserts construction fails fast when the API
// key environment variable is empty, so a provider never sends unauthenticated
// requests.
func TestOpenAIBackend_MissingAPIKey(t *testing.T) {
	t.Setenv("MATRIX_TEST_MISSING_KEY", "")
	if _, err := NewOpenAIBackend(OpenAIConfig{APIKeyEnv: "MATRIX_TEST_MISSING_KEY"}); !errors.Is(err, ErrMissingAPIKey) {
		t.Fatalf("expected ErrMissingAPIKey, got %v", err)
	}
}

// TestOpenAIBackend_ErrorStatus asserts a non-2xx upstream maps to
// ErrProviderStatus.
func TestOpenAIBackend_ErrorStatus(t *testing.T) {
	t.Setenv("MATRIX_TEST_OPENAI_KEY", "k")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()

	backend, err := NewOpenAIBackend(OpenAIConfig{
		BaseURL:    srv.URL,
		APIKeyEnv:  "MATRIX_TEST_OPENAI_KEY",
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenAIBackend: %v", err)
	}
	if _, err := backend.Infer(context.Background(), InferenceRequest{Model: "m", Prompt: "hi"}); !errors.Is(err, ErrProviderStatus) {
		t.Fatalf("expected ErrProviderStatus, got %v", err)
	}
}

// TestOpenAIBackend_NoChoices asserts an empty choices array maps to
// ErrNoCompletion.
func TestOpenAIBackend_NoChoices(t *testing.T) {
	t.Setenv("MATRIX_TEST_OPENAI_KEY", "k")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"model":"m","choices":[],"usage":{}}`))
	}))
	defer srv.Close()

	backend, err := NewOpenAIBackend(OpenAIConfig{
		BaseURL:    srv.URL,
		APIKeyEnv:  "MATRIX_TEST_OPENAI_KEY",
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenAIBackend: %v", err)
	}
	if _, err := backend.Infer(context.Background(), InferenceRequest{Model: "m", Prompt: "hi"}); !errors.Is(err, ErrNoCompletion) {
		t.Fatalf("expected ErrNoCompletion, got %v", err)
	}
}
