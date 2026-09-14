package inference

import (
	"context"
	"testing"
)

// A nonce is a uniquifier checked against the set a sender has already
// committed, so it has to come from the chain. Settlement counted instead: a map
// on the Service, starting empty on every node start.
//
// The result on a real provider was that a buyer who had ever made a transfer
// could not buy inference at all. Their first invoice was always nonce zero, the
// chain had seen zero, and the job failed AFTER the GPU had produced the answer:
//
//   inference: submit the payment for job "93433a22-...": consensus: sender
//   nonce already used: sender 9ccfa14d... has already committed a transfer at
//   nonce 0
//
// Found on the live network, one command after the same buyer's first transfer.

func TestAnInvoiceUsesTheNonceTheChainReports(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := serviceWithBackend(t, greedyBackend{completion: "ok", claimTokens: 1}, fs, 1, 1_000_000)

	// The buyer has history: the chain says their next nonce is 7.
	fs.chainNonce = map[string]uint64{buyer: 7}

	job, err := svc.SubmitInferenceJob(buyer, provider, InferenceRequest{Prompt: "hi", Model: "m"}, 100)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	if _, err := svc.FulfillJob(context.Background(), job.ID); err != nil {
		t.Fatalf("FulfillJob: %v", err)
	}
	if got := fs.nonceSettled(); got != 7 {
		t.Fatalf("settled at nonce %d, want 7 - the chain's answer must be the floor", got)
	}
}

// Two jobs for one buyer before either transfer reaches a mempool must not be
// told the same nonce. The chain cannot see an invoice that has not been
// submitted, which is the whole reason the Service keeps a counter at all.
func TestTwoInvoicesForOneBuyerDoNotShareANonce(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := serviceWithBackend(t, greedyBackend{completion: "ok", claimTokens: 1}, fs, 1, 1_000_000)
	fs.chainNonce = map[string]uint64{buyer: 3}

	seen := map[uint64]bool{}
	for i := 0; i < 3; i++ {
		job, err := svc.SubmitInferenceJob(buyer, provider, InferenceRequest{Prompt: "hi", Model: "m"}, 100)
		if err != nil {
			t.Fatalf("job %d: %v", i, err)
		}
		if _, err := svc.FulfillJob(context.Background(), job.ID); err != nil {
			t.Fatalf("fulfill %d: %v", i, err)
		}
		n := fs.nonceSettled()
		if seen[n] {
			t.Fatalf("nonce %d handed out twice", n)
		}
		if n < 3 {
			t.Fatalf("nonce %d is below the chain's floor of 3", n)
		}
		seen[n] = true
	}
}
