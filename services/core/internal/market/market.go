package market

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
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
	// JobQuoteIncomplete is an in-memory read marker returned for a persisted
	// active legacy job whose immutable quote snapshot is incomplete. It is never
	// persisted as a lifecycle transition; CancelJob still sees the underlying
	// pending/running state, while settlement coordinators that read through
	// GetJob refuse it before submitting payment.
	JobQuoteIncomplete JobStatus = "incomplete_quote_snapshot"
)

// Provider represents an idle-machine compute provider that has registered
// capacity on the marketplace.
type Provider struct {
	ID       string `json:"id"`
	Capacity uint64 `json:"capacity"`
	// PricePerUnit is the final gross customer quote in native MATRIX base
	// units. It includes provider markup and protocol-fee gross-up when
	// CostPerUnit is configured.
	PricePerUnit uint64 `json:"price_per_unit"`
	// CostPerUnit is the provider's manually observed MATRIX-denominated upstream
	// cost basis. Zero means PricePerUnit was supplied as an already-final quote.
	CostPerUnit       uint64 `json:"cost_per_unit,omitempty"`
	MarkupBasisPoints uint32 `json:"markup_basis_points,omitempty"`
	// Quote identity and validity make price changes auditable and let job
	// reservations snapshot exactly what the buyer accepted.
	QuoteID      string    `json:"quote_id"`
	QuoteVersion uint64    `json:"quote_version"`
	ObservedAt   time.Time `json:"observed_at"`
	ValidUntil   time.Time `json:"valid_until"`
	Available    uint64    `json:"available"`
	// Models are the model identifiers this provider will serve, lowercased,
	// de-duplicated and sorted by RegisterProvider. It is what a request naming
	// a model is routed on: without it a caller has to know a provider ID, and
	// an inference job's reported model is only ever an echo of what the backend
	// answered with. Empty means the provider advertises no model and is
	// therefore never selected by model, which is the right reading for a
	// compute-only provider.
	Models []string `json:"models,omitempty"`
	// Suspended takes the provider off the market without touching its capacity
	// accounting, its quote, or its live reservations. It is set by whoever
	// watches whether the thing behind the provider can actually serve - for an
	// inference backend, the node's health check against the model server.
	//
	// It is a flag rather than "set Available to 0" because the two would fight:
	// a job releasing its reservation adds its units back to Available, so a
	// zeroed provider would quietly come back onto the market the moment an
	// in-flight job finished. Suspension has to survive that, because the
	// condition that caused it has not changed.
	Suspended bool `json:"suspended,omitempty"`
}

// NormalizeModel puts a model identifier in the one form the order book stores
// and matches on: trimmed and lowercased. Model names are case-insensitive
// across the vendors we proxy, so "Llama-3.3-70B" and "llama-3.3-70b" must not
// be two different routing targets.
func NormalizeModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

// normalizeModels normalizes, de-duplicates and sorts a model list, dropping
// blank entries. Sorting is what makes a persisted provider record and a
// re-registration of the same provider byte-identical.
func normalizeModels(models []string) []string {
	if len(models) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(models))
	out := make([]string, 0, len(models))
	for _, m := range models {
		m = NormalizeModel(m)
		if m == "" {
			continue
		}
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

// ServesModel reports whether the provider advertises the given model. The
// comparison is on the normalized form, so a caller may pass a model name in
// whatever case it arrived in.
func (p Provider) ServesModel(model string) bool {
	model = NormalizeModel(model)
	if model == "" {
		return false
	}
	for _, m := range p.Models {
		if m == model {
			return true
		}
	}
	return false
}

// Job represents a paid compute job submitted by a buyer against a provider.
// CreatedAt records submit time and UpdatedAt the last status transition, giving
// ListJobs a stable chronological order and basic auditability.
type Job struct {
	ID       string `json:"id"`
	Buyer    string `json:"buyer"`
	Provider string `json:"provider"`
	Units    uint64 `json:"units"`
	Price    uint64 `json:"price"`
	// PricePerUnit and quote metadata are immutable reservation snapshots. A
	// provider may refresh its live quote while work is running without changing
	// what this buyer pays.
	PricePerUnit    uint64    `json:"price_per_unit"`
	QuoteID         string    `json:"quote_id"`
	QuoteVersion    uint64    `json:"quote_version"`
	QuoteObservedAt time.Time `json:"quote_observed_at"`
	QuoteValidUntil time.Time `json:"quote_valid_until"`
	// RemoteRequestDigest and RemoteRequestNonce identify a v2 remote request.
	// Persisting both makes redelivery idempotent across process restarts and
	// prevents a buyer from reserving twice by reusing one nonce with new terms.
	RemoteRequestDigest string    `json:"remote_request_digest,omitempty"`
	RemoteRequestNonce  uint64    `json:"remote_request_nonce,omitempty"`
	Status              JobStatus `json:"status"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// AcceptedQuote is the exact fixed-price quote snapshot a remote buyer signed.
// Total must equal Units * PricePerUnit. Quote expiry gates reservation only;
// once accepted, settlement continues to use this immutable snapshot.
type AcceptedQuote struct {
	PricePerUnit uint64
	QuoteID      string
	QuoteVersion uint64
	ObservedAt   time.Time
	ValidUntil   time.Time
	Total        uint64
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

	jobsMu         sync.RWMutex
	jobs           map[string]Job
	remoteRequests map[string]string
}

// NewMarket creates a new Market backed by the given store, constructing the
// native MATRIX ledger internally. It rehydrates any providers and jobs previously
// persisted to the store so a Market built over an existing store observes the
// durable marketplace state rather than starting empty. Over a fresh store this
// is a no-op and the maps start empty.
func NewMarket(store *kv.Store) (*Market, error) {
	m := &Market{
		store:          store,
		ledger:         NewLedger(store),
		providers:      make(map[string]Provider),
		jobs:           make(map[string]Job),
		remoteRequests: make(map[string]string),
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
		// Active records from versions before quote snapshots may contain a total
		// price (and, during partial rollouts, even a per-unit price) without the
		// quote identity/times that prove what the buyer accepted. Keep the record
		// cancellable and visible, but force inference's existing CheckedMul path to
		// return ErrStaleQuote rather than construct a payment. Do not persist this
		// marker and never fabricate historical quote identity.
		if (j.Status == JobPending || j.Status == JobRunning) && validateJobQuoteSnapshot(j) != nil {
			j.PricePerUnit = 0
		}
		m.jobs[j.ID] = j
		if j.RemoteRequestDigest != "" {
			key := remoteRequestKey(j.Buyer, j.RemoteRequestNonce)
			if existingID, exists := m.remoteRequests[key]; exists && existingID != j.ID {
				return fmt.Errorf("persisted remote request nonce %q belongs to jobs %q and %q: %w", key, existingID, j.ID, ErrRemoteRequestConflict)
			}
			m.remoteRequests[key] = j.ID
		}
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
	m.observer.ProviderCountChanged(m.registeredProviderCount())
	m.observer.ActiveJobsChanged(m.countActiveJobs())
}

// registeredProviderCount returns every persisted local provider, including one
// whose quote needs an administrative refresh. Buyer-facing listings filter
// those stale records.
func (m *Market) registeredProviderCount() int {
	m.providersMu.RLock()
	defer m.providersMu.RUnlock()
	return len(m.providers)
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
	m.observer.ProviderCountChanged(m.registeredProviderCount())
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

// normalizeProviderQuote fills explicit quote identity/timestamps for legacy
// callers and validates freshness. Registration is the manual quote update: no
// DEX price is trusted implicitly.
func normalizeProviderQuote(p Provider, now time.Time) (Provider, error) {
	if p.PricePerUnit == 0 {
		return Provider{}, fmt.Errorf("provider %q price_per_unit must be > 0: %w", p.ID, ErrInvalidProvider)
	}
	if p.QuoteID == "" {
		p.QuoteID = uuid.NewString()
	}
	if p.QuoteVersion == 0 {
		p.QuoteVersion = 1
	}
	if p.ObservedAt.IsZero() {
		p.ObservedAt = now
	} else {
		p.ObservedAt = p.ObservedAt.UTC()
	}
	if p.ValidUntil.IsZero() {
		p.ValidUntil = p.ObservedAt.Add(DefaultQuoteTTL)
	} else {
		p.ValidUntil = p.ValidUntil.UTC()
	}
	if p.ObservedAt.After(now.Add(MaxQuoteClockSkew)) {
		return Provider{}, fmt.Errorf("provider %q quote observed_at %s is in the future: %w", p.ID, p.ObservedAt.Format(time.RFC3339), ErrInvalidProvider)
	}
	if !p.ValidUntil.After(p.ObservedAt) {
		return Provider{}, fmt.Errorf("provider %q quote valid_until must be after observed_at: %w", p.ID, ErrInvalidProvider)
	}
	if !p.ValidUntil.After(now) {
		return Provider{}, fmt.Errorf("provider %q quote expired at %s: %w", p.ID, p.ValidUntil.Format(time.RFC3339), ErrStaleQuote)
	}
	return p, nil
}

// UpdateProviderQuote refreshes only the economic quote of an existing
// provider, preserving its capacity reservation and model advertisement. It is
// the safe startup path for config-backed providers: restarting no longer
// leaves an old persisted price in force or resets capacity held by jobs.
func (m *Market) UpdateProviderQuote(id string, quote Provider) error {
	m.providersMu.Lock()
	defer m.providersMu.Unlock()
	existing, ok := m.providers[id]
	if !ok {
		return fmt.Errorf("update quote for provider %q: %w", id, ErrProviderNotFound)
	}
	quote.ID = id
	quote.Capacity = existing.Capacity
	quote.Available = existing.Available
	quote.Models = existing.Models
	// Suspension is liveness, not pricing. A restart re-applying the config quote
	// must not silently put a provider whose backend is down back on the market;
	// the health check clears this when the backend answers again.
	quote.Suspended = existing.Suspended
	if quote.QuoteVersion <= existing.QuoteVersion {
		if existing.QuoteVersion == ^uint64(0) {
			return fmt.Errorf("provider %q quote version overflow: %w", id, ErrPriceOverflow)
		}
		quote.QuoteVersion = existing.QuoteVersion + 1
	}
	prepared, err := normalizeProviderQuote(quote, time.Now().UTC())
	if err != nil {
		return err
	}
	if err := m.persistProvider(prepared); err != nil {
		return err
	}
	m.providers[id] = prepared
	return nil
}

// SetProviderSuspended takes a provider off the market, or puts it back on. It
// reports whether the flag actually changed, so a caller polling on an interval
// can log a transition rather than the same state every tick.
//
// Suspending does NOT cancel live jobs. A job already reserved against this
// provider keeps its reservation and settles or expires on its own terms; what
// stops is NEW reservations. Cancelling in-flight work would be the wrong call
// from a health check, which cannot tell a backend that has died from one that
// answered a probe slowly while finishing a real completion.
func (m *Market) SetProviderSuspended(id string, suspended bool) (bool, error) {
	m.providersMu.Lock()
	defer m.providersMu.Unlock()
	provider, ok := m.providers[id]
	if !ok {
		return false, fmt.Errorf("suspend provider %q: %w", id, ErrProviderNotFound)
	}
	if provider.Suspended == suspended {
		return false, nil
	}
	provider.Suspended = suspended
	if err := m.persistProvider(provider); err != nil {
		return false, err
	}
	m.providers[id] = provider
	return true, nil
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
	prepared, err := normalizeProviderQuote(p, time.Now().UTC())
	if err != nil {
		return err
	}
	p = prepared

	p.Available = p.Capacity
	p.Models = normalizeModels(p.Models)

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

// validateProviderQuoteForUse rejects legacy or expired provider records before
// they can reserve new work or appear in buyer-facing listings. Operators can
// still address a stale provider by ID and refresh it with UpdateProviderQuote;
// it remains deliberately ineligible until every quote field is fresh.
func validateProviderQuoteForUse(p Provider, now time.Time) error {
	if p.PricePerUnit == 0 || p.QuoteID == "" || p.QuoteVersion == 0 || p.ObservedAt.IsZero() || p.ValidUntil.IsZero() {
		return fmt.Errorf("provider %q quote %q version %d is incomplete: %w", p.ID, p.QuoteID, p.QuoteVersion, ErrStaleQuote)
	}
	if p.ObservedAt.After(now.Add(MaxQuoteClockSkew)) {
		return fmt.Errorf("provider %q quote observation %s is in the future: %w", p.ID, p.ObservedAt.Format(time.RFC3339), ErrStaleQuote)
	}
	if !p.ValidUntil.After(p.ObservedAt) || !p.ValidUntil.After(now) {
		return fmt.Errorf("provider %q quote %q version %d expired or invalid (valid until %s): %w",
			p.ID, p.QuoteID, p.QuoteVersion, p.ValidUntil.Format(time.RFC3339), ErrStaleQuote)
	}
	return nil
}

// validateJobQuoteSnapshot rejects active records written before immutable quote
// snapshots existed. Cancellation remains available, but no completion path may
// settle a zero or unauditable legacy amount. Old quote identity is never
// invented because doing so would misrepresent what the buyer accepted.
func validateJobQuoteSnapshot(j Job) error {
	if j.PricePerUnit == 0 || j.QuoteID == "" || j.QuoteVersion == 0 || j.QuoteObservedAt.IsZero() || j.QuoteValidUntil.IsZero() {
		return fmt.Errorf("job %q price/quote snapshot is incomplete and must not settle: %w", j.ID, ErrStaleQuote)
	}
	expected, err := CheckedMul(j.Units, j.PricePerUnit)
	if err != nil {
		return fmt.Errorf("job %q snapshot price is invalid: %w", j.ID, err)
	}
	if expected != j.Price {
		return fmt.Errorf("job %q snapshot total %d does not match units * price_per_unit (%d): %w",
			j.ID, j.Price, expected, ErrStaleQuote)
	}
	return nil
}

// ValidateJobQuoteForSettlement checks that an existing active job has a
// complete immutable quote snapshot before any external settlement coordinator
// signs or submits payment for it.
func (m *Market) ValidateJobQuoteForSettlement(jobID string) error {
	m.jobsMu.RLock()
	defer m.jobsMu.RUnlock()
	job, ok := m.jobs[jobID]
	if !ok {
		return fmt.Errorf("validate quote for job %q: %w", jobID, ErrJobNotFound)
	}
	if job.Status != JobPending && job.Status != JobRunning {
		return fmt.Errorf("validate quote for job %q in state %q: %w", jobID, job.Status, ErrInvalidJobState)
	}
	return validateJobQuoteSnapshot(job)
}

func remoteRequestKey(buyer string, nonce uint64) string {
	return fmt.Sprintf("%s/%d", buyer, nonce)
}

// persistReservation atomically stores both sides of a capacity reservation.
// A crash can therefore expose neither the job nor the capacity decrement, or
// both, but never a provider record that discarded the accepted snapshot.
func (m *Market) persistReservation(provider Provider, job Job) error {
	providerData, err := json.Marshal(provider)
	if err != nil {
		return fmt.Errorf("failed to marshal provider %q: %w", provider.ID, err)
	}
	jobData, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed to marshal job %q: %w", job.ID, err)
	}
	batch := m.store.NewBatch()
	defer batch.Close()
	if err := batch.Set([]byte(providerKeyPrefix+provider.ID), providerData, nil); err != nil {
		return fmt.Errorf("failed to stage provider %q reservation: %w", provider.ID, err)
	}
	if err := batch.Set([]byte(jobKeyPrefix+job.ID), jobData, nil); err != nil {
		return fmt.Errorf("failed to stage job %q reservation: %w", job.ID, err)
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return fmt.Errorf("failed to commit reservation for job %q: %w", job.ID, err)
	}
	return nil
}

// ReserveRemoteJob validates and persists a v2 remote request against one exact
// accepted quote. Quote comparison, expiry, capacity checking, and persistence
// all happen while the provider lock is held, so a concurrent quote refresh can
// only win before the request (which is rejected) or after its old snapshot has
// been durably reserved. Redelivery of the same buyer nonce and digest is
// idempotent, including after restart.
func (m *Market) ReserveRemoteJob(buyer, providerID string, units, nonce uint64, requestDigest string, accepted AcceptedQuote, now time.Time) (*Job, error) {
	if units == 0 {
		return nil, fmt.Errorf("units must be > 0: %w", ErrInsufficientCapacity)
	}
	if buyer == providerID {
		return nil, fmt.Errorf("buyer %q equals provider %q: %w", buyer, providerID, ErrSelfDealing)
	}
	if requestDigest == "" {
		return nil, fmt.Errorf("remote request digest must not be empty: %w", ErrRemoteRequestConflict)
	}
	now = now.UTC()

	var reservedJob Job
	created := false
	err := func() error {
		m.providersMu.Lock()
		defer m.providersMu.Unlock()
		m.jobsMu.Lock()
		defer m.jobsMu.Unlock()

		requestKey := remoteRequestKey(buyer, nonce)
		if existingID, ok := m.remoteRequests[requestKey]; ok {
			existing, exists := m.jobs[existingID]
			if !exists || existing.RemoteRequestDigest != requestDigest {
				return fmt.Errorf("buyer %q reused remote nonce %d: %w", buyer, nonce, ErrRemoteRequestConflict)
			}
			reservedJob = existing
			return nil
		}

		provider, ok := m.providers[providerID]
		if !ok {
			return fmt.Errorf("reserve remote job for provider %q: %w", providerID, ErrProviderNotFound)
		}
		if provider.Suspended {
			return fmt.Errorf("provider %q is not currently serving: %w", providerID, ErrProviderSuspended)
		}
		if err := validateProviderQuoteForUse(provider, now); err != nil {
			return err
		}
		if provider.PricePerUnit != accepted.PricePerUnit ||
			provider.QuoteID != accepted.QuoteID ||
			provider.QuoteVersion != accepted.QuoteVersion ||
			!provider.ObservedAt.Equal(accepted.ObservedAt) ||
			!provider.ValidUntil.Equal(accepted.ValidUntil) {
			return fmt.Errorf("provider %q current quote %q/%d differs from accepted %q/%d: %w",
				providerID, provider.QuoteID, provider.QuoteVersion, accepted.QuoteID, accepted.QuoteVersion, ErrQuoteMismatch)
		}
		if !accepted.ValidUntil.After(now) {
			return fmt.Errorf("accepted quote %q expired at %s: %w", accepted.QuoteID, accepted.ValidUntil.UTC().Format(time.RFC3339Nano), ErrStaleQuote)
		}
		total, err := CheckedMul(units, accepted.PricePerUnit)
		if err != nil {
			return fmt.Errorf("price remote job for provider %q: %w", providerID, err)
		}
		if total != accepted.Total {
			return fmt.Errorf("accepted total %d does not equal %d units * %d: %w",
				accepted.Total, units, accepted.PricePerUnit, ErrQuoteMismatch)
		}
		if provider.Available < units {
			return fmt.Errorf("provider %q has %d units available, need %d: %w",
				providerID, provider.Available, units, ErrInsufficientCapacity)
		}
		balance, err := m.ledger.Balance(buyer)
		if err != nil {
			return err
		}
		if balance < total {
			return fmt.Errorf("buyer %q has %d native MATRIX, job costs %d: %w",
				buyer, balance, total, ErrInsufficientFunds)
		}

		job := Job{
			ID:                  uuid.NewString(),
			Buyer:               buyer,
			Provider:            providerID,
			Units:               units,
			Price:               total,
			PricePerUnit:        accepted.PricePerUnit,
			QuoteID:             accepted.QuoteID,
			QuoteVersion:        accepted.QuoteVersion,
			QuoteObservedAt:     accepted.ObservedAt.UTC(),
			QuoteValidUntil:     accepted.ValidUntil.UTC(),
			RemoteRequestDigest: requestDigest,
			RemoteRequestNonce:  nonce,
			Status:              JobPending,
			CreatedAt:           now,
			UpdatedAt:           now,
		}
		reserved := provider
		reserved.Available -= units
		if err := m.persistReservation(reserved, job); err != nil {
			return err
		}
		m.providers[providerID] = reserved
		m.jobs[job.ID] = job
		m.remoteRequests[requestKey] = job.ID
		reservedJob = job
		created = true
		return nil
	}()
	if err != nil {
		return nil, err
	}
	if created {
		m.notifyActiveJobs()
	}
	copy := reservedJob
	return &copy, nil
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
	if provider.Suspended {
		return nil, fmt.Errorf("provider %q is not currently serving: %w", providerID, ErrProviderSuspended)
	}
	if provider.Available < units {
		return nil, fmt.Errorf("provider %q has %d units available, need %d: %w",
			providerID, provider.Available, units, ErrInsufficientCapacity)
	}
	now := time.Now().UTC()
	if err := validateProviderQuoteForUse(provider, now); err != nil {
		return nil, err
	}

	price, err := CheckedMul(units, provider.PricePerUnit)
	if err != nil {
		return nil, fmt.Errorf("price job for provider %q: %w", providerID, err)
	}

	balance, err := m.ledger.Balance(buyer)
	if err != nil {
		return nil, err
	}
	if balance < price {
		return nil, fmt.Errorf("buyer %q has %d native MATRIX, job costs %d: %w",
			buyer, balance, price, ErrInsufficientFunds)
	}

	job := Job{
		ID:              uuid.NewString(),
		Buyer:           buyer,
		Provider:        providerID,
		Units:           units,
		Price:           price,
		PricePerUnit:    provider.PricePerUnit,
		QuoteID:         provider.QuoteID,
		QuoteVersion:    provider.QuoteVersion,
		QuoteObservedAt: provider.ObservedAt,
		QuoteValidUntil: provider.ValidUntil,
		Status:          JobPending,
		CreatedAt:       now,
		UpdatedAt:       now,
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
// its price from buyer to provider directly through the ledger. If the transfer
// fails the job is left non-completed and the error is returned.
//
// This is the DIRECT, non-consensus settlement path. It moves native MATRIX with
// a single local ledger.Transfer that does NOT go through consensus ordering or
// the committed-transaction dedup set. The node's default/production compute
// flow settles through consensus instead, via the node's compute settlement
// coordinator (node.ComputeSettlementCoordinator), which submits a signed
// transfer, waits for it to commit and apply, then finalizes the job via
// ReleaseAsCompleted, so compute and inference share one
// authoritative native-MATRIX ledger. CompleteJob is retained for local,
// single-node or test contexts that intentionally want the direct path without a
// running consensus engine; do NOT combine it with the consensus path for the
// same job, or the buyer would be charged twice.
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
	if err := validateJobQuoteSnapshot(job); err != nil {
		m.jobsMu.Unlock()
		return err
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

// ReleaseAsCompleted finalizes a job whose payment already settled through
// consensus: it returns the reserved capacity to the provider (like CancelJob)
// but transitions the job to JobCompleted and reports the settled amount to the
// observer. Crucially it moves NO native MATRIX itself, because the consensus
// apply path already transferred settledAmount buyer -> provider on the shared
// ledger; doing a ledger.Transfer here (as CompleteJob does) would double-charge
// the buyer. This is the compute analogue of how internal/inference releases the
// reservation via CancelJob after settling through consensus, kept as a distinct
// method so the job ends in COMPLETED (not CANCELLED) and the JobCompleted metric
// fires exactly once.
//
// It is exported so the node's consensus-backed compute settlement coordinator
// (which lives outside this package to avoid a market -> token -> market import
// cycle) can finalize a job after the consensus transfer applies. It requires
// the job to be pending or running and returns a copy of the completed job.
func (m *Market) ReleaseAsCompleted(jobID string, settledAmount uint64) (*Job, error) {
	var completed Job
	if err := func() error {
		// Same providersMu-before-jobsMu lock order as SubmitJob/CancelJob.
		m.providersMu.Lock()
		defer m.providersMu.Unlock()

		m.jobsMu.Lock()
		defer m.jobsMu.Unlock()

		job, ok := m.jobs[jobID]
		if !ok {
			return fmt.Errorf("complete settled job %q: %w", jobID, ErrJobNotFound)
		}
		if job.Status != JobPending && job.Status != JobRunning {
			return fmt.Errorf("complete settled job %q in state %q: %w", jobID, job.Status, ErrInvalidJobState)
		}
		if err := validateJobQuoteSnapshot(job); err != nil {
			return err
		}

		// Return the reserved capacity to the provider. The reservation only ever
		// held capacity; the buyer was charged by the consensus settlement, not
		// here, so no ledger transfer happens.
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

		job.Status = JobCompleted
		job.UpdatedAt = time.Now().UTC()
		if err := m.persistJob(job); err != nil {
			return err
		}
		m.jobs[jobID] = job
		completed = job
		return nil
	}(); err != nil {
		return nil, err
	}

	// A completed job leaves the active set and settled native MATRIX (already
	// moved through consensus). Notify after releasing the locks to avoid
	// deadlocking against the observer's reads.
	m.notifyActiveJobs()
	m.notifyJobCompleted(settledAmount)

	cp := completed
	return &cp, nil
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
	if ok && (j.Status == JobPending || j.Status == JobRunning) && validateJobQuoteSnapshot(j) != nil {
		// Settlement coordinators consume GetJob before creating payment. Return a
		// non-settleable read marker so even a caller that bypasses the market API's
		// explicit validation refuses before funds move. The stored status remains
		// pending/running, keeping CancelJob available.
		j.PricePerUnit = 0
		j.Status = JobQuoteIncomplete
	}
	return j, ok
}

// ListProviders returns providers with fresh quotes sorted by ID. It is the
// buyer-facing local order book; stale records remain addressable by ID so an
// operator can refresh them without exposing an offer that cannot accept work.
func (m *Market) ListProviders() []Provider {
	m.providersMu.RLock()
	defer m.providersMu.RUnlock()

	out := make([]Provider, 0, len(m.providers))
	now := time.Now().UTC()
	for _, p := range m.providers {
		if validateProviderQuoteForUse(p, now) != nil {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ProvidersForModel returns the local providers that advertise model and still
// have capacity to reserve, cheapest first and breaking ties on ID so the choice
// is deterministic. An unknown model, or one no provider advertises, returns an
// empty slice rather than an error: "nobody serves this" is a routing outcome,
// not a failure of the order book.
func (m *Market) ProvidersForModel(model string) []Provider {
	model = NormalizeModel(model)
	if model == "" {
		return nil
	}

	m.providersMu.RLock()
	defer m.providersMu.RUnlock()

	out := make([]Provider, 0, len(m.providers))
	now := time.Now().UTC()
	for _, p := range m.providers {
		if p.Suspended || p.Available == 0 || !p.ServesModel(model) || validateProviderQuoteForUse(p, now) != nil {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PricePerUnit != out[j].PricePerUnit {
			return out[i].PricePerUnit < out[j].PricePerUnit
		}
		return out[i].ID < out[j].ID
	})
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
