package inference

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ecirlabs/matrix-core/internal/ethsig"
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

// IsEth reports whether this authorization is signed by an Ethereum key, which
// is decided by the key's length: 20 bytes is an address, 32 is an ed25519
// public key.
func (a *RunAuthorization) IsEth() bool {
	return len(a.PublicKey) == ethsig.AddressLen
}

// BuyerID returns the account the authorization is for, in whichever form its
// key implies.
func (a *RunAuthorization) BuyerID() string {
	if a.IsEth() {
		addr, err := ethsig.AddressFromBytes(a.PublicKey)
		if err != nil {
			return ""
		}
		return token.EthAccountID(addr)
	}
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

// requestDigest hashes what the buyer is actually asking for, so the digest does
// not depend on whether a caller sent `prompt` or an equivalent one-message
// transcript. Everything is length-prefixed for the same no-collisions reason as
// the rest of this file.
//
// WHY MaxTokens AND Temperature ARE IN HERE. They were not, and the omission was
// not cosmetic: the authorization's whole job is to say "I am the buyer and I am
// asking for THIS work". A node could raise MaxTokens on a request the buyer
// signed and the signature still verified, and MaxTokens is what the meter counts
// - so it decided how much of the reservation was spent. The reservation capped
// the loss, but the buyer had signed for one job and paid for a larger one.
//
// Temperature changes the answer rather than the price, which is a smaller thing
// to be able to alter silently and still not the node's call to make.
//
// Temperature is hashed as its IEEE-754 bits, big-endian, rather than formatted.
// A decimal rendering has to agree across two languages about trailing zeros, the
// exponent form and how many digits to emit; the bits are the value and have one
// spelling. JavaScript numbers are float64, so this is exact on both sides.
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
	// Appended AFTER the transcript, so the framing of the part that already
	// existed is untouched and the two digests differ only by this suffix.
	var tokens [8]byte
	binary.BigEndian.PutUint64(tokens[:], uint64(req.MaxTokens))
	_, _ = h.Write(tokens[:])
	var temp [8]byte
	binary.BigEndian.PutUint64(temp[:], math.Float64bits(req.Temperature))
	_, _ = h.Write(temp[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// legacyRequestDigest is requestDigest as it was before MaxTokens and Temperature
// were covered: the transcript and nothing else.
//
// WHY IT STILL EXISTS. The nodes on the live network verify with the old rule and
// the browser signs with whatever it was served. Shipping only the new rule breaks
// every /chat run in the window between the web deploying and the last node being
// upgraded - and apps/web deploys on merge while a node rollout is a deliberate
// operator step, so that window is guaranteed, not hypothetical.
//
// So a signature is accepted under either rule for ONE release. That does not
// reopen the hole for anyone who has upgraded: a client signing the new digest is
// checked against it, and only a client still signing the old one falls through to
// the legacy arm.
//
// DELETE THIS, AND acceptsLegacyDigest, once every client is on the new rule. The
// node logs the first legacy acceptance per process, which is how an operator can
// tell when that has happened rather than guessing.
func legacyRequestDigest(req InferenceRequest) [32]byte {
	msgs, err := req.EffectiveMessages()
	if err != nil {
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

// legacyDigestReported makes the transition observable with one line rather than
// one per request: an operator needs to know THAT old clients are still signing,
// not how many times.
var legacyDigestReported atomic.Bool

func reportLegacyDigestAccepted() {
	if legacyDigestReported.Swap(true) {
		return
	}
	fmt.Println("Inference: accepted a run authorization signed under the PRE-v0.5.8 digest, " +
		"which does not cover max_tokens or temperature. This is the compatibility window; " +
		"once no client does this, legacyRequestDigest can be deleted.")
}

// acceptsEitherDigest checks a signature against the current authorization bytes
// and, failing that, against the pre-v0.5.8 ones.
//
// Current FIRST, so an upgraded client never touches the legacy arm and the log
// line below means what it says: some client is still signing the old digest.
func acceptsEitherDigest(a *RunAuthorization, req InferenceRequest, verify func([]byte) bool) bool {
	if verify(a.SigningBytes(req)) {
		return true
	}
	if verify(a.legacySigningBytes(req)) {
		reportLegacyDigestAccepted()
		return true
	}
	return false
}

// legacySigningBytes is the authorization payload built over the old digest.
func (a *RunAuthorization) legacySigningBytes(req InferenceRequest) []byte {
	digest := legacyRequestDigest(req)

	buf := make([]byte, 0, 256)
	buf = appendLenPrefixed(buf, []byte(runAuthDomain))
	buf = appendLenPrefixed(buf, a.PublicKey)
	buf = appendLenPrefixed(buf, []byte(a.Provider))
	buf = appendLenPrefixed(buf, []byte(a.Model))
	buf = appendLenPrefixed(buf, digest[:])
	buf = binary.BigEndian.AppendUint64(buf, uint64(a.Timestamp))
	return buf
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
//
// It accepts either kind of key. A 32-byte PublicKey is an ed25519 account and
// the signature covers SigningBytes; a 20-byte one is an Ethereum address and
// the signature is an ecrecover over the EIP-712 digest, because MetaMask signs
// typed data and never arbitrary bytes. Which kind it is comes from the key's
// LENGTH, matching how token.Transaction tells its two senders apart.
func (s *Service) VerifyRunAuthorization(buyer string, req InferenceRequest, auth *RunAuthorization) error {
	if auth == nil {
		return fmt.Errorf("%w: no authorization supplied", ErrRunUnauthorized)
	}

	now := nowUTC()
	signed := time.Unix(0, auth.Timestamp)
	if delta := now.Sub(signed); delta > RunAuthorizationWindow || delta < -RunAuthorizationWindow {
		return fmt.Errorf("%w: signed at %s, which is outside the %s window",
			ErrRunUnauthorized, signed.UTC().Format(time.RFC3339), RunAuthorizationWindow)
	}

	if err := s.verifySignature(buyer, req, auth); err != nil {
		return err
	}

	// Replay: the same authorization twice would be two runs for one
	// authorization, which is the free work this whole file exists to prevent.
	//
	// Keyed on the SIGNED CONTENT, not the signature bytes. Those are not the
	// same thing: an ECDSA signature has a malleable twin (r, n-s) that recovers
	// the same signer, so a set keyed on signature bytes saw a re-encoded
	// signature as a brand-new authorization and let the work run again.
	// ethsig.RecoverAddress now rejects the high-S form, which closes that
	// particular door - but keying on the content closes the whole doorway, and
	// does not depend on every signature scheme this ever accepts having exactly
	// one canonical encoding.
	//
	// The signing bytes carry the buyer's key, the provider, the model, the
	// prompt digest and the timestamp, so two different buyers, prompts or
	// moments are different keys. Two requests identical in all of those ARE the
	// same authorization, and refusing the second is the point.
	if !s.runAuth.accept(sha256.Sum256(auth.SigningBytes(req)), now, RunAuthorizationWindow) {
		return fmt.Errorf("%w: this authorization has already been used", ErrRunUnauthorized)
	}
	return nil
}

// verifySignature checks the signature and that it authorises `buyer`,
// dispatching on the key kind.
func (s *Service) verifySignature(buyer string, req InferenceRequest, auth *RunAuthorization) error {
	// A BUDGET authorises its delegate. The buyer's own wallet named that key
	// when it opened the budget, and the name is inside the signature that
	// funded it - so checking the key against the account's own terms is the
	// same guarantee as deriving an account from a key, one step removed.
	//
	// This is what lets a browser run a job without a wallet prompt: the key it
	// generated and cannot export is the one the terms name.
	if terms, err := token.ParseSpendEscrow(buyer); err == nil {
		if auth.IsEth() {
			return fmt.Errorf("%w: a budget is drawn on by an ed25519 delegate, not by an "+
				"ethereum key", ErrRunUnauthorized)
		}
		if len(auth.PublicKey) != ed25519.PublicKeySize {
			return fmt.Errorf("%w: a delegate key must be %d bytes, got %d",
				ErrRunUnauthorized, ed25519.PublicKeySize, len(auth.PublicKey))
		}
		if got := token.AccountIDFromPublicKey(auth.PublicKey); got != terms.Delegate {
			return fmt.Errorf("%w: authorized by %s, but this budget names %s",
				ErrRunUnauthorized, got, terms.Delegate)
		}
		if !acceptsEitherDigest(auth, req, func(bytes []byte) bool {
			return ed25519.Verify(auth.PublicKey, bytes, auth.Signature)
		}) {
			return fmt.Errorf("%w: signature does not verify", ErrRunUnauthorized)
		}
		return nil
	}

	if auth.IsEth() {
		addr, err := ethsig.AddressFromBytes(auth.PublicKey)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrRunUnauthorized, err)
		}
		// The account is DERIVED from the address, so a caller cannot name an
		// account it does not control.
		if got := token.EthAccountID(addr); !strings.EqualFold(got, buyer) {
			return fmt.Errorf("%w: authorized by %s, but the buyer is %s", ErrRunUnauthorized, got, buyer)
		}
		if len(auth.Signature) != ethsig.SignatureLen {
			return fmt.Errorf("%w: an ethereum signature must be %d bytes, got %d",
				ErrRunUnauthorized, ethsig.SignatureLen, len(auth.Signature))
		}
		// Either digest, for the compatibility window. The EIP-712 struct is
		// unchanged; only what promptDigest covers moved.
		recoversTo := func(d [32]byte) bool {
			message, err := token.EthRunAuthorizationDigest(addr, auth.Provider, auth.Model, d[:], auth.Timestamp)
			if err != nil {
				return false
			}
			recovered, err := ethsig.RecoverAddress(message, auth.Signature)
			return err == nil && recovered == addr
		}
		if recoversTo(requestDigest(req)) {
			return nil
		}
		if recoversTo(legacyRequestDigest(req)) {
			reportLegacyDigestAccepted()
			return nil
		}
		return fmt.Errorf("%w: signed by a different key than the authorization claims (%s)",
			ErrRunUnauthorized, addr.Hex())
	}

	if len(auth.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: a key must be %d bytes (ed25519) or %d (an ethereum address), got %d",
			ErrRunUnauthorized, ed25519.PublicKeySize, ethsig.AddressLen, len(auth.PublicKey))
	}
	if len(auth.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature must be %d bytes", ErrRunUnauthorized, ed25519.SignatureSize)
	}
	if got := auth.BuyerID(); !strings.EqualFold(got, buyer) {
		return fmt.Errorf("%w: authorized by %s, but the buyer is %s", ErrRunUnauthorized, got, buyer)
	}
	if !acceptsEitherDigest(auth, req, func(bytes []byte) bool {
		return ed25519.Verify(auth.PublicKey, bytes, auth.Signature)
	}) {
		return fmt.Errorf("%w: signature does not verify", ErrRunUnauthorized)
	}
	return nil
}
