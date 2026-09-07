package inference

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// oneShotBackend can Infer but not stream, so the fallback wrapper is exercised
// by a real type rather than by a mock of the wrapper itself.
type oneShotBackend struct{ completion string }

func (b oneShotBackend) Name() string { return "one-shot" }

func (b oneShotBackend) Infer(context.Context, InferenceRequest) (InferenceResponse, error) {
	usage := Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}
	return InferenceResponse{
		Model:      "one-shot",
		Completion: b.completion,
		Usage:      usage,
		Units:      UnitsFor(usage),
	}, nil
}

// TestTheEchoBackendStreamsAndTheChunksReassemble is the property every consumer
// depends on: a stream whose pieces do not add up to what was billed for is
// worse than no stream.
func TestTheEchoBackendStreamsAndTheChunksReassemble(t *testing.T) {
	b := NewEchoBackend()
	var got strings.Builder
	chunks := 0

	resp, err := b.InferStream(context.Background(),
		InferenceRequest{Prompt: "one two three four"},
		func(delta string) error {
			chunks++
			got.WriteString(delta)
			return nil
		})
	if err != nil {
		t.Fatalf("InferStream: %v", err)
	}

	if got.String() != resp.Completion {
		t.Fatalf("chunks assembled to %q, want the completion %q", got.String(), resp.Completion)
	}
	if chunks < 2 {
		t.Fatalf("got %d chunks, want it actually split", chunks)
	}

	// And the response must be identical to the one-shot one, or what settles
	// would depend on which method a caller happened to use.
	oneShot, err := b.Infer(context.Background(), InferenceRequest{Prompt: "one two three four"})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if resp != oneShot {
		t.Fatalf("streamed response %+v differs from the one-shot %+v", resp, oneShot)
	}
}

// TestABackendThatCannotStreamStillProducesAStream: the fallback exists so no
// caller has to ask which backends support streaming and branch.
func TestABackendThatCannotStreamStillProducesAStream(t *testing.T) {
	var got strings.Builder
	chunks := 0

	result, err := streamBackend(context.Background(), oneShotBackend{completion: "whole answer"},
		InferenceRequest{Prompt: "hi"}, func(delta string) error {
			chunks++
			got.WriteString(delta)
			return nil
		})
	if err != nil {
		t.Fatalf("streamBackend: %v", err)
	}
	if got.String() != "whole answer" {
		t.Fatalf("assembled %q, want the whole completion", got.String())
	}
	if chunks != 1 {
		t.Fatalf("got %d chunks, want exactly 1 from a one-shot backend", chunks)
	}
	// And it says so, rather than leaving a UI to guess why nothing appeared to
	// type itself.
	if !result.StreamedOneShot {
		t.Fatal("a one-shot fallback must report itself")
	}
}

func TestAStreamingBackendIsNotReportedAsOneShot(t *testing.T) {
	result, err := streamBackend(context.Background(), NewEchoBackend(),
		InferenceRequest{Prompt: "a b"}, func(string) error { return nil })
	if err != nil {
		t.Fatalf("streamBackend: %v", err)
	}
	if result.StreamedOneShot {
		t.Fatal("a backend that really streamed must not be reported as one-shot")
	}
}

// TestACallbackErrorAbortsTheStream is how a transport reports that its client
// hung up, so the backend stops generating tokens nobody will read.
func TestACallbackErrorAbortsTheStream(t *testing.T) {
	clientGone := errors.New("client hung up")
	seen := 0

	_, err := NewEchoBackend().InferStream(context.Background(),
		InferenceRequest{Prompt: "one two three four five"},
		func(string) error {
			seen++
			if seen == 2 {
				return clientGone
			}
			return nil
		})

	if !errors.Is(err, clientGone) {
		t.Fatalf("err = %v, want the callback's error", err)
	}
	if seen != 2 {
		t.Fatalf("the backend produced %d chunks after the abort, want it to stop at 2", seen)
	}
}

// TestStreamJobSettlesExactlyLikeFulfill: streaming must not become a cheaper or
// dearer way to buy the same inference.
func TestStreamJobSettlesExactlyLikeFulfill(t *testing.T) {
	streamed := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := newTestService(t, streamed, 3, 1_000_000)
	job, err := svc.SubmitInferenceJob(buyer, provider, InferenceRequest{Prompt: "hello"}, 1000)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	settled, result, err := svc.StreamJob(context.Background(), job.ID, func(string) error { return nil })
	if err != nil {
		t.Fatalf("StreamJob: %v", err)
	}
	if settled.Status != InferenceJobCompleted {
		t.Fatalf("status = %s, want completed", settled.Status)
	}
	if result == nil || result.StreamedOneShot {
		t.Fatal("the echo backend streams, so this should not be a one-shot")
	}

	oneShotSettler := &fakeSettler{committed: true, applied: true}
	svc2, buyer2, provider2 := newTestService(t, oneShotSettler, 3, 1_000_000)
	job2, err := svc2.SubmitInferenceJob(buyer2, provider2, InferenceRequest{Prompt: "hello"}, 1000)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	fulfilled, err := svc2.FulfillJob(context.Background(), job2.ID)
	if err != nil {
		t.Fatalf("FulfillJob: %v", err)
	}

	if streamed.amount() != oneShotSettler.amount() {
		t.Fatalf("streaming settled %d but fulfilling settled %d for the same request",
			streamed.amount(), oneShotSettler.amount())
	}
	if settled.Completion != fulfilled.Completion {
		t.Fatalf("streamed completion %q differs from the fulfilled %q",
			settled.Completion, fulfilled.Completion)
	}
	if settled.Units != fulfilled.Units {
		t.Fatalf("streamed units %d differ from fulfilled %d", settled.Units, fulfilled.Units)
	}
}

// TestStreamingIsRefusedOnTheClientSignedPath is the one combination refused by
// design rather than by circumstance: withholding the completion is the only
// enforcement there, and streaming gives it away before the buyer has paid.
func TestStreamingIsRefusedOnTheClientSignedPath(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)

	pr, err := svc.RunUnsettled(context.Background(), buyer.AccountID(), provider,
		InferenceRequest{Prompt: "hello"}, 1000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}

	delivered := 0
	_, _, err = svc.StreamJob(context.Background(), pr.JobID, func(string) error {
		delivered++
		return nil
	})
	if !errors.Is(err, ErrStreamNotSupported) {
		t.Fatalf("err = %v, want ErrStreamNotSupported", err)
	}
	if delivered != 0 {
		t.Fatal("not one byte of a withheld completion may be streamed out")
	}
}

// TestAFailedStreamReleasesTheReservation: a provider whose capacity stayed held
// by every abandoned stream would be taken off the market by them.
func TestAFailedStreamReleasesTheReservation(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := newTestService(t, fs, 3, 1_000_000)

	before, _ := svc.market.GetProvider(provider)
	job, err := svc.SubmitInferenceJob(buyer, provider, InferenceRequest{Prompt: "hello"}, 1000)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}

	clientGone := errors.New("client hung up")
	if _, _, err := svc.StreamJob(context.Background(), job.ID, func(string) error {
		return clientGone
	}); err == nil {
		t.Fatal("want an error when the client hangs up")
	}

	after, _ := svc.market.GetProvider(provider)
	if after.Available != before.Available {
		t.Fatalf("Available = %d, want the reservation released back to %d",
			after.Available, before.Available)
	}
	got, _ := svc.GetJob(job.ID)
	if got.Status != InferenceJobFailed {
		t.Fatalf("status = %s, want failed", got.Status)
	}
	if fs.submitted != 0 {
		t.Fatal("an abandoned stream must not be billed")
	}
}

func TestStreamJobRequiresACallback(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := newTestService(t, fs, 3, 1_000_000)
	job, err := svc.SubmitInferenceJob(buyer, provider, InferenceRequest{Prompt: "hi"}, 100)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	if _, _, err := svc.StreamJob(context.Background(), job.ID, nil); err == nil {
		t.Fatal("want an error with no callback")
	}
}
