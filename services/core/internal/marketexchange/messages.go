// Package marketexchange implements the P2P marketplace exchange for Matrix OS.
//
// It turns the local, in-process compute marketplace (internal/market) into a
// networked one: providers announce their capacity and pricing over the
// existing libp2p gossip network (internal/transport), remote nodes discover
// those providers and populate a local registry, buyers submit signed job
// requests across the network, and cryptographically-signed settlement
// transactions (internal/token) propagate over P2P and are verified before
// being applied to the local ledger.
//
// Wire messages are encoded as JSON, matching the marketplace's existing JSON
// persistence style, and every message that mutates remote state carries an
// ed25519 signature that receivers verify before acting on it. Unsigned or
// invalid messages are rejected. The package depends only on the standard
// library plus the already-vendored libp2p and internal packages, adding no new
// module dependencies.
package marketexchange

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// Gossip topic names. These are stable, versioned identifiers so nodes running
// compatible protocol versions rendezvous on the same pubsub topics. Bumping the
// version suffix is how an incompatible wire change is rolled out.
const (
	// TopicAnnounce carries the v3 ProviderAnnouncement layout.
	//
	// V3 adds the two things that make an announcement usable by a BUYER rather
	// than only by another node: a reachable endpoint, and an announcer identity
	// distinct from the payout account. V2 carried neither, so a buyer who
	// received one learned that somebody somewhere sold a model and had no way to
	// reach them. It does not rendezvous with v2 for the same reason v2 did not
	// rendezvous with v1: a node that cannot validate the new fields cannot be
	// trusted to relay them.
	TopicAnnounce = "matrix/market/announce/v3"
	// providerAnnouncementSigningDomain cryptographically separates v3 provider
	// announcements from every other signed message and protocol version.
	providerAnnouncementSigningDomain = "matrix/market/provider-announcement/v3"
	// TopicJobs carries the incompatible v2 JobRequest layout. V2 binds the
	// buyer's signature to the exact provider quote and fixed total accepted.
	TopicJobs = "matrix/market/jobs/v2"
	// jobRequestSigningDomain separates v2 requests from announcements, token
	// transfers, and every prior/future job protocol version.
	jobRequestSigningDomain = "matrix/market/job-request/v2"
	// TopicSettle carries Settlement messages.
	TopicSettle = "matrix/market/settle/v1"
)

// Wire-message validation errors. Callers and tests may errors.Is against them.
var (
	// ErrUnsignedMessage is returned when a message carries no signature.
	ErrUnsignedMessage = errors.New("marketexchange: message is not signed")
	// ErrInvalidSignature is returned when a message signature does not verify
	// against its declared public key and canonical payload.
	ErrInvalidSignature = errors.New("marketexchange: invalid message signature")
	// ErrInvalidMessage is returned when a message is structurally invalid, for
	// example a malformed public key or an empty required field.
	ErrInvalidMessage = errors.New("marketexchange: invalid message")
)

// ProviderAnnouncement advertises a serving node's capacity, quote and address
// over the v3 announce topic.
//
// WHO SIGNS, AND WHY IT CHANGED. In v2 the signer WAS the provider: ProviderID
// had to equal the account id of PublicKey. That made two things impossible at
// once. A provider paid to a wallet - which is what the GPU runbook tells an
// operator to use, so revenue lands where they can spend it - could not be
// announced at all, because no ed25519 key derives an `eth:0x` id. And nothing
// in the node ever called the publish path, so no announcement was ever made on
// a real network: the discovery half of the marketplace was wired and unused.
//
// So v3 separates two identities v2 conflated:
//
//   - The ANNOUNCER is the serving node, and it signs. It is the thing reachable
//     at Endpoint, the thing that takes the reservation, and the thing that runs
//     the model - so it is the counterparty a buyer is actually choosing, and
//     its key is the one it has.
//   - The PAYOUT ACCOUNT (ProviderID) is data. It does not sign because it is
//     not authorising anything: it names where the node's revenue goes, which
//     the node already decides locally, and it may be a wallet address.
//
// This removes rather than adds an impersonation question. An announcement no
// longer claims to BE a provider, so there is nothing to impersonate: it says
// "I, this node, serve these models at this address for this price", and a buyer
// who disagrees connects to a different node.
//
// WHAT IS DELIBERATELY ABSENT: any self-reported uptime, latency or throughput.
// A number a seller publishes about its own reliability is a claim, not a fact,
// and it is free to inflate. What a buyer can actually rely on is measured by
// the OBSERVER - how long this node has been hearing announcements and how many
// it heard - and settled on the chain, where a paid job is a committed transfer
// nobody can fabricate. Those live in the registry and the ledger, not here.
//
// ObservedAt and ValidUntil use UTC instants and are encoded as Unix nanoseconds
// in the canonical signature payload. Timestamp is the announcement's
// Unix-nanosecond creation time. Signature is an ed25519 signature by PublicKey
// over every field plus the v3 signing domain.
type ProviderAnnouncement struct {
	// NodeID is the announcing node's stable account id, hex of PublicKey. The
	// signature binds it, so an announcement can only be made by the node it
	// names.
	NodeID string `json:"node_id"`
	// Endpoint is the base URL a buyer connects to, serving both the
	// OpenAI-compatible route and the Connect API. Signed, so a relaying peer
	// cannot redirect somebody else's traffic to a host of its choosing.
	//
	// Optional: a node selling compute units rather than inference has nothing
	// for a buyer's HTTP client to reach, and requiring an address it does not
	// have would keep it off the directory entirely.
	Endpoint string `json:"endpoint,omitempty"`
	// ProviderID is the payout account the node settles this provider's revenue
	// into, and the id its local order book uses. It may be an `eth:0x` address.
	ProviderID        string            `json:"provider_id"`
	PublicKey         ed25519.PublicKey `json:"public_key"`
	Capacity          uint64            `json:"capacity"`
	PricePerUnit      uint64            `json:"price_per_unit"`
	CostPerUnit       uint64            `json:"cost_per_unit,omitempty"`
	MarkupBasisPoints uint32            `json:"markup_basis_points,omitempty"`
	QuoteID           string            `json:"quote_id"`
	QuoteVersion      uint64            `json:"quote_version"`
	ObservedAt        time.Time         `json:"observed_at"`
	ValidUntil        time.Time         `json:"valid_until"`
	Available         uint64            `json:"available"`
	PeerID            string            `json:"peer_id"`
	// Models are the model identifiers the announcing provider serves, so a
	// remote provider can be routed to by model exactly like a local one. They
	// are signed with the rest of the announcement: an unsigned model list would
	// let any relaying peer advertise capabilities on someone else's behalf.
	Models    []string `json:"models,omitempty"`
	Timestamp int64    `json:"timestamp"`
	Signature []byte   `json:"signature"`
}

// signingBytes returns the canonical, deterministic, length-prefixed
// serialization of the signable fields (everything except Signature). Using the
// same length-prefixed layout as token.Transaction guarantees no two distinct
// field combinations collide into the same signed payload.
func (a *ProviderAnnouncement) signingBytes() []byte {
	buf := make([]byte, 0, 256)
	buf = appendLenPrefixed(buf, []byte(providerAnnouncementSigningDomain))
	buf = appendLenPrefixed(buf, []byte(a.NodeID))
	buf = appendLenPrefixed(buf, []byte(a.Endpoint))
	buf = appendLenPrefixed(buf, []byte(a.ProviderID))
	buf = appendLenPrefixed(buf, a.PublicKey)
	buf = appendUint64(buf, a.Capacity)
	buf = appendUint64(buf, a.PricePerUnit)
	buf = appendUint64(buf, a.CostPerUnit)
	buf = appendUint32(buf, a.MarkupBasisPoints)
	buf = appendLenPrefixed(buf, []byte(a.QuoteID))
	buf = appendUint64(buf, a.QuoteVersion)
	buf = appendUint64(buf, uint64(a.ObservedAt.UTC().UnixNano()))
	buf = appendUint64(buf, uint64(a.ValidUntil.UTC().UnixNano()))
	buf = appendUint64(buf, a.Available)
	buf = appendLenPrefixed(buf, []byte(a.PeerID))
	// The count is signed alongside the entries so no two distinct lists share a
	// payload (an empty list and a single empty model would otherwise collide).
	buf = appendUint64(buf, uint64(len(a.Models)))
	for _, m := range a.Models {
		buf = appendLenPrefixed(buf, []byte(m))
	}
	buf = appendUint64(buf, uint64(a.Timestamp))
	return buf
}

// Sign signs the announcement with priv, which must correspond to PublicKey, and
// stores the signature on the message.
func (a *ProviderAnnouncement) Sign(priv ed25519.PrivateKey) error {
	if err := checkKeyPair(a.PublicKey, priv); err != nil {
		return err
	}
	a.Signature = ed25519.Sign(priv, a.signingBytes())
	return nil
}

// Verify validates the announcement at the wall clock. Receive paths should use
// VerifyAt with their injected clock; this wrapper is convenient for callers
// that do not need deterministic time.
func (a *ProviderAnnouncement) Verify() error {
	return a.VerifyAt(time.Now().UTC())
}

// VerifyAt validates the v2 signature, identity, complete quote metadata, and
// quote validity at now. Quote expiry is independent of registry receipt TTL:
// an announcement can be recently received while its economic quote is stale.
func (a *ProviderAnnouncement) VerifyAt(now time.Time) error {
	now = now.UTC()
	if len(a.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key must be %d bytes", ErrInvalidMessage, ed25519.PublicKeySize)
	}
	if a.NodeID == "" {
		return fmt.Errorf("%w: node id must not be empty", ErrInvalidMessage)
	}
	if a.NodeID != token.AccountIDFromPublicKey(a.PublicKey) {
		return fmt.Errorf("%w: node id does not match public key", ErrInvalidMessage)
	}
	if a.ProviderID == "" {
		return fmt.Errorf("%w: provider id must not be empty", ErrInvalidMessage)
	}
	if err := ValidateEndpoint(a.Endpoint); err != nil {
		return err
	}
	if len(a.Signature) == 0 {
		return ErrUnsignedMessage
	}
	if !ed25519.Verify(a.PublicKey, a.signingBytes(), a.Signature) {
		return ErrInvalidSignature
	}
	if a.PricePerUnit == 0 {
		return fmt.Errorf("%w: price_per_unit must be > 0", ErrInvalidMessage)
	}
	if a.QuoteID == "" {
		return fmt.Errorf("%w: quote_id must not be empty", ErrInvalidMessage)
	}
	if a.QuoteVersion == 0 {
		return fmt.Errorf("%w: quote_version must be > 0", ErrInvalidMessage)
	}
	if a.ObservedAt.IsZero() || !unixNanoRoundTrips(a.ObservedAt) {
		return fmt.Errorf("%w: observed_at must be a Unix-nanosecond UTC instant", ErrInvalidMessage)
	}
	if a.ValidUntil.IsZero() || !unixNanoRoundTrips(a.ValidUntil) {
		return fmt.Errorf("%w: valid_until must be a Unix-nanosecond UTC instant", ErrInvalidMessage)
	}
	if a.ObservedAt.After(now.Add(market.MaxQuoteClockSkew)) {
		return fmt.Errorf("%w: observed_at exceeds maximum future clock skew", ErrInvalidMessage)
	}
	if !a.ValidUntil.After(a.ObservedAt) {
		return fmt.Errorf("%w: valid_until must be after observed_at", ErrInvalidMessage)
	}
	if !a.ValidUntil.After(now) {
		return fmt.Errorf("%w: quote expired at %s: %w", ErrInvalidMessage, a.ValidUntil.UTC().Format(time.RFC3339Nano), market.ErrStaleQuote)
	}
	announcementTime := time.Unix(0, a.Timestamp).UTC()
	if announcementTime.After(now.Add(market.MaxQuoteClockSkew)) {
		return fmt.Errorf("%w: announcement timestamp exceeds maximum future clock skew", ErrInvalidMessage)
	}
	return nil
}

// unixNanoRoundTrips rejects times outside time.Time's lossless Unix-nanosecond
// range. Accepted timestamps therefore have one deterministic signed encoding.
func unixNanoRoundTrips(t time.Time) bool {
	return time.Unix(0, t.UnixNano()).UTC().Equal(t)
}

// JobRequest is a v2 fixed-price request for compute from a specific remote
// provider. The buyer signs the exact quote identity, version, unit price,
// observation/expiry interval, and Total = Units * PricePerUnit. A provider may
// therefore reject a delayed or refreshed request rather than silently charging
// terms the buyer never accepted.
type JobRequest struct {
	BuyerID         string            `json:"buyer_id"`
	PublicKey       ed25519.PublicKey `json:"public_key"`
	Provider        string            `json:"provider"`
	Units           uint64            `json:"units"`
	PricePerUnit    uint64            `json:"price_per_unit"`
	QuoteID         string            `json:"quote_id"`
	QuoteVersion    uint64            `json:"quote_version"`
	QuoteObservedAt time.Time         `json:"quote_observed_at"`
	QuoteValidUntil time.Time         `json:"quote_valid_until"`
	Total           uint64            `json:"total"`
	Nonce           uint64            `json:"nonce"`
	Timestamp       int64             `json:"timestamp"`
	Signature       []byte            `json:"signature"`
}

// signingBytes returns the canonical v2 domain-separated payload signed by the
// buyer, covering every request and accepted-quote field except Signature.
func (r *JobRequest) signingBytes() []byte {
	buf := make([]byte, 0, 256)
	buf = appendLenPrefixed(buf, []byte(jobRequestSigningDomain))
	buf = appendLenPrefixed(buf, []byte(r.BuyerID))
	buf = appendLenPrefixed(buf, r.PublicKey)
	buf = appendLenPrefixed(buf, []byte(r.Provider))
	buf = appendUint64(buf, r.Units)
	buf = appendUint64(buf, r.PricePerUnit)
	buf = appendLenPrefixed(buf, []byte(r.QuoteID))
	buf = appendUint64(buf, r.QuoteVersion)
	buf = appendUint64(buf, uint64(r.QuoteObservedAt.UTC().UnixNano()))
	buf = appendUint64(buf, uint64(r.QuoteValidUntil.UTC().UnixNano()))
	buf = appendUint64(buf, r.Total)
	buf = appendUint64(buf, r.Nonce)
	buf = appendUint64(buf, uint64(r.Timestamp))
	return buf
}

// Digest returns the stable identity of this signed request payload. Providers
// persist it with the reservation to make gossip redelivery idempotent.
func (r *JobRequest) Digest() string {
	digest := sha256.Sum256(r.signingBytes())
	return hex.EncodeToString(digest[:])
}

// AcceptedQuote returns the exact fixed-price snapshot carried by the request.
func (r *JobRequest) AcceptedQuote() market.AcceptedQuote {
	return market.AcceptedQuote{
		PricePerUnit: r.PricePerUnit,
		QuoteID:      r.QuoteID,
		QuoteVersion: r.QuoteVersion,
		ObservedAt:   r.QuoteObservedAt.UTC(),
		ValidUntil:   r.QuoteValidUntil.UTC(),
		Total:        r.Total,
	}
}

// Sign signs the job request with priv, which must correspond to PublicKey.
func (r *JobRequest) Sign(priv ed25519.PrivateKey) error {
	if err := checkKeyPair(r.PublicKey, priv); err != nil {
		return err
	}
	r.Signature = ed25519.Sign(priv, r.signingBytes())
	return nil
}

// Verify validates a request against the wall clock. Receive paths use VerifyAt
// with their injected clock so expiry races are deterministic in tests.
func (r *JobRequest) Verify() error {
	return r.VerifyAt(time.Now().UTC())
}

// VerifyAt validates the v2 signature and complete accepted fixed-price quote.
// Quote expiry is exclusive: a request received exactly at ValidUntil is stale.
func (r *JobRequest) VerifyAt(now time.Time) error {
	now = now.UTC()
	if len(r.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key must be %d bytes", ErrInvalidMessage, ed25519.PublicKeySize)
	}
	if r.BuyerID == "" {
		return fmt.Errorf("%w: buyer id must not be empty", ErrInvalidMessage)
	}
	if r.BuyerID != token.AccountIDFromPublicKey(r.PublicKey) {
		return fmt.Errorf("%w: buyer id does not match public key", ErrInvalidMessage)
	}
	if r.Provider == "" {
		return fmt.Errorf("%w: target provider must not be empty", ErrInvalidMessage)
	}
	if r.Units == 0 {
		return fmt.Errorf("%w: units must be > 0", ErrInvalidMessage)
	}
	if len(r.Signature) == 0 {
		return ErrUnsignedMessage
	}
	if !ed25519.Verify(r.PublicKey, r.signingBytes(), r.Signature) {
		return ErrInvalidSignature
	}
	if r.PricePerUnit == 0 || r.QuoteID == "" || r.QuoteVersion == 0 {
		return fmt.Errorf("%w: accepted quote identity, version, and price are required", ErrInvalidMessage)
	}
	if r.QuoteObservedAt.IsZero() || !unixNanoRoundTrips(r.QuoteObservedAt) {
		return fmt.Errorf("%w: quote_observed_at must be a Unix-nanosecond instant", ErrInvalidMessage)
	}
	if r.QuoteValidUntil.IsZero() || !unixNanoRoundTrips(r.QuoteValidUntil) {
		return fmt.Errorf("%w: quote_valid_until must be a Unix-nanosecond instant", ErrInvalidMessage)
	}
	if r.QuoteObservedAt.After(now.Add(market.MaxQuoteClockSkew)) {
		return fmt.Errorf("%w: quote_observed_at exceeds maximum future clock skew", ErrInvalidMessage)
	}
	if !r.QuoteValidUntil.After(r.QuoteObservedAt) {
		return fmt.Errorf("%w: quote_valid_until must be after quote_observed_at", ErrInvalidMessage)
	}
	if !r.QuoteValidUntil.After(now) {
		return fmt.Errorf("%w: accepted quote expired at %s: %w", ErrInvalidMessage, r.QuoteValidUntil.UTC().Format(time.RFC3339Nano), market.ErrStaleQuote)
	}
	requestTime := time.Unix(0, r.Timestamp).UTC()
	if requestTime.After(now.Add(market.MaxQuoteClockSkew)) {
		return fmt.Errorf("%w: request timestamp exceeds maximum future clock skew", ErrInvalidMessage)
	}
	total, err := market.CheckedMul(r.Units, r.PricePerUnit)
	if err != nil {
		return fmt.Errorf("%w: invalid fixed total: %v", ErrInvalidMessage, err)
	}
	if r.Total != total {
		return fmt.Errorf("%w: total %d does not equal units * price_per_unit (%d)", ErrInvalidMessage, r.Total, total)
	}
	return nil
}

// Settlement propagates a signed settlement transaction over P2P. Tx is a
// self-authenticating token.Transaction (already ed25519-signed by its sender),
// and JobRef is an optional reference to the job the settlement pays for so
// receivers can correlate it. The Settlement's authenticity derives entirely
// from Tx.Verify(); no separate envelope signature is required because the
// transaction already binds the sender's key over all its fields.
type Settlement struct {
	Tx     token.Transaction `json:"tx"`
	JobRef string            `json:"job_ref"`
}

// Verify validates the embedded transaction signature. A Settlement is valid iff
// its transaction verifies; an unsigned or tampered transaction is rejected.
func (s *Settlement) Verify() error {
	return s.Tx.Verify()
}

// checkKeyPair confirms priv is a well-formed ed25519 private key whose public
// half equals pub, so a message can only be signed by the key it claims.
func checkKeyPair(pub ed25519.PublicKey, priv ed25519.PrivateKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key must be %d bytes", ErrInvalidMessage, ed25519.PublicKeySize)
	}
	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: private key must be %d bytes", ErrInvalidMessage, ed25519.PrivateKeySize)
	}
	if !priv.Public().(ed25519.PublicKey).Equal(pub) {
		return fmt.Errorf("%w: private key does not match public key", ErrInvalidMessage)
	}
	return nil
}

// appendLenPrefixed appends a 4-byte big-endian length followed by the bytes,
// mirroring token.Transaction's canonical encoding.
func appendLenPrefixed(dst, b []byte) []byte {
	var lp [4]byte
	binary.BigEndian.PutUint32(lp[:], uint32(len(b)))
	dst = append(dst, lp[:]...)
	return append(dst, b...)
}

// appendUint64 appends v as 8 big-endian bytes.
func appendUint64(dst []byte, v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return append(dst, b[:]...)
}

// appendUint32 appends v as 4 big-endian bytes.
func appendUint32(dst []byte, v uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return append(dst, b[:]...)
}

// MaxEndpointLen bounds an announced address. Generous for a hostname and a
// port, small enough that the field cannot be used to push payload around the
// gossip network.
const MaxEndpointLen = 512

// ValidateEndpoint checks an announced address before anyone is handed it.
//
// This is the one field in an announcement that a buyer's client CONNECTS TO,
// which makes it the one field where being permissive is a security decision
// rather than a convenience. Everything else in a message that fails to validate
// costs a bad quote; a bad endpoint costs a request sent somewhere the buyer did
// not intend.
//
// So the rules are narrow and the reasons are specific:
//
//   - Only http and https. A scheme this does not constrain is whatever the
//     client library happens to support - file://, and in some stacks gopher://
//     or ftp:// - which turns a directory listing into a request generator
//     pointed at the buyer's own machine.
//   - No user info. `https://user:pass@host` puts credentials the announcer
//     chose into the buyer's request, and a client that follows it authenticates
//     as somebody it never agreed to be.
//   - A host, and no path, query or fragment. A base URL is what the OpenAI SDK
//     appends `/v1/chat/completions` to; a path here is either ignored or
//     silently changes where the request lands.
//
// An empty endpoint is valid and means "not reachable by HTTP", which is the
// honest state of a node selling compute units rather than inference.
func ValidateEndpoint(endpoint string) error {
	if endpoint == "" {
		return nil
	}
	if len(endpoint) > MaxEndpointLen {
		return fmt.Errorf("%w: endpoint is %d bytes, limit is %d",
			ErrInvalidMessage, len(endpoint), MaxEndpointLen)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("%w: endpoint is not a url: %v", ErrInvalidMessage, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: endpoint scheme %q is not http or https", ErrInvalidMessage, u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("%w: endpoint must not carry credentials", ErrInvalidMessage)
	}
	if u.Host == "" {
		return fmt.Errorf("%w: endpoint has no host", ErrInvalidMessage)
	}
	if u.Path != "" && u.Path != "/" {
		return fmt.Errorf("%w: endpoint must be a base url with no path, got %q", ErrInvalidMessage, u.Path)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%w: endpoint must carry no query or fragment", ErrInvalidMessage)
	}
	return nil
}
