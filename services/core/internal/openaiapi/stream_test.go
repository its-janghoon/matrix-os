package openaiapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// frames splits an SSE body into its `data:` payloads, in order.
func frames(t *testing.T, body string) []string {
	t.Helper()
	var out []string
	for _, block := range strings.Split(body, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		payload, ok := strings.CutPrefix(block, "data: ")
		if !ok {
			t.Fatalf("block %q is not an SSE data frame", block)
		}
		out = append(out, payload)
	}
	return out
}

func streamRequest(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, ChatCompletionsPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestAStreamIsSSEFramesTerminatedByDone pins the wire format, because every
// client library parses exactly this and a subtle deviation makes them hang or
// report a parse error rather than fall back.
func TestAStreamIsSSEFramesTerminatedByDone(t *testing.T) {
	h := newHandler(t, &fakeInference{}, twoProviders(), fakeAuth{account: "buyer"})

	rec := streamRequest(t, h,
		`{"model":"llama-3.3-70b","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	// Named for the proxies that buffer text/event-stream by default and would
	// otherwise hold every frame until the response closed.
	if rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Error("X-Accel-Buffering: no is missing, so a buffering proxy would defeat the stream")
	}

	got := frames(t, rec.Body.String())
	if len(got) < 3 {
		t.Fatalf("got %d frames, want at least an open, a delta and [DONE]: %v", len(got), got)
	}
	if got[len(got)-1] != "[DONE]" {
		t.Fatalf("last frame = %q, want [DONE]: a client waiting for it would hang", got[len(got)-1])
	}
}

func TestTheFirstFrameOpensAnAssistantMessage(t *testing.T) {
	h := newHandler(t, &fakeInference{}, twoProviders(), fakeAuth{account: "buyer"})

	rec := streamRequest(t, h,
		`{"model":"llama-3.3-70b","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	var first struct {
		Object  string `json:"object"`
		Choices []struct {
			Delta struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(frames(t, rec.Body.String())[0]), &first); err != nil {
		t.Fatalf("first frame is not JSON: %v", err)
	}
	if first.Object != "chat.completion.chunk" {
		t.Errorf("object = %q, want chat.completion.chunk", first.Object)
	}
	// Role first with no content is what OpenAI sends and what clients rely on
	// to open an assistant message.
	if first.Choices[0].Delta.Role != "assistant" || first.Choices[0].Delta.Content != "" {
		t.Errorf("first delta = %+v, want the role alone", first.Choices[0].Delta)
	}
}

// TestTheDeltasReassembleIntoTheCompletion: a stream whose pieces do not add up
// to what was billed for is worse than no stream.
func TestTheDeltasReassembleIntoTheCompletion(t *testing.T) {
	h := newHandler(t, &fakeInference{}, twoProviders(), fakeAuth{account: "buyer"})

	rec := streamRequest(t, h,
		`{"model":"llama-3.3-70b","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	var assembled strings.Builder
	for _, f := range frames(t, rec.Body.String()) {
		if f == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct{ Content string } `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(f), &chunk); err != nil {
			t.Fatalf("frame %q is not JSON: %v", f, err)
		}
		for _, c := range chunk.Choices {
			assembled.WriteString(c.Delta.Content)
		}
	}
	// The fake's canned completion.
	if assembled.String() != "hello back" {
		t.Fatalf("assembled = %q, want the whole completion", assembled.String())
	}
}

func TestTheStreamEndsWithAStopAndTheUsage(t *testing.T) {
	h := newHandler(t, &fakeInference{}, twoProviders(), fakeAuth{account: "buyer"})

	rec := streamRequest(t, h,
		`{"model":"llama-3.3-70b","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	got := frames(t, rec.Body.String())
	// [DONE], the usage frame, then the stop frame.
	var usage struct {
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal([]byte(got[len(got)-2]), &usage); err != nil {
		t.Fatalf("the penultimate frame is not JSON: %v", err)
	}
	if usage.Usage == nil || usage.Usage.TotalTokens != 12 {
		t.Fatalf("usage = %+v, want the backend's reported counts", usage.Usage)
	}
	if usage.Provider == "" {
		t.Error("the final frame should name who served it")
	}

	var stop struct {
		Choices []struct {
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(got[len(got)-3]), &stop); err != nil {
		t.Fatalf("the stop frame is not JSON: %v", err)
	}
	if len(stop.Choices) != 1 || stop.Choices[0].FinishReason == nil || *stop.Choices[0].FinishReason != "stop" {
		t.Fatalf("stop frame = %+v, want finish_reason stop", stop.Choices)
	}
}

// TestAOneShotBackendIsReportedAsSuch: "this provider cannot stream, so you got
// one big frame" is something a UI should be able to know rather than infer.
func TestAOneShotBackendIsReportedAsSuch(t *testing.T) {
	h := newHandler(t, &fakeInference{oneShot: true}, twoProviders(), fakeAuth{account: "buyer"})

	rec := streamRequest(t, h,
		`{"model":"llama-3.3-70b","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	got := frames(t, rec.Body.String())
	var final struct {
		StreamedOneShot bool `json:"streamed_one_shot"`
	}
	if err := json.Unmarshal([]byte(got[len(got)-2]), &final); err != nil {
		t.Fatalf("final frame is not JSON: %v", err)
	}
	if !final.StreamedOneShot {
		t.Fatal("a one-shot fallback must say so on the final frame")
	}
}

// TestAFailureMidStreamTravelsInTheBody: the status is already 200 by then, so
// this is the only place a failure can go, and a stream that merely stopped
// would look complete.
func TestAFailureMidStreamTravelsInTheBody(t *testing.T) {
	h := newHandler(t, &fakeInference{streamErr: errStreamBroke}, twoProviders(), fakeAuth{account: "buyer"})

	rec := streamRequest(t, h,
		`{"model":"llama-3.3-70b","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: the headers went out before the failure", rec.Code)
	}
	got := frames(t, rec.Body.String())
	if got[len(got)-1] != "[DONE]" {
		t.Fatal("a failed stream must still terminate, or a client waiting for [DONE] hangs")
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(got[len(got)-2]), &env); err != nil {
		t.Fatalf("the failure frame is not the OpenAI error envelope: %v", err)
	}
	if env.Error.Message == "" || env.Error.Type == "" {
		t.Fatalf("error = %+v, want a typed, non-empty message", env.Error)
	}
}

// TestAStreamedRequestStillNeedsACredential: streaming must not be a way around
// the check that decides whose balance is charged.
func TestAStreamedRequestStillNeedsACredential(t *testing.T) {
	inf := &fakeInference{}
	h := newHandler(t, inf, twoProviders(), fakeAuth{account: ""})

	rec := streamRequest(t, h,
		`{"model":"llama-3.3-70b","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if len(inf.submitted) != 0 {
		t.Fatal("a refused streaming request must not reserve capacity")
	}
}

// TestAnUnservedModelIsRefusedBeforeTheStreamOpens: while nothing has been
// written the route can still answer with a real status code, which is far more
// useful to a client than a 200 whose body says 404.
func TestAnUnservedModelIsRefusedBeforeTheStreamOpens(t *testing.T) {
	h := newHandler(t, &fakeInference{}, twoProviders(), fakeAuth{account: "buyer"})

	rec := streamRequest(t, h,
		`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
