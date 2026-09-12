package openaiapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// memIdempotency is an in-memory store with the same contract as the node's.
type memIdempotency struct {
	mu      sync.Mutex
	records map[string]IdempotencyRecord
}

func newMemIdempotency() *memIdempotency {
	return &memIdempotency{records: map[string]IdempotencyRecord{}}
}

func (m *memIdempotency) id(buyer, key string) string { return buyer + "\x00" + key }

func (m *memIdempotency) Reserve(buyer, key string, rec IdempotencyRecord) (IdempotencyRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.records[m.id(buyer, key)]; ok {
		return existing, true, nil
	}
	rec.CreatedAt = time.Now().UTC()
	m.records[m.id(buyer, key)] = rec
	return rec, false, nil
}

func (m *memIdempotency) Complete(buyer, key, jobID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.records[m.id(buyer, key)]
	rec.JobID = jobID
	rec.Completed = true
	m.records[m.id(buyer, key)] = rec
	return nil
}

func (m *memIdempotency) Release(buyer, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.records, m.id(buyer, key))
	return nil
}

// handlerWithIdempotency builds the route with deduplication enabled, and an
// auth that reports one buyer unless a test overrides it.
type idemAuth struct{ account string }

func (a *idemAuth) AccountFor(*http.Request) (string, error) { return a.account, nil }

func handlerWithIdempotency(t *testing.T) (http.Handler, *fakeInference, *idemAuth) {
	t.Helper()
	inf := &fakeInference{}
	auth := &idemAuth{account: "buyer-a"}
	h, err := NewHandler(Config{
		Inference:   inf,
		Router:      twoProviders(),
		Auth:        auth,
		Idempotency: newMemIdempotency(),
		Now:         func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	mux := http.NewServeMux()
	for path, handler := range h.Routes() {
		mux.Handle(path, handler)
	}
	return mux, inf, auth
}

// postChat posts a chat completion with an optional idempotency key.
func postChat(t *testing.T, h http.Handler, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, ChatCompletionsPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set(IdempotencyHeader, key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestARetriedRequestIsNotASecondCharge is the bug this closes. The openai SDKs
// retry on a connection error or a 5xx by default, and so does any proxy in
// front of this endpoint, so the second POST is rarely the caller's decision.
func TestARetriedRequestIsNotASecondCharge(t *testing.T) {
	h, svc, _ := handlerWithIdempotency(t)

	body := `{"model":"llama-3.3-70b","messages":[{"role":"user","content":"hello"}]}`
	first := postChat(t, h, body, "key-1")
	if first.Code != 200 {
		t.Fatalf("first request = %d: %s", first.Code, first.Body.String())
	}
	if len(svc.submitted) != 1 {
		t.Fatalf("%d jobs submitted for one request", len(svc.submitted))
	}

	second := postChat(t, h, body, "key-1")
	if second.Code != 409 {
		t.Fatalf("the retry = %d, want 409: %s", second.Code, second.Body.String())
	}
	if len(svc.submitted) != 1 {
		t.Fatalf("the retry submitted a second job: %d total", len(svc.submitted))
	}
	// The refusal has to say the work was already done and paid for, or a caller
	// reads a 409 as a transient conflict and retries again.
	if !strings.Contains(second.Body.String(), "will not be charged again") {
		t.Fatalf("the refusal did not say the charge stands:\n%s", second.Body.String())
	}
}

// TestTwoDifferentPromptsAreTwoRequests is the deliberate non-behaviour.
// Requests are NOT deduplicated by content: two identical prompts are an
// ordinary thing to send - a regenerate, a second sample at temperature - and
// only an explicit key means "these are the same request".
func TestTwoDifferentPromptsAreTwoRequests(t *testing.T) {
	h, svc, _ := handlerWithIdempotency(t)

	body := `{"model":"llama-3.3-70b","messages":[{"role":"user","content":"same prompt"}]}`
	if got := postChat(t, h, body, "").Code; got != 200 {
		t.Fatalf("first = %d", got)
	}
	if got := postChat(t, h, body, "").Code; got != 200 {
		t.Fatalf("an identical prompt with no key should run again, got %d", got)
	}
	if len(svc.submitted) != 2 {
		t.Fatalf("%d jobs for two keyless requests, want 2", len(svc.submitted))
	}
}

// TestOneKeyForTwoDifferentRequestsIsRefused catches the client bug where a key
// is not rotated. Serving either answer would be wrong.
func TestOneKeyForTwoDifferentRequestsIsRefused(t *testing.T) {
	h, svc, _ := handlerWithIdempotency(t)

	if got := postChat(t, h, `{"model":"llama-3.3-70b","messages":[{"role":"user","content":"one"}]}`, "k").Code; got != 200 {
		t.Fatalf("first = %d", got)
	}
	resp := postChat(t, h, `{"model":"llama-3.3-70b","messages":[{"role":"user","content":"two"}]}`, "k")
	if resp.Code != 422 {
		t.Fatalf("a reused key with a different request = %d, want 422: %s", resp.Code, resp.Body.String())
	}
	if len(svc.submitted) != 1 {
		t.Fatalf("the mismatched reuse still submitted work: %d", len(svc.submitted))
	}
}

// TestTwoBuyersDoNotShareAKeyspace pins that a key is the CALLER's identifier.
// Two buyers have no reason to coordinate, and without the buyer in the record
// one buyer's "1" would silently block another's.
func TestTwoBuyersDoNotShareAKeyspace(t *testing.T) {
	h, svc, auth := handlerWithIdempotency(t)
	body := `{"model":"llama-3.3-70b","messages":[{"role":"user","content":"hello"}]}`

	auth.account = "buyer-a"
	if got := postChat(t, h, body, "key-1").Code; got != 200 {
		t.Fatalf("buyer a = %d", got)
	}
	auth.account = "buyer-b"
	if got := postChat(t, h, body, "key-1").Code; got != 200 {
		t.Fatalf("buyer b was blocked by buyer a's key: %d", got)
	}
	if len(svc.submitted) != 2 {
		t.Fatalf("%d jobs for two buyers, want 2", len(svc.submitted))
	}
}

// TestAFailedRequestFreesItsKey covers the retry a buyer actually wants. A job
// that failed charged nobody, so keeping the key would turn one transport error
// into a permanently unusable key.
func TestAFailedRequestFreesItsKey(t *testing.T) {
	h, svc, _ := handlerWithIdempotency(t)
	svc.fulfillErr = errors.New("the backend fell over")

	body := `{"model":"llama-3.3-70b","messages":[{"role":"user","content":"hello"}]}`
	if got := postChat(t, h, body, "k").Code; got == 200 {
		t.Fatal("the fulfil was meant to fail")
	}

	svc.fulfillErr = nil
	if got := postChat(t, h, body, "k").Code; got != 200 {
		t.Fatalf("retrying under the key of a FAILED request = %d, want 200", got)
	}
}

// TestAnOversizedKeyIsRefused stops a key being used as storage.
func TestAnOversizedKeyIsRefused(t *testing.T) {
	h, _, _ := handlerWithIdempotency(t)
	resp := postChat(t, h,
		`{"model":"llama-3.3-70b","messages":[{"role":"user","content":"hello"}]}`,
		strings.Repeat("k", maxIdempotencyKeyLen+1))
	if resp.Code != 400 {
		t.Fatalf("an oversized key = %d, want 400", resp.Code)
	}
}
