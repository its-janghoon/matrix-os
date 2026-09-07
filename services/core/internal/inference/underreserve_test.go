package inference

import (
	"context"
	"strings"
	"testing"
)

// TestABuyerCannotUnderReserveAndGetUnboundedWork
//
// THE ATTACK. `unitsEstimate` is chosen by the CLIENT and is what the
// affordability check runs against: market.SubmitJob reserves
// price = unitsEstimate * PricePerUnit and verifies the buyer can afford THAT.
// The real cost is only known after the work is done, and FulfillJob then clamps
// the billable units DOWN to the reservation:
//
//	if billableUnits > mjob.Units { billableUnits = mjob.Units }
//
// The comment there frames the clamp as buyer protection, which it is - the
// buyer is never charged more than it agreed to. Nothing guards the mirror side.
// A buyer reserves 1 unit, sends a prompt worth thousands, and the provider does
// all of that work and is paid for one unit.
//
// That is theft of service from providers, and providers are the party the whole
// emission exists to attract. It needs no privileges: a fresh account with the
// price of a single unit is enough.
func TestABuyerCannotUnderReserveAndGetUnboundedWork(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	// Price 1 per unit, and the buyer holds exactly 1 - enough to reserve
	// exactly one unit and nothing more.
	svc, buyerID, providerID := newTestService(t, fs, 1, 1)

	// A prompt far larger than one unit of work by any measure.
	huge := strings.Repeat("the quick brown fox jumps over the lazy dog. ", 2000)

	_, err := svc.SubmitInferenceJob(buyerID, providerID, InferenceRequest{Prompt: huge}, 1)
	if err == nil {
		t.Fatalf("a 1-unit reservation was accepted for a %d-byte prompt: the provider would do "+
			"all of that work and be paid for one unit", len(huge))
	}
	if !strings.Contains(err.Error(), "reserve") && !strings.Contains(err.Error(), "units") {
		t.Fatalf("error = %v, want it to name the under-reservation", err)
	}
}

// TestAnHonestEstimateIsStillAccepted. The bound must not refuse ordinary work:
// a buyer who reserves enough for the prompt it is sending has to go through,
// including with multi-byte text where bytes and characters differ.
func TestAnHonestEstimateIsStillAccepted(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyerID, providerID := newTestService(t, fs, 1, 100000)

	cases := map[string]InferenceRequest{
		"short ascii":   {Prompt: "hello world"},
		"a paragraph":   {Prompt: strings.Repeat("a sentence of ordinary length. ", 40)},
		"korean text":   {Prompt: strings.Repeat("한국어 문장입니다. ", 40)},
		"chat messages": {Messages: []Message{{Role: "user", Content: strings.Repeat("hi there ", 50)}}},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			// A generous but not absurd estimate, the way a client that measured
			// its own prompt would pick.
			job, err := svc.SubmitInferenceJob(buyerID, providerID, req, 5000)
			if err != nil {
				t.Fatalf("an honest 5000-unit reservation was refused: %v", err)
			}
			if _, err := svc.FulfillJob(context.Background(), job.ID); err != nil {
				t.Fatalf("FulfillJob: %v", err)
			}
		})
	}
}

// TestTheBoundScalesWithTheReservation: the check is a relationship between the
// request and what was reserved, not a fixed size limit. The same prompt that is
// refused at 1 unit must be accepted once enough is reserved for it.
func TestTheBoundScalesWithTheReservation(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyerID, providerID := newTestService(t, fs, 1, 1_000_000)

	prompt := strings.Repeat("x", 8000)
	req := InferenceRequest{Prompt: prompt}

	if _, err := svc.SubmitInferenceJob(buyerID, providerID, req, 1); err == nil {
		t.Fatal("8000 bytes of prompt was accepted against a 1-unit reservation")
	}
	if _, err := svc.SubmitInferenceJob(buyerID, providerID, req, 100000); err != nil {
		t.Fatalf("the same prompt was refused with a large reservation: %v", err)
	}
}

// TestMaxTokensCountsTowardTheReservation. The completion is work too, and
// MaxTokens is the one part of it the node knows before running. A buyer that
// reserves one unit and asks for 4000 completion tokens is the same attack
// wearing a different hat.
func TestMaxTokensCountsTowardTheReservation(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyerID, providerID := newTestService(t, fs, 1, 1_000_000)

	req := InferenceRequest{Prompt: "hi", MaxTokens: 4000}
	if _, err := svc.SubmitInferenceJob(buyerID, providerID, req, 10); err == nil {
		t.Fatal("max_tokens=4000 was accepted against a 10-unit reservation")
	}
	if _, err := svc.SubmitInferenceJob(buyerID, providerID, req, 5000); err != nil {
		t.Fatalf("max_tokens=4000 was refused with a 5000-unit reservation: %v", err)
	}
}
