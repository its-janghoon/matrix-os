package inference

import (
	"context"
	"strings"
	"testing"
)

// A reasoning model spends most of its tokens on working the buyer never saw.
//
// The overbilling ceiling bounds a charge by the text that crossed the wire,
// which is what stops a provider claiming a whole reservation for a one-word
// answer. On a reasoning model that bound was wrong in the other direction: the
// first live sale on this network reported 21 prompt and 843 completion tokens
// for one sentence of answer, and settled 241 units - 28% of the work.
//
// The fix is not to trust the count. It is to DELIVER the reasoning, so the
// buyer can see what they are paying for and the ceiling can honestly include
// it. A charge for tokens the buyer never receives is one they cannot check.

// reasoningBackend answers with a short conclusion and a long body of working,
// the shape a reasoning model actually produces.
type reasoningBackend struct {
	answer, working string
}

func (b reasoningBackend) Name() string { return "reasoning" }

func (b reasoningBackend) Infer(context.Context, InferenceRequest) (InferenceResponse, error) {
	u := Usage{PromptTokens: 21, CompletionTokens: 843, TotalTokens: 864}
	return InferenceResponse{
		Model:      "m",
		Completion: b.answer,
		Reasoning:  b.working,
		Usage:      u,
		Units:      UnitsFor(u),
	}, nil
}

func TestDeliveredReasoningRaisesTheBillingCeiling(t *testing.T) {
	req := InferenceRequest{Prompt: "hi", Model: "m"}
	const answer = "A Merkle root summarises a tree of hashes."
	working := strings.Repeat("considering the alternatives, ", 200)

	without := MaxUnitsFor(req, answer, "")
	with := MaxUnitsFor(req, answer, working)

	if with <= without {
		t.Fatalf("ceiling with reasoning %d must exceed %d without it - the working was delivered", with, without)
	}
	t.Logf("ceiling: %d without the working, %d with it", without, with)
}

func TestAReasoningModelsWorkingReachesTheBuyer(t *testing.T) {
	const answer = "A Merkle root summarises a tree of hashes."
	working := strings.Repeat("step. ", 400)

	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := serviceWithBackend(t,
		reasoningBackend{answer: answer, working: working}, fs, 1, 10_000_000)

	job, err := svc.SubmitInferenceJob(buyer, provider, InferenceRequest{Prompt: "hi", Model: "m"}, 2000)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	done, err := svc.FulfillJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("FulfillJob: %v", err)
	}
	if done.Reasoning != working {
		t.Fatalf("the buyer received %d bytes of reasoning, want %d - it is billed, so it has to be delivered",
			len(done.Reasoning), len(working))
	}
	if done.Completion != answer {
		t.Fatalf("completion = %q, want %q", done.Completion, answer)
	}
}

// The receipt has to cover the reasoning, or a seller could bill for one body of
// working and hand over another with the signature still verifying.
func TestTheReceiptDigestCoversTheReasoning(t *testing.T) {
	req := InferenceRequest{Prompt: "hi", Model: "m"}
	a := ExchangeDigest(req, "answer", "one line of working")
	b := ExchangeDigest(req, "answer", "a different line of working")
	if string(a) == string(b) {
		t.Fatal("two different bodies of reasoning produced the same digest")
	}
	if string(a) == string(ExchangeDigest(req, "answer", "")) {
		t.Fatal("reasoning is not in the digest at all")
	}
}
