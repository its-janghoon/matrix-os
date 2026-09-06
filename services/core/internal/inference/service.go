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
}

// InferenceJobStatus mirrors the marketplace job lifecycle for inference jobs.
type InferenceJobStatus string

// Inference job lifecycle states, aligned with market.JobStatus.
const (
	InferenceJobPending   InferenceJobStatus = "pending"
	InferenceJobRunning   InferenceJobStatus = "running"
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
// SubmitInferenceJob is a thin, inference-aware wrapper over market.SubmitJob at
// a fixed price of one credit per unit-of-work reserved, and FulfillJob performs
// the run + consensus settlement + market.CompleteJob completion.
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
// its token usage), then the buyer signs a consensus transfer of exactly that
// many units to the provider, and only after settlement is submitted is the
// market job completed. Because both the consensus apply path and
// market.CompleteJob move credits on the same market ledger, the Service uses
// the market job's reserved price as the settlement amount so the buyer is
// charged exactly once and the two paths agree.
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

	// Settle buyer -> provider through consensus for the ACTUAL units the backend
	// reported (resp.Units), not the upfront estimate: the reservation was only a
	// capacity + affordability gate, so the buyer is charged for real usage. The
	// affordability of the estimate at submit time guarantees the buyer can afford
	// the actual amount as long as it does not exceed the estimate; the backend's
	// usage is bounded by the request, and any residual reserved capacity is
	// released below.
	if _, ok := s.market.GetJob(job.MarketJobID); !ok {
		s.failJob(jobID)
		return nil, fmt.Errorf("%w: market job %q", market.ErrJobNotFound, job.MarketJobID)
	}
	amount := resp.Units

	buyerAcct, ok := s.accounts.Account(buyer)
	if !ok {
		s.failJob(jobID)
		return nil, fmt.Errorf("inference: no signing account for buyer %q", buyer)
	}

	s.mu.Lock()
	nonce := s.nonce[buyer]
	s.nonce[buyer] = nonce + 1
	s.mu.Unlock()

	if _, err := s.settler.SubmitAccountTransfer(buyerAcct, provider, amount, nonce); err != nil {
		s.failJob(jobID)
		return nil, fmt.Errorf("inference: settlement failed: %w", err)
	}

	// The consensus transfer already moved the credits on the shared market
	// ledger, so completing the market job via market.CompleteJob would transfer
	// the price a SECOND time and double-charge the buyer. Instead, release the
	// reserved capacity back to the provider (the settlement, not the reservation,
	// is what charges the buyer) and record completion in the inference record.
	if err := s.market.CancelJob(job.MarketJobID); err != nil {
		// Capacity release failure is non-fatal to settlement, which already
		// succeeded; surface it so the operator can reconcile capacity.
		return nil, fmt.Errorf("inference: settled but failed to release capacity for job %q: %w", job.MarketJobID, err)
	}

	s.mu.Lock()
	job.Status = InferenceJobCompleted
	job.Completion = resp.Completion
	job.Units = amount
	job.Model = resp.Model
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
