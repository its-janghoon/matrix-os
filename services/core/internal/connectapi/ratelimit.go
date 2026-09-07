package connectapi

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// This file bounds how fast one caller can drive the HTTP endpoint. Nothing in
// services/core did before: the only limit was maxRequestBytes on a single body,
// so a caller could open as many requests as it liked as fast as it liked. On a
// public endpoint that is not a theoretical problem - every request here reserves
// capacity, reads a ledger or runs a model.
//
// The bucket is keyed on the CREDENTIAL when one is present and on the remote
// address otherwise. Keying only on the address is wrong for an API where many
// callers legitimately share an egress IP, and keying only on the credential
// would leave unauthenticated floods unbounded. Note the honest limitation: the
// remote address is the TCP peer, not X-Forwarded-For, which any client can
// forge. Behind a reverse proxy every unauthenticated request therefore looks
// like one caller, so an operator terminating TLS at a proxy should rate-limit
// there as well.

// RateLimit configures the per-caller request limit. A zero RequestsPerMinute
// disables limiting entirely, which is the right setting for a node on a
// loopback interface and the wrong one for anything reachable.
type RateLimit struct {
	// RequestsPerMinute is the sustained rate allowed per caller. Zero disables
	// the limiter.
	RequestsPerMinute int
	// Burst is how many requests a caller may make back to back before the
	// sustained rate applies. Zero means RequestsPerMinute, i.e. one minute's
	// worth.
	Burst int
}

// idleBucketTTL is how long an unused bucket is kept. Without eviction the map
// would grow with every distinct caller for the life of the process, which is a
// slow memory leak driven by whoever can reach the port.
const idleBucketTTL = 10 * time.Minute

// bucket is one caller's token bucket. Tokens are stored as a float so a
// fractional refill (one request per second is 1/60th of a per-minute budget)
// does not round down to nothing.
type bucket struct {
	tokens   float64
	lastSeen time.Time
}

type limiter struct {
	perSecond float64
	burst     float64
	now       func() time.Time

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastPurge time.Time
}

func newLimiter(cfg RateLimit, now func() time.Time) *limiter {
	if cfg.RequestsPerMinute <= 0 {
		return nil
	}
	burst := cfg.Burst
	if burst <= 0 {
		burst = cfg.RequestsPerMinute
	}
	if now == nil {
		now = time.Now
	}
	return &limiter{
		perSecond: float64(cfg.RequestsPerMinute) / 60,
		burst:     float64(burst),
		now:       now,
		buckets:   make(map[string]*bucket),
		lastPurge: now(),
	}
}

// allow reports whether the caller may proceed, and when it may retry if not.
func (l *limiter) allow(key string) (bool, time.Duration) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.purgeLocked(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst}
		l.buckets[key] = b
	} else {
		elapsed := now.Sub(b.lastSeen).Seconds()
		if elapsed > 0 {
			b.tokens += elapsed * l.perSecond
			if b.tokens > l.burst {
				b.tokens = l.burst
			}
		}
	}
	b.lastSeen = now

	if b.tokens < 1 {
		// Round up: a Retry-After of 0 tells a client to retry immediately, which
		// is exactly what the limiter is refusing.
		wait := time.Duration((1-b.tokens)/l.perSecond*float64(time.Second)) + time.Second
		return false, wait.Truncate(time.Second)
	}
	b.tokens--
	return true, 0
}

// purgeLocked drops buckets nobody has used for idleBucketTTL. It runs at most
// once per TTL so a busy endpoint does not walk the whole map per request.
func (l *limiter) purgeLocked(now time.Time) {
	if now.Sub(l.lastPurge) < idleBucketTTL {
		return
	}
	l.lastPurge = now
	for key, b := range l.buckets {
		if now.Sub(b.lastSeen) >= idleBucketTTL {
			delete(l.buckets, key)
		}
	}
}

// limitKey identifies the caller: its credential when it presented one,
// otherwise its remote address. A credential is hashed down to a prefix rather
// than used whole so a key never reaches a log line or an error message through
// this map.
func limitKey(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		if token != "" {
			return "cred:" + fingerprint(token)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "addr:" + host
}

// fingerprint reduces a credential to a short, stable, non-reversible label.
// FNV-1a is enough: this is a map key for accounting, not a security boundary,
// and the credential itself has already been checked (or will be) elsewhere.
func fingerprint(s string) string {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return strconv.FormatUint(h, 16)
}

// withRateLimit wraps next, refusing a caller that is over its budget. A nil
// limiter returns next unchanged so the disabled case costs nothing.
func withRateLimit(l *limiter, next http.Handler) http.Handler {
	if l == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A CORS preflight carries no credential and does no work; counting it
		// would halve every browser caller's real budget.
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if ok, retryAfter := l.allow(limitKey(r)); !ok {
			writeTooManyRequests(w, r, retryAfter)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeTooManyRequests answers in the envelope the caller's protocol expects.
// One endpoint carries two: a Connect client reads {code, message} and an
// OpenAI client reads {error: {message, type}}, and a client that cannot parse
// the refusal reports it as an unknown failure instead of "slow down".
func writeTooManyRequests(w http.ResponseWriter, r *http.Request, retryAfter time.Duration) {
	seconds := int(retryAfter.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)

	const message = "too many requests: slow down and retry"
	if strings.HasPrefix(r.URL.Path, "/v1/") {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"message": message, "type": "rate_limit_error"},
		})
		return
	}
	_ = json.NewEncoder(w).Encode(connectError{Code: "resource_exhausted", Message: message})
}
