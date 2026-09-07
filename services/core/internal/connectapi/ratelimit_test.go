package connectapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
)

// clock is a hand-wound clock so a test can prove a refill without sleeping.
type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func limitedHandler(t *testing.T, cfg RateLimit, now func() time.Time) http.Handler {
	t.Helper()
	h, err := NewHandler(Config{
		Bindings: []Binding{{Desc: &marketv1.MarketService_ServiceDesc, Impl: &fakeMarket{}}},
		ExtraRoutes: map[string]http.Handler{
			"/v1/models": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}),
		},
		AllowedOrigins: []string{"*"},
		RateLimit:      cfg,
		Now:            now,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

func callAs(h http.Handler, method, path, credential, addr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, http.NoBody)
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	if addr != "" {
		req.RemoteAddr = addr
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestZeroRateLimitDisablesTheLimiter(t *testing.T) {
	h := limitedHandler(t, RateLimit{}, nil)

	// A loopback daemon should not be throttled by a default nobody asked for.
	for i := 0; i < 50; i++ {
		if rec := callAs(h, http.MethodGet, "/v1/models", "", "10.0.0.1:1"); rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d was limited with limiting disabled", i)
		}
	}
}

func TestABurstIsAllowedThenTheSustainedRateApplies(t *testing.T) {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	h := limitedHandler(t, RateLimit{RequestsPerMinute: 60, Burst: 3}, c.now)

	for i := 0; i < 3; i++ {
		if rec := callAs(h, http.MethodGet, "/v1/models", "", "10.0.0.1:1"); rec.Code != http.StatusOK {
			t.Fatalf("burst request %d = %d, want 200", i, rec.Code)
		}
	}
	rec := callAs(h, http.MethodGet, "/v1/models", "", "10.0.0.1:1")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the request past the burst = %d, want 429", rec.Code)
	}
	// Retry-After has to be at least a second: telling a client to retry
	// immediately is exactly what the limiter is refusing.
	after, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || after < 1 {
		t.Fatalf("Retry-After = %q, want a positive number of seconds", rec.Header().Get("Retry-After"))
	}
}

func TestABudgetRefillsOverTime(t *testing.T) {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	h := limitedHandler(t, RateLimit{RequestsPerMinute: 60, Burst: 1}, c.now)

	if rec := callAs(h, http.MethodGet, "/v1/models", "", "10.0.0.1:1"); rec.Code != http.StatusOK {
		t.Fatalf("first request = %d, want 200", rec.Code)
	}
	if rec := callAs(h, http.MethodGet, "/v1/models", "", "10.0.0.1:1"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request = %d, want 429", rec.Code)
	}

	// 60/minute is one per second, and a fractional refill must not round down
	// to nothing.
	c.add(2 * time.Second)
	if rec := callAs(h, http.MethodGet, "/v1/models", "", "10.0.0.1:1"); rec.Code != http.StatusOK {
		t.Fatalf("after waiting = %d, want 200", rec.Code)
	}
}

// TestCallersAreCountedSeparatelyByCredential is why the key is not the address:
// many callers legitimately share an egress IP, and one of them exhausting the
// budget must not lock out the others.
func TestCallersAreCountedSeparatelyByCredential(t *testing.T) {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	h := limitedHandler(t, RateLimit{RequestsPerMinute: 60, Burst: 1}, c.now)

	if rec := callAs(h, http.MethodGet, "/v1/models", "key-a", "10.0.0.1:1"); rec.Code != http.StatusOK {
		t.Fatal("first caller's first request should be allowed")
	}
	if rec := callAs(h, http.MethodGet, "/v1/models", "key-a", "10.0.0.1:1"); rec.Code != http.StatusTooManyRequests {
		t.Fatal("first caller's second request should be limited")
	}
	// Same address, different credential.
	if rec := callAs(h, http.MethodGet, "/v1/models", "key-b", "10.0.0.1:1"); rec.Code != http.StatusOK {
		t.Fatal("a second credential from the same address must have its own budget")
	}
}

// TestUnauthenticatedCallersAreCountedByAddress: keying only on the credential
// would leave an anonymous flood unbounded.
func TestUnauthenticatedCallersAreCountedByAddress(t *testing.T) {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	h := limitedHandler(t, RateLimit{RequestsPerMinute: 60, Burst: 1}, c.now)

	if rec := callAs(h, http.MethodGet, "/v1/models", "", "10.0.0.1:1"); rec.Code != http.StatusOK {
		t.Fatal("first anonymous request should be allowed")
	}
	if rec := callAs(h, http.MethodGet, "/v1/models", "", "10.0.0.1:1"); rec.Code != http.StatusTooManyRequests {
		t.Fatal("a second anonymous request from the same address should be limited")
	}
	if rec := callAs(h, http.MethodGet, "/v1/models", "", "10.0.0.2:1"); rec.Code != http.StatusOK {
		t.Fatal("a different address must have its own budget")
	}
}

// TestOneBudgetCoversBothProtocols: the limit is per caller, not per route, so
// a caller cannot double its allowance by alternating surfaces.
func TestOneBudgetCoversBothProtocols(t *testing.T) {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	h := limitedHandler(t, RateLimit{RequestsPerMinute: 60, Burst: 2}, c.now)

	callAs(h, http.MethodGet, "/v1/models", "k", "10.0.0.1:1")
	callAs(h, http.MethodPost, "/matrix.market.v1.MarketService/GetBalance", "k", "10.0.0.1:1")

	rec := callAs(h, http.MethodGet, "/v1/models", "k", "10.0.0.1:1")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: the two surfaces share one budget", rec.Code)
	}
}

// TestTheRefusalUsesTheCallersOwnEnvelope: one endpoint carries two protocols,
// and a client that cannot parse the refusal reports an unknown failure rather
// than "slow down".
func TestTheRefusalUsesTheCallersOwnEnvelope(t *testing.T) {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	h := limitedHandler(t, RateLimit{RequestsPerMinute: 60, Burst: 0}, c.now)
	// Burst 0 means one minute's worth (60); spend it.
	for i := 0; i < 60; i++ {
		callAs(h, http.MethodGet, "/v1/models", "k", "10.0.0.1:1")
	}

	openAI := callAs(h, http.MethodGet, "/v1/models", "k", "10.0.0.1:1")
	if openAI.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", openAI.Code)
	}
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(openAI.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("the /v1 refusal is not the OpenAI envelope: %v", err)
	}
	if envelope.Error.Type != "rate_limit_error" {
		t.Errorf("type = %q, want rate_limit_error", envelope.Error.Type)
	}

	connect := callAs(h, http.MethodPost, "/matrix.market.v1.MarketService/GetBalance", "k", "10.0.0.1:1")
	if connect.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", connect.Code)
	}
	var ce connectError
	if err := json.Unmarshal(connect.Body.Bytes(), &ce); err != nil {
		t.Fatalf("the Connect refusal is not a Connect error: %v", err)
	}
	if ce.Code != "resource_exhausted" {
		t.Errorf("code = %q, want resource_exhausted", ce.Code)
	}
}

// TestARefusedRequestStillCarriesCORSHeaders: without them a browser reports a
// CORS failure and the developer never sees the 429 at all.
func TestARefusedRequestStillCarriesCORSHeaders(t *testing.T) {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	h := limitedHandler(t, RateLimit{RequestsPerMinute: 60, Burst: 1}, c.now)

	callAs(h, http.MethodGet, "/v1/models", "k", "10.0.0.1:1")

	req := httptest.NewRequest(http.MethodGet, "/v1/models", http.NoBody)
	req.Header.Set("Authorization", "Bearer k")
	req.Header.Set("Origin", "https://app.test")
	req.RemoteAddr = "10.0.0.1:1"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Fatal("a 429 must still carry CORS headers or the browser hides it")
	}
}

// TestAPreflightIsNotCountedAgainstTheBudget: a browser sends one per request,
// so counting them would halve every browser caller's real allowance.
func TestAPreflightIsNotCountedAgainstTheBudget(t *testing.T) {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	h := limitedHandler(t, RateLimit{RequestsPerMinute: 60, Burst: 1}, c.now)

	req := httptest.NewRequest(http.MethodOptions, "/v1/models", http.NoBody)
	req.Header.Set("Origin", "https://app.test")
	req.RemoteAddr = "10.0.0.1:1"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight = %d, want 204", rec.Code)
	}

	if got := callAs(h, http.MethodGet, "/v1/models", "", "10.0.0.1:1"); got.Code != http.StatusOK {
		t.Fatalf("the real request after a preflight = %d, want 200", got.Code)
	}
}

// TestThePreflightAllowsGET: /v1/models is a GET, and a POST-only allow list
// made a browser preflight for it fail even though the route worked.
func TestThePreflightAllowsGET(t *testing.T) {
	h := limitedHandler(t, RateLimit{}, nil)

	req := httptest.NewRequest(http.MethodOptions, "/v1/models", http.NoBody)
	req.Header.Set("Origin", "https://app.test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "GET") {
		t.Fatalf("Allow-Methods = %q, want it to include GET", got)
	}
}

// TestIdleBucketsAreEvicted: without eviction the map grows with every distinct
// caller for the life of the process, which is a leak driven by whoever can
// reach the port.
func TestIdleBucketsAreEvicted(t *testing.T) {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	l := newLimiter(RateLimit{RequestsPerMinute: 60}, c.now)

	for i := 0; i < 100; i++ {
		l.allow(fmt.Sprintf("addr:10.0.0.%d", i))
	}
	l.mu.Lock()
	before := len(l.buckets)
	l.mu.Unlock()
	if before != 100 {
		t.Fatalf("tracked %d callers, want 100", before)
	}

	c.add(idleBucketTTL + time.Second)
	l.allow("addr:10.0.0.200")

	l.mu.Lock()
	after := len(l.buckets)
	l.mu.Unlock()
	if after != 1 {
		t.Fatalf("tracked %d callers after the TTL, want only the active one", after)
	}
}

func TestTheCredentialIsNotStoredInTheClear(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/models", http.NoBody)
	req.Header.Set("Authorization", "Bearer super-secret-key")

	key := limitKey(req)

	// The map key reaches error paths and debugging output; a raw credential
	// must not ride along in it.
	if strings.Contains(key, "super-secret-key") {
		t.Fatalf("limit key %q contains the credential", key)
	}
	if key == "" {
		t.Fatal("limit key is empty")
	}
}
