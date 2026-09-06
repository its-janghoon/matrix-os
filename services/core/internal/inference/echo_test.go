package inference

import (
	"context"
	"errors"
	"testing"
)

// TestEchoBackend_Deterministic asserts the echo backend returns a deterministic
// completion and unit count for a given prompt: identical inputs always yield
// identical outputs, which is what lets the marketplace price stub inference.
func TestEchoBackend_Deterministic(t *testing.T) {
	b := NewEchoBackend()
	if b.Name() != "echo" {
		t.Fatalf("expected name echo, got %q", b.Name())
	}

	req := InferenceRequest{Model: "test-model", Prompt: "hello there world"}

	first, err := b.Infer(context.Background(), req)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}

	// The completion is the prefix followed by the flattened "user: <prompt>".
	wantCompletion := "echo: user: hello there world"
	if first.Completion != wantCompletion {
		t.Fatalf("completion = %q, want %q", first.Completion, wantCompletion)
	}

	// Prompt: "user: hello there world" -> 4 words.
	if first.Usage.PromptTokens != 4 {
		t.Fatalf("prompt tokens = %d, want 4", first.Usage.PromptTokens)
	}
	// Completion: "echo: user: hello there world" -> 5 words.
	if first.Usage.CompletionTokens != 5 {
		t.Fatalf("completion tokens = %d, want 5", first.Usage.CompletionTokens)
	}
	if first.Usage.TotalTokens != 9 {
		t.Fatalf("total tokens = %d, want 9", first.Usage.TotalTokens)
	}
	if first.Units != 9 {
		t.Fatalf("units = %d, want 9", first.Units)
	}
	if first.Model != "test-model" {
		t.Fatalf("model = %q, want test-model", first.Model)
	}

	// Determinism: a second call with the same request matches exactly.
	second, err := b.Infer(context.Background(), req)
	if err != nil {
		t.Fatalf("Infer (second): %v", err)
	}
	if second.Completion != first.Completion || second.Units != first.Units {
		t.Fatalf("non-deterministic: %+v vs %+v", first, second)
	}
}

// TestEchoBackend_EmptyPrompt asserts an empty request is rejected.
func TestEchoBackend_EmptyPrompt(t *testing.T) {
	b := NewEchoBackend()
	if _, err := b.Infer(context.Background(), InferenceRequest{Model: "m"}); !errors.Is(err, ErrEmptyPrompt) {
		t.Fatalf("expected ErrEmptyPrompt, got %v", err)
	}
}

// TestEchoBackend_MessagesTakePrecedence asserts that explicit messages are used
// over a prompt and drive the flattened completion.
func TestEchoBackend_MessagesTakePrecedence(t *testing.T) {
	b := NewEchoBackend()
	req := InferenceRequest{
		Prompt: "ignored",
		Messages: []Message{
			{Role: RoleSystem, Content: "be terse"},
			{Role: RoleUser, Content: "hi"},
		},
	}
	resp, err := b.Infer(context.Background(), req)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	want := "echo: system: be terse\nuser: hi"
	if resp.Completion != want {
		t.Fatalf("completion = %q, want %q", resp.Completion, want)
	}
}

// TestUnitsFor asserts the pricing floor and the total-token derivation.
func TestUnitsFor(t *testing.T) {
	if got := UnitsFor(Usage{}); got != 1 {
		t.Fatalf("empty usage units = %d, want floor 1", got)
	}
	if got := UnitsFor(Usage{PromptTokens: 3, CompletionTokens: 4}); got != 7 {
		t.Fatalf("units = %d, want 7 (derived from prompt+completion)", got)
	}
	if got := UnitsFor(Usage{TotalTokens: 42}); got != 42 {
		t.Fatalf("units = %d, want 42", got)
	}
}
