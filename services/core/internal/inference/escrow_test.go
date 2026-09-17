package inference

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// The escrowed path, end to end: the buyer pays the cap, the answer STREAMS, and
// the settlement names what it really cost.
//
// The property under test is the one the other two paths cannot have at the same
// time - the buyer keeps their key AND watches the answer arrive - so what these
// check is mostly that nothing streams before the deposit has applied.

func signPlan(t *testing.T, acct *token.Account, pr *PaymentRequest) *token.Transaction {
	t.Helper()
	tx := pr.Transaction(acct.PublicKey)
	if err := tx.Sign(acct.PrivateKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tx
}

// escrowJob submits a job and opens a reservation on it.
func escrowJob(t *testing.T, svc *Service, buyerID, providerID string, units uint64) *EscrowPlan {
	t.Helper()
	job, err := svc.SubmitInferenceJob(buyerID, providerID, InferenceRequest{Prompt: "hello world"}, units)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	plan, err := svc.ReserveEscrow(job.ID)
	if err != nil {
		t.Fatalf("ReserveEscrow: %v", err)
	}
	return plan
}

func TestTheReservationIsTheReservedPriceAndNamesItsOwnTerms(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyerID, providerID := newTestService(t, fs, 3, 1000)

	plan := escrowJob(t, svc, buyerID, providerID, 8)

	// 8 units at 3 each. The deposit is the whole reservation and not a guess.
	if plan.Deposit != 24 {
		t.Fatalf("deposit should be the reserved price 24, got %d", plan.Deposit)
	}
	if plan.Deposit != plan.Request.Amount || plan.Request.To != plan.Account {
		t.Fatalf("the request must be the deposit into the escrow: to=%q amount=%d",
			plan.Request.To, plan.Request.Amount)
	}

	// Everything a buyer is agreeing to is in the account's name, which is what
	// lets one ordinary transfer signature cover all of it.
	terms, err := token.ParseInferEscrow(plan.Account)
	if err != nil {
		t.Fatalf("the escrow account does not parse: %v", err)
	}
	if terms.Reserved != plan.Deposit || terms.Provider != providerID || terms.Payer != buyerID {
		t.Fatalf("terms do not match the plan: %+v", terms)
	}
	if uint64(plan.ExpiresAt.Unix()) != terms.Expiry {
		t.Fatalf("the plan's expiry %s does not match the name's %d", plan.ExpiresAt, terms.Expiry)
	}
}

// The check the whole path rests on. Streaming before the deposit has APPLIED
// would be handing over the answer for money that may still be skipped.
func TestNothingStreamsUntilTheDepositHasApplied(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyerID, providerID := newTestService(t, fs, 3, 1000)
	buyer := svc.accounts.(memAccounts).m[buyerID]

	plan := escrowJob(t, svc, buyerID, providerID, 8)

	// Before funding.
	_, _, err := svc.StreamEscrowed(context.Background(), plan.JobID, func(string) error { return nil })
	if err == nil {
		t.Fatal("streamed a job whose reservation was never funded")
	}
	if !errors.Is(err, ErrNotAwaitingPayment) {
		t.Fatalf("wrong refusal: %v", err)
	}

	// Fund it, and only then does it run.
	if _, err := svc.FundEscrow(context.Background(), plan.JobID, signPlan(t, buyer, plan.Request)); err != nil {
		t.Fatalf("FundEscrow: %v", err)
	}
	var streamed strings.Builder
	pr, res, err := svc.StreamEscrowed(context.Background(), plan.JobID, func(d string) error {
		streamed.WriteString(d)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamEscrowed: %v", err)
	}
	if streamed.Len() == 0 {
		t.Fatal("nothing was streamed to the caller")
	}
	if streamed.String() != res.Response.Completion {
		t.Fatalf("the streamed text is not the completion:\n got %q\nwant %q",
			streamed.String(), res.Response.Completion)
	}
	if pr.Amount == 0 || pr.Amount > plan.Deposit {
		t.Fatalf("the settlement must be within the reservation, got %d of %d", pr.Amount, plan.Deposit)
	}
	if !strings.HasPrefix(pr.To, token.InferSettlePrefix) {
		t.Fatalf("the settlement should pay a settle recipient, got %q", pr.To)
	}
}

// A deposit that did not apply must not leave a job that can stream. Otherwise
// the failure mode is the provider giving the answer away.
func TestADepositThatDidNotApplyLeavesNothingToStream(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: false}
	svc, buyerID, providerID := newTestService(t, fs, 3, 1000)
	buyer := svc.accounts.(memAccounts).m[buyerID]

	plan := escrowJob(t, svc, buyerID, providerID, 8)
	if _, err := svc.FundEscrow(context.Background(), plan.JobID, signPlan(t, buyer, plan.Request)); err == nil {
		t.Fatal("FundEscrow reported success for a deposit that never applied")
	}
	if _, _, err := svc.StreamEscrowed(context.Background(), plan.JobID, func(string) error { return nil }); err == nil {
		t.Fatal("a job whose deposit failed still streamed")
	}
}

// The deposit is checked field by field, not by amount alone: a buyer could
// otherwise sign a valid transfer of one base unit to an account they control.
func TestTheSignedDepositMustBeExactlyTheOneAskedFor(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyerID, providerID := newTestService(t, fs, 3, 1000)
	buyer := svc.accounts.(memAccounts).m[buyerID]
	plan := escrowJob(t, svc, buyerID, providerID, 8)

	for _, tc := range []struct {
		name  string
		munge func(*PaymentRequest)
	}{
		{"a smaller amount", func(pr *PaymentRequest) { pr.Amount = 1 }},
		{"a different recipient", func(pr *PaymentRequest) { pr.To = buyerID }},
		{"a different nonce", func(pr *PaymentRequest) { pr.Nonce++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			munged := *plan.Request
			tc.munge(&munged)
			if _, err := svc.FundEscrow(context.Background(), plan.JobID, signPlan(t, buyer, &munged)); err == nil {
				t.Fatal("accepted a deposit that was not the one asked for")
			}
		})
	}
}

// The charge is clamped by the text that crossed the wire, not only by the
// reservation - which is the hole MaxUnitsFor exists to close, and it must still
// be closed on this path.
func TestTheSettlementIsClampedByTheAnswerAndNotOnlyByTheCap(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	// A large reservation against a short prompt: the echo backend answers
	// briefly, so the ceiling rather than the cap is what must bind.
	svc, buyerID, providerID := newTestService(t, fs, 1, 100000)
	buyer := svc.accounts.(memAccounts).m[buyerID]

	plan := escrowJob(t, svc, buyerID, providerID, 5000)
	if _, err := svc.FundEscrow(context.Background(), plan.JobID, signPlan(t, buyer, plan.Request)); err != nil {
		t.Fatalf("FundEscrow: %v", err)
	}
	pr, res, err := svc.StreamEscrowed(context.Background(), plan.JobID, func(string) error { return nil })
	if err != nil {
		t.Fatalf("StreamEscrowed: %v", err)
	}
	ceiling := MaxUnitsFor(InferenceRequest{Prompt: "hello world"}, res.Response.Completion, res.Response.Reasoning)
	if pr.Amount > ceiling {
		t.Fatalf("charged %d for an answer whose ceiling is %d", pr.Amount, ceiling)
	}
	if pr.Amount >= plan.Deposit {
		t.Fatalf("a short answer billed the whole %d reservation", plan.Deposit)
	}
}

func TestSettlingCompletesTheJobAndLeavesAReceipt(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyerID, providerID := newTestService(t, fs, 3, 1000)
	buyer := svc.accounts.(memAccounts).m[buyerID]
	// A receipt is signed by the NODE's own key, so a service without one issues
	// none - correctly. Given one here because the receipt is the point of this
	// test: on the escrowed path the seller is a stranger, and evidence the buyer
	// can hold is their only recourse.
	node, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate node: %v", err)
	}
	svc.node = node

	plan := escrowJob(t, svc, buyerID, providerID, 8)
	if _, err := svc.FundEscrow(context.Background(), plan.JobID, signPlan(t, buyer, plan.Request)); err != nil {
		t.Fatalf("FundEscrow: %v", err)
	}
	pr, _, err := svc.StreamEscrowed(context.Background(), plan.JobID, func(string) error { return nil })
	if err != nil {
		t.Fatalf("StreamEscrowed: %v", err)
	}

	done, err := svc.SettleEscrowed(context.Background(), plan.JobID, signPlan(t, buyer, pr))
	if err != nil {
		t.Fatalf("SettleEscrowed: %v", err)
	}
	if done.Status != InferenceJobCompleted {
		t.Fatalf("job is %s after settling", done.Status)
	}
	if done.Completion == "" {
		t.Fatal("the completion is missing from a settled job")
	}
	if done.Receipt == nil {
		t.Fatal("no receipt: this is the path where the seller is a stranger and evidence is the buyer's only recourse")
	}
	if done.Units != pr.Amount {
		t.Fatalf("the job's charge %d is not what was settled %d", done.Units, pr.Amount)
	}
}
