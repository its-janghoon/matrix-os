package marketexchange

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
	"github.com/ecirlabs/matrix-core/internal/transport"
)

// DefaultProviderTTL is how long a received announcement keeps a remote provider
// in the discovery registry before it is considered stale and aged out. A
// provider that keeps announcing (re-announcing within the TTL) stays visible;
// one that goes quiet disappears from discovery.
const DefaultProviderTTL = 2 * time.Minute

// Transport is the subset of internal/transport.Transport the Exchange needs. It
// is an interface so tests can drive the receive/apply handlers directly with an
// in-memory fake, while production wiring passes the real gossipsub transport.
type Transport interface {
	// Subscribe joins topic and returns a channel of received messages. The
	// channel is closed when ctx is cancelled.
	Subscribe(ctx context.Context, topic string) (<-chan transport.Message, error)
	// Publish sends data to topic. The topic must have been joined (via
	// Subscribe) first, matching the real transport's join-before-publish rule.
	Publish(ctx context.Context, topic string, data []byte) error
}

// RemoteProvider is a provider discovered from a received announcement, together
// with the local time the announcement was received (ReceivedAt) so the registry
// can age out stale entries.
type RemoteProvider struct {
	market.Provider
	PublicKey  ed25519.PublicKey
	PeerID     string
	ReceivedAt time.Time
}

// Exchange is the P2P marketplace exchange. It subscribes to the gossip topics,
// maintains a registry of remote providers discovered from announcements,
// publishes signed announcements/job-requests/settlements, and applies received
// settlements to the local ledger through the signed-settlement path.
//
// Settlement semantics (important): each node keeps its OWN independent token
// chain and ledger. A signed settlement transaction commits to node-local state
// (the sender's per-node nonce and that node's chain head hash), so a gossiped
// settlement applies successfully on exactly the one receiving node whose local
// chain head and sender nonce match the transaction the sender built against it,
// namely the counterparty (provider) node the buyer settled with. On every other
// node the SettledLedger.Settle apply fails the nonce or prev-hash check and is
// dropped. This is deliberately NOT a shared/global ledger or a consensus
// protocol: there is no network-wide agreement on balances or ordering. It is a
// signed message bus over independent local ledgers, where settlement is a
// pairwise fact between a buyer and a specific provider node.
//
// The receive loops follow the goroutine + context-cancellation pattern used by
// internal/transport.Transport.Subscribe: each loop reads from a channel that
// the transport closes on ctx cancellation, so cancelling the node context stops
// the Exchange cleanly with no separate stop signal required.
type Exchange struct {
	transport Transport
	settled   *token.SettledLedger
	peerID    string

	// providerTTL bounds how long a remote provider stays discoverable after its
	// last announcement.
	providerTTL time.Duration
	// now is the clock, injectable so tests can control staleness deterministically.
	now func() time.Time

	mu        sync.RWMutex
	providers map[string]RemoteProvider

	wg      sync.WaitGroup
	started bool
}

// Config configures an Exchange.
type Config struct {
	// Transport is the gossip transport (required).
	Transport Transport
	// Settled is the signed-settlement entrypoint into the local ledger and token
	// chain (required). Received settlements are applied through it.
	Settled *token.SettledLedger
	// PeerID is this node's libp2p peer ID string, embedded in outgoing
	// announcements so remote buyers know where to reach the provider.
	PeerID string
	// ProviderTTL overrides DefaultProviderTTL when non-zero.
	ProviderTTL time.Duration
	// Now overrides the wall clock when non-nil (used by tests).
	Now func() time.Time
}

// New constructs an Exchange from cfg. It does not start any goroutines; call
// Start to subscribe and begin processing.
func New(cfg Config) (*Exchange, error) {
	if cfg.Transport == nil {
		return nil, fmt.Errorf("marketexchange: transport is required")
	}
	if cfg.Settled == nil {
		return nil, fmt.Errorf("marketexchange: settled ledger is required")
	}
	ttl := cfg.ProviderTTL
	if ttl <= 0 {
		ttl = DefaultProviderTTL
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &Exchange{
		transport:   cfg.Transport,
		settled:     cfg.Settled,
		peerID:      cfg.PeerID,
		providerTTL: ttl,
		now:         nowFn,
		providers:   make(map[string]RemoteProvider),
	}, nil
}

// Start subscribes to the announce, jobs and settle topics and launches a
// receive loop per topic. It must be called once. The provided ctx governs the
// lifetime of every loop: when ctx is cancelled the transport closes the
// subscription channels and the loops return, so a caller only needs to cancel
// ctx (and optionally Wait) to shut the Exchange down.
//
// Subscribing to a topic also joins it, which the real transport requires before
// Publish will accept a message on that topic; subscribing up front therefore
// makes AnnounceProvider/SubmitRemoteJob/PublishSettlement immediately usable.
func (e *Exchange) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return fmt.Errorf("marketexchange: already started")
	}
	e.started = true
	e.mu.Unlock()

	announceCh, err := e.transport.Subscribe(ctx, TopicAnnounce)
	if err != nil {
		return fmt.Errorf("marketexchange: subscribe announce: %w", err)
	}
	jobsCh, err := e.transport.Subscribe(ctx, TopicJobs)
	if err != nil {
		return fmt.Errorf("marketexchange: subscribe jobs: %w", err)
	}
	settleCh, err := e.transport.Subscribe(ctx, TopicSettle)
	if err != nil {
		return fmt.Errorf("marketexchange: subscribe settle: %w", err)
	}

	e.wg.Add(3)
	go e.runLoop(ctx, announceCh, e.handleAnnouncement)
	go e.runLoop(ctx, jobsCh, e.handleJobRequest)
	go e.runLoop(ctx, settleCh, e.handleSettlement)
	return nil
}

// runLoop consumes messages from ch until ctx is cancelled or the channel is
// closed, dispatching each payload to handle. It mirrors the cancellation
// pattern in transport.Subscribe: the transport closes ch on ctx cancellation,
// so the range terminates without a dedicated stop channel.
func (e *Exchange) runLoop(ctx context.Context, ch <-chan transport.Message, handle func(transport.Message)) {
	defer e.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			handle(msg)
		}
	}
}

// Wait blocks until all receive loops have exited, which happens after the
// Start ctx is cancelled. It is safe to call after Start; calling it before
// Start returns immediately.
func (e *Exchange) Wait() {
	e.wg.Wait()
}

// handleAnnouncement decodes and verifies a ProviderAnnouncement and, if valid,
// updates the remote-provider registry. Invalid or unverifiable messages are
// dropped silently (gossip is best-effort; a malformed message from a peer must
// not crash the loop).
func (e *Exchange) handleAnnouncement(msg transport.Message) {
	var ann ProviderAnnouncement
	if err := json.Unmarshal(msg.Payload, &ann); err != nil {
		return
	}
	if err := ann.Verify(); err != nil {
		return
	}
	e.mu.Lock()
	e.providers[ann.ProviderID] = RemoteProvider{
		Provider: market.Provider{
			ID:           ann.ProviderID,
			Capacity:     ann.Capacity,
			PricePerUnit: ann.PricePerUnit,
			Available:    ann.Available,
			Models:       ann.Models,
		},
		PublicKey:  ann.PublicKey,
		PeerID:     ann.PeerID,
		ReceivedAt: e.now(),
	}
	e.mu.Unlock()
}

// handleJobRequest decodes and verifies a JobRequest. Verified requests are
// accepted (a provider node would act on them); invalid ones are dropped. The
// hook exists so the settlement path and future scheduling can build on verified
// requests; today it validates and records nothing beyond the verification gate,
// keeping the wire contract enforced end to end.
func (e *Exchange) handleJobRequest(msg transport.Message) {
	var req JobRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return
	}
	if err := req.Verify(); err != nil {
		return
	}
	// A verified request is addressed to a specific provider. Only the targeted
	// provider would fulfil it; other nodes ignore it. We record nothing here
	// because job bookkeeping happens on settlement, but the verification gate
	// guarantees no unsigned/forged request is ever acted upon.
}

// handleSettlement decodes and verifies a Settlement and, if valid, attempts to
// apply it to THIS node's local ledger through the signed-settlement path
// (token.SettledLedger.Settle). Because the transaction commits to node-local
// state (the sender's nonce and this node's chain head), the apply succeeds only
// on the specific counterparty node the transaction was built against; on every
// other receiving node it fails the nonce or prev-hash check. Invalid
// settlements and settlements that do not apply to local state (bad nonce,
// unaffordable, prev-hash mismatch) are dropped silently: the signed path is
// authoritative and rejects anything inconsistent with local state, so a
// settlement that is not for this node simply has no effect here. This is not a
// network-wide balance update; it is the receiving counterparty recording the
// pairwise settlement on its own chain.
func (e *Exchange) handleSettlement(msg transport.Message) {
	var st Settlement
	if err := json.Unmarshal(msg.Payload, &st); err != nil {
		return
	}
	if err := st.Verify(); err != nil {
		return
	}
	// Copy the transaction so Settle operates on a stable value.
	tx := st.Tx
	_, _ = e.settled.Settle(&tx)
}

// ApplySettlement verifies and applies a settlement directly, returning the
// resulting chain record or an error. It is the synchronous form of the receive
// path: the gossip handler calls into the same SettledLedger.Settle, but this
// method surfaces the error so callers (and tests) can assert on the outcome.
func (e *Exchange) ApplySettlement(st *Settlement) (*token.Record, error) {
	if err := st.Verify(); err != nil {
		return nil, err
	}
	tx := st.Tx
	return e.settled.Settle(&tx)
}

// AnnounceProvider signs a ProviderAnnouncement for acct advertising p's
// capacity/pricing and publishes it on the announce topic. The announcement is
// stamped with the current time and this node's peer ID so remote buyers can age
// it out and know where to reach the provider.
func (e *Exchange) AnnounceProvider(ctx context.Context, acct *token.Account, p market.Provider) error {
	if acct == nil {
		return fmt.Errorf("marketexchange: account is required to announce")
	}
	ann := ProviderAnnouncement{
		ProviderID:   acct.AccountID(),
		PublicKey:    acct.PublicKey,
		Capacity:     p.Capacity,
		PricePerUnit: p.PricePerUnit,
		Available:    p.Available,
		Models:       p.Models,
		PeerID:       e.peerID,
		Timestamp:    e.now().UnixNano(),
	}
	if err := ann.Sign(acct.PrivateKey); err != nil {
		return err
	}
	data, err := json.Marshal(&ann)
	if err != nil {
		return fmt.Errorf("marketexchange: marshal announcement: %w", err)
	}
	if err := e.transport.Publish(ctx, TopicAnnounce, data); err != nil {
		return fmt.Errorf("marketexchange: publish announcement: %w", err)
	}
	return nil
}

// SubmitRemoteJob discovers the target provider in the registry, then signs and
// publishes a JobRequest from buyer for the requested units. It returns
// ErrProviderNotFound if the provider is unknown or has aged out, so a buyer
// cannot submit into the void.
func (e *Exchange) SubmitRemoteJob(ctx context.Context, buyer *token.Account, providerID string, units uint64, nonce uint64) (*JobRequest, error) {
	if buyer == nil {
		return nil, fmt.Errorf("marketexchange: buyer account is required")
	}
	if _, ok := e.LookupRemoteProvider(providerID); !ok {
		return nil, fmt.Errorf("marketexchange: remote provider %q: %w", providerID, market.ErrProviderNotFound)
	}
	req := JobRequest{
		BuyerID:   buyer.AccountID(),
		PublicKey: buyer.PublicKey,
		Provider:  providerID,
		Units:     units,
		Nonce:     nonce,
		Timestamp: e.now().UnixNano(),
	}
	if err := req.Sign(buyer.PrivateKey); err != nil {
		return nil, err
	}
	data, err := json.Marshal(&req)
	if err != nil {
		return nil, fmt.Errorf("marketexchange: marshal job request: %w", err)
	}
	if err := e.transport.Publish(ctx, TopicJobs, data); err != nil {
		return nil, fmt.Errorf("marketexchange: publish job request: %w", err)
	}
	reqCopy := req
	return &reqCopy, nil
}

// PublishSettlement signs nothing (the transaction is already signed by its
// sender) and publishes the settlement on the settle topic. The transaction is
// built against one specific counterparty node's chain head and the sender's
// nonce on that node, so it applies on that one receiver and is dropped
// everywhere else (see handleSettlement); publishing over gossip is how that
// counterparty receives it, not a broadcast that updates balances network-wide.
// The caller is responsible for having produced a valid signed transaction
// (typically via token helpers); PublishSettlement verifies it before publishing
// so a malformed settlement is never gossiped.
func (e *Exchange) PublishSettlement(ctx context.Context, st *Settlement) error {
	if err := st.Verify(); err != nil {
		return err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("marketexchange: marshal settlement: %w", err)
	}
	if err := e.transport.Publish(ctx, TopicSettle, data); err != nil {
		return fmt.Errorf("marketexchange: publish settlement: %w", err)
	}
	return nil
}

// LookupRemoteProvider returns a single non-stale remote provider by ID. A
// provider whose last announcement is older than the TTL is treated as absent.
func (e *Exchange) LookupRemoteProvider(id string) (RemoteProvider, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	rp, ok := e.providers[id]
	if !ok {
		return RemoteProvider{}, false
	}
	if e.isStale(rp) {
		return RemoteProvider{}, false
	}
	return rp, true
}

// ListRemoteProviders returns all currently-known, non-stale remote providers
// sorted by ID, so buyers and the gRPC API can enumerate cross-network capacity.
// Stale entries are both filtered from the result and pruned from the registry
// so it does not grow unbounded with departed providers.
func (e *Exchange) ListRemoteProviders() []RemoteProvider {
	e.mu.Lock()
	defer e.mu.Unlock()

	out := make([]RemoteProvider, 0, len(e.providers))
	for id, rp := range e.providers {
		if e.isStale(rp) {
			delete(e.providers, id)
			continue
		}
		out = append(out, rp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// isStale reports whether rp's last announcement is older than the TTL. Callers
// must hold e.mu (read or write).
func (e *Exchange) isStale(rp RemoteProvider) bool {
	return e.now().Sub(rp.ReceivedAt) > e.providerTTL
}
