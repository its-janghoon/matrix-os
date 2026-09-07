// Package inference integrates real LLM inference into the Matrix OS compute
// marketplace. A provider node contributes inference capacity in one of two
// modes:
//
//   - LOCAL contribution: the node runs a local model/runner (an Ollama or
//     llama.cpp-style HTTP server) and serves inference itself. The
//     LocalHTTPBackend speaks that runner's API; the EchoBackend is a concrete
//     local stub runner that returns a deterministic completion so the whole
//     path is real and testable without a GPU.
//   - PROVIDER-API contribution: the node contributes by proxying to an external
//     OpenAI-compatible LLM provider API. The OpenAIBackend POSTs to a
//     configurable base URL's /v1/chat/completions endpoint with an API key read
//     from the environment (never hardcoded).
//
// A Backend is a pluggable inference engine selected by name from configuration
// via the Registry/factory. Buyers submit inference jobs, providers fulfill them
// through their configured Backend, and the computed usage units settle
// buyer -> provider in the token through the consensus-backed settlement path
// (see the marketplace wiring in service.go).
//
// The package depends only on the Go standard library so it adds no new module
// dependencies.
package inference

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors so callers and tests can errors.Is against them.
var (
	// ErrEmptyPrompt is returned when a request carries neither a prompt nor any
	// messages, so there is nothing to infer.
	ErrEmptyPrompt = errors.New("inference: request has no prompt or messages")
	// ErrBackendNotFound is returned by a Registry when no backend is registered
	// under the requested name.
	ErrBackendNotFound = errors.New("inference: backend not found")
	// ErrMissingAPIKey is returned when a provider-API backend is constructed
	// without an API key available in the environment.
	ErrMissingAPIKey = errors.New("inference: provider API key not set")
	// ErrProviderStatus is returned when an upstream provider responds with a
	// non-2xx HTTP status.
	ErrProviderStatus = errors.New("inference: provider returned error status")
	// ErrNoCompletion is returned when an upstream provider response contains no
	// completion choices.
	ErrNoCompletion = errors.New("inference: provider returned no completion")
)

// Role enumerates the standard chat message roles used by OpenAI-compatible
// APIs. Buyers may submit either a single Prompt or a list of Messages.
type Role string

// Chat roles.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn in a chat conversation.
type Message struct {
	// Role is the speaker: system, user, or assistant.
	Role Role `json:"role"`
	// Content is the message text.
	Content string `json:"content"`
}

// InferenceRequest describes an inference job to run against a Backend. A caller
// supplies either Prompt (a single user prompt) or Messages (a full chat
// transcript); Messages takes precedence when both are set.
type InferenceRequest struct {
	// Model is the model identifier to run (e.g. "gpt-4o-mini", "llama3").
	Model string `json:"model"`
	// Prompt is a single user prompt. Used when Messages is empty.
	Prompt string `json:"prompt,omitempty"`
	// Messages is a full chat transcript. Takes precedence over Prompt.
	Messages []Message `json:"messages,omitempty"`
	// MaxTokens optionally caps the completion length. Zero means the backend
	// default.
	MaxTokens int `json:"max_tokens,omitempty"`
	// Temperature optionally controls sampling. Zero means the backend default.
	Temperature float64 `json:"temperature,omitempty"`
}

// Messages returns the effective chat transcript for the request: the explicit
// Messages when present, otherwise a single user message built from Prompt. It
// returns ErrEmptyPrompt when neither is set.
func (r InferenceRequest) EffectiveMessages() ([]Message, error) {
	if len(r.Messages) > 0 {
		return r.Messages, nil
	}
	if strings.TrimSpace(r.Prompt) != "" {
		return []Message{{Role: RoleUser, Content: r.Prompt}}, nil
	}
	return nil, ErrEmptyPrompt
}

// Usage reports the token accounting for a completed inference, mirroring the
// OpenAI usage object. Units is the marketplace billing quantity derived from
// this usage (see UnitsFor); it is what settles buyer -> provider in the token.
type Usage struct {
	// PromptTokens is the number of tokens in the prompt/messages.
	PromptTokens int `json:"prompt_tokens"`
	// CompletionTokens is the number of tokens in the generated completion.
	CompletionTokens int `json:"completion_tokens"`
	// TotalTokens is prompt_tokens + completion_tokens.
	TotalTokens int `json:"total_tokens"`
}

// InferenceResponse is the result of a Backend.Infer call.
type InferenceResponse struct {
	// Model is the model that produced the completion (echoing the request or the
	// provider's reported model).
	Model string `json:"model"`
	// Completion is the generated assistant text.
	Completion string `json:"completion"`
	// Usage is the token accounting for the inference.
	Usage Usage `json:"usage"`
	// Units is the billable marketplace quantity for this inference. It is the
	// number of compute units that settle buyer -> provider and is computed by
	// UnitsFor from Usage so pricing is deterministic and backend-independent.
	Units uint64 `json:"units"`
}

// Backend is a pluggable inference engine. An implementation runs a model either
// locally (a local runner or the built-in EchoBackend) or by proxying to an
// external provider API, and returns the completion plus a usage/units count
// used for marketplace pricing. Implementations must be safe for concurrent use.
type Backend interface {
	// Infer runs the request and returns the completion and usage. It must
	// respect ctx cancellation for any network or long-running work.
	Infer(ctx context.Context, req InferenceRequest) (InferenceResponse, error)
	// Name returns the backend's registered name (e.g. "echo", "openai",
	// "local-http"), used for diagnostics and provider advertisement.
	Name() string
}

// UnitsFor derives the billable marketplace units from token usage. Billing is
// per total token, with a floor of one unit so any non-empty completion costs at
// least one unit. Keeping this in one place makes pricing deterministic and
// identical regardless of which backend served the request, which matters
// because settlement moves exactly this many units of the token.
func UnitsFor(u Usage) uint64 {
	total := u.TotalTokens
	if total <= 0 {
		total = u.PromptTokens + u.CompletionTokens
	}
	if total <= 0 {
		return 1
	}
	return uint64(total)
}

// bytesPerTokenFloor is the divisor used to turn a request's byte size into a
// LOWER BOUND on the tokens it will cost.
//
// It is deliberately generous. A BPE token is usually 3-4 bytes of English, so
// bytes/4 would be a close estimate - but this must never over-refuse honest
// work, and a single token can encode many more bytes in some vocabularies and
// scripts. 32 is the safe direction: it under-counts tokens, so the bound only
// ever fires on a request that is unambiguously larger than what was reserved.
const bytesPerTokenFloor = 32

// MinUnitsFor returns a lower bound on the units a request will cost, from what
// is knowable BEFORE running it: the size of the prompt, and the completion
// length if the caller capped one.
//
// WHY A LOWER BOUND IS THE USEFUL QUANTITY. `unitsEstimate` is chosen by the
// client and is what the affordability check runs against; the real cost is only
// known after the work is done, and FulfillJob then clamps the billable units
// DOWN to the reservation so the buyer is never overcharged. Nothing guarded the
// mirror side: a buyer could reserve ONE unit, send a prompt worth thousands,
// and the provider would do all of that work and be paid for one. Confirmed by
// test - a 90,000-byte prompt against a 1-unit reservation.
//
// Comparing this floor against the reservation refuses exactly that case and
// leaves honest estimates alone, because it is a relationship between the
// request and the reservation rather than a fixed size limit: the same prompt
// goes through once enough is reserved for it.
func MinUnitsFor(r InferenceRequest) uint64 {
	msgs, err := r.EffectiveMessages()
	if err != nil {
		// An empty request is rejected elsewhere; it costs at least the one unit
		// every job costs.
		return 1
	}
	var bytes int
	for _, m := range msgs {
		// The role is part of what a backend sends, so it is part of the cost.
		bytes += len(m.Role) + len(m.Content)
	}
	units := uint64(bytes / bytesPerTokenFloor)
	if r.MaxTokens > 0 {
		// The completion is work too, and MaxTokens is the only part of it
		// knowable in advance. A buyer asking for 4000 completion tokens against
		// a 10-unit reservation is the same attack wearing a different hat.
		units += uint64(r.MaxTokens)
	}
	if units == 0 {
		return 1
	}
	return units
}

// promptText joins the effective messages into a single string for backends
// (like the echo/local stub) that reason over the flattened prompt text.
func promptText(msgs []Message) string {
	var b strings.Builder
	for i, m := range msgs {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s: %s", m.Role, m.Content)
	}
	return b.String()
}
