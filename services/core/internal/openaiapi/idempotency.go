package openaiapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ecirlabs/matrix-core/internal/inference"
)

// Not charging twice for one request.
//
// THE BUG THIS CLOSES. Nothing here deduplicated anything. Two identical POSTs
// were two jobs and two charges, and the second POST is rarely the caller's
// decision: the openai SDKs retry on a connection error or a 5xx by default, and
// so does any proxy in front of this endpoint. A buyer whose network blinked
// paid twice and had no way to tell.
//
// WHAT IS NOT DONE, on purpose. Requests are NOT deduplicated by their content.
// Two identical prompts from one buyer are an ordinary thing to send - a
// regenerate, a second sample at temperature - and refusing the second because
// it looks like the first would break a legitimate use to fix an accidental one.
// Only an explicit key deduplicates, which is the caller saying "these two are
// the same request" rather than us guessing.
//
// The response body is not stored either. A replay is told the original job id
// and refused, rather than handed a cached completion, because storing prompts
// and completions on the provider's disk for a day is a retention decision a
// provider should make deliberately and not inherit from a bug fix.

// IdempotencyRecord is what one key remembers.
type IdempotencyRecord struct {
	// JobID is the job the key first created.
	JobID string `json:"job_id"`
	// RequestDigest binds the key to the request it was first used with, so a
	// client that forgot to rotate its key is told rather than silently served
	// the wrong answer.
	RequestDigest string `json:"request_digest"`
	// Completed is true once the job finished and the buyer was charged.
	Completed bool `json:"completed"`
	// CreatedAt is when the key was first seen, for expiry.
	CreatedAt time.Time `json:"created_at"`
}

// IdempotencyStore remembers keys. It is an interface so this package does not
// depend on a particular store and a test can drive it directly.
type IdempotencyStore interface {
	// Reserve records a key as in-flight and returns the existing record when the
	// key has been seen before. found reports which happened.
	Reserve(buyer, key string, rec IdempotencyRecord) (existing IdempotencyRecord, found bool, err error)
	// Complete marks a key's job as finished and charged, recording which job it
	// was. The id is supplied here rather than at Reserve because the job does
	// not exist until the work is admitted.
	Complete(buyer, key, jobID string) error
	// Release forgets a key whose job failed, so the caller may retry under it.
	// A failed job charged nobody, so a retry is what the buyer wants.
	Release(buyer, key string) error
}

// IdempotencyHeader is the header a client sends. It is the name Stripe
// established and the SDKs already know, so a client that already sets it
// somewhere else needs no new concept.
const IdempotencyHeader = "Idempotency-Key"

// maxIdempotencyKeyLen bounds a key. It is generous for a UUID and small enough
// that a key cannot be used as storage.
const maxIdempotencyKeyLen = 255

// ErrIdempotencyInFlight is returned when a key names a job that has not
// finished.
var ErrIdempotencyInFlight = errors.New("openaiapi: a request with this idempotency key is still running")

// idempotencyGuard is the per-request state the handler carries between
// reserving a key and resolving it.
type idempotencyGuard struct {
	store IdempotencyStore
	buyer string
	key   string
}

// active reports whether a key is in play for this request.
func (g *idempotencyGuard) active() bool { return g != nil && g.store != nil && g.key != "" }

// complete marks the key as charged, against the job that did the work.
func (g *idempotencyGuard) complete(jobID string) {
	if !g.active() {
		return
	}
	if err := g.store.Complete(g.buyer, g.key, jobID); err != nil {
		fmt.Printf("openaiapi: could not record idempotency key completion: %v\n", err)
	}
}

// release forgets the key after a failure, so the caller can retry under it.
func (g *idempotencyGuard) release() {
	if !g.active() {
		return
	}
	if err := g.store.Release(g.buyer, g.key); err != nil {
		fmt.Printf("openaiapi: could not release idempotency key: %v\n", err)
	}
}

// requestFingerprint hashes what makes two requests the same request. It covers
// the model and the messages, which is everything that decides what work is done
// and what it costs.
func requestFingerprint(model string, msgs []inference.Message, maxTokens int, temperature float64) string {
	h := sha256.New()
	fmt.Fprintf(h, "model\x00%s\x00max\x00%d\x00temp\x00%v\x00", model, maxTokens, temperature)
	for _, m := range msgs {
		fmt.Fprintf(h, "%s\x00%s\x00", m.Role, m.Content)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// beginIdempotent resolves the key on an incoming request. It returns a guard to
// carry through the rest of the handler, and false when it has already written
// the response and the handler must stop.
func (h *Handler) beginIdempotent(w http.ResponseWriter, r *http.Request, buyer, fingerprint string) (*idempotencyGuard, bool) {
	if h.cfg.Idempotency == nil {
		return nil, true
	}
	key := strings.TrimSpace(r.Header.Get(IdempotencyHeader))
	if key == "" {
		// No key means no deduplication, which is exactly the old behaviour. It is
		// not made mandatory because OpenAI's protocol does not require it and a
		// client that sends none is not doing anything wrong.
		return nil, true
	}
	if len(key) > maxIdempotencyKeyLen {
		writeError(w, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("%s must be at most %d characters", IdempotencyHeader, maxIdempotencyKeyLen))
		return nil, false
	}

	existing, found, err := h.cfg.Idempotency.Reserve(buyer, key, IdempotencyRecord{
		RequestDigest: fingerprint,
		CreatedAt:     h.cfg.Now().UTC(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return nil, false
	}
	guard := &idempotencyGuard{store: h.cfg.Idempotency, buyer: buyer, key: key}
	if !found {
		return guard, true
	}

	// The same key with a DIFFERENT request is a client bug - a key that was not
	// rotated - and serving either answer would be wrong. 422 is what Stripe
	// returns for it and what the SDKs surface.
	if existing.RequestDigest != fingerprint {
		writeError(w, 422, "invalid_request_error",
			fmt.Sprintf("%s %q was already used for a different request; use a new key per request",
				IdempotencyHeader, key))
		return nil, false
	}
	if !existing.Completed {
		// Still running. Refused rather than queued behind it: the caller asked
		// for this work once and it is being done, so resending is the thing to
		// stop, and waiting is the caller's to do.
		writeError(w, http.StatusConflict, "invalid_request_error",
			fmt.Sprintf("a request with %s %q is still running; wait for it rather than resending",
				IdempotencyHeader, key))
		return nil, false
	}
	// Already done and already charged. The completion is deliberately not
	// stored, so the honest answer is the job id and a refusal to charge again -
	// which is the outcome that actually matters.
	writeError(w, http.StatusConflict, "invalid_request_error",
		fmt.Sprintf("%s %q was already used and settled as job %s; it will not be charged again",
			IdempotencyHeader, key, existing.JobID))
	return nil, false
}
