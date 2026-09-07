package inference

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// This file makes the provider-API backend stream for real, by asking the
// upstream to stream and forwarding its deltas as they arrive. It is the one
// backend where streaming is worth anything: the upstream is where the latency
// is, so a caller sees the first token in roughly the time the model takes to
// produce it rather than the time it takes to finish.

// streamRequest adds the streaming fields to the upstream request.
type streamRequest struct {
	chatCompletionRequest
	Stream bool `json:"stream"`
	// StreamOptions asks OpenAI to send a final chunk carrying the usage, which
	// a stream otherwise omits. Vendors that do not know the field ignore it; see
	// InferStream for what happens when the usage never arrives.
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// streamChunk is the subset of an SSE `data:` frame we read.
type streamChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// maxStreamLineBytes bounds one SSE line. A frame is a small JSON object; a cap
// stops a hostile or broken upstream from making the node buffer without limit.
const maxStreamLineBytes = 1 << 20

// InferStream streams the completion from the upstream provider, forwarding each
// delta to onChunk, and returns the assembled response.
//
// USAGE IS THE HARD PART, because usage is what settles. A streamed
// chat-completions response carries no usage unless the vendor honours
// stream_options.include_usage, and not every OpenAI-compatible vendor does. So
// when the final usage never arrives it is derived locally with the same
// countTokens the stub uses, from the prompt and the completion we just
// assembled. That is a documented approximation of somebody else's tokeniser, it
// is deterministic given the same text, and it is the same basis the buyer can
// recompute from the prompt and completion they hold - which is what makes an
// inflated bill detectable rather than a matter of trust. A vendor that does
// report usage is always preferred.
func (b *OpenAIBackend) InferStream(ctx context.Context, req InferenceRequest, onChunk ChunkFunc) (InferenceResponse, error) {
	msgs, err := req.EffectiveMessages()
	if err != nil {
		return InferenceResponse{}, err
	}

	wire := streamRequest{
		chatCompletionRequest: chatCompletionRequest{
			Model:       req.Model,
			Messages:    toWireMessages(msgs),
			MaxTokens:   req.MaxTokens,
			Temperature: req.Temperature,
		},
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return InferenceResponse{}, fmt.Errorf("inference: marshal streaming request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return InferenceResponse{}, fmt.Errorf("inference: build streaming request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+b.apiKey)

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return InferenceResponse{}, fmt.Errorf("inference: provider streaming request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return InferenceResponse{}, fmt.Errorf("%w: status %d: %s",
			ErrProviderStatus, resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var (
		completion strings.Builder
		model      = req.Model
		usage      *Usage
	)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64<<10), maxStreamLineBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Blank lines separate frames and a `:` line is a comment keep-alive.
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		payload, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		payload = strings.TrimSpace(payload)
		if payload == "[DONE]" {
			break
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			// One malformed frame is not a reason to throw away a completion that
			// is otherwise arriving fine, and a vendor that sends something we do
			// not model should not break the request.
			continue
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Usage != nil {
			usage = &Usage{
				PromptTokens:     chunk.Usage.PromptTokens,
				CompletionTokens: chunk.Usage.CompletionTokens,
				TotalTokens:      chunk.Usage.TotalTokens,
			}
		}
		for _, choice := range chunk.Choices {
			delta := choice.Delta.Content
			if delta == "" {
				continue
			}
			completion.WriteString(delta)
			if err := onChunk(delta); err != nil {
				return InferenceResponse{}, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return InferenceResponse{}, fmt.Errorf("inference: read provider stream: %w", err)
	}

	text := completion.String()
	if text == "" {
		return InferenceResponse{}, ErrNoCompletion
	}

	if usage == nil {
		// The vendor did not report it. Derive it, deterministically, from the
		// text both sides hold. See the doc comment above for why this is
		// acceptable and what it costs.
		promptTokens := countTokens(promptText(msgs))
		completionTokens := countTokens(text)
		usage = &Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		}
	}

	return InferenceResponse{
		Model:      model,
		Completion: text,
		Usage:      *usage,
		Units:      UnitsFor(*usage),
	}, nil
}
