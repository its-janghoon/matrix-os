package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A streamed tool call arrives in PIECES: the id and the name in one frame, then the
// argument JSON a few characters at a time across many more, each fragment tagged with
// an index. Arrival order says nothing about which call a fragment belongs to when two
// are in flight - only the index does - so appending in arrival order produced one
// call per frame, each holding a few characters of somebody's arguments.

func sseServer(t *testing.T, frames []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

// toolBackend is an OpenAI backend pointed at a stub upstream. Named to avoid the
// package's existing streamBackend, which is the fallback wrapper and a different
// thing entirely.
func toolBackend(t *testing.T, url string) *OpenAIBackend {
	t.Helper()
	t.Setenv("MATRIX_TOOLTEST_KEY", "k")
	b, err := NewOpenAIBackend(OpenAIConfig{BaseURL: url, APIKeyEnv: "MATRIX_TOOLTEST_KEY"})
	if err != nil {
		t.Fatalf("backend: %v", err)
	}
	return b
}

func frag(index int, id, name, args string) string {
	call := map[string]any{"index": index}
	if id != "" {
		call["id"] = id
	}
	fn := map[string]any{}
	if name != "" {
		fn["name"] = name
	}
	if args != "" {
		fn["arguments"] = args
	}
	call["function"] = fn
	body, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{call}}}},
	})
	return string(body)
}

func TestAStreamedToolCallIsStitchedBackTogether(t *testing.T) {
	srv := sseServer(t, []string{
		frag(0, "call_abc", "web_search", ""),
		frag(0, "", "", `{"que`),
		frag(0, "", "", `ry":"seo`),
		frag(0, "", "", `ul"}`),
	})
	defer srv.Close()

	resp, err := toolBackend(t, srv.URL).InferStream(context.Background(),
		InferenceRequest{Prompt: "weather?"}, func(string) error { return nil }, nil)
	if err != nil {
		t.Fatalf("InferStream: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("got %d calls, want 1 stitched from 4 frames: %+v", len(resp.ToolCalls), resp.ToolCalls)
	}
	got := resp.ToolCalls[0]
	if got.ID != "call_abc" || got.Name != "web_search" {
		t.Errorf("identity fields wrong: %+v", got)
	}
	if got.Arguments != `{"query":"seoul"}` {
		t.Errorf("arguments = %q, want the concatenation of every fragment", got.Arguments)
	}
}

func TestTwoInterleavedCallsAreKeptApartByIndex(t *testing.T) {
	// The case arrival order gets wrong. Both calls are in flight and their fragments
	// alternate; only the index says which is which.
	srv := sseServer(t, []string{
		frag(0, "c0", "alpha", ""),
		frag(1, "c1", "beta", ""),
		frag(0, "", "", `{"a":`),
		frag(1, "", "", `{"b":`),
		frag(0, "", "", `1}`),
		frag(1, "", "", `2}`),
	})
	defer srv.Close()

	resp, err := toolBackend(t, srv.URL).InferStream(context.Background(),
		InferenceRequest{Prompt: "x"}, func(string) error { return nil }, nil)
	if err != nil {
		t.Fatalf("InferStream: %v", err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("got %d calls, want 2: %+v", len(resp.ToolCalls), resp.ToolCalls)
	}
	// Index order, not arrival order.
	if resp.ToolCalls[0].Name != "alpha" || resp.ToolCalls[0].Arguments != `{"a":1}` {
		t.Errorf("first call wrong: %+v", resp.ToolCalls[0])
	}
	if resp.ToolCalls[1].Name != "beta" || resp.ToolCalls[1].Arguments != `{"b":2}` {
		t.Errorf("second call wrong: %+v", resp.ToolCalls[1])
	}
}

func TestAMissingIndexMeansTheCallAlreadyInProgress(t *testing.T) {
	// A server sending one call at a time may omit the index. Treating a missing index
	// as a new call would start a fresh one on every frame.
	srv := sseServer(t, []string{
		frag(0, "c0", "t", ""),
		strings.ReplaceAll(frag(0, "", "", `{"a":1}`), `"index":0,`, ""),
	})
	defer srv.Close()

	resp, err := toolBackend(t, srv.URL).InferStream(context.Background(),
		InferenceRequest{Prompt: "x"}, func(string) error { return nil }, nil)
	if err != nil {
		t.Fatalf("InferStream: %v", err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Arguments != `{"a":1}` {
		t.Fatalf("an index-less fragment started a new call: %+v", resp.ToolCalls)
	}
}

func TestACallOnlyCompletionIsNotAnError(t *testing.T) {
	// A model that chose to ACT rather than speak emits no text. That used to come back
	// as ErrNoCompletion, which reads as a provider fault and is the one thing that
	// would have made tool calls unusable.
	srv := sseServer(t, []string{frag(0, "c0", "t", `{}`)})
	defer srv.Close()

	resp, err := toolBackend(t, srv.URL).InferStream(context.Background(),
		InferenceRequest{Prompt: "x"}, func(string) error { return nil }, nil)
	if err != nil {
		t.Fatalf("a tool-call-only turn was refused: %v", err)
	}
	if resp.Completion != "" {
		t.Errorf("completion = %q, want empty", resp.Completion)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("want the call: %+v", resp.ToolCalls)
	}
	// And it is billed, because the model generated the name and the arguments. A
	// fallback that ignored them would charge nothing for a turn that did work.
	if resp.Usage.CompletionTokens == 0 {
		t.Error("a tool-call-only turn was billed as if it had produced nothing")
	}
}

func TestAStreamWithNeitherTextNorCallsIsStillAnError(t *testing.T) {
	srv := sseServer(t, []string{`{"choices":[{"delta":{}}]}`})
	defer srv.Close()

	_, err := toolBackend(t, srv.URL).InferStream(context.Background(),
		InferenceRequest{Prompt: "x"}, func(string) error { return nil }, nil)
	if err == nil {
		t.Error("an empty stream was accepted, so a provider that produced nothing would settle")
	}
}

func TestANamelessCallIsDropped(t *testing.T) {
	// A client asked to run a nameless tool can only fail; carrying it forward turns a
	// malformed model server into a client error the buyer cannot act on.
	srv := sseServer(t, []string{
		frag(0, "c0", "", `{}`),
		`{"choices":[{"delta":{"content":"hi"}}]}`,
	})
	defer srv.Close()

	resp, err := toolBackend(t, srv.URL).InferStream(context.Background(),
		InferenceRequest{Prompt: "x"}, func(string) error { return nil }, nil)
	if err != nil {
		t.Fatalf("InferStream: %v", err)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("a nameless call survived: %+v", resp.ToolCalls)
	}
	if resp.Completion != "hi" {
		t.Errorf("completion = %q", resp.Completion)
	}
}

// The tools have to reach the model server, or none of the above ever happens.
func TestToolsAreSentUpstreamInTheOpenAIShape(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}],`+
			`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer srv.Close()

	_, err := toolBackend(t, srv.URL).Infer(context.Background(), InferenceRequest{
		Prompt: "x",
		Tools: []ToolDefinition{{
			Name:        "web_search",
			Description: "Search.",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		}},
	})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools not sent upstream: %v", body["tools"])
	}
	first, _ := tools[0].(map[string]any)
	if first["type"] != "function" {
		t.Errorf(`type = %v, want "function"`, first["type"])
	}
	fn, _ := first["function"].(map[string]any)
	if fn["name"] != "web_search" {
		t.Errorf("name = %v", fn["name"])
	}
	if _, isObject := fn["parameters"].(map[string]any); !isObject {
		t.Errorf("parameters were not sent as a JSON object: %#v", fn["parameters"])
	}
}

func TestAToolResultTurnIsSentWithItsCallID(t *testing.T) {
	// Iteration two. Without the id the model cannot pair the result with the call, and
	// without the assistant turn it cannot see the call at all.
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"sunny"}}],`+
			`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer srv.Close()

	_, err := toolBackend(t, srv.URL).Infer(context.Background(), InferenceRequest{
		Messages: []Message{
			{Role: RoleUser, Content: "weather?"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "web_search", Arguments: `{"query":"seoul"}`}}},
			{Role: RoleTool, ToolCallID: "c1", Content: "sunny, 26"},
		},
	})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("got %d messages upstream, want 3", len(msgs))
	}
	assistant, _ := msgs[1].(map[string]any)
	calls, ok := assistant["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("the assistant's own call was not replayed: %#v", assistant)
	}
	toolMsg, _ := msgs[2].(map[string]any)
	if toolMsg["tool_call_id"] != "c1" {
		t.Errorf("tool_call_id = %v, want c1", toolMsg["tool_call_id"])
	}
}
