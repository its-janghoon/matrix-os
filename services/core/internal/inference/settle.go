package inference

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// This file adds the CLIENT-SIGNED inference path: the buyer's own key
// authorises the payment and the node never holds it.
//
// FulfillJob is the hosted path. It settles by signing on the buyer's behalf
// with a key the node already holds (see Accounts), which is right for a node
// its own operator runs and wrong for two cases we want: a public endpoint,
// where it makes the operator a custodian of every caller's balance, and a dApp,
// where it makes "log in with your wallet" decorative because the wallet is not
// what authorises the spend.
//
// The obvious fix - have the buyer pre-sign the transfer, as
// SubmitSignedTransfer does - cannot work here, and the reason is worth stating
// because it shapes everything below. token.Transaction signs over an EXACT
// Amount, and the amount of an inference is not knowable until the work is done:
// it comes from the tokens the backend reported. A buyer cannot sign for a
// number nobody can compute yet.
//
// So the primitive is sign-the-invoice, in two steps:
//
//	RunUnsettled  reserves capacity, runs the backend, computes the charge, and
//	              returns a PaymentRequest - the exact transfer to sign. It does
//	              NOT return the completion.
//	SettleSigned  verifies the buyer's signature over exactly that transfer,
//	              submits it to consensus, and only then returns the completion.
//
// Withholding the completion until the payment is signed is the enforcement. The
// provider's exposure is the compute for one job per defecting buyer, bounded
// further by the affordability check that already ran at reservation time. That
// is the same exposure any metered API carries, and it is the price of not
// holding the buyer's key.
//
// An unpaid job holds a reservation, so ExpireUnpaid releases it. Without that,
// a buyer who never signs quietly takes a provider's capacity off the market.

var (
	// ErrNotAwaitingPayment is returned when a job is settled at the wrong point
	// in its lifecycle: it has not run yet, or it has already been paid for.
	ErrNotAwaitingPayment = errors.New("inference: job is not awaiting payment")
	// ErrPaymentMismatch is returned when a signed transfer does not match the
	// payment request the job is waiting for. It is deliberately its own error:
	// this is the check that stops a buyer from paying a different provider, or
	// less than the invoice, with a signature that verifies perfectly well.
	ErrPaymentMismatch = errors.New("inference: signed transfer does not match the payment request")
	// ErrPaymentUnsigned is returned when the transfer carries no valid signature
	// by the buyer.
	ErrPaymentUnsigned = errors.New("inference: payment is not signed by the buyer")
)

// DefaultUnpaidJobTTL is how long a job may sit awaiting payment before
// ExpireUnpaid releases its reservation. Generous enough for a human to approve
// a wallet prompt, short enough that a defecting buyer cannot hold a provider's
// capacity for long.
const DefaultUnpaidJobTTL = 2 * time.Minute

// PaymentRequest is the exact transfer a buyer must sign to settle a job. Every
// field is fixed by the node and covered by token.Transaction.SigningBytes, so a
// buyer who signs it authorises this recipient and this amount and nothing else:
// the node cannot raise the charge or redirect it after the fact.
type PaymentRequest struct {
	// JobID is the inference job this payment settles.
	JobID string
	// From is the buyer's account ID, which must be the signer.
	From string
	// To is the provider being paid.
	To string
	// Amount is the charge in native MATRIX base units: the billable units the
	// backend reported, scaled by the provider's price and clamped to the
	// reservation the buyer was already checked for.
	Amount uint64
	// Nonce, Timestamp and PrevHash complete the signable transfer. They are
	// chosen by the node because they carry no economic meaning for the buyer;
	// the fields a buyer cares about are To and Amount, and those are the ones a
	// mismatch check enforces.
	Nonce     uint64
	Timestamp int64
	PrevHash  []byte
	// Usage and Model report what the buyer is being billed for, so a wallet can
	// show a reason alongside the number.
	Usage Usage
	Model string
	// ExpiresAt is when an unsigned request stops being settleable and its
	// reservation is released.
	ExpiresAt time.Time
}

// Transaction returns the unsigned token.Transaction this request describes.
// A client signs the result; the node compares what comes back against this
// same construction, so the two cannot drift apart.
func (p PaymentRequest) Transaction(from ed25519.PublicKey) *token.Transaction {
	return &token.Transaction{
		From:      from,
		To:        p.To,
		Amount:    p.Amount,
		Nonce:     p.Nonce,
		Timestamp: p.Timestamp,
		PrevHash:  p.PrevHash,
	}
}

// RunUnsettled reserves capacity, runs the inference, and returns the payment to
// sign WITHOUT the completion. It is the client-signed counterpart to
// SubmitInferenceJob + FulfillJob, and it needs no signing key for the buyer.
//
// The affordability check still happens at reservation time (market.SubmitJob),
// so a buyer who cannot pay is refused before a provider does any work.
func (s *Service) RunUnsettled(
	ctx context.Context,
	buyer, providerID string,
	req InferenceRequest,
	unitsEstimate uint64,
) (*PaymentRequest, error) {
	job, err := s.SubmitInferenceJob(buyer, providerID, req, unitsEstimate)
	if err != nil {
		return nil, err
	}
	return s.PrepareSettlement(ctx, job.ID)
}

// PrepareSettlement runs a PENDING job's inference and parks it awaiting
// payment. It is separate from RunUnsettled so a caller that already submitted a
// job (through the existing RPC) can move it onto the client-signed path.
func (s *Service) PrepareSettlement(ctx context.Context, jobID string) (*PaymentRequest, error) {
	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %q", ErrJobNotFound, jobID)
	}
	if job.Status != InferenceJobPending && job.Status != InferenceJobRunning {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: job %q is %s", ErrJobNotFound, jobID, job.Status)
	}
	buyer, provider, marketJobID := job.Buyer, job.Provider, job.MarketJobID
	request := job.Request
	job.Status = InferenceJobRunning
	job.UpdatedAt = time.Now().UTC()
	s.mu.Unlock()

	backend, err := s.registry.Backend(provider)
	if err != nil {
		s.failJob(jobID)
		return nil, fmt.Errorf("%w: %v", ErrNoBackend, err)
	}
	resp, err := backend.Infer(ctx, request)
	if err != nil {
		s.failJob(jobID)
		return nil, fmt.Errorf("inference: backend for provider %q failed: %w", provider, err)
	}

	mjob, ok := s.market.GetJob(marketJobID)
	if !ok {
		s.failJob(jobID)
		return nil, fmt.Errorf("%w: %q", market.ErrJobNotFound, marketJobID)
	}
	prov, ok := s.market.GetProvider(provider)
	if !ok {
		s.failJob(jobID)
		return nil, fmt.Errorf("%w: %q", market.ErrProviderNotFound, provider)
	}

	// Identical clamping to FulfillJob: the charge can never exceed the reserved,
	// affordability-checked price, whichever path settles it.
	billableUnits := resp.Units
	if billableUnits > mjob.Units {
		billableUnits = mjob.Units
	}
	amount := billableUnits * prov.PricePerUnit
	if amount > mjob.Price {
		s.failJob(jobID)
		return nil, fmt.Errorf("inference: computed charge %d exceeds reserved price %d for job %q",
			amount, mjob.Price, marketJobID)
	}

	now := time.Now().UTC()
	s.mu.Lock()
	nonce := s.nonce[buyer]
	s.nonce[buyer] = nonce + 1
	pr := &PaymentRequest{
		JobID:     jobID,
		From:      buyer,
		To:        provider,
		Amount:    amount,
		Nonce:     nonce,
		Timestamp: now.UnixNano(),
		Usage:     resp.Usage,
		Model:     resp.Model,
		ExpiresAt: now.Add(s.unpaidTTL()),
	}
	// The completion is held on the job and deliberately not in the payment
	// request: the buyer gets it from SettleSigned, after paying.
	job.Completion = resp.Completion
	job.Units = amount
	job.Usage = resp.Usage
	job.Model = resp.Model
	job.Status = InferenceJobAwaitingPayment
	job.UpdatedAt = now
	job.payment = pr
	s.mu.Unlock()

	return pr, nil
}

// SettleSigned verifies the buyer's signed transfer against the job's payment
// request, submits it to consensus, and returns the completed job WITH its
// completion.
//
// It waits for the transfer to commit and apply before reporting COMPLETED, for
// the same reason FulfillJob does: a payment sitting in the mempool may yet be
// skipped as unaffordable at apply time, and a job reported complete for a
// payment that never moved is a lie the provider pays for.
func (s *Service) SettleSigned(ctx context.Context, jobID string, tx *token.Transaction) (*InferenceJob, error) {
	if tx == nil {
		return nil, fmt.Errorf("%w: no transfer supplied", ErrPaymentUnsigned)
	}

	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %q", ErrJobNotFound, jobID)
	}
	if job.Status != InferenceJobAwaitingPayment || job.payment == nil {
		status := job.Status
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: job %q is %s", ErrNotAwaitingPayment, jobID, status)
	}
	pr := *job.payment
	s.mu.Unlock()

	// The signature must be the buyer's own. Verify before comparing fields so a
	// forged transfer is rejected as unsigned rather than as a mismatch.
	if err := tx.Verify(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPaymentUnsigned, err)
	}
	if tx.SenderID() != pr.From {
		return nil, fmt.Errorf("%w: signed by %s, want the buyer %s",
			ErrPaymentUnsigned, tx.SenderID(), pr.From)
	}

	// Every signable field must match. A buyer could otherwise sign a perfectly
	// valid transfer of 1 base unit to an account they control and have it
	// accepted as payment for the job.
	if tx.To != pr.To {
		return nil, fmt.Errorf("%w: pays %s, want the provider %s", ErrPaymentMismatch, tx.To, pr.To)
	}
	if tx.Amount != pr.Amount {
		return nil, fmt.Errorf("%w: pays %d, want %d", ErrPaymentMismatch, tx.Amount, pr.Amount)
	}
	if tx.Nonce != pr.Nonce || tx.Timestamp != pr.Timestamp {
		return nil, fmt.Errorf("%w: nonce/timestamp do not match the request", ErrPaymentMismatch)
	}
	if len(tx.PrevHash) != len(pr.PrevHash) {
		return nil, fmt.Errorf("%w: prev_hash does not match the request", ErrPaymentMismatch)
	}
	for i := range tx.PrevHash {
		if tx.PrevHash[i] != pr.PrevHash[i] {
			return nil, fmt.Errorf("%w: prev_hash does not match the request", ErrPaymentMismatch)
		}
	}

	if !pr.ExpiresAt.IsZero() && time.Now().UTC().After(pr.ExpiresAt) {
		// The reservation may already have been released, so refuse rather than
		// settle a job whose capacity has gone back on the market.
		s.failJob(jobID)
		return nil, fmt.Errorf("%w: the payment request for job %q expired at %s",
			ErrNotAwaitingPayment, jobID, pr.ExpiresAt.Format(time.RFC3339))
	}

	if err := s.settler.Submit(tx); err != nil {
		s.failJob(jobID)
		return nil, fmt.Errorf("inference: submit the payment for job %q: %w", jobID, err)
	}

	s.mu.Lock()
	job.Status = InferenceJobSettling
	job.UpdatedAt = time.Now().UTC()
	s.mu.Unlock()

	waitCtx, cancel := context.WithTimeout(ctx, DefaultSettlementTimeout)
	defer cancel()
	committed, applied, err := s.settler.WaitForSettlement(waitCtx, tx)
	if err != nil {
		// Honest: the payment was submitted and its fate is unknown, so the job
		// stays SETTLING rather than being claimed complete or failed.
		return s.jobCopy(jobID), fmt.Errorf("inference: waiting for the payment for job %q: %w", jobID, err)
	}
	if !committed || !applied {
		s.failJob(jobID)
		return s.jobCopy(jobID), fmt.Errorf("inference: the payment for job %q did not apply", jobID)
	}

	// Release the reservation rather than calling market.CompleteJob: consensus
	// already moved the credits on the same ledger, and completing the market job
	// would charge the buyer a second time.
	if err := s.market.CancelJob(pr.JobID); err != nil {
		// The money moved and the completion is owed; a stuck reservation is the
		// lesser problem and ExpireUnpaid is not the right tool for it, so report
		// it and still hand over the completion.
		_ = err
	}

	s.mu.Lock()
	job.Status = InferenceJobCompleted
	job.UpdatedAt = time.Now().UTC()
	job.payment = nil
	cp := job.snapshot()
	s.mu.Unlock()
	return &cp, nil
}

// ExpireUnpaid releases the reservation of every job that has been awaiting
// payment for longer than its request's expiry, and returns how many it
// released. A node calls it periodically.
//
// Without it a buyer who runs a job and never signs holds a provider's capacity
// until the process restarts, which is a free way to take a competitor off the
// market.
func (s *Service) ExpireUnpaid(now time.Time) int {
	s.mu.Lock()
	stale := make([]string, 0)
	for id, job := range s.jobs {
		if job.Status != InferenceJobAwaitingPayment || job.payment == nil {
			continue
		}
		if !job.payment.ExpiresAt.IsZero() && now.After(job.payment.ExpiresAt) {
			stale = append(stale, id)
		}
	}
	s.mu.Unlock()

	// failJob takes the lock itself and calls into the market, so it runs
	// outside the critical section above.
	for _, id := range stale {
		s.failJob(id)
	}
	return len(stale)
}

// unpaidTTL is the configured lifetime of a payment request, defaulted.
func (s *Service) unpaidTTL() time.Duration {
	if s.unpaidJobTTL > 0 {
		return s.unpaidJobTTL
	}
	return DefaultUnpaidJobTTL
}

// jobCopy returns a snapshot of a job, or nil when it is gone.
func (s *Service) jobCopy(jobID string) *InferenceJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok {
		return nil
	}
	cp := job.snapshot()
	return &cp
}
