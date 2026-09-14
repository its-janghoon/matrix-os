package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultOpenAIBaseURL is the canonical OpenAI API base URL. A provider that
// proxies to a different OpenAI-compatible vendor (Together, Groq, a local
// gateway, etc.) overrides this via config.
const DefaultOpenAIBaseURL = "https://api.openai.com"

// defaultHTTPTimeout bounds a single provider request so a hung upstream cannot
// stall a marketplace job indefinitely.
const defaultHTTPTimeout = 60 * time.Second

// effectiveTimeout resolves a configured per-request timeout, falling back to
// defaultHTTPTimeout when the operator set none. A non-positive value is the
// "unset" signal rather than "no timeout": a backend with no deadline at all
// lets one wedged upstream hold a marketplace job open forever, so it is not a
// value config is allowed to express.
func effectiveTimeout(configured time.Duration) time.Duration {
	if configured <= 0 {
		return defaultHTTPTimeout
	}
	return configured
}

// OpenAIBackend implements the PROVIDER-API contribution mode: it fulfills
// inference by POSTing to an OpenAI-compatible /v1/chat/completions endpoint on
// a configurable base URL, authenticating with a Bearer API key. The API key is
// read from the environment and NEVER hardcoded or logged.
type OpenAIBackend struct {
	baseURL   string
	apiKey    string
	probePath string
	client    *http.Client
}

// OpenAIConfig configures an OpenAIBackend.
type OpenAIConfig struct {
	// BaseURL is the API root (without the /v1/... path). Empty means
	// DefaultOpenAIBaseURL. A trailing slash is trimmed.
	BaseURL string
	// APIKeyEnv is the environment variable holding the API key. Empty means
	// "OPENAI_API_KEY". The key is read at construction time; it is required.
	APIKeyEnv string
	// RequestTimeout bounds one upstream request end to end, INCLUDING the time
	// spent reading a streamed body, because http.Client.Timeout covers the body
	// read and not just the response headers. Zero means defaultHTTPTimeout.
	//
	// It is configurable because 60s is a reasonable cap on somebody else's API
	// and a wrong one on a GPU the provider owns. A local model emitting a few
	// thousand tokens runs past a minute as a matter of course, and the fixed cap
	// cut the request off after the GPU had already done the work - the provider
	// paid for the tokens in electricity and the job failed anyway.
	RequestTimeout time.Duration
	// ProbePath is the path Probe issues a GET against to decide whether this
	// upstream is answering. Empty means DefaultOpenAIProbePath. Point it at
	// /health on a local server that offers one; see DefaultOpenAIProbePath.
	ProbePath string
	// HTTPClient overrides the HTTP client (mainly for tests). When nil a client
	// with RequestTimeout, or defaultHTTPTimeout when that is zero, is used.
	HTTPClient *http.Client
}

// NewOpenAIBackend constructs an OpenAIBackend, reading the API key from the
// configured environment variable. It returns ErrMissingAPIKey when the variable
// is unset or empty so a misconfigured provider fails fast rather than sending
// unauthenticated requests.
func NewOpenAIBackend(cfg OpenAIConfig) (*OpenAIBackend, error) {
	envName := cfg.APIKeyEnv
	if envName == "" {
		envName = "OPENAI_API_KEY"
	}
	apiKey := os.Getenv(envName)
	if apiKey == "" {
		return nil, fmt.Errorf("%w: environment variable %q is empty", ErrMissingAPIKey, envName)
	}

	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultOpenAIBaseURL
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: effectiveTimeout(cfg.RequestTimeout)}
	}

	return &OpenAIBackend{
		baseURL:   baseURL,
		apiKey:    apiKey,
		probePath: normalizeProbePath(cfg.ProbePath, DefaultOpenAIProbePath),
		client:    client,
	}, nil
}

// Name identifies the backend for advertisement and diagnostics.
func (b *OpenAIBackend) Name() string { return "openai" }

// chatMessage is the wire representation of a chat message.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatCompletionRequest is the OpenAI /v1/chat/completions request body.
type chatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
}

// chatCompletionResponse is the subset of the OpenAI response we parse.
type chatCompletionResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// Infer POSTs the request to {baseURL}/v1/chat/completions with an
// Authorization: Bearer <key> header, then parses the standard chat-completions
// response into an InferenceResponse. The completion is the first choice's
// message content; units are derived from the reported usage.
func (b *OpenAIBackend) Infer(ctx context.Context, req InferenceRequest) (InferenceResponse, error) {
	msgs, err := req.EffectiveMessages()
	if err != nil {
		return InferenceResponse{}, err
	}

	wire := chatCompletionRequest{
		Model:       req.Model,
		Messages:    toWireMessages(msgs),
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return InferenceResponse{}, fmt.Errorf("inference: marshal request: %w", err)
	}

	url := b.baseURL + "/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return InferenceResponse{}, fmt.Errorf("inference: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+b.apiKey)

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return InferenceResponse{}, fmt.Errorf("inference: provider request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return InferenceResponse{}, fmt.Errorf("inference: read provider response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return InferenceResponse{}, fmt.Errorf("%w: status %d: %s", ErrProviderStatus, resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var parsed chatCompletionResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return InferenceResponse{}, fmt.Errorf("inference: decode provider response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return InferenceResponse{}, ErrNoCompletion
	}

	usage := Usage{
		PromptTokens:     parsed.Usage.PromptTokens,
		CompletionTokens: parsed.Usage.CompletionTokens,
		TotalTokens:      parsed.Usage.TotalTokens,
	}
	model := parsed.Model
	if model == "" {
		model = req.Model
	}

	return InferenceResponse{
		Model:      model,
		Completion: parsed.Choices[0].Message.Content,
		Usage:      usage,
		Units:      UnitsFor(usage),
	}, nil
}

// toWireMessages converts internal messages to the OpenAI wire representation.
func toWireMessages(msgs []Message) []chatMessage {
	out := make([]chatMessage, len(msgs))
	for i, m := range msgs {
		out[i] = chatMessage{Role: string(m.Role), Content: m.Content}
	}
	return out
}
