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
//
// WHY THE TOOL BLOCK IS CONDITIONAL. A tool definition tells the model what it may
// do, so a node able to add, remove or rewrite one on a signed request asks the
// buyer's model a different question at the buyer's expense. It therefore has to
// be signed. But appending a zero count unconditionally would change the digest of
// every request that uses no tools - which is all of them today - and cost a
// second compatibility window three weeks after the first. So the block is emitted
// only when the request actually has a tool surface, and a request without one
// hashes byte-for-byte as it did in v0.5.8.
//
// That is not a hole. Signing without tools and running with them is a different
// digest, so the added tools fail; signing with tools and running without them is
// also a different digest. Both directions are refused, which is the whole
// requirement.
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
	if hasToolSurface(req, msgs) {
		writeToolBlock(h, req.Tools, msgs)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// hasToolSurface reports whether this request involves tools at all, either by
// offering them or by carrying a turn that came out of a previous tool round.
//
// It reads the TRANSCRIPT as well as the tool list because iteration two of a loop
// offers the same tools but also replays the model's own call and the client's
// result - and those have to be signed for the same reason the definitions do. A
// node that rewrote the arguments of a call already made would be telling the model
// it had asked something it did not ask.
func hasToolSurface(req InferenceRequest, msgs []Message) bool {
	if len(req.Tools) > 0 {
		return true
	}
	for _, m := range msgs {
		if m.ToolCallID != "" || len(m.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

// writeToolBlock hashes the tool definitions and then every message's tool
// metadata, length-prefixed throughout.
//
// ORDER IS AS SENT, not sorted. The sequence is part of what the buyer signed, so
// a node reordering the tools produces a different digest - and sorting would be a
// second rule three implementations must agree on for no gain.
//
// The per-message part walks EVERY message rather than only the ones carrying
// metadata, so there is no index to encode and no sparse-set convention for a
// second language to get subtly wrong. An ordinary turn contributes an empty
// prefix and a zero count.
func writeToolBlock(h interface{ Write([]byte) (int, error) }, tools []ToolDefinition, msgs []Message) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(tools)))
	_, _ = h.Write(n[:])
	for _, t := range tools {
		writeLenPrefixed(h, []byte(t.Name))
		writeLenPrefixed(h, []byte(t.Description))
		writeLenPrefixed(h, t.Parameters)
	}
	for _, m := range msgs {
		writeLenPrefixed(h, []byte(m.ToolCallID))
		binary.BigEndian.PutUint64(n[:], uint64(len(m.ToolCalls)))
		_, _ = h.Write(n[:])
		for _, c := range m.ToolCalls {
			writeLenPrefixed(h, []byte(c.ID))
			writeLenPrefixed(h, []byte(c.Name))
			writeLenPrefixed(h, []byte(c.Arguments))
		}
	}
}

// legacyRequestDigest is requestDigest as it was before MaxTokens and Temperature
// were covered: the transcript and nothing else.
//
// IT IS NO LONGER ACCEPTED. For one release a signature verified under either rule,
// because apps/web deploys on merge while a node rollout is a deliberate operator
// step, so there was a guaranteed window where the browser signed the new digest
// and the nodes knew only the old one. v0.5.8 and v0.5.9 are now on every box and a
// real /chat run verified under the new rule, so the window is closed and the
// either-rule arm is gone: a digest that does not cover max_tokens or temperature
// lets a node raise the token bound on a request the buyer signed, and the buyer
// pays for a larger job than the one they approved.
//
// IT IS STILL COMPUTED, for one reason: to NAME that failure. A client older than
// v0.5.8 produces a well-formed request with a signature over the old bytes, and
// reporting that as "signature does not verify" sends the reader to their keys when
// the answer is a stale client. Computing the old digest costs one hash on a path
// that has already failed, and buys an error message that says what to do.
//
// This is a REFUSAL, not a fallback: nothing downstream of it treats the request as
// authorized. Delete it when no client that old can plausibly exist.
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

// staleClientSignature reports whether a signature that FAILED the current rule
// would have verified under the pre-v0.5.8 one - that is, whether the caller is an
// out-of-date client rather than a wrong key.
//
// Only ever called after the real check has already failed, so it cannot authorize
// anything. Tools are excluded because a client predating v0.5.8 cannot be sending
// them, so a tool request matching the old bytes is not a stale client; it is
// something to keep reporting as an ordinary failure.
func staleClientSignature(a *RunAuthorization, req InferenceRequest, verify func([]byte) bool) bool {
	if toolsPresent(req) {
		return false
	}
	return verify(a.legacySigningBytes(req))
}

// errStaleSigningRule is the failure a client older than v0.5.8 gets. It names the
// fix, because "signature does not verify" does not: the bytes are right and the key
// is right, and only the rule the client signed under is out of date.
func errStaleSigningRule() error {
	return fmt.Errorf("%w: this signature is valid under the pre-v0.5.8 signing rule, "+
		"which this network no longer accepts because it does not cover max_tokens or "+
		"temperature - a node could raise the token bound on a request you signed. The "+
		"key and the request are fine; the signing client is out of date and must be "+
		"upgraded", ErrRunUnauthorized)
}

// toolsPresent is hasToolSurface over a request's own effective transcript, for
// the callers that hold only the request.
func toolsPresent(req InferenceRequest) bool {
	msgs, err := req.EffectiveMessages()
	if err != nil {
		msgs = nil
	}
	return hasToolSurface(req, msgs)
}

// legacySigningBytes is the authorization payload built over the old digest. Used
// only to classify a failure - see legacyRequestDigest.
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
		verify := func(bytes []byte) bool {
			return ed25519.Verify(auth.PublicKey, bytes, auth.Signature)
		}
		if !verify(auth.SigningBytes(req)) {
			if staleClientSignature(auth, req, verify) {
				return errStaleSigningRule()
			}
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
		// ONE digest is accepted. The EIP-712 struct is unchanged; only what
		// promptDigest covers moved.
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
		// Diagnosis only - see legacyRequestDigest. Nothing is authorized here.
		if !toolsPresent(req) && recoversTo(legacyRequestDigest(req)) {
			return errStaleSigningRule()
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
	verify := func(bytes []byte) bool {
		return ed25519.Verify(auth.PublicKey, bytes, auth.Signature)
	}
	if !verify(auth.SigningBytes(req)) {
		if staleClientSignature(auth, req, verify) {
			return errStaleSigningRule()
		}
		return fmt.Errorf("%w: signature does not verify", ErrRunUnauthorized)
	}
	return nil
}
