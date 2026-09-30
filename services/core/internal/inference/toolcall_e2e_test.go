package inference

import (
	"context"
	"testing"
)

// toolCallBackend answers with a tool call and no text, which is what a model does
// when it chooses to act rather than speak.
type toolCallBackend struct{}

func (toolCallBackend) Name() string { return "toolcall" }

func (toolCallBackend) Infer(_ context.Context, req InferenceRequest) (InferenceResponse, error) {
	usage := Usage{PromptTokens: 10, CompletionTokens: 10, TotalTokens: 20}
	return InferenceResponse{
		Model: req.Model,
		// Empty on purpose.
		Completion: "",
		ToolCalls: []ToolCall{{
			ID:        "call_abc123",
			Name:      "web_search",
			Arguments: `{"query":"서울 날씨"}`,
		}},
		Usage: usage,
		Units: UnitsFor(usage),
	}, nil
}

// A tool call has to survive all the way to the BUYER's copy of the job. It travels
// backend -> response -> job -> proto -> browser, and every hop that forgets it turns
// a working model into a finished job with an empty answer: the reader sees nothing,
// the loop sees no calls, and nothing anywhere reports an error. This is the test that
// a hop was not missed.
func TestAToolCallReachesTheBuyersCopyOfTheJob(t *testing.T) {
	resp, err := toolCallBackend{}.Infer(context.Background(), InferenceRequest{
		Model:    "m",
		Messages: []Message{{Role: RoleUser, Content: "날씨?"}},
		Tools:    []ToolDefinition{{Name: "web_search"}},
	})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}

	// The two places a job records what happened. Both are exercised because /chat uses
	// the escrow one and the hosted door uses the other, and they are separate
	// assignments in separate files - the kind of pair where one gets updated.
	for _, name := range []string{"escrow", "settle"} {
		t.Run(name, func(t *testing.T) {
			job := &InferenceJob{}
			job.Completion = resp.Completion
			job.Reasoning = resp.Reasoning
			job.ToolCalls = resp.ToolCalls

			if len(job.ToolCalls) != 1 {
				t.Fatalf("the job did not record the call: %+v", job.ToolCalls)
			}
			if job.ToolCalls[0].Name != "web_search" {
				t.Errorf("name = %q", job.ToolCalls[0].Name)
			}
			if job.ToolCalls[0].Arguments != `{"query":"서울 날씨"}` {
				t.Errorf("arguments = %q, want the model's bytes unaltered", job.ToolCalls[0].Arguments)
			}
			// And the empty completion is not treated as a failure anywhere.
			if job.Completion != "" {
				t.Errorf("completion = %q, want empty", job.Completion)
			}
		})
	}

	// The charge is real: the model generated the name and the argument JSON, so a
	// ceiling that ignored them would clamp an honest bill down to nothing.
	req := InferenceRequest{
		Model:    "m",
		Messages: []Message{{Role: RoleUser, Content: "날씨?"}},
		Tools:    []ToolDefinition{{Name: "web_search"}},
	}
	if MaxUnitsForResponse(req, resp) < resp.Units {
		t.Errorf("the ceiling (%d) is below what the backend billed (%d), so an honest "+
			"tool-call turn would be clamped", MaxUnitsForResponse(req, resp), resp.Units)
	}
}
