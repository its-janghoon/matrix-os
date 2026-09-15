package inference

import (
	"context"
	"strings"
	"testing"
)

// Withholding the completion is the ONLY enforcement on the client-signed path.
// The provider has already done the work by the time the invoice exists, so the
// text is the only thing left to hold.
//
// It leaked twice, both times through the same mistake: the withholding lived in
// the CALLERS. RunInferenceJob blanked the completion by hand and
// GetInferenceJob did not - and a Get is public when connect.public_reads is on,
// which a self-custody page requires because a browser cannot hold an API key.
// So a buyer could run a job, take the id it was handed, ask for the job instead
// of signing for it, and read what it never paid for.
//
// Then a field was added. A reasoning model's working became part of the job,
// and the caller that stripped Completion knew nothing of Reasoning - which for
// this kind of model is most of what is billed and often contains the answer
// worked out in full.
//
// So these test the SERVICE accessor rather than any one handler. A strip
// repeated per caller is a list that grows wrong.

// reasoningWorker answers with a short conclusion and long working, the shape
// that made the second leak expensive.
type reasoningWorker struct{}

func (reasoningWorker) Name() string { return "reasoning-worker" }

func (reasoningWorker) Infer(context.Context, InferenceRequest) (InferenceResponse, error) {
	u := Usage{PromptTokens: 10, CompletionTokens: 900, TotalTokens: 910}
	return InferenceResponse{
		Model:      "m",
		Completion: "Yes, 8191 is prime.",
		Reasoning:  strings.Repeat("trial division rules out 53 and 79. ", 40),
		Usage:      u,
		Units:      UnitsFor(u),
	}, nil
}

func TestAJobAwaitingPaymentHandsOverNothing(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 10_000_000)
	svc.registry.Register(provider, reasoningWorker{})

	pr, err := svc.RunUnsettled(context.Background(), buyer.AccountID(), provider,
		InferenceRequest{Prompt: "is 8191 prime"}, 100_000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}

	// This is the request a buyer makes INSTEAD of signing. It is the public
	// read that used to answer it in full.
	job, ok := svc.GetJob(pr.JobID)
	if !ok {
		t.Fatal("the job vanished after running")
	}
	if job.Status != InferenceJobAwaitingPayment {
		t.Fatalf("status = %s, want awaiting_payment", job.Status)
	}
	if job.Completion != "" {
		t.Fatalf("the completion was handed over before payment: %q", job.Completion)
	}
	if job.Reasoning != "" {
		t.Fatalf("the working was handed over before payment: %d chars", len(job.Reasoning))
	}

	// The charge still reflects the whole of what was produced. Withholding the
	// text must not quietly reduce the bill - the provider did the work.
	if pr.Amount == 0 {
		t.Fatal("nothing was charged for work that was done")
	}
	if pr.Usage.CompletionTokens != 900 {
		t.Fatalf("usage reports %d completion tokens, want 900", pr.Usage.CompletionTokens)
	}
}

func TestPayingReleasesBothTheAnswerAndTheWorking(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 10_000_000)
	svc.registry.Register(provider, reasoningWorker{})

	pr, err := svc.RunUnsettled(context.Background(), buyer.AccountID(), provider,
		InferenceRequest{Prompt: "is 8191 prime"}, 100_000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}

	settled, err := svc.SettleSigned(context.Background(), pr.JobID, signedFor(t, pr, buyer))
	if err != nil {
		t.Fatalf("SettleSigned: %v", err)
	}
	if settled.Completion == "" {
		t.Fatal("the completion was still withheld after payment")
	}
	if settled.Reasoning == "" {
		t.Fatal("the working was still withheld after payment, though it was billed")
	}

	// And the accessor agrees, because a buyer who paid then asks for the job.
	job, ok := svc.GetJob(pr.JobID)
	if !ok {
		t.Fatal("the job vanished after settling")
	}
	if job.Completion == "" || job.Reasoning == "" {
		t.Fatal("a paid job must hand over everything it was paid for")
	}
}

// The hosted path never withholds: the node settles it itself, so there is
// nothing to hold and holding it would break every existing caller.
func TestTheHostedPathIsUnaffected(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyerID, provider := newTestService(t, fs, 3, 10_000_000)
	svc.registry.Register(provider, reasoningWorker{})

	job, err := svc.SubmitInferenceJob(buyerID, provider,
		InferenceRequest{Prompt: "is 8191 prime"}, 100_000)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	if _, err := svc.FulfillJob(context.Background(), job.ID); err != nil {
		t.Fatalf("FulfillJob: %v", err)
	}
	got, ok := svc.GetJob(job.ID)
	if !ok {
		t.Fatal("the job vanished after fulfilling")
	}
	if got.Completion == "" || got.Reasoning == "" {
		t.Fatal("the hosted path withheld something it settled for itself")
	}
}
