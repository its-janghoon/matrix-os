package inference

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// This file lets a caller PROVE it controls the buyer account before a provider
// does any work, which is what makes RunInferenceJob safe to reach without an
// API key.
//
// The client-signed path exists so a browser can pay with its own key, and a
// browser cannot hold an API key. Two of its three write methods are already
// self-authorising - SettleInferenceJob and SubmitSignedTransfer each carry the
// buyer's signature over the thing being authorised. RunInferenceJob was not:
// `buyer` was just a string, so opening it would have let anyone name someone
// else's funded account, run jobs against a provider, and never sign for them.
// The victim's balance is untouched, but the PROVIDER does the work for free and
// its capacity is held until the payment request expires. An API key does not
// fix that, because the key would be in the page for anyone to read.
//
// A RunAuthorization is signed over the request's identifying fields, so it
// authorises exactly one run and cannot be lifted onto another. It is separate
// from the payment signature because the two answer different questions at
// different times: this one says "I am the buyer and I am asking for this work",
// and the payment says "I accept this bill".

var (
	// ErrRunUnauthorized is returned when a run authorization is missing,
	// malformed, expired, replayed, or signed by anyone other than the buyer.
	ErrRunUnauthorized = errors.New("inference: run is not authorized by the buyer")
)

// RunAuthorizationWindow is how far a run authorization's timestamp may be from
// the node's clock. It bounds replay to that window even before the seen-set
// below, and it is generous enough for an ordinary clock difference between a
// browser and a server.
const RunAuthorizationWindow = 2 * time.Minute

// runAuthDomain is a domain-separation prefix. Without it a signature over these
// bytes could in principle be presented as a signature over some other
// length-prefixed structure that happened to serialize identically. It costs one
// field and removes a whole class of question.
const runAuthDomain = "matrix/inference/run-authorization/v1"

// RunAuthorization is a buyer's signed request to have a provider run a specific
// inference. Every field is covered by the signature.
type RunAuthorization struct {
	// PublicKey is the buyer's ed25519 public key. The buyer account ID is
	// derived from it, so a caller cannot claim an account it lacks the key for.
	PublicKey ed25519.PublicKey
	// Provider, Model and the request identify the work being asked for, so an
	// authorization cannot be replayed against a different provider or prompt.
	Provider string
	Model    string
	// Timestamp is unix nanoseconds at signing time, bounded by
	// RunAuthorizationWindow.
	Timestamp int64
	// Signature is over SigningBytes.
	Signature []byte
}

// SigningBytes returns the canonical, length-prefixed serialization that is
// signed. The layout mirrors token.Transaction.SigningBytes: every variable
// field is length-prefixed so no two distinct field combinations can collide
// into the same signed payload.
//
// The request is included as a SHA-256 digest of its effective messages rather
// than verbatim, so the authorization stays small while still being bound to the
// exact prompt. A different prompt is a different digest is a different
// signature.
func (a *RunAuthorization) SigningBytes(req InferenceRequest) []byte {
	digest := requestDigest(req)

	buf := make([]byte, 0, 256)
	buf = appendLenPrefixed(buf, []byte(runAuthDomain))
	buf = appendLenPrefixed(buf, a.PublicKey)
	buf = appendLenPrefixed(buf, []byte(a.Provider))
	buf = appendLenPrefixed(buf, []byte(a.Model))
	buf = appendLenPrefixed(buf, digest[:])
	buf = binary.BigEndian.AppendUint64(buf, uint64(a.Timestamp))
	return buf
}

// BuyerID returns the account the authorization is for.
func (a *RunAuthorization) BuyerID() string {
	return token.AccountIDFromPublicKey(a.PublicKey)
}

// Sign signs the authorization for req with priv, which must correspond to
// PublicKey.
func (a *RunAuthorization) Sign(req InferenceRequest, priv ed25519.PrivateKey) error {
	if len(a.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key must be %d bytes", ErrRunUnauthorized, ed25519.PublicKeySize)
	}
	a.Signature = ed25519.Sign(priv, a.SigningBytes(req))
	return nil
}

// requestDigest hashes the effective messages of a request, so the digest does
// not depend on whether a caller sent `prompt` or an equivalent one-message
// transcript. Role and content are length-prefixed for the same
// no-collisions reason as everything else here.
func requestDigest(req InferenceRequest) [32]byte {
	msgs, err := req.EffectiveMessages()
	if err != nil {
		// An empty request has a stable digest of its own; the run itself is
		// refused later by EffectiveMessages, so this does not need to fail here.
		msgs = nil
	}
	h := sha256.New()
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(msgs)))
	_, _ = h.Write(count[:])
	for _, m := range msgs {
		writeLenPrefixed(h, []byte(m.Role))
		writeLenPrefixed(h, []byte(m.Content))
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func appendLenPrefixed(buf, b []byte) []byte {
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(b)))
	return append(buf, b...)
}

func writeLenPrefixed(h interface{ Write([]byte) (int, error) }, b []byte) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(b)))
	_, _ = h.Write(n[:])
	_, _ = h.Write(b)
}

// runAuthSeen remembers recently accepted authorizations so one cannot be
// replayed inside its freshness window. Entries older than the window are
// dropped, so it stays bounded by the request rate over two minutes rather than
// growing forever.
type runAuthSeen struct {
	mu    sync.Mutex
	seen  map[[32]byte]time.Time
	purge time.Time
}

func newRunAuthSeen() *runAuthSeen {
	return &runAuthSeen{seen: make(map[[32]byte]time.Time)}
}

// accept records the authorization and reports whether it is new. A replay
// returns false.
func (s *runAuthSeen) accept(key [32]byte, now time.Time, window time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now.Sub(s.purge) > window {
		s.purge = now
		for k, at := range s.seen {
			if now.Sub(at) > window {
				delete(s.seen, k)
			}
		}
	}

	if _, dup := s.seen[key]; dup {
		return false
	}
	s.seen[key] = now
	return true
}

// VerifyRunAuthorization checks that auth authorises req for buyer, and that it
// has not been seen before. It is the whole gate: a caller that passes this has
// proved it holds the buyer's key and is asking for this exact work now.
func (s *Service) VerifyRunAuthorization(buyer string, req InferenceRequest, auth *RunAuthorization) error {
	if auth == nil {
		return fmt.Errorf("%w: no authorization supplied", ErrRunUnauthorized)
	}
	if len(auth.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key must be %d bytes", ErrRunUnauthorized, ed25519.PublicKeySize)
	}
	if len(auth.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature must be %d bytes", ErrRunUnauthorized, ed25519.SignatureSize)
	}
	// The account is DERIVED from the key, so this is what stops a caller from
	// naming an account it does not hold.
	if got := auth.BuyerID(); !strings.EqualFold(got, buyer) {
		return fmt.Errorf("%w: authorized by %s, but the buyer is %s", ErrRunUnauthorized, got, buyer)
	}

	now := nowUTC()
	signed := time.Unix(0, auth.Timestamp)
	if delta := now.Sub(signed); delta > RunAuthorizationWindow || delta < -RunAuthorizationWindow {
		return fmt.Errorf("%w: signed at %s, which is outside the %s window",
			ErrRunUnauthorized, signed.UTC().Format(time.RFC3339), RunAuthorizationWindow)
	}

	message := auth.SigningBytes(req)
	if !ed25519.Verify(auth.PublicKey, message, auth.Signature) {
		return fmt.Errorf("%w: signature does not verify", ErrRunUnauthorized)
	}

	// Replay: the same signature twice would be two runs for one authorization,
	// which is the free work this whole file exists to prevent.
	if !s.runAuth.accept(sha256.Sum256(auth.Signature), now, RunAuthorizationWindow) {
		return fmt.Errorf("%w: this authorization has already been used", ErrRunUnauthorized)
	}
	return nil
}
