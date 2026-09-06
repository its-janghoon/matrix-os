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
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// Gossip topic names. These are stable, versioned identifiers so nodes running
// compatible protocol versions rendezvous on the same pubsub topics. Bumping the
// version suffix is how an incompatible wire change is rolled out.
const (
	// TopicAnnounce carries ProviderAnnouncement messages.
	TopicAnnounce = "matrix/market/announce/v1"
	// TopicJobs carries JobRequest messages.
	TopicJobs = "matrix/market/jobs/v1"
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

// ProviderAnnouncement advertises a provider's compute capacity and pricing over
// the announce topic. ProviderID is the announcing account's stable ID (hex of
// PublicKey). PeerID is the libp2p peer ID string the provider is reachable at.
// Timestamp is unix nanoseconds at announcement time and is bound into the
// signature so a receiver can age out stale announcements. Signature is an
// ed25519 signature by PublicKey over the canonical payload.
type ProviderAnnouncement struct {
	ProviderID   string            `json:"provider_id"`
	PublicKey    ed25519.PublicKey `json:"public_key"`
	Capacity     uint64            `json:"capacity"`
	PricePerUnit uint64            `json:"price_per_unit"`
	Available    uint64            `json:"available"`
	PeerID       string            `json:"peer_id"`
	Timestamp    int64             `json:"timestamp"`
	Signature    []byte            `json:"signature"`
}

// signingBytes returns the canonical, deterministic, length-prefixed
// serialization of the signable fields (everything except Signature). Using the
// same length-prefixed layout as token.Transaction guarantees no two distinct
// field combinations collide into the same signed payload.
func (a *ProviderAnnouncement) signingBytes() []byte {
	buf := make([]byte, 0, 128)
	buf = appendLenPrefixed(buf, []byte(a.ProviderID))
	buf = appendLenPrefixed(buf, a.PublicKey)
	buf = appendUint64(buf, a.Capacity)
	buf = appendUint64(buf, a.PricePerUnit)
	buf = appendUint64(buf, a.Available)
	buf = appendLenPrefixed(buf, []byte(a.PeerID))
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

// Verify validates the announcement's signature and basic structure. It returns
// ErrInvalidMessage for a malformed key or empty ProviderID, ErrUnsignedMessage
// when no signature is present, and ErrInvalidSignature when the signature does
// not verify. It also confirms ProviderID is derived from PublicKey so a
// message cannot claim an identity it does not hold the key for.
func (a *ProviderAnnouncement) Verify() error {
	if len(a.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key must be %d bytes", ErrInvalidMessage, ed25519.PublicKeySize)
	}
	if a.ProviderID == "" {
		return fmt.Errorf("%w: provider id must not be empty", ErrInvalidMessage)
	}
	if a.ProviderID != token.AccountIDFromPublicKey(a.PublicKey) {
		return fmt.Errorf("%w: provider id does not match public key", ErrInvalidMessage)
	}
	if len(a.Signature) == 0 {
		return ErrUnsignedMessage
	}
	if !ed25519.Verify(a.PublicKey, a.signingBytes(), a.Signature) {
		return ErrInvalidSignature
	}
	return nil
}

// JobRequest is a buyer's signed request for compute from a specific remote
// provider. BuyerID is the buyer's account ID (hex of PublicKey). Provider is
// the target provider's account ID. Units is the requested compute units. Nonce
// gives the buyer a per-request unique value bound into the signature so two
// otherwise-identical requests differ. Signature is by PublicKey.
type JobRequest struct {
	BuyerID   string            `json:"buyer_id"`
	PublicKey ed25519.PublicKey `json:"public_key"`
	Provider  string            `json:"provider"`
	Units     uint64            `json:"units"`
	Nonce     uint64            `json:"nonce"`
	Timestamp int64             `json:"timestamp"`
	Signature []byte            `json:"signature"`
}

// signingBytes returns the canonical length-prefixed payload signed by the
// buyer, covering every field except Signature.
func (r *JobRequest) signingBytes() []byte {
	buf := make([]byte, 0, 128)
	buf = appendLenPrefixed(buf, []byte(r.BuyerID))
	buf = appendLenPrefixed(buf, r.PublicKey)
	buf = appendLenPrefixed(buf, []byte(r.Provider))
	buf = appendUint64(buf, r.Units)
	buf = appendUint64(buf, r.Nonce)
	buf = appendUint64(buf, uint64(r.Timestamp))
	return buf
}

// Sign signs the job request with priv, which must correspond to PublicKey.
func (r *JobRequest) Sign(priv ed25519.PrivateKey) error {
	if err := checkKeyPair(r.PublicKey, priv); err != nil {
		return err
	}
	r.Signature = ed25519.Sign(priv, r.signingBytes())
	return nil
}

// Verify validates the job request's signature and structure, confirming BuyerID
// is derived from PublicKey and the target Provider and Units are set.
func (r *JobRequest) Verify() error {
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
