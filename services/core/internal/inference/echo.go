package inference

import (
	"context"
	"strings"
)

// EchoBackend is a concrete LOCAL stub runner. It implements the local
// contribution mode without a GPU or a real model: it returns a deterministic
// completion derived from the request prompt, and computes a deterministic token
// usage from word counts. Because its output is a pure function of its input, it
// makes the whole inference + settlement path real and testable end to end.
//
// It is the local analogue that a real deployment would swap for LocalHTTPBackend
// pointed at an Ollama/llama.cpp runner: the marketplace wiring, pricing, and
// settlement are identical regardless of which local backend serves the request.
type EchoBackend struct {
	// Prefix is prepended to the echoed prompt in the completion. When empty it
	// defaults to "echo: ". It exists so a deployment can label stub output.
	Prefix string
}

// NewEchoBackend constructs an EchoBackend with the default "echo: " prefix.
func NewEchoBackend() *EchoBackend { return &EchoBackend{Prefix: "echo: "} }

// Name identifies the backend for advertisement and diagnostics.
func (b *EchoBackend) Name() string { return "echo" }

// Infer returns a deterministic completion: the configured prefix followed by
// the flattened prompt text. Usage is computed deterministically by counting
// whitespace-separated words in the prompt (prompt tokens) and completion
// (completion tokens), so identical inputs always yield identical units. This
// determinism is what lets the marketplace test assert an exact settled amount.
func (b *EchoBackend) Infer(_ context.Context, req InferenceRequest) (InferenceResponse, error) {
	msgs, err := req.EffectiveMessages()
	if err != nil {
		return InferenceResponse{}, err
	}
	prompt := promptText(msgs)

	prefix := b.Prefix
	if prefix == "" {
		prefix = "echo: "
	}
	completion := prefix + prompt

	promptTokens := countTokens(prompt)
	completionTokens := countTokens(completion)
	usage := Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}

	model := req.Model
	if model == "" {
		model = "echo"
	}

	return InferenceResponse{
		Model:      model,
		Completion: completion,
		Usage:      usage,
		Units:      UnitsFor(usage),
	}, nil
}

// InferStream produces the same completion as Infer, one whitespace-separated
// word at a time. The stub does not gain anything from streaming itself; it is
// here so the streaming path is real and testable end to end without a GPU,
// exactly as Infer makes the settlement path testable.
//
// The chunks concatenate to the identical completion Infer returns, and the
// returned response is identical too, so what settles does not depend on which
// method a caller used.
func (b *EchoBackend) InferStream(ctx context.Context, req InferenceRequest, onChunk ChunkFunc) (InferenceResponse, error) {
	resp, err := b.Infer(ctx, req)
	if err != nil {
		return InferenceResponse{}, err
	}

	// Split on the spaces rather than on words, so re-joining the deltas gives
	// back the exact string including its whitespace. A stream whose pieces do
	// not reassemble into what was billed for is worse than no stream.
	remaining := resp.Completion
	for remaining != "" {
		if err := ctx.Err(); err != nil {
			return resp, err
		}
		cut := strings.IndexByte(remaining, ' ')
		var delta string
		if cut < 0 {
			delta, remaining = remaining, ""
		} else {
			delta, remaining = remaining[:cut+1], remaining[cut+1:]
		}
		if err := onChunk(delta); err != nil {
			return resp, err
		}
	}
	return resp, nil
}

// countTokens is a deterministic, dependency-free token estimator: the number of
// whitespace-separated fields in s. It is not a real BPE tokenizer, but it is
// stable and monotonic in input length, which is all the marketplace needs to
// price stub inference deterministically.
func countTokens(s string) int {
	return len(strings.Fields(s))
}
