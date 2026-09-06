package market

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/google/uuid"
)

// KV key prefixes for persisted marketplace entities.
const (
	providerKeyPrefix = "market/provider/"
	jobKeyPrefix      = "market/job/"
)

// JobStatus is a string-typed enum describing the lifecycle of a compute job.
type JobStatus string

// Job lifecycle states.
const (
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobCompleted JobStatus = "completed"
	JobFailed    JobStatus = "failed"
	JobCancelled JobStatus = "cancelled"
)

// Provider represents an idle-machine compute provider that has registered
// capacity on the marketplace.
type Provider struct {
	ID           string `json:"id"`
	Capacity     uint64 `json:"capacity"`
	PricePerUnit uint64 `json:"price_per_unit"`
	Available    uint64 `json:"available"`
}

// Job represents a paid compute job submitted by a buyer against a provider.
type Job struct {
	ID       string    `json:"id"`
	Buyer    string    `json:"buyer"`
	Provider string    `json:"provider"`
	Units    uint64    `json:"units"`
	Price    uint64    `json:"price"`
	Status   JobStatus `json:"status"`
}

// Market is the compute-job marketplace engine. It owns a credits Ledger and
// in-memory indexes of providers and jobs, each guarded by its own RWMutex
// following the map+mutex field convention used in node.go. All entities are
// persisted through the Pebble kv.Store as JSON.
type Market struct {
	store  *kv.Store
	ledger *Ledger

	providersMu sync.RWMutex
	providers   map[string]Provider

	jobsMu sync.RWMutex
	jobs   map[string]Job
}

// NewMarket creates a new Market backed by the given store, constructing the
// credits ledger internally.
func NewMarket(store *kv.Store) *Market {
	return &Market{
		store:     store,
		ledger:    NewLedger(store),
		providers: make(map[string]Provider),
		jobs:      make(map[string]Job),
	}
}

// Ledger exposes the underlying compute-credits ledger so callers can credit
// buyer accounts and inspect balances.
func (m *Market) Ledger() *Ledger {
	return m.ledger
}

// persistProvider writes a provider to the KV store as JSON.
func (m *Market) persistProvider(p Provider) error {
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("failed to marshal provider %q: %w", p.ID, err)
	}
	if err := m.store.Put([]byte(providerKeyPrefix+p.ID), data); err != nil {
		return fmt.Errorf("failed to persist provider %q: %w", p.ID, err)
	}
	return nil
}

// persistJob writes a job to the KV store as JSON.
func (m *Market) persistJob(j Job) error {
	data, err := json.Marshal(j)
	if err != nil {
		return fmt.Errorf("failed to marshal job %q: %w", j.ID, err)
	}
	if err := m.store.Put([]byte(jobKeyPrefix+j.ID), data); err != nil {
		return fmt.Errorf("failed to persist job %q: %w", j.ID, err)
	}
	return nil
}

// RegisterProvider validates and registers a compute provider. PricePerUnit and
// Capacity must both be greater than zero. Available is initialized to Capacity.
func (m *Market) RegisterProvider(p Provider) error {
	if p.ID == "" {
		return fmt.Errorf("provider id must not be empty: %w", ErrInvalidProvider)
	}
	if p.Capacity == 0 {
		return fmt.Errorf("provider %q capacity must be > 0: %w", p.ID, ErrInvalidProvider)
	}
	if p.PricePerUnit == 0 {
		return fmt.Errorf("provider %q price_per_unit must be > 0: %w", p.ID, ErrInvalidProvider)
	}

	p.Available = p.Capacity

	m.providersMu.Lock()
	defer m.providersMu.Unlock()

	if err := m.persistProvider(p); err != nil {
		return err
	}
	m.providers[p.ID] = p
	return nil
}

// SubmitJob reserves capacity for a paid compute job. It looks up the provider
// (ErrProviderNotFound if unknown), verifies available capacity
// (ErrInsufficientCapacity), computes price = units * PricePerUnit, and verifies
// the buyer can afford it (ErrInsufficientFunds). No credits move at submit
// time; the buyer is only charged on completion. On success it decrements the
// provider's Available capacity, creates a pending Job with a generated ID, and
// persists both.
func (m *Market) SubmitJob(buyer, providerID string, units uint64) (*Job, error) {
	if units == 0 {
		return nil, fmt.Errorf("units must be > 0: %w", ErrInsufficientCapacity)
	}

	m.providersMu.Lock()
	defer m.providersMu.Unlock()

	provider, ok := m.providers[providerID]
	if !ok {
		return nil, fmt.Errorf("submit job for provider %q: %w", providerID, ErrProviderNotFound)
	}
	if provider.Available < units {
		return nil, fmt.Errorf("provider %q has %d units available, need %d: %w",
			providerID, provider.Available, units, ErrInsufficientCapacity)
	}

	price := units * provider.PricePerUnit

	balance, err := m.ledger.Balance(buyer)
	if err != nil {
		return nil, err
	}
	if balance < price {
		return nil, fmt.Errorf("buyer %q has %d credits, job costs %d: %w",
			buyer, balance, price, ErrInsufficientFunds)
	}

	job := Job{
		ID:       uuid.NewString(),
		Buyer:    buyer,
		Provider: providerID,
		Units:    units,
		Price:    price,
		Status:   JobPending,
	}

	// Reserve capacity on a copy first so a persistence failure does not mutate
	// the in-memory provider or create a job.
	reserved := provider
	reserved.Available -= units

	if err := m.persistProvider(reserved); err != nil {
		return nil, err
	}

	m.jobsMu.Lock()
	defer m.jobsMu.Unlock()
	if err := m.persistJob(job); err != nil {
		// Roll back the reservation persistence to keep capacity consistent.
		_ = m.persistProvider(provider)
		return nil, err
	}

	m.providers[providerID] = reserved
	m.jobs[job.ID] = job

	jobCopy := job
	return &jobCopy, nil
}

// CompleteJob transitions a pending or running job to JobCompleted and transfers
// its price from buyer to provider through the ledger. If the transfer fails the
// job is left non-completed and the error is returned.
func (m *Market) CompleteJob(jobID string) error {
	m.jobsMu.Lock()
	defer m.jobsMu.Unlock()

	job, ok := m.jobs[jobID]
	if !ok {
		return fmt.Errorf("complete job %q: %w", jobID, ErrJobNotFound)
	}
	if job.Status != JobPending && job.Status != JobRunning {
		return fmt.Errorf("complete job %q in state %q: %w", jobID, job.Status, ErrInvalidJobState)
	}

	// Transfer credits first; only mark completed if the transfer succeeds so a
	// failed transfer leaves the job non-completed.
	if err := m.ledger.Transfer(job.Buyer, job.Provider, job.Price); err != nil {
		return fmt.Errorf("complete job %q: %w", jobID, err)
	}

	job.Status = JobCompleted
	if err := m.persistJob(job); err != nil {
		return err
	}
	m.jobs[jobID] = job
	return nil
}

// CancelJob cancels a pending or running job, returning the reserved capacity to
// its provider. No credits are transferred.
func (m *Market) CancelJob(jobID string) error {
	m.jobsMu.Lock()
	defer m.jobsMu.Unlock()

	job, ok := m.jobs[jobID]
	if !ok {
		return fmt.Errorf("cancel job %q: %w", jobID, ErrJobNotFound)
	}
	if job.Status != JobPending && job.Status != JobRunning {
		return fmt.Errorf("cancel job %q in state %q: %w", jobID, job.Status, ErrInvalidJobState)
	}

	m.providersMu.Lock()
	defer m.providersMu.Unlock()

	if provider, ok := m.providers[job.Provider]; ok {
		restored := provider
		restored.Available += job.Units
		if restored.Available > restored.Capacity {
			restored.Available = restored.Capacity
		}
		if err := m.persistProvider(restored); err != nil {
			return err
		}
		m.providers[job.Provider] = restored
	}

	job.Status = JobCancelled
	if err := m.persistJob(job); err != nil {
		return err
	}
	m.jobs[jobID] = job
	return nil
}

// GetProvider returns a provider by ID and whether it exists.
func (m *Market) GetProvider(id string) (Provider, bool) {
	m.providersMu.RLock()
	defer m.providersMu.RUnlock()
	p, ok := m.providers[id]
	return p, ok
}

// GetJob returns a job by ID and whether it exists.
func (m *Market) GetJob(id string) (Job, bool) {
	m.jobsMu.RLock()
	defer m.jobsMu.RUnlock()
	j, ok := m.jobs[id]
	return j, ok
}

// ListProviders returns all registered providers sorted by ID.
func (m *Market) ListProviders() []Provider {
	m.providersMu.RLock()
	defer m.providersMu.RUnlock()

	out := make([]Provider, 0, len(m.providers))
	for _, p := range m.providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ListJobs returns all jobs sorted by ID.
func (m *Market) ListJobs() []Job {
	m.jobsMu.RLock()
	defer m.jobsMu.RUnlock()

	out := make([]Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
