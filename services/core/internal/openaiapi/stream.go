package openaiapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/ecirlabs/matrix-core/internal/inference"
)

// This file answers `"stream": true` with the server-sent events an OpenAI
// client expects, instead of the refusal that used to be the only honest
// answer.
//
// The wire format is not JSON-over-HTTP: it is a sequence of
//
//	data: {"object":"chat.completion.chunk", ...}\n\n
//
// frames terminated by `data: [DONE]`, and every client library parses exactly
// that. Getting it subtly wrong is worse than not streaming, because the client
// hangs or reports a parse error rather than falling back.
//
// Two things this has to get right, and both are invisible in a test that only
// inspects the final bytes:
//
//   - FLUSH EVERY FRAME. Without it the frames sit in the response buffer until
//     the handler returns, which delivers the whole answer at once at the end.
//     That is the exact thing streaming exists to avoid, and it would still look
//     like a working stream to a client that buffers.
//   - A FAILURE AFTER THE FIRST FRAME CANNOT BE AN HTTP STATUS. The status is
//     already 200. So it travels as a final `data:` frame carrying an error
//     object, which is what OpenAI does and what a client can distinguish from a
//     complete answer.

// streamChatCompletions serves a streaming chat completion.
func (h *Handler) streamChatCompletions(
	w http.ResponseWriter,
	r *http.Request,
	buyer string,
	req chatRequest,
	msgs []inference.Message,
	provider string,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "server_error",
			"this server cannot stream: the response writer does not support flushing")
		return
	}

	job, err := h.cfg.Inference.SubmitInferenceJob(buyer, provider, inference.InferenceRequest{
		Model:       req.Model,
		Messages:    msgs,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}, estimateUnits(msgs, req.MaxTokens))
	if err != nil {
		// Nothing has been written yet, so this can still be a real status code.
		status, kind := classify(err)
		writeError(w, status, kind, err.Error())
		return
	}

	streamer, ok := h.cfg.Inference.(Streamer)
	if !ok {
		writeError(w, http.StatusInternalServerError, "server_error",
			"this server cannot stream: no streaming inference service is configured")
		return
	}

	// Headers first. After this the status is fixed at 200 and every failure
	// travels in the body.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// Named for the proxies that buffer text/event-stream by default and would
	// otherwise hold every frame until the response closed.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	id := "chatcmpl-" + job.ID
	created := h.cfg.Now().UTC().Unix()
	model := req.Model

	// The first frame carries the role and no content, which is what OpenAI
	// sends and what clients rely on to open an assistant message.
	writeSSE(w, flusher, chunkResponse{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []chunkChoice{{Index: 0, Delta: chunkDelta{Role: string(inference.RoleAssistant)}}},
	})

	onChunk := func(delta string) error {
		if delta == "" {
			return nil
		}
		if err := writeSSE(w, flusher, chunkResponse{
			ID:      id,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   model,
			Choices: []chunkChoice{{Index: 0, Delta: chunkDelta{Content: delta}}},
		}); err != nil {
			// The client is gone. Returning the error aborts the run so the
			// provider stops generating tokens nobody will read.
			return err
		}
		// Honour a client that hung up even when the write happened to succeed.
		return r.Context().Err()
	}

	done, result, err := streamer.StreamJob(r.Context(), job.ID, onChunk)
	if err != nil {
		writeSSEError(w, flusher, err)
		return
	}

	// The stop frame, then the usage frame OpenAI sends when asked for it, then
	// [DONE]. Usage last matches the upstream ordering, so a client accumulating
	// deltas has already finished by the time it arrives.
	writeSSE(w, flusher, chunkResponse{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []chunkChoice{{Index: 0, Delta: chunkDelta{}, FinishReason: strPtr("stop")}},
	})

	final := chunkResponse{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []chunkChoice{},
	}
	if done != nil {
		if done.Model != "" {
			final.Model = done.Model
		}
		final.Usage = &chatUsage{
			PromptTokens:     done.Usage.PromptTokens,
			CompletionTokens: done.Usage.CompletionTokens,
			TotalTokens:      done.Usage.TotalTokens,
		}
		final.Provider = done.Provider
	}
	if result != nil && result.StreamedOneShot {
		// Not part of OpenAI's schema, and an SDK ignores it. It is here because
		// "this provider's backend cannot stream, so you got one big frame" is
		// something a UI should be able to know rather than infer.
		final.StreamedOneShot = true
	}
	writeSSE(w, flusher, final)

	writeSSEDone(w, flusher)
}

// chunkResponse is one `chat.completion.chunk` frame.
type chunkResponse struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []chunkChoice `json:"choices"`
	Usage   *chatUsage    `json:"usage,omitempty"`
	// Marketplace extras an SDK ignores.
	Provider        string `json:"provider,omitempty"`
	StreamedOneShot bool   `json:"streamed_one_shot,omitempty"`
}

type chunkChoice struct {
	Index        int        `json:"index"`
	Delta        chunkDelta `json:"delta"`
	FinishReason *string    `json:"finish_reason"`
}

type chunkDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

func strPtr(s string) *string { return &s }

// writeSSE writes one frame and flushes it. The flush is the whole point; see
// the file comment.
func writeSSE(w http.ResponseWriter, flusher http.Flusher, frame chunkResponse) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("openaiapi: marshal stream frame: %w", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// writeSSEError reports a failure that happened after the response began. The
// status is already 200, so this is the only place it can go.
func writeSSEError(w http.ResponseWriter, flusher http.Flusher, err error) {
	_, kind := classify(err)
	payload, mErr := json.Marshal(errorEnvelope{
		Error: errorBody{Message: err.Error(), Type: kind},
	})
	if mErr != nil {
		payload = []byte(`{"error":{"message":"stream failed","type":"server_error"}}`)
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	flusher.Flush()
	// Still terminate the stream: a client waiting for [DONE] would otherwise
	// hang on a request that has already failed.
	writeSSEDone(w, flusher)
}

func writeSSEDone(w http.ResponseWriter, flusher http.Flusher) {
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	flusher.Flush()
}
