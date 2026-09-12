package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LocalHTTPBackend implements the LOCAL contribution mode against a local model
// runner that exposes an Ollama-style HTTP API (POST /api/chat with a non-stream
// JSON response). It is the production local path a node uses when it runs a
// real model on its own hardware; in the sandbox (no GPU / no runner) the
// EchoBackend stands in for it, exercising the identical marketplace and
// settlement flow.
//
// The Ollama /api/chat non-streaming response shape parsed here is:
//
//	{"model":"llama3","message":{"role":"assistant","content":"..."},
//	 "prompt_eval_count":12,"eval_count":34}
//
// prompt_eval_count / eval_count are Ollama's prompt/completion token counts,
// which map directly onto our Usage.
type LocalHTTPBackend struct {
	baseURL   string
	probePath string
	client    *http.Client
}

// LocalHTTPConfig configures a LocalHTTPBackend.
type LocalHTTPConfig struct {
	// BaseURL is the runner root (e.g. "http://127.0.0.1:11434"). A trailing
	// slash is trimmed. Required.
	BaseURL string
	// RequestTimeout bounds one runner request end to end. Zero means
	// defaultHTTPTimeout. A local runner on the provider's own GPU is exactly the
	// case where the 60s default is too short: it is generating every token
	// itself, so a long completion outlasts a cap sized for a hosted API.
	RequestTimeout time.Duration
	// ProbePath is the path Probe issues a GET against. Empty means
	// DefaultLocalHTTPProbePath.
	ProbePath string
	// HTTPClient overrides the HTTP client (mainly for tests). When nil a client
	// with RequestTimeout, or defaultHTTPTimeout when that is zero, is used.
	HTTPClient *http.Client
}

// NewLocalHTTPBackend constructs a LocalHTTPBackend pointed at a local runner.
func NewLocalHTTPBackend(cfg LocalHTTPConfig) (*LocalHTTPBackend, error) {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		return nil, fmt.Errorf("inference: local runner base URL is required")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: effectiveTimeout(cfg.RequestTimeout)}
	}
	return &LocalHTTPBackend{
		baseURL:   baseURL,
		probePath: normalizeProbePath(cfg.ProbePath, DefaultLocalHTTPProbePath),
		client:    client,
	}, nil
}

// Name identifies the backend for advertisement and diagnostics.
func (b *LocalHTTPBackend) Name() string { return "local-http" }

// ollamaChatRequest is the Ollama /api/chat request body. Stream is forced false
// so the response is a single JSON object we can parse directly.
type ollamaChatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Options  *ollamaOpts   `json:"options,omitempty"`
}

// ollamaOpts carries optional sampling parameters.
type ollamaOpts struct {
	NumPredict  int     `json:"num_predict,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
}

// ollamaChatResponse is the subset of the Ollama non-streaming response parsed.
type ollamaChatResponse struct {
	Model           string      `json:"model"`
	Message         chatMessage `json:"message"`
	PromptEvalCount int         `json:"prompt_eval_count"`
	EvalCount       int         `json:"eval_count"`
}

// Infer POSTs the request to {baseURL}/api/chat with streaming disabled and maps
// the runner's response and token counts into an InferenceResponse.
func (b *LocalHTTPBackend) Infer(ctx context.Context, req InferenceRequest) (InferenceResponse, error) {
	msgs, err := req.EffectiveMessages()
	if err != nil {
		return InferenceResponse{}, err
	}

	wire := ollamaChatRequest{
		Model:    req.Model,
		Messages: toWireMessages(msgs),
		Stream:   false,
	}
	if req.MaxTokens > 0 || req.Temperature > 0 {
		wire.Options = &ollamaOpts{NumPredict: req.MaxTokens, Temperature: req.Temperature}
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return InferenceResponse{}, fmt.Errorf("inference: marshal request: %w", err)
	}

	url := b.baseURL + "/api/chat"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return InferenceResponse{}, fmt.Errorf("inference: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return InferenceResponse{}, fmt.Errorf("inference: local runner request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return InferenceResponse{}, fmt.Errorf("inference: read local runner response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return InferenceResponse{}, fmt.Errorf("%w: status %d: %s", ErrProviderStatus, resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var parsed ollamaChatResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return InferenceResponse{}, fmt.Errorf("inference: decode local runner response: %w", err)
	}
	if strings.TrimSpace(parsed.Message.Content) == "" {
		return InferenceResponse{}, ErrNoCompletion
	}

	usage := Usage{
		PromptTokens:     parsed.PromptEvalCount,
		CompletionTokens: parsed.EvalCount,
		TotalTokens:      parsed.PromptEvalCount + parsed.EvalCount,
	}
	model := parsed.Model
	if model == "" {
		model = req.Model
	}

	return InferenceResponse{
		Model:      model,
		Completion: parsed.Message.Content,
		Usage:      usage,
		Units:      UnitsFor(usage),
	}, nil
}
