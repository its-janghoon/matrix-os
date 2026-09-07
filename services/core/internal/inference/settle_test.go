package inference

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// signedFor signs the payment request with acct, producing what an honest
// client sends back.
func signedFor(t *testing.T, pr *PaymentRequest, acct *token.Account) *token.Transaction {
	t.Helper()
	tx := pr.Transaction(acct.PublicKey)
	if err := tx.Sign(acct.PrivateKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tx
}

// clientSignedService builds a Service whose Accounts resolver knows NOBODY, so
// a test cannot accidentally pass by falling back to the hosted path. The buyer
// account is returned for the test to sign with, exactly as a wallet would.
func clientSignedService(t *testing.T, settler Settler, pricePerUnit, credits uint64) (*Service, *token.Account, string) {
	t.Helper()
	svc, buyerID, providerID := newTestService(t, settler, pricePerUnit, credits)
	buyerAcct, ok := svc.accounts.Account(buyerID)
	if !ok {
		t.Fatal("the test harness should know the buyer account")
	}
	// Strip custody: the whole point is that this path needs no key.
	svc.accounts = memAccounts{m: map[string]*token.Account{}}
	return svc, buyerAcct, providerID
}

// TestTheClientSignedPathNeedsNoKeyOnTheNode is the property (c) exists for. The
// resolver knows nothing, so if this passes the node settled without custody.
func TestTheClientSignedPathNeedsNoKeyOnTheNode(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)

	pr, err := svc.RunUnsettled(context.Background(), buyer.AccountID(), provider,
		InferenceRequest{Prompt: "hello"}, 1000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}

	job, err := svc.SettleSigned(context.Background(), pr.JobID, signedFor(t, pr, buyer))
	if err != nil {
		t.Fatalf("SettleSigned: %v", err)
	}
	if job.Status != InferenceJobCompleted {
		t.Fatalf("status = %s, want completed", job.Status)
	}
	if job.Completion == "" {
		t.Fatal("a settled job must carry its completion")
	}

	// And the transfer that reached consensus is the buyer's own signed one.
	got := fs.signed()
	if got == nil {
		t.Fatal("nothing was submitted")
	}
	if got.SenderID() != buyer.AccountID() {
		t.Fatalf("submitted transfer is from %s, want the buyer", got.SenderID())
	}
	if err := got.Verify(); err != nil {
		t.Fatalf("the submitted transfer does not verify: %v", err)
	}
}

// TestTheCompletionIsWithheldUntilThePaymentIsSigned: withholding is the ONLY
// enforcement on this path, since the provider has already done the work.
func TestTheCompletionIsWithheldUntilThePaymentIsSigned(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)

	pr, err := svc.RunUnsettled(context.Background(), buyer.AccountID(), provider,
		InferenceRequest{Prompt: "hello"}, 1000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}

	// The payment request itself carries no completion, by type.
	if pr.Amount == 0 {
		t.Fatal("the payment request should name a charge")
	}
	if pr.Usage.TotalTokens == 0 {
		t.Fatal("the payment request should say what is being billed for")
	}
	if pr.To != provider {
		t.Fatalf("payment pays %s, want the provider %s", pr.To, provider)
	}

	// The job is parked and nothing has been submitted yet.
	job, ok := svc.GetJob(pr.JobID)
	if !ok {
		t.Fatal("job not found")
	}
	if job.Status != InferenceJobAwaitingPayment {
		t.Fatalf("status = %s, want awaiting_payment", job.Status)
	}
	if fs.signed() != nil {
		t.Fatal("nothing may be submitted before the buyer signs")
	}
}

// TestABuyerCannotUnderpayWithAValidSignature is the check that a signature
// alone is not enough: this transfer verifies perfectly and pays one base unit.
func TestABuyerCannotUnderpayWithAValidSignature(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)

	pr, err := svc.RunUnsettled(context.Background(), buyer.AccountID(), provider,
		InferenceRequest{Prompt: "hello"}, 1000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}

	cheap := *pr
	cheap.Amount = 1
	tx := signedFor(t, &cheap, buyer)

	_, err = svc.SettleSigned(context.Background(), pr.JobID, tx)
	if !errors.Is(err, ErrPaymentMismatch) {
		t.Fatalf("err = %v, want ErrPaymentMismatch", err)
	}
	if fs.signed() != nil {
		t.Fatal("an underpaying transfer must not reach consensus")
	}
}

// TestABuyerCannotRedirectThePaymentToThemselves: the same amount, paid to an
// account they control, would settle the job for free.
func TestABuyerCannotRedirectThePaymentToThemselves(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)

	pr, err := svc.RunUnsettled(context.Background(), buyer.AccountID(), provider,
		InferenceRequest{Prompt: "hello"}, 1000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}

	accomplice, _ := token.GenerateAccount()
	redirected := *pr
	redirected.To = accomplice.AccountID()

	_, err = svc.SettleSigned(context.Background(), pr.JobID, signedFor(t, &redirected, buyer))
	if !errors.Is(err, ErrPaymentMismatch) {
		t.Fatalf("err = %v, want ErrPaymentMismatch", err)
	}
	if fs.signed() != nil {
		t.Fatal("a redirected transfer must not reach consensus")
	}
}

func TestSomebodyElsesSignatureIsRefused(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)

	pr, err := svc.RunUnsettled(context.Background(), buyer.AccountID(), provider,
		InferenceRequest{Prompt: "hello"}, 1000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}

	// A valid transfer for the right amount to the right provider, signed by
	// someone who is not the buyer. Without the sender check it would settle the
	// buyer's job out of a stranger's balance.
	stranger, _ := token.GenerateAccount()
	_, err = svc.SettleSigned(context.Background(), pr.JobID, signedFor(t, pr, stranger))
	if !errors.Is(err, ErrPaymentUnsigned) {
		t.Fatalf("err = %v, want ErrPaymentUnsigned", err)
	}
}

func TestAnUnsignedTransferIsRefused(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)

	pr, err := svc.RunUnsettled(context.Background(), buyer.AccountID(), provider,
		InferenceRequest{Prompt: "hello"}, 1000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}

	if _, err := svc.SettleSigned(context.Background(), pr.JobID, pr.Transaction(buyer.PublicKey)); !errors.Is(err, ErrPaymentUnsigned) {
		t.Fatalf("err = %v, want ErrPaymentUnsigned", err)
	}
	if _, err := svc.SettleSigned(context.Background(), pr.JobID, nil); !errors.Is(err, ErrPaymentUnsigned) {
		t.Fatalf("nil transfer: err = %v, want ErrPaymentUnsigned", err)
	}
}

// TestAJobCannotBePaidTwice: a replayed settlement would charge the buyer again
// for a completion they already have.
func TestAJobCannotBePaidTwice(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)

	pr, err := svc.RunUnsettled(context.Background(), buyer.AccountID(), provider,
		InferenceRequest{Prompt: "hello"}, 1000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}
	tx := signedFor(t, pr, buyer)
	if _, err := svc.SettleSigned(context.Background(), pr.JobID, tx); err != nil {
		t.Fatalf("first settlement: %v", err)
	}

	if _, err := svc.SettleSigned(context.Background(), pr.JobID, tx); !errors.Is(err, ErrNotAwaitingPayment) {
		t.Fatalf("second settlement: err = %v, want ErrNotAwaitingPayment", err)
	}
}

func TestSettlingAJobThatHasNotRunIsRefused(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)

	job, err := svc.SubmitInferenceJob(buyer.AccountID(), provider, InferenceRequest{Prompt: "hi"}, 100)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}

	// PENDING, so there is no charge to sign for yet.
	tx := &token.Transaction{From: buyer.PublicKey, To: provider, Amount: 1}
	if err := tx.Sign(buyer.PrivateKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := svc.SettleSigned(context.Background(), job.ID, tx); !errors.Is(err, ErrNotAwaitingPayment) {
		t.Fatalf("err = %v, want ErrNotAwaitingPayment", err)
	}
}

// TestTheChargeIsClampedToTheReservation holds on this path too: the buyer is
// never invoiced more than the amount they were checked for at reservation time.
func TestTheChargeIsClampedToTheReservationOnTheSignedPath(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)

	// The echo backend reports a fixed 7 units; reserve fewer than that.
	pr, err := svc.RunUnsettled(context.Background(), buyer.AccountID(), provider,
		InferenceRequest{Prompt: "hello"}, 2)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}
	if want := uint64(2 * 3); pr.Amount != want {
		t.Fatalf("amount = %d, want %d (2 reserved units at price 3)", pr.Amount, want)
	}
}

// TestAnUnpaidJobReleasesItsReservation: otherwise a buyer who runs a job and
// never signs holds a provider's capacity until the process restarts.
func TestAnUnpaidJobReleasesItsReservation(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)
	svc.unpaidJobTTL = time.Millisecond

	before, _ := svc.market.GetProvider(provider)
	pr, err := svc.RunUnsettled(context.Background(), buyer.AccountID(), provider,
		InferenceRequest{Prompt: "hello"}, 1000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}
	held, _ := svc.market.GetProvider(provider)
	if held.Available >= before.Available {
		t.Fatal("running a job should hold a reservation")
	}

	released := svc.ExpireUnpaid(time.Now().UTC().Add(time.Second))
	if released != 1 {
		t.Fatalf("released %d jobs, want 1", released)
	}
	after, _ := svc.market.GetProvider(provider)
	if after.Available != before.Available {
		t.Fatalf("Available = %d, want the reservation back at %d", after.Available, before.Available)
	}

	job, _ := svc.GetJob(pr.JobID)
	if job.Status != InferenceJobFailed {
		t.Fatalf("status = %s, want failed after expiry", job.Status)
	}
}

func TestAPaidJobIsNotExpired(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)

	pr, err := svc.RunUnsettled(context.Background(), buyer.AccountID(), provider,
		InferenceRequest{Prompt: "hello"}, 1000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}
	if _, err := svc.SettleSigned(context.Background(), pr.JobID, signedFor(t, pr, buyer)); err != nil {
		t.Fatalf("SettleSigned: %v", err)
	}

	// Far in the future: a completed job must never be swept.
	if released := svc.ExpireUnpaid(time.Now().UTC().Add(24 * time.Hour)); released != 0 {
		t.Fatalf("released %d jobs, want 0", released)
	}
	job, _ := svc.GetJob(pr.JobID)
	if job.Status != InferenceJobCompleted {
		t.Fatalf("status = %s, want it to stay completed", job.Status)
	}
}

func TestAnExpiredPaymentRequestIsRefusedRatherThanSettled(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)
	svc.unpaidJobTTL = time.Nanosecond

	pr, err := svc.RunUnsettled(context.Background(), buyer.AccountID(), provider,
		InferenceRequest{Prompt: "hello"}, 1000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}
	time.Sleep(2 * time.Millisecond)

	// The reservation may already be back on the market, so settling now would
	// pay for capacity somebody else could be holding.
	if _, err := svc.SettleSigned(context.Background(), pr.JobID, signedFor(t, pr, buyer)); !errors.Is(err, ErrNotAwaitingPayment) {
		t.Fatalf("err = %v, want ErrNotAwaitingPayment", err)
	}
	if fs.signed() != nil {
		t.Fatal("an expired payment must not reach consensus")
	}
}

// TestAPaymentThatDoesNotApplyFailsTheJob: consensus may commit a transfer and
// skip it as unaffordable at apply time, and a job reported complete for a
// payment that never moved is a lie the provider pays for.
func TestAPaymentThatDoesNotApplyFailsTheJob(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: false}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)

	pr, err := svc.RunUnsettled(context.Background(), buyer.AccountID(), provider,
		InferenceRequest{Prompt: "hello"}, 1000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}

	if _, err := svc.SettleSigned(context.Background(), pr.JobID, signedFor(t, pr, buyer)); err == nil {
		t.Fatal("want an error when the payment does not apply")
	}
	job, _ := svc.GetJob(pr.JobID)
	if job.Status == InferenceJobCompleted {
		t.Fatal("a job whose payment never applied must not read COMPLETED")
	}
}

// TestAnUnaffordableJobIsRefusedBeforeTheProviderWorks: the affordability check
// at reservation time is what bounds the provider's exposure on this path.
func TestAnUnaffordableJobIsRefusedBeforeTheProviderWorks(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1)

	if _, err := svc.RunUnsettled(context.Background(), buyer.AccountID(), provider,
		InferenceRequest{Prompt: "hello"}, 1000); err == nil {
		t.Fatal("want a refusal for a buyer who cannot cover the reservation")
	}
}

// TestTheJobCopyDoesNotLeakTheNodesPaymentRecord: SettleSigned compares an
// incoming transfer against that record, so a caller must not be able to edit it.
func TestTheJobCopyDoesNotLeakTheNodesPaymentRecord(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)

	pr, err := svc.RunUnsettled(context.Background(), buyer.AccountID(), provider,
		InferenceRequest{Prompt: "hello"}, 1000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}

	// A returned copy must not carry the payment record at all. It used to be a
	// shallow copy sharing the pointer, so an in-process caller could lower the
	// invoice it was about to be checked against.
	copyOfJob, _ := svc.GetJob(pr.JobID)
	if copyOfJob.payment != nil {
		t.Fatal("a returned job copy must not carry the node's payment record")
	}

	cheap := *pr
	cheap.Amount = 1
	if _, err := svc.SettleSigned(context.Background(), pr.JobID, signedFor(t, &cheap, buyer)); err == nil {
		t.Fatal("editing a returned job copy must not lower the invoice")
	}
}
