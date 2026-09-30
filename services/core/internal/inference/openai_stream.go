package inference

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
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
			// Both spellings, for the same reason chatMessage carries both: a
			// server emits one or the other and reading only one loses the
			// working. This path read NEITHER, so a streamed reasoning job
			// delivered no working at all and was billed as if it had produced
			// only its answer.
			Reasoning        string `json:"reasoning"`
			ReasoningContent string `json:"reasoning_content"`
			// ToolCalls arrive in PIECES. A server sends the id and the name in
			// one frame and then the arguments a few characters at a time across
			// many more, each fragment tagged with an index saying which call it
			// belongs to. See accumulateToolCalls for why that index, and not
			// arrival order, is what stitches them back together.
			ToolCalls []wireToolCall `json:"tool_calls"`
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
func (b *OpenAIBackend) InferStream(ctx context.Context, req InferenceRequest, onChunk ChunkFunc, onWorking WorkingFunc) (InferenceResponse, error) {
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
			Tools:       toWireTools(req.Tools),
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
		reasoning  strings.Builder
		model      = req.Model
		usage      *Usage
		calls      toolCallAccumulator
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
			// The working accumulates but is NOT forwarded to onChunk. onChunk is
			// the buyer's completion stream, and interleaving reasoning into it
			// would make the assembled text neither the answer nor the working -
			// and that text is what the receipt's digest commits to. The working
			// is delivered whole, on the settled job, exactly as it is on the
			// non-streaming path.
			//
			// Its SIZE does travel, through onWorking, because a reasoning model
			// spends most of a run here and a progress signal that ignores it
			// reports nothing until the thinking is already over. A count is not
			// the text: nothing a caller can forward by mistake.
			if r := choice.Delta.Reasoning; r != "" {
				reasoning.WriteString(r)
				if err := reportWorking(onWorking, countTokens(r)); err != nil {
					return InferenceResponse{}, err
				}
			} else if r := choice.Delta.ReasoningContent; r != "" {
				reasoning.WriteString(r)
				if err := reportWorking(onWorking, countTokens(r)); err != nil {
					return InferenceResponse{}, err
				}
			}

			if len(choice.Delta.ToolCalls) > 0 {
				calls.add(choice.Delta.ToolCalls)
			}

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
	working := reasoning.String()
	toolCalls := calls.result()
	// A completion with no text is ordinarily a failed run - but a model that chose
	// to CALL something rather than answer emits exactly that, and it is a
	// successful turn. Refusing it here was the one thing that would have made tool
	// calls arrive as ErrNoCompletion, which reads as a provider fault.
	if text == "" && len(toolCalls) == 0 {
		return InferenceResponse{}, ErrNoCompletion
	}

	if usage == nil {
		// The vendor did not report it. Derive it, deterministically, from the
		// text both sides hold. See the doc comment above for why this is
		// acceptable and what it costs.
		// The working counts: those tokens were generated and they settle. A
		// derivation that ignored them would under-report a reasoning model by
		// most of what it did.
		//
		// A tool call counts too, for the same reason: the model generated the name
		// and the argument JSON, and a fallback that ignored them would bill a
		// tool-calling turn as if it had produced nothing.
		promptTokens := countTokens(promptText(msgs))
		completionTokens := countTokens(text) + countTokens(working) + countTokens(toolCallText(toolCalls))
		usage = &Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		}
	}

	return InferenceResponse{
		Model:      model,
		Completion: text,
		Reasoning:  working,
		ToolCalls:  toolCalls,
		Usage:      *usage,
		Units:      UnitsFor(*usage),
	}, nil
}

// toolCallAccumulator stitches streamed tool-call fragments back into whole calls.
//
// WHY AN INDEX AND NOT APPEND. The fragments of one call arrive across many frames
// and the fragments of two parallel calls are interleaved, so arrival order says
// nothing about which call a fragment belongs to - only the index does. Appending
// in arrival order produced one call per frame, each holding a few characters of
// somebody's argument JSON.
//
// The index is also allowed to be ABSENT: a server that sends one call at a time
// may omit it, and there the fragments all belong to the call already in progress.
// Treating a missing index as zero is what makes that case work rather than
// starting a new call on every frame.
type toolCallAccumulator struct {
	order []int
	byIdx map[int]*ToolCall
}

func (a *toolCallAccumulator) add(frags []wireToolCall) {
	if a.byIdx == nil {
		a.byIdx = make(map[int]*ToolCall)
	}
	for _, f := range frags {
		idx := 0
		if f.Index != nil {
			idx = *f.Index
		}
		cur, ok := a.byIdx[idx]
		if !ok {
			cur = &ToolCall{}
			a.byIdx[idx] = cur
			a.order = append(a.order, idx)
		}
		// Identity fields arrive once and are not appended; only the arguments are
		// a stream. A later frame repeating the id must not double it.
		if f.ID != "" {
			cur.ID = f.ID
		}
		if f.Function.Name != "" {
			cur.Name = f.Function.Name
		}
		cur.Arguments += f.Function.Arguments
	}
}

// result returns the completed calls in index order, dropping any that never
// received a name - see fromWireToolCalls for why a nameless call is not passed on.
func (a *toolCallAccumulator) result() []ToolCall {
	if len(a.order) == 0 {
		return nil
	}
	sorted := append([]int(nil), a.order...)
	sort.Ints(sorted)
	out := make([]ToolCall, 0, len(sorted))
	for _, idx := range sorted {
		c := a.byIdx[idx]
		if c == nil || c.Name == "" {
			continue
		}
		out = append(out, *c)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// toolCallText is the generated text of a set of calls, for the local usage
// fallback only. It is never sent anywhere.
func toolCallText(calls []ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	var b strings.Builder
	for _, c := range calls {
		b.WriteString(c.Name)
		b.WriteString(c.Arguments)
	}
	return b.String()
}
