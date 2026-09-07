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
	// CreatedAt / UpdatedAt track timing.
	CreatedAt time.Time
	UpdatedAt time.Time
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

	mu    sync.Mutex
	jobs  map[string]*InferenceJob
	nonce map[string]uint64
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
		jobs:     make(map[string]*InferenceJob),
		nonce:    make(map[string]uint64),
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

	mjob, err := s.market.SubmitJob(buyer, providerID, unitsEstimate)
	if err != nil {
		return nil, err
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

	cp := *job
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
	job.UpdatedAt = time.Now().UTC()
	reqCopy := job.Request
	buyer := job.Buyer
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

	// Determine the amount to charge. The market reservation ran the affordability
	// check against the reserved PRICE = unitsEstimate * PricePerUnit, so the
	// settled charge must be (a) scaled by the provider's PricePerUnit and (b)
	// bounded by that reserved price. We therefore:
	//   1. clamp the billable units to the reserved estimate (a backend that
	//      reports more usage than estimated is capped at what was reserved, never
	//      silently over-charging the buyer), and
	//   2. multiply by PricePerUnit to get the credit amount.
	// This keeps the charged quantity identical to what was reserved and
	// affordability-checked, closing the gap where raw resp.Units (unscaled,
	// unbounded) was settled.
	mjob, ok := s.market.GetJob(job.MarketJobID)
	if !ok {
		s.failJob(jobID)
		return nil, fmt.Errorf("%w: market job %q", market.ErrJobNotFound, job.MarketJobID)
	}
	prov, ok := s.market.GetProvider(provider)
	if !ok {
		s.failJob(jobID)
		return nil, fmt.Errorf("%w: %q", market.ErrProviderNotFound, provider)
	}

	billableUnits := resp.Units
	if billableUnits > mjob.Units {
		// Backend reported more usage than was reserved; cap at the reservation so
		// the buyer is never charged more than it agreed to and was checked for.
		billableUnits = mjob.Units
	}
	amount := billableUnits * prov.PricePerUnit
	// Defensive re-check: the charge must not exceed the reserved, affordability-
	// checked price. Clamping units to the estimate guarantees this, but assert it
	// so a future pricing change cannot silently reintroduce an over-charge.
	if amount > mjob.Price {
		s.failJob(jobID)
		return nil, fmt.Errorf("inference: computed charge %d exceeds reserved price %d for job %q", amount, mjob.Price, job.MarketJobID)
	}

	buyerAcct, ok := s.accounts.Account(buyer)
	if !ok {
		s.failJob(jobID)
		return nil, fmt.Errorf("inference: no signing account for buyer %q", buyer)
	}

	s.mu.Lock()
	nonce := s.nonce[buyer]
	s.nonce[buyer] = nonce + 1
	s.mu.Unlock()

	tx, err := s.settler.SubmitAccountTransfer(buyerAcct, provider, amount, nonce)
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
	job.Units = amount
	job.Usage = resp.Usage
	job.Model = resp.Model
	job.UpdatedAt = time.Now().UTC()
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
		cp := *job
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
		cp := *job
		s.mu.Unlock()
		return &cp, fmt.Errorf("inference: settlement for job %q did not apply (payment skipped as unaffordable)", jobID)
	}

	// Settlement applied: credits moved buyer -> provider on the shared market
	// ledger. Completing the market job via market.CompleteJob would transfer the
	// price a SECOND time and double-charge the buyer, so instead release the
	// reserved capacity (the settlement, not the reservation, charged the buyer)
	// and mark the inference job COMPLETED.
	if err := s.market.CancelJob(job.MarketJobID); err != nil {
		// Capacity release failure is non-fatal to settlement, which already
		// applied; surface it so the operator can reconcile capacity.
		return nil, fmt.Errorf("inference: settled but failed to release capacity for job %q: %w", job.MarketJobID, err)
	}

	s.mu.Lock()
	job.Status = InferenceJobCompleted
	job.UpdatedAt = time.Now().UTC()
	cp := *job
	s.mu.Unlock()

	return &cp, nil
}

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
	cp := *job
	return &cp, true
}
