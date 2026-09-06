package market

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

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
// CreatedAt records submit time and UpdatedAt the last status transition, giving
// ListJobs a stable chronological order and basic auditability.
type Job struct {
	ID        string    `json:"id"`
	Buyer     string    `json:"buyer"`
	Provider  string    `json:"provider"`
	Units     uint64    `json:"units"`
	Price     uint64    `json:"price"`
	Status    JobStatus `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Observer receives notifications about marketplace activity so an integration
// layer (the node) can update observability such as Prometheus metrics without
// this package importing the metrics package. All methods are called after the
// corresponding state change has been persisted and applied in memory, and must
// be safe for concurrent use. A nil Observer disables notifications.
//
// The interface is defined here but implemented by the node, which keeps
// internal/market free of any internal/metrics import.
type Observer interface {
	// ProviderCountChanged reports the current number of registered providers.
	ProviderCountChanged(count int)
	// ActiveJobsChanged reports the current number of pending or running jobs.
	ActiveJobsChanged(count int)
	// JobCompleted reports that one job settled to completion, transferring the
	// given amount of native MATRIX (in native base units) from buyer to
	// provider.
	JobCompleted(amount uint64)
}

// Market is the compute-job marketplace engine. It owns the native MATRIX
// Ledger and in-memory indexes of providers and jobs, each guarded by its own
// RWMutex following the map+mutex field convention used in node.go. All entities
// are persisted through the Pebble kv.Store as JSON.
type Market struct {
	store  *kv.Store
	ledger *Ledger

	observer Observer

	providersMu sync.RWMutex
	providers   map[string]Provider

	jobsMu sync.RWMutex
	jobs   map[string]Job
}

// NewMarket creates a new Market backed by the given store, constructing the
// native MATRIX ledger internally. It rehydrates any providers and jobs previously
// persisted to the store so a Market built over an existing store observes the
// durable marketplace state rather than starting empty. Over a fresh store this
// is a no-op and the maps start empty.
func NewMarket(store *kv.Store) (*Market, error) {
	m := &Market{
		store:     store,
		ledger:    NewLedger(store),
		providers: make(map[string]Provider),
		jobs:      make(map[string]Job),
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

// load rehydrates the in-memory provider and job indexes from the KV store by
// scanning the persisted key prefixes. It is called once during construction so
// a node restart recovers the marketplace state that write paths persisted.
func (m *Market) load() error {
	if err := m.store.Iterate([]byte(providerKeyPrefix), func(_, value []byte) error {
		var p Provider
		if err := json.Unmarshal(value, &p); err != nil {
			return fmt.Errorf("failed to unmarshal persisted provider: %w", err)
		}
		m.providers[p.ID] = p
		return nil
	}); err != nil {
		return err
	}

	if err := m.store.Iterate([]byte(jobKeyPrefix), func(_, value []byte) error {
		var j Job
		if err := json.Unmarshal(value, &j); err != nil {
			return fmt.Errorf("failed to unmarshal persisted job: %w", err)
		}
		m.jobs[j.ID] = j
		return nil
	}); err != nil {
		return err
	}

	return nil
}

// Ledger exposes the underlying native MATRIX ledger so callers can inspect
// balances and perform internal balance adjustments. Honest issuance of new
// native MATRIX goes through token.Treasury, not raw ledger credits.
func (m *Market) Ledger() *Ledger {
	return m.ledger
}

// SetObserver registers an Observer that receives marketplace-activity
// notifications. Passing nil disables notifications. It is intended to be called
// once during node setup before the market handles traffic.
func (m *Market) SetObserver(o Observer) {
	m.observer = o
}

// SyncMetrics pushes the current provider and active-job counts to the
// registered observer. It is safe to call at any time and is used at startup to
// seed gauges from rehydrated state. It is a no-op when no observer is set.
func (m *Market) SyncMetrics() {
	if m.observer == nil {
		return
	}
	m.observer.ProviderCountChanged(len(m.ListProviders()))
	m.observer.ActiveJobsChanged(m.countActiveJobs())
}

// countActiveJobs returns the number of jobs in a pending or running state.
func (m *Market) countActiveJobs() int {
	m.jobsMu.RLock()
	defer m.jobsMu.RUnlock()

	active := 0
	for _, j := range m.jobs {
		if j.Status == JobPending || j.Status == JobRunning {
			active++
		}
	}
	return active
}

// notifyProviderCount reports the current provider count to the observer.
func (m *Market) notifyProviderCount() {
	if m.observer == nil {
		return
	}
	m.observer.ProviderCountChanged(len(m.ListProviders()))
}

// notifyActiveJobs reports the current active-job count to the observer.
func (m *Market) notifyActiveJobs() {
	if m.observer == nil {
		return
	}
	m.observer.ActiveJobsChanged(m.countActiveJobs())
}

// notifyJobCompleted reports a settled job and the native MATRIX it transferred.
func (m *Market) notifyJobCompleted(amount uint64) {
	if m.observer == nil {
		return
	}
	m.observer.JobCompleted(amount)
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
	if err := m.persistProvider(p); err != nil {
		m.providersMu.Unlock()
		return err
	}
	m.providers[p.ID] = p
	m.providersMu.Unlock()

	// Notify after releasing the lock so the observer's read of the provider
	// count does not deadlock against the write lock held above.
	m.notifyProviderCount()
	return nil
}

// SubmitJob reserves capacity for a paid compute job. It looks up the provider
// (ErrProviderNotFound if unknown), verifies available capacity
// (ErrInsufficientCapacity), computes price = units * PricePerUnit, and verifies
// the buyer can afford it (ErrInsufficientFunds). No native MATRIX moves at
// submit time; the buyer is only charged on completion. On success it decrements the
// provider's Available capacity, creates a pending Job with a generated ID, and
// persists both.
func (m *Market) SubmitJob(buyer, providerID string, units uint64) (*Job, error) {
	if units == 0 {
		return nil, fmt.Errorf("units must be > 0: %w", ErrInsufficientCapacity)
	}
	// Reject self-dealing: a buyer settling a job against their own provider
	// account would transfer native MATRIX from an account to itself on completion,
	// which is economically meaningless and would otherwise exercise the
	// self-transfer path in the ledger.
	if buyer == providerID {
		return nil, fmt.Errorf("buyer %q equals provider %q: %w", buyer, providerID, ErrSelfDealing)
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
		return nil, fmt.Errorf("buyer %q has %d native MATRIX, job costs %d: %w",
			buyer, balance, price, ErrInsufficientFunds)
	}

	now := time.Now().UTC()
	job := Job{
		ID:        uuid.NewString(),
		Buyer:     buyer,
		Provider:  providerID,
		Units:     units,
		Price:     price,
		Status:    JobPending,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Reserve capacity on a copy first so a persistence failure does not mutate
	// the in-memory provider or create a job.
	reserved := provider
	reserved.Available -= units

	if err := m.persistProvider(reserved); err != nil {
		return nil, err
	}

	m.jobsMu.Lock()
	if err := m.persistJob(job); err != nil {
		// Roll back the reservation persistence to keep capacity consistent.
		_ = m.persistProvider(provider)
		m.jobsMu.Unlock()
		return nil, err
	}

	m.providers[providerID] = reserved
	m.jobs[job.ID] = job
	m.jobsMu.Unlock()

	// A new pending job raises the active-job count. Notify after releasing the
	// locks to avoid deadlocking against the observer's reads.
	m.notifyActiveJobs()

	jobCopy := job
	return &jobCopy, nil
}

// CompleteJob transitions a pending or running job to JobCompleted and transfers
// its price from buyer to provider through the ledger. If the transfer fails the
// job is left non-completed and the error is returned.
func (m *Market) CompleteJob(jobID string) error {
	m.jobsMu.Lock()

	job, ok := m.jobs[jobID]
	if !ok {
		m.jobsMu.Unlock()
		return fmt.Errorf("complete job %q: %w", jobID, ErrJobNotFound)
	}
	if job.Status != JobPending && job.Status != JobRunning {
		m.jobsMu.Unlock()
		return fmt.Errorf("complete job %q in state %q: %w", jobID, job.Status, ErrInvalidJobState)
	}

	// Transfer native MATRIX first; only mark completed if the transfer succeeds
	// so a failed transfer leaves the job non-completed. job.Provider is always a
	// provider ID that RegisterProvider validated and SubmitJob looked up, and
	// SubmitJob rejects buyer == provider, so the ledger credits a real, distinct
	// counterparty rather than stranding funds on a typo'd account.
	if err := m.ledger.Transfer(job.Buyer, job.Provider, job.Price); err != nil {
		m.jobsMu.Unlock()
		return fmt.Errorf("complete job %q: %w", jobID, err)
	}

	job.Status = JobCompleted
	job.UpdatedAt = time.Now().UTC()
	if err := m.persistJob(job); err != nil {
		m.jobsMu.Unlock()
		return err
	}
	m.jobs[jobID] = job
	settled := job.Price
	m.jobsMu.Unlock()

	// A completed job leaves the active set and settles native MATRIX. Notify
	// after releasing the lock to avoid deadlocking against the observer's reads.
	m.notifyActiveJobs()
	m.notifyJobCompleted(settled)
	return nil
}

// CancelJob cancels a pending or running job, returning the reserved capacity to
// its provider. No native MATRIX is transferred.
func (m *Market) CancelJob(jobID string) error {
	if err := func() error {
		// Acquire providersMu before jobsMu to match SubmitJob's global lock
		// order. Both paths take providersMu then jobsMu, so a concurrent
		// SubmitJob/CancelJob pair can no longer deadlock by grabbing the two
		// mutexes in opposite orders.
		m.providersMu.Lock()
		defer m.providersMu.Unlock()

		m.jobsMu.Lock()
		defer m.jobsMu.Unlock()

		job, ok := m.jobs[jobID]
		if !ok {
			return fmt.Errorf("cancel job %q: %w", jobID, ErrJobNotFound)
		}
		if job.Status != JobPending && job.Status != JobRunning {
			return fmt.Errorf("cancel job %q in state %q: %w", jobID, job.Status, ErrInvalidJobState)
		}

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
		job.UpdatedAt = time.Now().UTC()
		if err := m.persistJob(job); err != nil {
			return err
		}
		m.jobs[jobID] = job
		return nil
	}(); err != nil {
		return err
	}

	// A cancelled job leaves the active set. Notify after releasing the locks to
	// avoid deadlocking against the observer's reads.
	m.notifyActiveJobs()
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

// ListJobs returns all jobs in chronological order by creation time, breaking
// ties on ID so the order is stable across calls and restarts.
func (m *Market) ListJobs() []Job {
	m.jobsMu.RLock()
	defer m.jobsMu.RUnlock()

	out := make([]Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}
