package agentapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/ecirlabs/matrix-core/internal/agent"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// KV key layout for persisted agent deployments. Everything lives under a
// distinct agent/* namespace so it never collides with the market/balance/*,
// token/native/* or bridge/* keyspaces. It mirrors the pattern documented in
// internal/bridge/bridge.go.
//
//	agent/record/<id>  -> Deployment JSON (id, module hash, size, limits, status,
//	                      last-run output/error/charge, timestamps)
//	agent/module/<id>  -> the raw wasm module bytes, stored separately so the
//	                      (potentially large) module is not rehydrated every time
//	                      a small record is read, and so ListAgents can page
//	                      records without touching module bytes.
const (
	recordPrefix = "agent/record/" // agent/record/<id> -> Deployment JSON
	modulePrefix = "agent/module/" // agent/module/<id> -> raw wasm bytes
)

// Status is the lifecycle status of a persisted deployment. It maps 1:1 to the
// matrix.agent.v1.AgentStatus enum at the service boundary.
type Status string

const (
	// StatusDeployed means the module was accepted and persisted but has not run.
	StatusDeployed Status = "deployed"
	// StatusRunning means the module's most recent run completed without error.
	StatusRunning Status = "running"
	// StatusFailed means the module's most recent run failed. LastError carries
	// the reason.
	StatusFailed Status = "failed"
)

// Errors the manager returns. They are mapped to gRPC status codes by the
// service (see mapAgentError).
var (
	// ErrEmptyID rejects a deploy or lookup with no agent ID.
	ErrEmptyID = errors.New("agentapi: agent id must not be empty")
	// ErrEmptyModule rejects a deploy carrying no module bytes.
	ErrEmptyModule = errors.New("agentapi: wasm module must not be empty")
	// ErrModuleTooLarge rejects a module larger than the configured cap.
	ErrModuleTooLarge = errors.New("agentapi: wasm module exceeds the size limit")
	// ErrAgentNotFound is returned when a lookup references an unknown agent id.
	ErrAgentNotFound = errors.New("agentapi: agent not found")
	// ErrNoSigningAccount is returned when a metered deploy's payer has no
	// resolvable signing key, so no consensus transfer can be signed for it. The
	// deploy is refused rather than run for free.
	ErrNoSigningAccount = errors.New("agentapi: no signing account for the deployer")
	// ErrMeterNotApplied is returned when the metering transfer committed but was
	// skipped at apply time (the deployer could not afford the charge). No
	// credits moved and the module is not run, so a metered deploy never runs for
	// free.
	ErrMeterNotApplied = errors.New("agentapi: metering charge did not apply (deployer could not afford it)")
)

// DefaultMaxModuleBytes bounds the size of a submitted wasm module. It is
// generous relative to the trivial guest modules the runtime runs while keeping
// a hostile caller from making the node store an arbitrarily large blob. An
// operator can raise or lower it through Config.
const DefaultMaxModuleBytes = 32 << 20 // 32 MiB

// Deployment is the persisted record of a deployed agent. It is what ListAgents
// and GetAgent return (mapped to the proto Agent); the raw module bytes are
// stored under a separate key and are not part of this record beyond their hash
// and size.
type Deployment struct {
	// ID is the deployer-chosen agent identifier.
	ID string `json:"id"`
	// Status is the current lifecycle status.
	Status Status `json:"status"`
	// ModuleHash is the hex-encoded sha256 of the deployed module bytes.
	ModuleHash string `json:"module_hash"`
	// ModuleSize is the size of the deployed module in bytes.
	ModuleSize uint64 `json:"module_size"`
	// MaxMemoryPages / MaxRunTimeMS are the resource limits the module runs
	// under, stored as scalars so the record round-trips through JSON cleanly.
	MaxMemoryPages uint32 `json:"max_memory_pages"`
	MaxRunTimeMS   uint64 `json:"max_run_time_ms"`
	// LastOutput is whatever the module wrote to stdout during its most recent
	// run.
	LastOutput string `json:"last_output"`
	// LastError is the error from the most recent run, empty on success.
	LastError string `json:"last_error"`
	// LastCharge is the credits charged for the most recent run through
	// consensus (0 when metering is disabled).
	LastCharge uint64 `json:"last_charge"`
	// CreatedAtNS / LastRunAtNS are unix-nanosecond wall-clock timestamps.
	CreatedAtNS int64 `json:"created_at_ns"`
	LastRunAtNS int64 `json:"last_run_at_ns"`
}

// normalizeLimits fills any unset field of a submitted limits value from the
// runtime's DefaultMemoryLimits, so a caller may omit limits entirely (or set
// only one field) and get the safe defaults for the rest. A MaxRunTime of zero
// means "no deadline", which is a deliberate choice only appropriate for a
// trusted module; because a submitted module is untrusted, an unset run time
// defaults to the runtime's bounded default rather than "run forever".
func normalizeLimits(l agent.ResourceLimits) agent.ResourceLimits {
	if l.MaxMemoryPages == 0 {
		l.MaxMemoryPages = agent.DefaultMemoryLimits.MaxMemoryPages
	}
	if l.MaxRunTime <= 0 {
		l.MaxRunTime = agent.DefaultMemoryLimits.MaxRunTime
	}
	return l
}

// Limits reconstructs the agent.ResourceLimits a Deployment records, applying
// the runtime default for any unset field.
func (d Deployment) Limits() agent.ResourceLimits {
	lim := agent.DefaultMemoryLimits
	if d.MaxMemoryPages != 0 {
		lim.MaxMemoryPages = d.MaxMemoryPages
	}
	if d.MaxRunTimeMS != 0 {
		lim.MaxRunTime = time.Duration(d.MaxRunTimeMS) * time.Millisecond
	}
	return lim
}

// Settler is the consensus-backed metering dependency. It is the same shape as
// node.ComputeSettler / inference.Settler, so *consensus.Engine satisfies it and
// tests can drive a fake without a running engine. Charging an agent run moves
// native MATRIX from the deployer to the operator-configured recipient THROUGH
// CONSENSUS (a quorum orders and applies it), never a per-node ledger write.
type Settler interface {
	// SubmitAccountTransfer signs a transfer from the deployer to recipient for
	// amount and submits it into consensus, returning the transaction so the
	// caller can wait for it.
	SubmitAccountTransfer(from *token.Account, recipient string, amount, nonce uint64) (*token.Transaction, error)
	// WaitForSettlement blocks until tx is committed and its apply outcome is
	// known, or ctx is done.
	WaitForSettlement(ctx context.Context, tx *token.Transaction) (committed bool, applied bool, err error)
}

// Accounts resolves a deployer account ID to its signing token.Account. Metering
// through consensus needs the payer's private key to sign the charge, so the
// manager is given a resolver rather than assuming custody, exactly as the
// compute/inference settlement coordinators are. It is satisfied by the node's
// wallet resolver; tests supply an in-memory map.
type Accounts interface {
	// Account returns the signing account for id, and whether it is known.
	Account(id string) (*token.Account, bool)
}

// MeterConfig configures per-run metering. Price defaults to 0, which means
// UNMETERED: nothing is charged and no signing key is required, so a node runs
// agents for free unless the operator explicitly opts in by setting a price and
// recipient. This is deliberate: the per-run price is monetary policy and is not
// baked into a default here.
type MeterConfig struct {
	// Price is the credits charged per run. Zero disables metering entirely.
	Price uint64
	// Recipient is the account the charge is paid to (the operator/provider
	// account). Required when Price > 0.
	Recipient string
	// DefaultDeployer is the account charged when a DeployAgentRequest names no
	// deployer. Optional; when empty a metered deploy must name a deployer.
	DefaultDeployer string
}

// Enabled reports whether metering charges anything.
func (m MeterConfig) Enabled() bool { return m.Price > 0 }

// ManagerConfig configures a Manager.
type ManagerConfig struct {
	// Store persists deployments (required).
	Store *kv.Store
	// MaxModuleBytes caps a submitted module's size. Zero means
	// DefaultMaxModuleBytes.
	MaxModuleBytes int
	// Meter configures per-run metering (default disabled).
	Meter MeterConfig
	// Settler settles the metering charge through consensus. Required only when
	// Meter.Enabled(); a manager with metering disabled needs no settler.
	Settler Settler
	// Accounts resolves the deployer's signing key. Required only when
	// Meter.Enabled().
	Accounts Accounts
	// SendPolicy governs the agent runtime's inter-agent send() primitive: who a
	// running module may address and, together with the Manager's inbox delivery,
	// what a target name resolves to. The zero value (Enabled false, empty Allow)
	// refuses every send, identical to today's nil SendFunc: inter-agent send is
	// OFF unless the operator turns it on and names an allowlist. See
	// agent.SendPolicy.
	SendPolicy agent.SendPolicy
}

// Manager instantiates, runs, persists, and meters WebAssembly agents. It owns
// the agent/* keyspace in the kv store and reloads persisted deployments on
// construction so ListAgents reflects them after a node restart. It is safe for
// concurrent use.
type Manager struct {
	store      *kv.Store
	maxBytes   int
	meter      MeterConfig
	settler    Settler
	accounts   Accounts
	sendPolicy agent.SendPolicy

	mu      sync.Mutex
	records map[string]Deployment
	// inbox holds the messages delivered to each agent by a permitted send()
	// from another agent on this node. It is keyed by recipient deployment id.
	// A message only lands here when the send policy permits the target AND the
	// target is a known deployment on this node; that is what a target "name"
	// resolves to (see deliver).
	inbox map[string][]agent.Message
	// nonce is a per-deployer metering-transfer nonce sequence so repeated
	// same-amount charges from one deployer remain distinct consensus
	// transactions (the transfer nonce is a uniquifier; see
	// consensus.SubmitTransfer).
	nonce map[string]uint64
}

// NewManager builds a Manager over the given kv store and reloads any persisted
// deployments so a restart preserves the deploy list. It validates that a
// metering-enabled config supplies a recipient, a settler, and an accounts
// resolver, so a node cannot come up "metered" yet unable to actually charge.
func NewManager(cfg ManagerConfig) (*Manager, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("agentapi: kv store is required")
	}
	maxBytes := cfg.MaxModuleBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxModuleBytes
	}
	if cfg.Meter.Enabled() {
		if cfg.Meter.Recipient == "" {
			return nil, fmt.Errorf("agentapi: metering price is set but no recipient is configured")
		}
		if cfg.Settler == nil {
			return nil, fmt.Errorf("agentapi: metering price is set but no consensus settler is configured")
		}
		if cfg.Accounts == nil {
			return nil, fmt.Errorf("agentapi: metering price is set but no accounts resolver is configured")
		}
	}
	m := &Manager{
		store:      cfg.Store,
		maxBytes:   maxBytes,
		meter:      cfg.Meter,
		settler:    cfg.Settler,
		accounts:   cfg.Accounts,
		sendPolicy: cfg.SendPolicy,
		records:    make(map[string]Deployment),
		nonce:      make(map[string]uint64),
		inbox:      make(map[string][]agent.Message),
	}
	if err := m.reload(); err != nil {
		return nil, err
	}
	return m, nil
}

// reload reads every persisted deployment record from the store into memory so
// ListAgents/GetAgent reflect deployments made before a restart.
func (m *Manager) reload() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store.Iterate([]byte(recordPrefix), func(key, value []byte) error {
		var d Deployment
		if err := json.Unmarshal(value, &d); err != nil {
			return fmt.Errorf("agentapi: corrupt deployment record %q: %w", key, err)
		}
		m.records[d.ID] = d
		return nil
	})
}

// DeployResult reports the outcome of a DeployAgent call: the persisted record,
// whether the deploy-time run succeeded, and the metered charge that settled
// through consensus.
type DeployResult struct {
	Deployment Deployment
	Ran        bool
	Charged    uint64
}

// Deploy persists a submitted module under id, meters the run through consensus
// (when a price is configured), then instantiates and runs the module once,
// recording the outcome. Metering happens BEFORE the run so a deploy that cannot
// be paid for never runs for free: if the deployer cannot afford the charge or
// has no signing key, Deploy returns an error and does not run the module.
//
// deployer names the paying account; when empty the configured DefaultDeployer
// is used. limits, when non-zero, override the runtime defaults.
func (m *Manager) Deploy(ctx context.Context, id string, module []byte, limits agent.ResourceLimits, deployer string) (*DeployResult, error) {
	if id == "" {
		return nil, ErrEmptyID
	}
	if len(module) == 0 {
		return nil, ErrEmptyModule
	}
	if len(module) > m.maxBytes {
		return nil, fmt.Errorf("%w: %d bytes exceeds the %d-byte limit", ErrModuleTooLarge, len(module), m.maxBytes)
	}
	// Fill any unset limit field from the runtime defaults, then validate. A
	// request may legitimately omit limits (the proto documents unset == default),
	// so a zero MaxMemoryPages means "use the default", not "invalid".
	limits = normalizeLimits(limits)
	if err := limits.Validate(); err != nil {
		return nil, fmt.Errorf("agentapi: %w", err)
	}

	// Meter first. A metered deploy the payer cannot cover is refused here,
	// before any module runs, so a run never happens for free.
	charged, err := m.charge(ctx, deployer)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	sum := sha256.Sum256(module)
	rec := Deployment{
		ID:             id,
		Status:         StatusDeployed,
		ModuleHash:     hex.EncodeToString(sum[:]),
		ModuleSize:     uint64(len(module)),
		MaxMemoryPages: limits.MaxMemoryPages,
		MaxRunTimeMS:   uint64(limits.MaxRunTime / time.Millisecond),
		LastCharge:     charged,
		CreatedAtNS:    now.UnixNano(),
	}
	// Preserve the original created-at across a re-deploy of the same id.
	m.mu.Lock()
	if prev, ok := m.records[id]; ok && prev.CreatedAtNS != 0 {
		rec.CreatedAtNS = prev.CreatedAtNS
	}
	m.mu.Unlock()

	// Run the module once and capture its startup output/error.
	output, runErr := m.run(ctx, id, module, limits)
	rec.LastRunAtNS = time.Now().UnixNano()
	rec.LastOutput = output
	if runErr != nil {
		rec.Status = StatusFailed
		rec.LastError = runErr.Error()
	} else {
		rec.Status = StatusRunning
	}

	if err := m.persist(rec, module); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.records[id] = rec
	m.mu.Unlock()

	return &DeployResult{Deployment: rec, Ran: runErr == nil, Charged: charged}, nil
}

// charge settles the per-run metering price from the deployer to the configured
// recipient through consensus, returning the amount charged (0 when metering is
// disabled). It enforces the honest-refusal rule: a metered deploy whose payer
// has no signing key, or whose charge commits but is skipped as unaffordable, is
// refused rather than run for free.
func (m *Manager) charge(ctx context.Context, deployer string) (uint64, error) {
	if !m.meter.Enabled() {
		return 0, nil
	}
	payer := deployer
	if payer == "" {
		payer = m.meter.DefaultDeployer
	}
	if payer == "" {
		return 0, fmt.Errorf("%w: metering is enabled but no deployer account was provided", ErrNoSigningAccount)
	}
	acct, ok := m.accounts.Account(payer)
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrNoSigningAccount, payer)
	}

	nonce := m.nextNonce(payer)
	tx, err := m.settler.SubmitAccountTransfer(acct, m.meter.Recipient, m.meter.Price, nonce)
	if err != nil {
		return 0, fmt.Errorf("agentapi: submit metering charge: %w", err)
	}
	committed, applied, err := m.settler.WaitForSettlement(ctx, tx)
	if err != nil {
		return 0, fmt.Errorf("agentapi: awaiting metering settlement: %w", err)
	}
	if !committed || !applied {
		// Committed but skipped as unaffordable (or not committed): no credits
		// moved. Refuse the deploy rather than running for free.
		return 0, fmt.Errorf("agentapi: charge %d from %s: %w", m.meter.Price, payer, ErrMeterNotApplied)
	}
	return m.meter.Price, nil
}

// nextNonce returns and advances the per-deployer metering-transfer nonce.
func (m *Manager) nextNonce(deployer string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := m.nonce[deployer]
	m.nonce[deployer] = n + 1
	return n
}

// run instantiates the module and runs its _start once under the given limits,
// returning whatever it wrote to stdout. The agent is stopped before run
// returns so no runtime outlives the call; a persisted deployment is a stored
// module plus its last outcome, not a live process.
func (m *Manager) run(ctx context.Context, id string, module []byte, limits agent.ResourceLimits) (string, error) {
	var out strings.Builder
	// Build the send handler from the configured policy. NewSendFunc returns nil
	// when the policy permits nothing (disabled or empty allowlist), which the
	// runtime treats as "refuse every send and say so" - the secure default. When
	// the operator has opted in, a permitted target is delivered into the
	// recipient agent's inbox that this Manager owns (see deliver).
	send := agent.NewSendFunc(m.sendPolicy, agent.DelivererFunc(m.deliver))
	a, err := agent.New(ctx, agent.Config{
		ID:     id,
		Code:   module,
		Stdout: &out,
		Stderr: &out,
		Send:   send,
	}, limits)
	if err != nil {
		return out.String(), err
	}
	defer func() { _ = a.Stop(ctx) }()
	if err := a.Start(ctx); err != nil {
		return out.String(), err
	}
	return out.String(), nil
}

// deliver resolves a permitted target name to a recipient and delivers a
// payload to it. It is the "what a name means" half of the send policy: a name
// is a deployment id on THIS node, and delivery appends the message to that
// deployment's inbox. A name the policy permitted but that names no deployment
// on this node does not resolve to a recipient, so it is a delivery error - the
// runtime surfaces it to the sending guest's stderr and records the attempt
// either way. Nothing off-node is addressable.
//
// The policy has already decided the target is permitted before deliver is
// called (see agent.NewSendFunc); deliver never widens that decision.
func (m *Manager) deliver(target string, payload []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.records[target]; !ok {
		return fmt.Errorf("agentapi: no agent named %q is deployed on this node", target)
	}
	m.inbox[target] = append(m.inbox[target], agent.Message{
		Target:  target,
		Payload: append([]byte(nil), payload...),
	})
	return nil
}

// Inbox returns the messages delivered to the agent named id by permitted
// send() calls from other agents on this node, in delivery order. It returns a
// copy so a caller cannot mutate the stored messages. It is how a test or an
// operator observes that a permitted send was actually delivered.
func (m *Manager) Inbox(id string) []agent.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	msgs := m.inbox[id]
	out := make([]agent.Message, len(msgs))
	for i, msg := range msgs {
		out[i] = agent.Message{Target: msg.Target, Payload: append([]byte(nil), msg.Payload...)}
	}
	return out
}

// persist writes a deployment record and its module bytes to the store in one
// atomic batch, so a record is never visible without the module that backs it.
func (m *Manager) persist(rec Deployment, module []byte) error {
	payload, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("agentapi: marshal deployment %q: %w", rec.ID, err)
	}
	batch := m.store.NewBatch()
	defer batch.Close()
	if err := batch.Set([]byte(recordPrefix+rec.ID), payload, nil); err != nil {
		return fmt.Errorf("agentapi: stage deployment record: %w", err)
	}
	if err := batch.Set([]byte(modulePrefix+rec.ID), module, nil); err != nil {
		return fmt.Errorf("agentapi: stage module bytes: %w", err)
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return fmt.Errorf("agentapi: commit deployment %q: %w", rec.ID, err)
	}
	return nil
}

// Get returns the persisted deployment for id.
func (m *Manager) Get(id string) (Deployment, error) {
	if id == "" {
		return Deployment{}, ErrEmptyID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.records[id]
	if !ok {
		return Deployment{}, fmt.Errorf("%w: %q", ErrAgentNotFound, id)
	}
	return d, nil
}

// List returns all persisted deployments ordered by ID.
func (m *Manager) List() []Deployment {
	m.mu.Lock()
	out := make([]Deployment, 0, len(m.records))
	for _, d := range m.records {
		out = append(out, d)
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
