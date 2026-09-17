package inference

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// The ESCROWED path: pay the reservation before the model runs, so the answer
// has nothing left to withhold and streams as it is produced.
//
// The other two paths and what each gives up:
//
//	HOSTED          streams, because the node holds the buyer's key and settles
//	                for itself. The buyer hands a stranger's machine a key that
//	                can move everything in that account.
//	CLIENT-SIGNED   the buyer keeps their key, and waits in silence: the charge
//	                is unknowable until the work is done, so the node runs first,
//	                invoices second, and withholds the completion until the
//	                invoice is signed. The withholding IS the enforcement.
//	ESCROWED        both. The reservation is knowable before any work happens -
//	                `units_reserved x price` - so the buyer signs for THAT, the
//	                provider is paid the most the job can cost before it starts,
//	                and consensus returns the difference at settlement.
//
// Four steps, and the middle one is the point:
//
//	ReserveEscrow   the deposit transfer to sign. Nothing has run.
//	FundEscrow      submit it and wait for it to COMMIT AND APPLY. A deposit in
//	                the mempool is not money in escrow, and streaming against one
//	                is streaming for free.
//	StreamEscrowed  run and stream. Returns the settlement to sign.
//	SettleEscrowed  submit it; consensus pays the actual and refunds the rest.
//
// WHAT HAPPENS IF THE BUYER NEVER SETTLES. The provider claims the whole
// reservation once its expiry passes. That is why streaming is safe: the seller
// is guaranteed the cap before the first token, and a buyer who reads the answer
// and walks away pays MORE than one who settles honestly.

// DefaultEscrowTTL is how long a reservation stays the buyer's to settle before
// the provider may claim it.
//
// Wide on purpose. Too short and a reservation is claimed out from under a
// reasoning model still working, which pays a provider in full for a job the
// buyer never received; too long and a buyer's money is parked after a provider
// has gone away. The first mistake takes money from someone who did nothing
// wrong, so the margin goes that way.
const DefaultEscrowTTL = 30 * time.Minute

// EscrowPlan is what a buyer signs to open a reservation, and everything they
// need to check it before they do.
type EscrowPlan struct {
	// JobID is the inference job this reservation pays for.
	JobID string
	// Account is the ledger account the deposit goes to. Its NAME carries every
	// term below, which is what lets one ordinary transfer signature cover them.
	Account string
	// Deposit is the reservation: exactly `units_reserved x price_per_unit`.
	Deposit uint64
	// Provider is who a settlement will pay.
	Provider string
	// Payer is who the refund returns to - the buyer, or their budget.
	Payer string
	// ExpiresAt is when the provider may claim whatever is left unsettled.
	ExpiresAt time.Time
	// Request is the exact transfer to sign, as fields rather than a built
	// transaction: the buyer's own public key goes in it, and this node does not
	// have it. Signing anything else is refused - the account name IS the terms,
	// so a different recipient is a different agreement.
	Request *PaymentRequest
}

// ReserveEscrow builds the deposit a buyer signs to fund a job's reservation.
//
// Nothing has run at this point and nothing is charged. The amount is the market
// job's reserved price, which was already affordability-checked when the job was
// submitted - so a buyer who cannot cover it is refused before a provider spends
// any GPU time, exactly as on the other paths.
func (s *Service) ReserveEscrow(jobID string, reserveAuth []byte) (*EscrowPlan, error) {
	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %q", ErrJobNotFound, jobID)
	}
	if job.Status != InferenceJobPending {
		status := job.Status
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: job %q is %s and a reservation is opened before a run",
			ErrJobNotFound, jobID, status)
	}
	buyer, provider, marketJobID := job.Buyer, job.Provider, job.MarketJobID
	s.mu.Unlock()

	mjob, ok := s.market.GetJob(marketJobID)
	if !ok {
		return nil, fmt.Errorf("%w: %q", market.ErrJobNotFound, marketJobID)
	}
	if mjob.Price == 0 {
		return nil, fmt.Errorf("inference: job %q reserved nothing, so there is no escrow to open", jobID)
	}

	terms := token.InferEscrow{
		JobID:    marketJobID,
		Reserved: mjob.Price,
		Expiry:   uint64(time.Now().Add(s.escrowTTL()).Unix()),
		Provider: provider,
		Payer:    buyer,
	}
	if err := terms.Validate(); err != nil {
		return nil, fmt.Errorf("inference: cannot name an escrow for job %q: %w", jobID, err)
	}

	// The SIGNER is the delegate when the buyer is a budget, and the nonce
	// belongs to whoever signs. Deriving it from the buyer would hand a browser
	// a nonce for an account it holds no key for.
	signer, _, err := token.SpenderFor(buyer)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	pr := &PaymentRequest{
		JobID:     jobID,
		From:      signer,
		To:        terms.Account(),
		Amount:    terms.Reserved,
		Nonce:     s.nextNonceFor(signer),
		Timestamp: now.UnixNano(),
		ExpiresAt: now.Add(s.unpaidTTL()),
	}

	s.mu.Lock()
	job.Status = InferenceJobAwaitingDeposit
	job.UpdatedAt = now
	job.escrow = &terms
	job.payment = pr
	job.reserveAuth = append([]byte(nil), reserveAuth...)
	s.mu.Unlock()

	return &EscrowPlan{
		JobID:     jobID,
		Account:   terms.Account(),
		Deposit:   terms.Reserved,
		Provider:  provider,
		Payer:     buyer,
		ExpiresAt: time.Unix(int64(terms.Expiry), 0).UTC(),
		Request:   pr,
	}, nil
}

// FundEscrow submits the buyer's signed deposit and waits for it to commit AND
// apply.
//
// The wait is the whole safety of this path. A deposit sitting in the mempool
// may still be skipped as unaffordable when it is applied, and a provider that
// started streaming against one would be giving the answer away - which is the
// thing every other path is arranged to prevent.
func (s *Service) FundEscrow(ctx context.Context, jobID string, tx *token.Transaction) (*InferenceJob, error) {
	if tx == nil {
		return nil, fmt.Errorf("%w: no transfer supplied", ErrPaymentUnsigned)
	}
	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %q", ErrJobNotFound, jobID)
	}
	if job.Status != InferenceJobAwaitingDeposit || job.payment == nil {
		status := job.Status
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: job %q is %s", ErrNotAwaitingPayment, jobID, status)
	}
	pr := *job.payment
	s.mu.Unlock()

	if err := s.checkSignedAgainst(tx, &pr); err != nil {
		return nil, err
	}
	if err := s.settler.Submit(tx); err != nil {
		s.failJob(jobID)
		return nil, fmt.Errorf("inference: submit the deposit for job %q: %w", jobID, err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, DefaultSettlementTimeout)
	defer cancel()
	committed, applied, err := s.settler.WaitForSettlement(waitCtx, tx)
	if err != nil {
		return s.jobCopy(jobID), fmt.Errorf("inference: waiting for the deposit for job %q: %w", jobID, err)
	}
	if !committed || !applied {
		s.failJob(jobID)
		return s.jobCopy(jobID), fmt.Errorf("inference: the deposit for job %q did not apply", jobID)
	}

	s.mu.Lock()
	// Back to PENDING rather than a state of its own, because from here the job
	// is an ordinary one that may run: the difference is only that it has
	// already been paid the most it can cost.
	job.Status = InferenceJobPending
	job.escrowFunded = true
	job.payment = nil
	job.UpdatedAt = time.Now().UTC()
	cp := job.snapshot()
	s.mu.Unlock()
	return &cp, nil
}

// StreamEscrowed runs a funded job and streams the answer as it is produced,
// then returns the settlement for the buyer's side to sign.
//
// It streams because there is nothing left to withhold. The provider already
// holds the reservation, so the text is not leverage any more - it is a delivery
// against money that has moved.
func (s *Service) StreamEscrowed(ctx context.Context, jobID string, streamAuth []byte, onChunk ChunkFunc) (*PaymentRequest, *StreamResult, error) {
	if onChunk == nil {
		return nil, nil, fmt.Errorf("inference: a chunk callback is required to stream")
	}

	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if !ok {
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("%w: %q", ErrJobNotFound, jobID)
	}
	if !job.escrowFunded || job.escrow == nil {
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("%w: job %q has no funded reservation, so streaming it would "+
			"hand over the answer for nothing", ErrNotAwaitingPayment, jobID)
	}
	if job.Status != InferenceJobPending {
		status := job.Status
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("%w: job %q is %s", ErrJobNotFound, jobID, status)
	}
	// A job id is not a credential. When the reservation was opened with an
	// authorization, the same one has to come back - otherwise anyone who
	// learned the id could race the buyer for an answer the buyer paid for.
	if len(job.reserveAuth) > 0 && !bytes.Equal(job.reserveAuth, streamAuth) {
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("%w: streaming job %q needs the authorization its reservation "+
			"was opened with", ErrRunUnauthorized, jobID)
	}
	buyer, provider, request := job.Buyer, job.Provider, job.Request
	marketJobID := job.MarketJobID
	terms := *job.escrow
	job.Status = InferenceJobRunning
	job.UpdatedAt = time.Now().UTC()
	s.mu.Unlock()

	backend, err := s.registry.Backend(provider)
	if err != nil {
		s.failJob(jobID)
		return nil, nil, fmt.Errorf("%w: %v", ErrNoBackend, err)
	}

	// EVERY DELTA IS KEPT HERE, not only in the caller's hands.
	//
	// A streaming backend returns an empty response when the run ends badly - it
	// has nothing coherent to report - so a run cut short at the hundredth token
	// would otherwise leave this node believing nothing was produced. On a funded
	// reservation that belief is expensive: a job marked FAILED has no settlement,
	// and no settlement means the provider claims the WHOLE reservation at the
	// expiry rather than the fraction the run cost. So the text is accumulated on
	// the way past and used to bill the partial below.
	var delivered strings.Builder
	keeping := func(delta string) error {
		delivered.WriteString(delta)
		return onChunk(delta)
	}

	result, err := streamBackend(ctx, backend, request, keeping, nil)
	cutShort := err != nil
	if cutShort {
		// Not failJob. The buyer paid the cap before the first token, so the only
		// question left is what fraction of it they owe - and answering "all of
		// it, by default" is what failing the job would do.
		result = StreamResult{Response: partialResponse(request, delivered.String())}
		if result.Response.Completion == "" {
			s.failJob(jobID)
			return nil, nil, fmt.Errorf("inference: streaming from provider %q failed before any "+
				"text was produced: %w", provider, err)
		}
	}

	amount, err := s.escrowCharge(marketJobID, request, result.Response, terms.Reserved)
	if err != nil {
		s.failJob(jobID)
		return nil, nil, err
	}

	signer, _, err := token.SpenderFor(buyer)
	if err != nil {
		s.failJob(jobID)
		return nil, nil, err
	}

	now := time.Now().UTC()
	pr := &PaymentRequest{
		JobID:     jobID,
		From:      signer,
		To:        terms.SettleRecipient(),
		Amount:    amount,
		Nonce:     s.nextNonceFor(signer),
		Timestamp: now.UnixNano(),
		Usage:     result.Response.Usage,
		Model:     result.Response.Model,
		// The provider's claim is the real deadline here, and it is on the chain
		// rather than in this process. Naming it keeps a client from thinking it
		// has longer than it does.
		ExpiresAt:       time.Unix(int64(terms.Expiry), 0).UTC(),
		StreamedOneShot: result.StreamedOneShot,
	}

	s.mu.Lock()
	// The completion is recorded but NOT withheld: the caller already received
	// it, chunk by chunk, which is the entire point of this path.
	job.Completion = result.Response.Completion
	job.Reasoning = result.Response.Reasoning
	job.Units = amount
	job.Usage = result.Response.Usage
	job.Model = result.Response.Model
	job.Status = InferenceJobAwaitingPayment
	job.UpdatedAt = now
	job.payment = pr
	job.escrowCutShort = cutShort
	s.mu.Unlock()

	if cutShort {
		// The settlement goes back with the error, because the error is not the
		// end of the story here: the caller is gone, but the job is now settleable
		// and RecoverEscrowSettlement will hand this same request to whoever comes
		// back for it.
		return pr, &result, fmt.Errorf("%w: provider %q", ErrStreamCutShort, provider)
	}
	return pr, &result, nil
}

// ErrStreamCutShort reports a run that stopped before the model was done -
// usually because the buyer hung up - on a job whose reservation is funded.
//
// It is an error and the job is still SETTLEABLE, which is the unusual part: the
// money moved before the work started, so the honest end of a cut-short run is a
// bill for what was produced rather than a forfeited reservation.
var ErrStreamCutShort = errors.New("inference: the stream ended before the model was done")

// RecoverEscrowSettlement returns the settlement for a funded job the buyer is
// no longer streaming.
//
// WHY A SECOND WAY TO GET IT. The settlement rides on the stream's last frame,
// so a buyer who cancels, reloads the page, or loses their connection mid-answer
// never receives it - and a buyer who cannot settle pays the WHOLE reservation
// when the provider claims it, rather than the fraction the run actually cost.
// Without this, offering a cancel button would mean offering to charge full
// price for a partial answer.
//
// It reads. Calling it twice returns the same request, and it runs nothing.
func (s *Service) RecoverEscrowSettlement(jobID string, auth []byte) (*PaymentRequest, *InferenceJob, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.jobs[jobID]
	if !ok {
		return nil, nil, false, fmt.Errorf("%w: %q", ErrJobNotFound, jobID)
	}
	if job.escrow == nil {
		return nil, nil, false, fmt.Errorf("%w: job %q has no reservation, so there is nothing "+
			"here that was paid for in advance", ErrNotAwaitingPayment, jobID)
	}
	// The same credential the stream needs, for the same reason: this call hands
	// back the completion, and a job id is not a secret.
	if len(job.reserveAuth) > 0 && !bytes.Equal(job.reserveAuth, auth) {
		return nil, nil, false, fmt.Errorf("%w: recovering job %q needs the authorization its "+
			"reservation was opened with", ErrRunUnauthorized, jobID)
	}
	if job.Status != InferenceJobAwaitingPayment || job.payment == nil {
		return nil, nil, false, fmt.Errorf("%w: job %q is %s and has no settlement waiting",
			ErrNotAwaitingPayment, jobID, job.Status)
	}
	pr := *job.payment
	cp := job.snapshot()
	return &pr, &cp, job.escrowCutShort, nil
}

// partialResponse bills a run that stopped early for the text that actually
// reached the buyer.
//
// The usage is DERIVED rather than reported, because a backend that errored
// reported none - and it is derived the same way openai_stream.go derives it
// when a vendor omits usage, from text both sides hold. The reasoning is not
// here at all: a reasoning model's working never travels through onChunk, so
// none of it was delivered and none of it is charged. That undercharges a
// cut-short reasoning run, deliberately - the buyer is the one who cannot check
// a number for text they never received.
func partialResponse(req InferenceRequest, delivered string) InferenceResponse {
	if delivered == "" {
		return InferenceResponse{}
	}
	promptTokens := 0
	if msgs, err := req.EffectiveMessages(); err == nil {
		promptTokens = countTokens(promptText(msgs))
	}
	completionTokens := countTokens(delivered)
	usage := Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}
	return InferenceResponse{
		Model:      req.Model,
		Completion: delivered,
		Usage:      usage,
		Units:      UnitsFor(usage),
	}
}

// SettleEscrowed submits the signed settlement and waits for consensus to pay
// the provider and return the change.
func (s *Service) SettleEscrowed(ctx context.Context, jobID string, tx *token.Transaction) (*InferenceJob, error) {
	if tx == nil {
		return nil, fmt.Errorf("%w: no transfer supplied", ErrPaymentUnsigned)
	}
	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %q", ErrJobNotFound, jobID)
	}
	if job.Status != InferenceJobAwaitingPayment || job.payment == nil || job.escrow == nil {
		status := job.Status
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: job %q is %s", ErrNotAwaitingPayment, jobID, status)
	}
	pr := *job.payment
	s.mu.Unlock()

	if err := s.checkSignedAgainst(tx, &pr); err != nil {
		return nil, err
	}
	if err := s.settler.Submit(tx); err != nil {
		return nil, fmt.Errorf("inference: submit the settlement for job %q: %w", jobID, err)
	}

	s.mu.Lock()
	job.Status = InferenceJobSettling
	job.UpdatedAt = time.Now().UTC()
	s.mu.Unlock()

	waitCtx, cancel := context.WithTimeout(ctx, DefaultSettlementTimeout)
	defer cancel()
	committed, applied, err := s.settler.WaitForSettlement(waitCtx, tx)
	if err != nil {
		return s.jobCopy(jobID), fmt.Errorf("inference: waiting for the settlement for job %q: %w", jobID, err)
	}
	if !committed || !applied {
		// NOT failed. The buyer already has the answer and the provider already
		// has the reservation; an unsettled job is one the provider will claim at
		// the expiry, which is a worse outcome for the buyer rather than a lost
		// one for the seller.
		return s.jobCopy(jobID), fmt.Errorf("inference: the settlement for job %q did not apply; "+
			"the provider may claim the whole reservation after %s",
			jobID, time.Unix(int64(job.escrow.Expiry), 0).UTC().Format(time.RFC3339))
	}

	if err := s.market.CancelJob(pr.JobID); err != nil {
		// Consensus moved the money on the same ledger, so completing the market
		// job would charge twice. A stuck reservation is the lesser problem.
		_ = err
	}

	s.mu.Lock()
	job.Status = InferenceJobCompleted
	job.UpdatedAt = time.Now().UTC()
	job.payment = nil
	if price := s.providerPrice(job.Provider); price > 0 {
		job.Receipt = s.issueReceipt(job, pr.Usage, job.Units/price, price, job.Units)
	}
	cp := job.snapshot()
	s.mu.Unlock()
	return &cp, nil
}

// escrowCharge computes what the job actually cost, under both clamps.
//
// Identical arithmetic to the other two paths, deliberately not a third copy of
// the reasoning: the reservation bounds it because that is what was paid, and
// MaxUnitsFor bounds it because a reservation is generous by design and without
// the ceiling a provider bills the whole cap whatever it did.
func (s *Service) escrowCharge(marketJobID string, req InferenceRequest, resp InferenceResponse, reserved uint64) (uint64, error) {
	mjob, ok := s.market.GetJob(marketJobID)
	if !ok {
		return 0, fmt.Errorf("%w: %q", market.ErrJobNotFound, marketJobID)
	}
	billable := resp.Units
	if billable > mjob.Units {
		billable = mjob.Units
	}
	if ceiling := MaxUnitsFor(req, resp.Completion, resp.Reasoning); billable > ceiling {
		billable = ceiling
	}
	amount, err := market.CheckedMul(billable, mjob.PricePerUnit)
	if err != nil {
		return 0, fmt.Errorf("inference: price job %q: %w", marketJobID, err)
	}
	if amount > reserved {
		// Cannot happen from the clamps above, and checked anyway: consensus
		// refuses a settlement over the reservation, so letting one through here
		// would turn a caught bug into a transaction every validator rejects.
		return 0, fmt.Errorf("inference: computed charge %d exceeds the reservation %d for job %q",
			amount, reserved, marketJobID)
	}
	if amount == 0 {
		// A settlement of nothing is refused by consensus, and rightly: it would
		// leave the escrow full and the job looking settled. One base unit is the
		// smallest honest answer for work that was done.
		amount = 1
	}
	return amount, nil
}

// checkSignedAgainst verifies a signed transfer is EXACTLY the one that was
// asked for.
//
// Every signable field, not the amount alone. A buyer could otherwise sign a
// perfectly valid transfer of one base unit to an account they control and have
// it accepted as the deposit.
func (s *Service) checkSignedAgainst(tx *token.Transaction, pr *PaymentRequest) error {
	if err := tx.Verify(); err != nil {
		return fmt.Errorf("%w: %v", ErrPaymentUnsigned, err)
	}
	if tx.SenderID() != pr.From {
		return fmt.Errorf("%w: signed by %s, want %s", ErrPaymentUnsigned, tx.SenderID(), pr.From)
	}
	if tx.To != pr.To {
		return fmt.Errorf("%w: pays %s, want %s", ErrPaymentMismatch, tx.To, pr.To)
	}
	if tx.Amount != pr.Amount {
		return fmt.Errorf("%w: pays %d, want %d", ErrPaymentMismatch, tx.Amount, pr.Amount)
	}
	if tx.Nonce != pr.Nonce || tx.Timestamp != pr.Timestamp {
		return fmt.Errorf("%w: nonce/timestamp do not match the request", ErrPaymentMismatch)
	}
	if len(tx.PrevHash) != len(pr.PrevHash) {
		return fmt.Errorf("%w: prev_hash does not match the request", ErrPaymentMismatch)
	}
	for i := range tx.PrevHash {
		if tx.PrevHash[i] != pr.PrevHash[i] {
			return fmt.Errorf("%w: prev_hash does not match the request", ErrPaymentMismatch)
		}
	}
	return nil
}

func (s *Service) escrowTTL() time.Duration {
	if s.EscrowTTL > 0 {
		return s.EscrowTTL
	}
	return DefaultEscrowTTL
}
