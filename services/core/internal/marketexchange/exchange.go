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

// RemoteProvider is one offer discovered from a received announcement: a serving
// node, a payout account, and the signed quote between them. The embedded
// market.Provider preserves the complete signed quote (CostPerUnit,
// MarkupBasisPoints, QuoteID, QuoteVersion, ObservedAt, and ValidUntil) alongside
// capacity and model data. ReceivedAt is local receipt time and controls gossip
// liveness independently from the quote's ValidUntil.
//
// FirstSeen and Announcements are the honest half of a reputation. They are
// measured HERE, by the node doing the reading, from messages it received - not
// reported by the seller about itself. A seller's own uptime figure is a claim
// and costs nothing to inflate; "this node has been announcing to me for six
// days and I have heard 812 of its announcements" is something the reader
// watched happen. It is still only one observer's view, and gossip can drop a
// message for reasons that are the observer's fault, so it is evidence about a
// node's presence rather than a service-level guarantee - which is exactly how
// it should be presented to a buyer.
type RemoteProvider struct {
	market.Provider
	// NodeID is the account id of the node that signed this announcement, and
	// the counterparty a buyer connecting to Endpoint is choosing.
	NodeID string
	// Endpoint is the base URL the announcing node serves buyers on. Empty means
	// it sells compute units and has no HTTP address to give.
	Endpoint   string
	PublicKey  ed25519.PublicKey
	PeerID     string
	ReceivedAt time.Time
	// FirstSeen is when this node first heard from that announcer, and
	// Announcements counts how many it has accepted since. Both are observed,
	// never announced.
	FirstSeen     time.Time
	Announcements uint64
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
	transport   Transport
	settled     *token.SettledLedger
	localMarket *market.Market
	peerID      string
	endpoint    string

	// providerTTL bounds how long a remote provider stays discoverable after its
	// last announcement.
	providerTTL time.Duration
	// now is the clock, injectable so tests can control staleness deterministically.
	now func() time.Time

	mu sync.RWMutex
	// providers is keyed by offerKey(NodeID, ProviderID). Neither id alone is
	// unique: one node announces every backend it runs, and two nodes may settle
	// into the same payout account (an operator running a second box). Keyed on
	// either alone, the second announcement silently replaces the first and half
	// the directory disappears.
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
	// Market owns local providers and durable jobs. When set, valid requests
	// addressed to a local provider are conditionally reserved against their
	// exact signed quote snapshot. Observer-only exchanges may leave it nil.
	Market *market.Market
	// PeerID is this node's libp2p peer ID string, embedded in outgoing
	// announcements so remote nodes know which peer announced.
	PeerID string
	// Endpoint is the base URL buyers reach this node on, published in its
	// announcements. A peer id is how NODES find each other and is not something
	// a buyer's HTTP client can dial, which is why announcing one without the
	// other made a directory nobody could act on.
	//
	// Empty means this node announces no address: it is selling compute units,
	// or its operator has not published one yet.
	Endpoint string
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
	// Checked here so a misconfigured address fails the node at startup, where an
	// operator is watching, rather than silently failing every announcement later
	// with nobody reading the log.
	if err := ValidateEndpoint(cfg.Endpoint); err != nil {
		return nil, fmt.Errorf("marketexchange: market.endpoint: %w", err)
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
		localMarket: cfg.Market,
		peerID:      cfg.PeerID,
		endpoint:    cfg.Endpoint,
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
	now := e.now().UTC()
	if err := ann.VerifyAt(now); err != nil {
		return
	}
	key := offerKey(ann.NodeID, ann.ProviderID)

	e.mu.Lock()
	firstSeen := now
	var heard uint64
	if existing, ok := e.providers[key]; ok {
		if ann.QuoteVersion < existing.QuoteVersion {
			e.mu.Unlock()
			return
		}
		if ann.QuoteVersion == existing.QuoteVersion && !sameEconomicQuote(existing.Provider, ann) {
			e.mu.Unlock()
			return
		}
		// Carried across the replacement, because the whole value of these two is
		// that they accumulate. Reset on every quote refresh - which is most
		// announcements - they would say nothing except that the seller is still
		// there right now, which ReceivedAt already says.
		firstSeen = existing.FirstSeen
		heard = existing.Announcements
	}
	e.providers[key] = RemoteProvider{
		Provider: market.Provider{
			ID:                ann.ProviderID,
			Capacity:          ann.Capacity,
			PricePerUnit:      ann.PricePerUnit,
			CostPerUnit:       ann.CostPerUnit,
			MarkupBasisPoints: ann.MarkupBasisPoints,
			QuoteID:           ann.QuoteID,
			QuoteVersion:      ann.QuoteVersion,
			ObservedAt:        ann.ObservedAt.UTC(),
			ValidUntil:        ann.ValidUntil.UTC(),
			Available:         ann.Available,
			Models:            ann.Models,
		},
		NodeID:        ann.NodeID,
		Endpoint:      ann.Endpoint,
		PublicKey:     ann.PublicKey,
		PeerID:        ann.PeerID,
		ReceivedAt:    now,
		FirstSeen:     firstSeen,
		Announcements: heard + 1,
	}
	e.mu.Unlock()
}

// offerKey identifies one node's offer of one payout account.
func offerKey(nodeID, providerID string) string {
	return nodeID + "\x00" + providerID
}

// sameEconomicQuote reports whether an equal-version announcement preserves
// the existing signed economic terms. Equal versions may refresh liveness,
// capacity, availability, peer, and models, but changing price identity or
// validity requires a strictly newer QuoteVersion.
func sameEconomicQuote(existing market.Provider, incoming ProviderAnnouncement) bool {
	return existing.PricePerUnit == incoming.PricePerUnit &&
		existing.CostPerUnit == incoming.CostPerUnit &&
		existing.MarkupBasisPoints == incoming.MarkupBasisPoints &&
		existing.QuoteID == incoming.QuoteID &&
		existing.ObservedAt.Equal(incoming.ObservedAt) &&
		existing.ValidUntil.Equal(incoming.ValidUntil)
}

// handleJobRequest verifies a v2 request at receipt time and attempts one
// durable conditional reservation. Gossip is best-effort, so rejection remains
// silent here; acceptJobRequest is the synchronous form used by tests and future
// request/receipt transports.
func (e *Exchange) handleJobRequest(msg transport.Message) {
	var req JobRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return
	}
	_, _ = e.acceptJobRequest(&req)
}

// acceptJobRequest rejects expired, refreshed, mismatched, unaffordable, or
// over-capacity requests and persists the exact accepted quote snapshot for a
// valid local provider request. Quote refresh and reservation are serialized by
// Market, closing the check-then-reserve race.
func (e *Exchange) acceptJobRequest(req *JobRequest) (*market.Job, error) {
	now := e.now().UTC()
	if err := req.VerifyAt(now); err != nil {
		return nil, err
	}
	if e.localMarket == nil {
		return nil, fmt.Errorf("marketexchange: no local market configured")
	}
	return e.localMarket.ReserveRemoteJob(
		req.BuyerID,
		req.Provider,
		req.Units,
		req.Nonce,
		req.Digest(),
		req.AcceptedQuote(),
		now,
	)
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

// AnnounceProvider publishes one of this node's providers to the directory: the
// node signs, naming p as the payout account and itself as the address to reach.
//
// The node key signs rather than the provider's because the node is what a buyer
// is choosing - it holds the model, takes the reservation and answers at the
// endpoint - and because a payout account may be a wallet address, which has no
// ed25519 key to sign with at all. See ProviderAnnouncement for why those two
// identities were separated.
//
// The announcement timestamp uses the current UTC clock; quote observation and
// validity come from p and must already satisfy the market quote policy.
func (e *Exchange) AnnounceProvider(ctx context.Context, node *token.Account, p market.Provider) error {
	if node == nil {
		return fmt.Errorf("marketexchange: node account is required to announce")
	}
	if p.ID == "" {
		return fmt.Errorf("marketexchange: provider id is required to announce")
	}
	now := e.now().UTC()
	ann := ProviderAnnouncement{
		NodeID:            node.AccountID(),
		Endpoint:          e.endpoint,
		ProviderID:        p.ID,
		PublicKey:         node.PublicKey,
		Capacity:          p.Capacity,
		PricePerUnit:      p.PricePerUnit,
		CostPerUnit:       p.CostPerUnit,
		MarkupBasisPoints: p.MarkupBasisPoints,
		QuoteID:           p.QuoteID,
		QuoteVersion:      p.QuoteVersion,
		ObservedAt:        p.ObservedAt.UTC(),
		ValidUntil:        p.ValidUntil.UTC(),
		Available:         p.Available,
		Models:            p.Models,
		PeerID:            e.peerID,
		Timestamp:         now.UnixNano(),
	}
	if err := ann.Sign(node.PrivateKey); err != nil {
		return err
	}
	if err := ann.VerifyAt(now); err != nil {
		return fmt.Errorf("marketexchange: invalid provider announcement: %w", err)
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

// SubmitRemoteJob discovers one complete remote-provider snapshot, signs every
// accepted v2 quote term plus the exact fixed Total, and publishes it. A quote
// that expires between lookup and signing is rejected locally; a provider quote
// refresh after publication is rejected atomically on receipt.
func (e *Exchange) SubmitRemoteJob(ctx context.Context, buyer *token.Account, providerID string, units uint64, nonce uint64) (*JobRequest, error) {
	if buyer == nil {
		return nil, fmt.Errorf("marketexchange: buyer account is required")
	}
	rp, ok := e.LookupRemoteProvider(providerID)
	if !ok {
		return nil, fmt.Errorf("marketexchange: remote provider %q: %w", providerID, market.ErrProviderNotFound)
	}
	if units == 0 || rp.Available < units {
		return nil, fmt.Errorf("marketexchange: remote provider %q has %d units available, need %d: %w",
			providerID, rp.Available, units, market.ErrInsufficientCapacity)
	}
	total, err := market.CheckedMul(units, rp.PricePerUnit)
	if err != nil {
		return nil, fmt.Errorf("marketexchange: price remote job: %w", err)
	}
	now := e.now().UTC()
	req := JobRequest{
		BuyerID:         buyer.AccountID(),
		PublicKey:       buyer.PublicKey,
		Provider:        providerID,
		Units:           units,
		PricePerUnit:    rp.PricePerUnit,
		QuoteID:         rp.QuoteID,
		QuoteVersion:    rp.QuoteVersion,
		QuoteObservedAt: rp.ObservedAt.UTC(),
		QuoteValidUntil: rp.ValidUntil.UTC(),
		Total:           total,
		Nonce:           nonce,
		Timestamp:       now.UnixNano(),
	}
	if err := req.Sign(buyer.PrivateKey); err != nil {
		return nil, err
	}
	if err := req.VerifyAt(now); err != nil {
		return nil, fmt.Errorf("marketexchange: invalid remote job request: %w", err)
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

// LookupOffer returns one node's offer of one payout account, only while both
// independent freshness conditions hold: its announcement receipt is within
// providerTTL and its signed economic quote remains valid.
func (e *Exchange) LookupOffer(nodeID, providerID string) (RemoteProvider, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	rp, ok := e.providers[offerKey(nodeID, providerID)]
	if !ok {
		return RemoteProvider{}, false
	}
	if e.isUnavailableAt(rp, e.now().UTC()) {
		return RemoteProvider{}, false
	}
	return rp, true
}

// LookupRemoteProvider returns the cheapest fresh offer for a payout account,
// whichever node is making it.
//
// It exists because a provider id no longer identifies one offer: a node
// announces every backend it runs, and an operator with two boxes settles both
// into the same account. Callers that name only a provider get the best price
// on offer for it, deterministically - ties break on node id so two nodes
// reading the same announcements make the same choice.
//
// A caller that means a SPECIFIC node's offer wants LookupOffer. The difference
// matters for anything a buyer is quoted, because the two boxes behind one
// payout account can advertise different prices and different capacity.
func (e *Exchange) LookupRemoteProvider(providerID string) (RemoteProvider, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	now := e.now().UTC()
	var (
		best  RemoteProvider
		found bool
	)
	for _, rp := range e.providers {
		if rp.ID != providerID || e.isUnavailableAt(rp, now) {
			continue
		}
		if !found || rp.PricePerUnit < best.PricePerUnit ||
			(rp.PricePerUnit == best.PricePerUnit && rp.NodeID < best.NodeID) {
			best, found = rp, true
		}
	}
	return best, found
}

// ListRemoteProviders returns all remotely announced providers whose receipt
// TTL and quote validity are both current, sorted by ID. Unavailable entries are
// filtered and pruned so expired quotes cannot reach listing or model-routing
// callers and departed providers do not accumulate in the registry.
func (e *Exchange) ListRemoteProviders() []RemoteProvider {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := e.now().UTC()
	out := make([]RemoteProvider, 0, len(e.providers))
	for key, rp := range e.providers {
		if e.isUnavailableAt(rp, now) {
			delete(e.providers, key)
			continue
		}
		out = append(out, rp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NodeID != out[j].NodeID {
			return out[i].NodeID < out[j].NodeID
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// isUnavailableAt keeps gossip liveness and economic validity as separate
// checks. Receipt TTL is measured from the local ReceivedAt clock; quote expiry
// is measured from the provider-signed ValidUntil instant.
func (e *Exchange) isUnavailableAt(rp RemoteProvider, now time.Time) bool {
	receiptStale := now.Sub(rp.ReceivedAt) > e.providerTTL
	quoteExpired := rp.ValidUntil.IsZero() || !rp.ValidUntil.After(now)
	return receiptStale || quoteExpired
}
