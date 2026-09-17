package inference

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// Service errors.
var (
	// ErrJobNotFound is returned when an inference job ID is unknown.
	ErrJobNotFound = errors.New("inference: job not found")
	// ErrNoBackend is returned when a provider has no registered inference
	// backend, so it cannot fulfill inference jobs.
	ErrNoBackend = errors.New("inference: provider has no inference backend")
	// ErrUnderReserved is returned when a request is already known, before
	// running it, to cost more units than the caller reserved. The charge is
	// capped at the reservation, so without this the provider would do the
	// remainder of the work unpaid.
	ErrUnderReserved = errors.New("inference: the request costs more than was reserved")
	// ErrPriceAboveCeiling is returned when a budget names a price ceiling and
	// the chosen provider is dearer. It is the buyer's own bound, checked by
	// the node acting for them, because consensus cannot: a draw is an amount
	// with no unit count in it to divide by.
	ErrPriceAboveCeiling = errors.New("inference: the seller charges more than this budget allows")
)

// Settler is the consensus-backed settlement dependency the inference Service
// uses to move the token from buyer to provider when a job completes. It is an
// interface so the Service can be driven by the real consensus engine
// (consensus.Engine.SubmitAccountTransfer, which submits a signed transfer that
// a committed block applies to the market ledger) in production, and by a
// direct-apply fake in tests, without importing internal/consensus here (which
// would create an import cycle: consensus already imports market).
//
// SubmitAccountTransfer returns the signed transaction that was submitted so a
// caller can correlate it with the committed block. The Service only needs the
// call to succeed; it reads settled balances back from the market ledger.
type Settler interface {
	SubmitAccountTransfer(from *token.Account, recipient string, amount, nonce uint64) (*token.Transaction, error)
	// NextNonce reports the nonce this sender should use next, counting what is
	// already in the mempool as well as what has committed. *consensus.Engine has
	// always had it - it is what eth_getTransactionCount answers with - and it is
	// named here because settlement used to invent nonces from a counter of its
	// own instead of asking.
	NextNonce(sender string, includePending bool) uint64
	// Submit submits an ALREADY-SIGNED transfer, for the client-signed path where
	// the node holds no key for the payer. *consensus.Engine has satisfied this
	// all along; it is named in the interface so the Service can settle without
	// custody.
	Submit(tx *token.Transaction) error
	// WaitForSettlement blocks until the submitted transfer is committed to a
	// block and its apply outcome is known, or ctx is done. It reports whether the
	// transfer committed and whether it actually moved credits (applied). The
	// Service uses this to avoid reporting a job COMPLETED for a payment that was
	// only submitted to the mempool and may yet be skipped as unaffordable at
	// apply time.
	WaitForSettlement(ctx context.Context, tx *token.Transaction) (committed bool, applied bool, err error)
}

// DefaultSettlementTimeout bounds how long FulfillJob waits for a submitted
// settlement to commit and apply before reporting the job as still SETTLING
// rather than COMPLETED. It is generous relative to consensus commit latency so
// the common case reports COMPLETED synchronously, while a stalled or dropped
// settlement is reported honestly instead of being claimed complete.
const DefaultSettlementTimeout = 5 * time.Second

// InferenceJobStatus mirrors the marketplace job lifecycle for inference jobs.
type InferenceJobStatus string

// Inference job lifecycle states, aligned with market.JobStatus.
const (
	InferenceJobPending InferenceJobStatus = "pending"
	InferenceJobRunning InferenceJobStatus = "running"
	// InferenceJobSettling means the backend ran and the payment transfer was
	// submitted to consensus, but the settlement has not yet committed+applied. It
	// is a distinct, honest state so an API client never reads COMPLETED for a
	// payment still pending in the mempool. A job in this state moves to COMPLETED
	// once the settlement applies, and the Units/Completion are already populated.
	InferenceJobSettling  InferenceJobStatus = "settling"
	InferenceJobCompleted InferenceJobStatus = "completed"
	InferenceJobFailed    InferenceJobStatus = "failed"
	// InferenceJobAwaitingPayment means the backend ran and the charge is known,
	// but the buyer has not signed for it yet. It is the state the CLIENT-SIGNED
	// path parks in, and it exists because the price of an inference is not
	// knowable until the work is done: a buyer cannot pre-sign a transfer for an
	// amount nobody can compute yet. The completion is withheld in this state -
	// see RunUnsettled.
	InferenceJobAwaitingPayment InferenceJobStatus = "awaiting_payment"
	// InferenceJobAwaitingDeposit means a reservation has been named and the
	// buyer has not funded it yet. It is where the ESCROWED path parks BEFORE
	// anything runs, which is the difference that lets that path stream: by the
	// time a model starts, the provider has already been paid the most the job
	// can cost, so the answer is a delivery rather than leverage.
	InferenceJobAwaitingDeposit InferenceJobStatus = "awaiting_deposit"
)

// InferenceJob is the record of an inference job submitted to the marketplace.
// It couples the marketplace compute job (MarketJobID) with the inference
// request and, once fulfilled, the completion and settled units.
type InferenceJob struct {
	// ID is the inference job identifier (equal to the underlying market job ID).
	ID string
	// MarketJobID is the underlying market.Job ID that reserved capacity.
	MarketJobID string
	// Buyer is the buyer account ID.
	Buyer string
	// Provider is the fulfilling provider ID.
	Provider string
	// Request is the submitted inference request.
	Request InferenceRequest
	// Status is the current lifecycle state.
	Status InferenceJobStatus
	// Completion is the generated text, set once fulfilled.
	Completion string
	// Reasoning is a reasoning model's working, set once fulfilled and empty for
	// a model that has none. It is billed, so it is delivered: the overbilling
	// ceiling counts the text that reached the buyer, and a charge for tokens the
	// buyer never saw is one they cannot check.
	Reasoning string
	// Units is the billed units (== settled token amount), set once fulfilled.
	Units uint64
	// Usage is the token accounting the backend reported, set once fulfilled. It
	// is kept alongside Units because the two answer different questions: Units
	// is what the buyer paid (billable units scaled by the provider's price and
	// clamped to the reservation), while Usage is how much work was done. An
	// OpenAI-compatible response has to report the token counts, and a caller
	// checking a bill needs both numbers to see how one became the other.
	Usage Usage
	// Model is the model that produced the completion.
	Model string
	// Receipt is the serving node's signed account of this sale, set once the
	// payment has actually applied. It is what lets a buyer hold the seller to
	// what it claimed: the model, the token counts, and the money, over this
	// exact prompt and completion. Nil when the node holds no signing key.
	Receipt *Receipt
	// CreatedAt / UpdatedAt track timing.
	CreatedAt time.Time
	UpdatedAt time.Time

	// payment is the transfer this job is waiting for a buyer signature on, set
	// only in the AWAITING_PAYMENT state. Unexported: it is the node's record of
	// what it asked for, and SettleSigned compares an incoming transfer against
	// it, so a caller must not be able to edit it through a returned job copy.
	payment *PaymentRequest
	// escrow is the reservation this job is paid through, on the ESCROWED path
	// only. Its terms are an account NAME, so this is the node's copy of a string
	// that consensus will re-derive from the recipient - kept to build the
	// settlement and to say when the provider may claim.
	escrow *token.InferEscrow
	// reserveAuth is the signature of the RunAuthorization the reservation was
	// opened with, when there was one.
	//
	// Streaming a funded job takes only a job id, and an id is not a secret: it
	// appears in logs, in a URL and in a client's own storage. So the caller has
	// to present the same authorization again, and this is what it is compared
	// against. Compared rather than re-verified, because a RunAuthorization is
	// single-use and verifying it twice would refuse the buyer as a replay.
	reserveAuth []byte
	// escrowFunded records that the deposit COMMITTED AND APPLIED, not merely
	// that it was submitted. Streaming turns on this bit, so anything less than
	// applied would be giving the answer away against money that may yet be
	// skipped as unaffordable.
	escrowFunded bool
}

// Service wires inference into the compute marketplace. A provider advertises an
// inference-capable service by registering a Backend in the Registry; a buyer
// submits an inference job which reserves capacity via market.SubmitJob; the
// provider fulfills it through its Backend; and on completion the computed units
// settle buyer -> provider in the token through the consensus-backed Settler,
// after which the underlying market job is marked COMPLETED.
//
// The Service is deliberately consistent with the existing market job lifecycle:
// SubmitInferenceJob is a thin, inference-aware wrapper over market.SubmitJob
// that reserves capacity and runs the affordability check against the provider's
// PricePerUnit, and FulfillJob performs the run + consensus settlement (scaled by
// PricePerUnit and bounded by the reservation) + confirmation before completion.
type Service struct {
	market   *market.Market
	registry *Registry
	settler  Settler

	// accounts resolves a buyer/provider ID to a signing account. Settlement
	// through consensus requires the payer's private key to sign the transfer, so
	// the Service is given an Accounts resolver rather than assuming key custody.
	accounts Accounts
	// node is the serving node's own key, which signs the receipt handed to a
	// buyer. Not the payout account: that may be a wallet address whose key this
	// node does not hold, and the node is the party answerable for the claim
	// anyway - it is what ran the model.
	node *token.Account

	// unpaidJobTTL bounds how long a job may sit awaiting a buyer's signature
	// before its reservation is released. Zero means DefaultUnpaidJobTTL.
	unpaidJobTTL time.Duration

	// EscrowTTL is how long a funded reservation stays the buyer's to settle
	// before the provider may claim the whole of it. Zero means
	// DefaultEscrowTTL. Exported because it is an operator's judgement about
	// their own slowest model, not a protocol constant.
	EscrowTTL time.Duration

	// runAuth remembers recently used run authorizations, so one cannot be
	// replayed into a second run of free work.
	runAuth *runAuthSeen

	mu   sync.Mutex
	jobs map[string]*InferenceJob
	// nonce is the next nonce this Service has HANDED OUT per buyer, which is not
	// the same question as what the chain has seen. It covers the window between
	// issuing an invoice and that transfer reaching a mempool: several jobs for
	// one buyer in that window would otherwise all be told the same nonce. The
	// chain's own answer is the floor; this only ever raises it.
	nonce map[string]uint64
}

// nextNonceFor picks the nonce a settlement uses, for both the path where this
// node signs and the path where the buyer does.
//
// ASK THE CHAIN, do not count. A nonce is a uniquifier checked against the set
// this sender has already committed, and settlement used to take it from a map
// that starts empty on every node start. A buyer who had ever made a transfer
// therefore could not buy inference at all: their first invoice was always nonce
// zero, the chain had seen zero, and the job failed AFTER the model had produced
// the answer.
//
// The local counter still exists, and only raises the floor. It covers the
// window between issuing an invoice and that transfer reaching a mempool, which
// the chain cannot see and where two concurrent jobs for one buyer would
// otherwise be told the same nonce.
func (s *Service) nextNonceFor(buyer string) uint64 {
	// Read before taking the lock: it reaches into consensus, and reconciling
	// against the issued counter below is what makes a stale answer safe.
	next := s.settler.NextNonce(buyer, true)

	s.mu.Lock()
	defer s.mu.Unlock()
	if issued := s.nonce[buyer]; issued > next {
		next = issued
	}
	s.nonce[buyer] = next + 1
	return next
}

// snapshot returns a copy of the job safe to hand to a caller. It drops the
// payment record: SettleSigned compares an incoming transfer against that record
// to decide whether the buyer paid the invoice, and a shallow copy shares the
// pointer, so an in-process caller holding a returned job could lower the
// invoice it is about to be checked against. A caller that needs the payment
// gets it returned explicitly from RunUnsettled or PrepareSettlement.
func (j *InferenceJob) snapshot() InferenceJob {
	cp := *j
	cp.payment = nil
	return cp
}

// Accounts resolves an account ID to its signing token.Account. A provider node
// holds keys for the buyer accounts it settles on behalf of (or, in a hosted
// deployment, a custodial wallet); tests supply an in-memory resolver.
type Accounts interface {
	// Account returns the signing account for id, and whether it is known.
	Account(id string) (*token.Account, bool)
}

// Config configures a Service.
type Config struct {
	// Market is the compute marketplace engine (required). Inference jobs reserve
	// and complete capacity through it, and settle on its ledger.
	Market *market.Market
	// Registry maps provider IDs to their inference Backend (required).
	Registry *Registry
	// Settler performs consensus-backed settlement of the token (required).
	Settler Settler
	// Accounts resolves buyer accounts to their signing keys for settlement
	// (required).
	Accounts Accounts
	// Node is the serving node's own account, used to sign the receipt it hands
	// a buyer for each settled job. Optional: with no key the service issues no
	// receipts rather than unsigned ones, because a receipt nobody signed is a
	// claim with no author, which is what there was before.
	Node *token.Account
}

// NewService constructs an inference Service.
func NewService(cfg Config) (*Service, error) {
	if cfg.Market == nil {
		return nil, fmt.Errorf("inference: market is required")
	}
	if cfg.Registry == nil {
		return nil, fmt.Errorf("inference: registry is required")
	}
	if cfg.Settler == nil {
		return nil, fmt.Errorf("inference: settler is required")
	}
	if cfg.Accounts == nil {
		return nil, fmt.Errorf("inference: accounts resolver is required")
	}
	return &Service{
		market:   cfg.Market,
		registry: cfg.Registry,
		settler:  cfg.Settler,
		accounts: cfg.Accounts,
		node:     cfg.Node,
		jobs:     make(map[string]*InferenceJob),
		nonce:    make(map[string]uint64),
		runAuth:  newRunAuthSeen(),
	}, nil
}

// Registry exposes the backend registry so a node can advertise inference
// providers (register a provider on the market and its backend here).
func (s *Service) Registry() *Registry { return s.registry }

// SubmitInferenceJob reserves capacity for an inference job and records it in a
// PENDING state. It reserves `units` capacity on the provider through
// market.SubmitJob (which performs the affordability check against the buyer's
// balance and reserves capacity), where units is an upfront estimate of the work
// the request will cost. No token moves at submit time; settlement happens on
// FulfillJob. The provider must have a registered inference backend.
func (s *Service) SubmitInferenceJob(buyer, providerID string, req InferenceRequest, unitsEstimate uint64) (*InferenceJob, error) {
	if _, err := req.EffectiveMessages(); err != nil {
		return nil, err
	}
	if _, err := s.registry.Backend(providerID); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoBackend, err)
	}
	if unitsEstimate == 0 {
		unitsEstimate = 1
	}
	// Refuse work that is already, before running, known to cost more than was
	// reserved. FulfillJob clamps the charge DOWN to the reservation, so without
	// this a buyer reserves one unit, sends a prompt worth thousands, and the
	// provider does all of it for one unit's pay. See MinUnitsFor.
	if minUnits := MinUnitsFor(req); minUnits > unitsEstimate {
		return nil, fmt.Errorf("%w: this request needs at least %d units but only %d were reserved; "+
			"reserve at least that many, because the charge is capped at the reservation and the "+
			"provider would otherwise do the rest of the work unpaid",
			ErrUnderReserved, minUnits, unitsEstimate)
	}

	mjob, err := s.market.SubmitJob(buyer, providerID, unitsEstimate)
	if err != nil {
		return nil, err
	}
	// A budget carries the buyer's price ceiling, and this is the one moment
	// both numbers are in the same place. Consensus cannot check it - a draw is
	// an amount with no unit count in it to divide by - so it is checked here,
	// by the node acting for the buyer. That bounds an honest purchase against
	// an expensive market; what bounds a stolen delegate key is the budget's
	// balance and its expiry, which consensus does check.
	if terms, err := token.ParseSpendEscrow(buyer); err == nil && mjob.PricePerUnit > terms.MaxPricePerUnit {
		return nil, fmt.Errorf("%w: %s charges %d per unit and this budget's ceiling is %d",
			ErrPriceAboveCeiling, providerID, mjob.PricePerUnit, terms.MaxPricePerUnit)
	}

	now := time.Now().UTC()
	job := &InferenceJob{
		ID:          mjob.ID,
		MarketJobID: mjob.ID,
		Buyer:       buyer,
		Provider:    providerID,
		Request:     req,
		Status:      InferenceJobPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.mu.Lock()
	s.jobs[job.ID] = job
	s.mu.Unlock()

	cp := job.snapshot()
	return &cp, nil
}

// FulfillJob runs the inference for a pending job through the provider's
// backend, settles the computed units buyer -> provider through the
// consensus-backed Settler, and marks the underlying market job COMPLETED.
//
// Ordering rationale: the backend runs first (producing the real completion and
// its token usage), then the buyer signs a consensus transfer for a charge that
// is scaled by the provider's PricePerUnit and clamped to the reserved,
// affordability-checked price, and the job is marked SETTLING. Only after the
// consensus settlement is confirmed to have committed AND applied is the job
// marked COMPLETED; if the transfer is skipped as unaffordable at apply time the
// job is reported FAILED, never COMPLETED. Because both the consensus apply path
// and market.CompleteJob would move credits on the same market ledger, the
// Service settles once through consensus and releases the reservation rather
// than calling CompleteJob, so the buyer is charged exactly once.
func (s *Service) FulfillJob(ctx context.Context, jobID string) (*InferenceJob, error) {
	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %q", ErrJobNotFound, jobID)
	}
	if job.Status != InferenceJobPending && job.Status != InferenceJobRunning {
		s.mu.Unlock()
		return nil, fmt.Errorf("inference: job %q in state %q cannot be fulfilled", jobID, job.Status)
	}
	job.Status = InferenceJobRunning
	job.UpdatedAt = nowUTC()
	reqCopy := job.Request
	provider := job.Provider
	s.mu.Unlock()

	backend, err := s.registry.Backend(provider)
	if err != nil {
		s.failJob(jobID)
		return nil, fmt.Errorf("%w: %v", ErrNoBackend, err)
	}

	resp, err := backend.Infer(ctx, reqCopy)
	if err != nil {
		s.failJob(jobID)
		return nil, fmt.Errorf("inference: backend %q failed: %w", backend.Name(), err)
	}

	return s.settleRun(ctx, jobID, resp)
}

// settleRun charges for a completed run and confirms the payment. It is the half
// of FulfillJob that follows the model producing an answer, extracted so the
// streaming path (StreamJob) shares one implementation rather than carrying a
// second copy of the charge computation. The clamping to the reservation and the
// commit-and-apply confirmation are precisely the properties a duplicate would
// eventually get wrong.
//
// It settles the HOSTED way: the node signs the transfer with a key it holds for
// the buyer. The client-signed path is settle.go, and it necessarily computes
// its charge at a different moment, which is why that one is separate.
func (s *Service) settleRun(ctx context.Context, jobID string, resp InferenceResponse) (*InferenceJob, error) {
	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %q", ErrJobNotFound, jobID)
	}
	buyer, provider, marketJobID := job.Buyer, job.Provider, job.MarketJobID
	s.mu.Unlock()

	// Determine the amount to charge. The market reservation ran the affordability
	// check against the reserved PRICE = unitsEstimate * PricePerUnit, so the
	// settled charge must be (a) scaled by the provider's PricePerUnit and (b)
	// bounded by that reserved price. We therefore:
	//   1. clamp the billable units to the reserved estimate (a backend that
	//      reports more usage than estimated is capped at what was reserved, never
	//      silently over-charging the buyer), and
	//   2. multiply by PricePerUnit to get the credit amount.
	mjob, ok := s.market.GetJob(marketJobID)
	if !ok {
		s.failJob(jobID)
		return nil, fmt.Errorf("%w: market job %q", market.ErrJobNotFound, marketJobID)
	}

	billableUnits := resp.Units
	if billableUnits > mjob.Units {
		// Backend reported more usage than was reserved; cap at the reservation so
		// the buyer is never charged more than it agreed to and was checked for.
		billableUnits = mjob.Units
	}
	// And cap at what the work can honestly have cost, which the reservation does
	// not bound at all.
	//
	// A reservation is a budget, deliberately generous: a request with no
	// max_tokens reserves room for a long answer that may never arrive. Clamping
	// only to it left a provider free to bill the whole budget whatever it did -
	// answer "hello" to "hi", report a thousand tokens, and every check passes
	// because the report is under the reservation and the reservation was
	// affordability-checked. Nothing was comparing the bill to the answer.
	//
	// This node holds both the prompt it sent and the completion it got, so it
	// can. Not the true token count - it does not know the provider's tokeniser -
	// but an upper bound, which is all that is needed to stop a bill two orders
	// of magnitude past the work.
	if ceiling := MaxUnitsFor(job.Request, resp.Completion, resp.Reasoning); billableUnits > ceiling {
		billableUnits = ceiling
	}
	amount, err := market.CheckedMul(billableUnits, mjob.PricePerUnit)
	if err != nil {
		s.failJob(jobID)
		return nil, fmt.Errorf("inference: price job %q from quote %s/%d: %w", jobID, mjob.QuoteID, mjob.QuoteVersion, err)
	}
	// Defensive re-check: the charge must not exceed the reserved, affordability-
	// checked price. Clamping units to the estimate guarantees this, but assert it
	// so a future pricing change cannot silently reintroduce an over-charge.
	if amount > mjob.Price {
		s.failJob(jobID)
		return nil, fmt.Errorf("inference: computed charge %d exceeds reserved price %d for job %q", amount, mjob.Price, marketJobID)
	}

	// Under a budget the node holds the DELEGATE's key and not the buyer's,
	// which is the scoped-custody shape: a key that can spend a capped, expiring
	// grant on inference, rather than a wallet key that can move everything.
	signer, payTo := invoiceParties(buyer, provider)
	signerAcct, ok := s.accounts.Account(signer)
	if !ok {
		s.failJob(jobID)
		return nil, fmt.Errorf("inference: no signing account for %q", signer)
	}

	nonce := s.nextNonceFor(signer)

	tx, err := s.settler.SubmitAccountTransfer(signerAcct, payTo, amount, nonce)
	if err != nil {
		s.failJob(jobID)
		return nil, fmt.Errorf("inference: settlement failed: %w", err)
	}

	// The settlement only moves credits when a consensus block commits and applies
	// it; commitAndApply deterministically SKIPS an unaffordable transfer. So we
	// must not report COMPLETED until we have confirmed the transfer actually
	// applied. Mark the job SETTLING, record the billed units/completion, and wait
	// (bounded) for the settlement to finalise.
	s.mu.Lock()
	job.Status = InferenceJobSettling
	job.Completion = resp.Completion
	job.Reasoning = resp.Reasoning
	job.Units = amount
	job.Usage = resp.Usage
	job.Model = resp.Model
	job.UpdatedAt = nowUTC()
	s.mu.Unlock()

	waitCtx, cancel := context.WithTimeout(ctx, DefaultSettlementTimeout)
	defer cancel()
	committed, applied, werr := s.settler.WaitForSettlement(waitCtx, tx)
	if werr != nil {
		// We could not confirm the settlement within the timeout (or ctx ended).
		// Leave the job in SETTLING: the transfer may still commit later, and a
		// caller polling GetJob will observe COMPLETED only once it truly applies.
		// We deliberately do NOT release capacity or claim completion here.
		s.mu.Lock()
		cp := job.snapshot()
		s.mu.Unlock()
		return &cp, nil
	}
	if !committed || !applied {
		// The transfer committed but was skipped as unaffordable (or did not
		// commit): no credits moved. Report the job FAILED and release the reserved
		// capacity. This is the honest outcome instead of a COMPLETED job whose
		// payment never landed.
		s.failJob(jobID)
		s.mu.Lock()
		cp := job.snapshot()
		s.mu.Unlock()
		return &cp, fmt.Errorf("inference: settlement for job %q did not apply (payment skipped as unaffordable)", jobID)
	}

	// Settlement applied: credits moved buyer -> provider on the shared market
	// ledger. Completing the market job via market.CompleteJob would transfer the
	// price a SECOND time and double-charge the buyer, so instead release the
	// reserved capacity (the settlement, not the reservation, charged the buyer)
	// and mark the inference job COMPLETED.
	if err := s.market.CancelJob(marketJobID); err != nil {
		// Capacity release failure is non-fatal to settlement, which already
		// applied; surface it so the operator can reconcile capacity.
		return nil, fmt.Errorf("inference: settled but failed to release capacity for job %q: %w", marketJobID, err)
	}

	s.mu.Lock()
	job.Status = InferenceJobCompleted
	job.UpdatedAt = nowUTC()
	// Issued here and not earlier, because a receipt is an account of a SALE. A
	// job whose payment was skipped as unaffordable was not one, and a signed
	// document saying otherwise would be the node's own word against its chain.
	job.Receipt = s.issueReceipt(job, resp.Usage, billableUnits, mjob.PricePerUnit, amount)
	cp := job.snapshot()
	s.mu.Unlock()

	return &cp, nil
}

// nowUTC is the one clock this package reads, so every timestamp it writes is
// in the same zone.
func nowUTC() time.Time { return time.Now().UTC() }

// failJob marks a job FAILED and returns any reserved market capacity.
func (s *Service) failJob(jobID string) {
	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if ok {
		job.Status = InferenceJobFailed
		job.UpdatedAt = time.Now().UTC()
	}
	marketJobID := ""
	if ok {
		marketJobID = job.MarketJobID
	}
	s.mu.Unlock()
	if marketJobID != "" {
		_ = s.market.CancelJob(marketJobID)
	}
}

// GetJob returns a copy of the inference job by ID.
func (s *Service) GetJob(jobID string) (*InferenceJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok {
		return nil, false
	}
	cp := job.snapshot()
	// WITHHELD HERE, not by each caller, and that placement is the fix.
	//
	// It used to be each caller. RunInferenceJob blanked the completion by hand;
	// GetInferenceJob did not - and a Get is PUBLIC when connect.public_reads is
	// on, which a self-custody page requires because a browser cannot hold an
	// API key. So a buyer could run a job, take the id it was handed back, ask
	// for the job instead of signing for it, and read the completion it never
	// paid for. Withholding is the whole of the enforcement on the client-signed
	// path, because the provider has already done the work by then.
	//
	// It also survived a field added later. When a reasoning model's working
	// became part of a job, the caller that stripped Completion knew nothing of
	// Reasoning, so the working went out before payment - most of what is billed,
	// and for some models the answer worked out in full. A strip repeated per
	// caller is a list that grows wrong; one at the source cannot.
	//
	// Internal callers read s.jobs directly and are unaffected: settling needs
	// the text to compute the digest it charges for.
	if cp.Status == InferenceJobAwaitingPayment {
		cp.Completion = ""
		cp.Reasoning = ""
	}
	return &cp, true
}
