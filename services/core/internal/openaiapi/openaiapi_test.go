package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/inference"
	"github.com/ecirlabs/matrix-core/internal/market"
)

// fakeInference records what the route asked for and answers with a canned
// result, so a test can assert on the translation rather than on a consensus
// engine's behaviour.
type fakeInference struct {
	submitted  []submitCall
	submitErr  error
	fulfilled  []string
	fulfillErr error
	result     *inference.InferenceJob
	streamed   []string
	streamErr  error
	oneShot    bool
}

type submitCall struct {
	buyer, provider string
	req             inference.InferenceRequest
	units           uint64
}

func (f *fakeInference) SubmitInferenceJob(buyer, providerID string, req inference.InferenceRequest, units uint64) (*inference.InferenceJob, error) {
	f.submitted = append(f.submitted, submitCall{buyer: buyer, provider: providerID, req: req, units: units})
	if f.submitErr != nil {
		return nil, f.submitErr
	}
	return &inference.InferenceJob{ID: "job-1", Buyer: buyer, Provider: providerID, Request: req}, nil
}

func (f *fakeInference) FulfillJob(ctx context.Context, jobID string) (*inference.InferenceJob, error) {
	f.fulfilled = append(f.fulfilled, jobID)
	if f.fulfillErr != nil {
		return nil, f.fulfillErr
	}
	if f.result != nil {
		return f.result, nil
	}
	return &inference.InferenceJob{
		ID:         jobID,
		Provider:   "gpu-cheap",
		Completion: "hello back",
		Model:      "llama-3.3-70b",
		Units:      12,
		Usage:      inference.Usage{PromptTokens: 7, CompletionTokens: 5, TotalTokens: 12},
	}, nil
}

// StreamJob makes fakeInference a Streamer, emitting the canned completion one
// word at a time so a test can count frames.
func (f *fakeInference) StreamJob(ctx context.Context, jobID string, onChunk inference.ChunkFunc) (*inference.InferenceJob, *inference.StreamResult, error) {
	f.streamed = append(f.streamed, jobID)
	if f.streamErr != nil {
		return nil, nil, f.streamErr
	}
	done, err := f.FulfillJob(ctx, jobID)
	if err != nil {
		return nil, nil, err
	}
	for _, word := range strings.SplitAfter(done.Completion, " ") {
		if word == "" {
			continue
		}
		if err := onChunk(word); err != nil {
			return nil, nil, err
		}
	}
	return done, &inference.StreamResult{
		Response: inference.InferenceResponse{
			Model: done.Model, Completion: done.Completion, Usage: done.Usage,
		},
		StreamedOneShot: f.oneShot,
	}, nil
}

// nonStreamingInference is an Inference that is NOT a Streamer, so the route's
// "this server cannot stream" path stays reachable in a test. The embedded
// methods are re-declared because embedding a value that satisfies Streamer
// would promote StreamJob onto this type too.
type nonStreamingInference struct{ inner fakeInference }

func (n *nonStreamingInference) SubmitInferenceJob(buyer, providerID string, req inference.InferenceRequest, units uint64) (*inference.InferenceJob, error) {
	return n.inner.SubmitInferenceJob(buyer, providerID, req, units)
}

func (n *nonStreamingInference) FulfillJob(ctx context.Context, jobID string) (*inference.InferenceJob, error) {
	return n.inner.FulfillJob(ctx, jobID)
}

// errStreamBroke stands in for a backend dying partway through a stream.
var errStreamBroke = errors.New("the model server hung up")

type fakeRouter struct{ providers []market.Provider }

func (f fakeRouter) ProvidersForModel(model string) []market.Provider {
	out := []market.Provider{}
	for _, p := range f.providers {
		if p.ServesModel(model) && p.Available > 0 {
			out = append(out, p)
		}
	}
	return out
}

func (f fakeRouter) ListProviders() []market.Provider { return f.providers }

type fakeAuth struct {
	account string
	err     error
}

func (f fakeAuth) AccountFor(*http.Request) (string, error) { return f.account, f.err }

func newHandler(t *testing.T, inf Inference, router Router, auth Authenticator) http.Handler {
	t.Helper()
	h, err := NewHandler(Config{
		Inference: inf,
		Router:    router,
		Auth:      auth,
		Now:       func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	mux := http.NewServeMux()
	for path, handler := range h.Routes() {
		mux.Handle(path, handler)
	}
	return mux
}

func twoProviders() fakeRouter {
	return fakeRouter{providers: []market.Provider{
		{ID: "gpu-dear", Capacity: 10, Available: 10, PricePerUnit: 9, Models: []string{"llama-3.3-70b"}},
		{ID: "gpu-cheap", Capacity: 10, Available: 10, PricePerUnit: 2, Models: []string{"llama-3.3-70b"}},
	}}
}

func post(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestAnOpenAIShapedRequestGetsAnOpenAIShapedResponse is the whole point of the
// package: a caller that only changed base_url sees the schema its SDK expects.
func TestAnOpenAIShapedRequestGetsAnOpenAIShapedResponse(t *testing.T) {
	inf := &fakeInference{}
	h := newHandler(t, inf, twoProviders(), fakeAuth{account: "buyer-acct"})

	rec := post(t, h, ChatCompletionsPath, `{
		"model": "llama-3.3-70b",
		"messages": [{"role": "user", "content": "hello"}]
	}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if got["object"] != "chat.completion" {
		t.Errorf("object = %v, want chat.completion", got["object"])
	}
	if got["id"] != "chatcmpl-job-1" {
		t.Errorf("id = %v, want chatcmpl-job-1", got["id"])
	}
	choices, ok := got["choices"].([]any)
	if !ok || len(choices) != 1 {
		t.Fatalf("choices = %v, want exactly one", got["choices"])
	}
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["role"] != "assistant" || msg["content"] != "hello back" {
		t.Errorf("message = %v, want the assistant completion", msg)
	}
	if choices[0].(map[string]any)["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v, want stop", choices[0].(map[string]any)["finish_reason"])
	}
	// Token counts, not billed units: an SDK reads usage to report token spend,
	// and units are that count scaled by the provider's price.
	usage := got["usage"].(map[string]any)
	if usage["prompt_tokens"] != float64(7) || usage["completion_tokens"] != float64(5) || usage["total_tokens"] != float64(12) {
		t.Errorf("usage = %v, want the backend's reported token counts", usage)
	}
}

// TestTheBuyerComesFromTheAPIKey pins the answer to "who pays" on a protocol
// that carries no buyer field.
func TestTheBuyerComesFromTheAPIKey(t *testing.T) {
	inf := &fakeInference{}
	h := newHandler(t, inf, twoProviders(), fakeAuth{account: "buyer-acct"})

	post(t, h, ChatCompletionsPath, `{"model":"llama-3.3-70b","messages":[{"role":"user","content":"hi"}]}`)

	if len(inf.submitted) != 1 {
		t.Fatalf("submitted %d jobs, want 1", len(inf.submitted))
	}
	if inf.submitted[0].buyer != "buyer-acct" {
		t.Fatalf("buyer = %q, want the account the key names", inf.submitted[0].buyer)
	}
}

func TestAKeyWithNoAccountCannotBuyInference(t *testing.T) {
	inf := &fakeInference{}
	h := newHandler(t, inf, twoProviders(), fakeAuth{account: ""})

	rec := post(t, h, ChatCompletionsPath, `{"model":"llama-3.3-70b","messages":[{"role":"user","content":"hi"}]}`)

	// Refusing is the safe reading: the alternative is guessing whose balance to
	// spend. And nothing must have been submitted.
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
	if len(inf.submitted) != 0 {
		t.Fatalf("submitted %d jobs, want none", len(inf.submitted))
	}
}

func TestAnInvalidKeyIsRefusedBeforeAnythingIsSubmitted(t *testing.T) {
	inf := &fakeInference{}
	h := newHandler(t, inf, twoProviders(), fakeAuth{err: errors.New("nope")})

	rec := post(t, h, ChatCompletionsPath, `{"model":"llama-3.3-70b","messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(inf.submitted) != 0 {
		t.Fatal("an unauthenticated request must not reserve capacity")
	}
}

func TestNoAuthenticatorRefusesEveryRequest(t *testing.T) {
	inf := &fakeInference{}
	h := newHandler(t, inf, twoProviders(), nil)

	rec := post(t, h, ChatCompletionsPath, `{"model":"llama-3.3-70b","messages":[{"role":"user","content":"hi"}]}`)

	// A route that charges an account cannot fall back to "no auth configured".
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(inf.submitted) != 0 {
		t.Fatal("a request with no auth policy must not reserve capacity")
	}
}

// TestTheModelPicksTheCheapestProvider is the routing contract a developer is
// buying: they name a model, not a machine.
func TestTheModelPicksTheCheapestProvider(t *testing.T) {
	inf := &fakeInference{}
	h := newHandler(t, inf, twoProviders(), fakeAuth{account: "buyer"})

	post(t, h, ChatCompletionsPath, `{"model":"llama-3.3-70b","messages":[{"role":"user","content":"hi"}]}`)

	if len(inf.submitted) != 1 {
		t.Fatalf("submitted %d jobs, want 1", len(inf.submitted))
	}
	if inf.submitted[0].provider != "gpu-cheap" {
		t.Fatalf("provider = %q, want gpu-cheap", inf.submitted[0].provider)
	}
}

func TestAModelNobodyServesIsA404NamingIt(t *testing.T) {
	h := newHandler(t, &fakeInference{}, twoProviders(), fakeAuth{account: "buyer"})

	rec := post(t, h, ChatCompletionsPath, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "gpt-4o") {
		t.Fatalf("body = %s, want the unknown model named", rec.Body.String())
	}
}

// TestErrorsUseTheOpenAIEnvelope matters because a client library reads
// error.message and error.type; anything else surfaces as "unknown error".
func TestErrorsUseTheOpenAIEnvelope(t *testing.T) {
	h := newHandler(t, &fakeInference{}, twoProviders(), fakeAuth{account: "buyer"})

	rec := post(t, h, ChatCompletionsPath, `{"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("body is not the OpenAI error envelope: %v", err)
	}
	if env.Error.Type != "invalid_request_error" || env.Error.Message == "" {
		t.Fatalf("error = %+v, want a typed, non-empty message", env.Error)
	}
}

func TestMarketErrorsMapToTheStatusAnSDKBranchesOn(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		want     int
		wantKind string
	}{
		{"out of money", market.ErrInsufficientFunds, http.StatusPaymentRequired, "insufficient_quota"},
		{"provider full", market.ErrInsufficientCapacity, http.StatusServiceUnavailable, "server_error"},
		{"provider vanished", market.ErrProviderNotFound, http.StatusNotFound, "invalid_request_error"},
		{"no backend", inference.ErrNoBackend, http.StatusServiceUnavailable, "server_error"},
		// Anything unrecognised has to be a 500: reporting an internal failure as
		// a client error sends a developer hunting a bug in their own request.
		{"something else", errors.New("disk on fire"), http.StatusInternalServerError, "server_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inf := &fakeInference{submitErr: fmt.Errorf("wrapped: %w", tt.err)}
			h := newHandler(t, inf, twoProviders(), fakeAuth{account: "buyer"})

			rec := post(t, h, ChatCompletionsPath, `{"model":"llama-3.3-70b","messages":[{"role":"user","content":"hi"}]}`)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tt.want, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantKind) {
				t.Fatalf("body = %s, want type %s", rec.Body.String(), tt.wantKind)
			}
		})
	}
}

// TestAStreamRequestOnANonStreamingServiceSaysSo: the route must not answer a
// streaming request with one whole body, because a client parsing SSE frames
// would fail in a way that looks like a broken server. Saying "this server
// cannot stream" is the honest alternative.
func TestAStreamRequestOnANonStreamingServiceSaysSo(t *testing.T) {
	h := newHandler(t, &nonStreamingInference{}, twoProviders(), fakeAuth{account: "buyer"})

	rec := post(t, h, ChatCompletionsPath,
		`{"model":"llama-3.3-70b","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "cannot stream") {
		t.Fatalf("body = %s, want it to say the server cannot stream", rec.Body.String())
	}
}

// TestUnknownRequestFieldsAreIgnored: SDKs send parameters we have no
// provider-independent meaning for, and failing the call over one of them breaks
// working code for no benefit.
func TestUnknownRequestFieldsAreIgnored(t *testing.T) {
	h := newHandler(t, &fakeInference{}, twoProviders(), fakeAuth{account: "buyer"})

	rec := post(t, h, ChatCompletionsPath, `{
		"model": "llama-3.3-70b",
		"messages": [{"role": "user", "content": "hi"}],
		"n": 1, "stop": ["\n"], "presence_penalty": 0.5, "user": "u-1"
	}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
}

func TestSystemAndAssistantTurnsSurviveTranslation(t *testing.T) {
	inf := &fakeInference{}
	h := newHandler(t, inf, twoProviders(), fakeAuth{account: "buyer"})

	post(t, h, ChatCompletionsPath, `{
		"model": "llama-3.3-70b",
		"messages": [
			{"role": "system", "content": "be terse"},
			{"role": "user", "content": "hi"},
			{"role": "assistant", "content": "hello"},
			{"role": "user", "content": "again"}
		]
	}`)

	if len(inf.submitted) != 1 {
		t.Fatalf("submitted %d jobs, want 1", len(inf.submitted))
	}
	got := inf.submitted[0].req.Messages
	if len(got) != 4 {
		t.Fatalf("got %d messages, want 4: %+v", len(got), got)
	}
	if got[0].Role != inference.RoleSystem || got[2].Role != inference.RoleAssistant {
		t.Fatalf("roles = %v, %v; want system, assistant", got[0].Role, got[2].Role)
	}
}

func TestAnUnsupportedRoleIsRejectedByName(t *testing.T) {
	h := newHandler(t, &fakeInference{}, twoProviders(), fakeAuth{account: "buyer"})

	rec := post(t, h, ChatCompletionsPath,
		`{"model":"llama-3.3-70b","messages":[{"role":"tool","content":"{}"}]}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "tool") {
		t.Fatalf("body = %s, want the role named", rec.Body.String())
	}
}

// TestTheReservationIsGenerousEnoughToPayForTheCompletion: the settled charge is
// clamped to the reservation, so under-reserving underpays a provider for work
// it was asked to do.
func TestTheReservationIsGenerousEnoughToPayForTheCompletion(t *testing.T) {
	inf := &fakeInference{}
	h := newHandler(t, inf, twoProviders(), fakeAuth{account: "buyer"})

	// A one-word prompt with no max_tokens. The reservation must still cover a
	// completion of ordinary length.
	post(t, h, ChatCompletionsPath, `{"model":"llama-3.3-70b","messages":[{"role":"user","content":"hi"}]}`)

	if got := inf.submitted[0].units; got < uncappedCompletionReserve {
		t.Fatalf("reserved %d units for an uncapped completion, want at least %d",
			got, uncappedCompletionReserve)
	}
}

func TestMaxTokensRaisesTheReservation(t *testing.T) {
	inf := &fakeInference{}
	h := newHandler(t, inf, twoProviders(), fakeAuth{account: "buyer"})

	post(t, h, ChatCompletionsPath,
		`{"model":"llama-3.3-70b","max_tokens":4000,"messages":[{"role":"user","content":"hi"}]}`)

	if got := inf.submitted[0].units; got < 4000 {
		t.Fatalf("reserved %d units for max_tokens 4000, want at least 4000", got)
	}
}

func TestModelsListsWhatTheOrderBookAdvertises(t *testing.T) {
	h := newHandler(t, &fakeInference{}, fakeRouter{providers: []market.Provider{
		{ID: "a", Available: 1, PricePerUnit: 9, Models: []string{"llama-3.3-70b", "qwen-2.5-72b"}},
		{ID: "b", Available: 1, PricePerUnit: 2, Models: []string{"llama-3.3-70b"}},
		{ID: "compute-only", Available: 1, PricePerUnit: 1},
	}}, fakeAuth{account: "buyer"})

	req := httptest.NewRequest(http.MethodGet, ModelsPath, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Object string `json:"object"`
		Data   []struct {
			ID           string `json:"id"`
			Providers    int    `json:"providers"`
			PricePerUnit uint64 `json:"price_per_unit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not a models list: %v", err)
	}
	if got.Object != "list" {
		t.Errorf("object = %q, want list", got.Object)
	}
	// Sorted, de-duplicated across providers, and a compute-only provider
	// contributes nothing because it advertises no model.
	if len(got.Data) != 2 {
		t.Fatalf("got %d models, want 2: %+v", len(got.Data), got.Data)
	}
	if got.Data[0].ID != "llama-3.3-70b" || got.Data[1].ID != "qwen-2.5-72b" {
		t.Fatalf("ids = %s, %s; want them sorted", got.Data[0].ID, got.Data[1].ID)
	}
	if got.Data[0].Providers != 2 {
		t.Errorf("llama providers = %d, want 2", got.Data[0].Providers)
	}
	// The cheapest price among the providers serving it, which is what a caller
	// choosing a model on a market wants.
	if got.Data[0].PricePerUnit != 2 {
		t.Errorf("llama price = %d, want 2 (the cheapest)", got.Data[0].PricePerUnit)
	}
}

func TestWrongMethodsAreRefused(t *testing.T) {
	h := newHandler(t, &fakeInference{}, twoProviders(), fakeAuth{account: "buyer"})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ChatCompletionsPath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET on chat completions = %d, want 405", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, ModelsPath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST on models = %d, want 405", rec.Code)
	}
}

func TestNewHandlerRequiresItsCollaborators(t *testing.T) {
	if _, err := NewHandler(Config{Router: twoProviders()}); err == nil {
		t.Error("want an error with no inference service")
	}
	if _, err := NewHandler(Config{Inference: &fakeInference{}}); err == nil {
		t.Error("want an error with no router")
	}
}

// TestRoutingDoesNotDependOnTheRouterOrdering: the market sorts cheapest-first,
// but the route must not rely on that. A Router that stopped sorting would
// otherwise silently change who gets paid.
func TestRoutingDoesNotDependOnTheRouterOrdering(t *testing.T) {
	unsorted := fakeRouter{providers: []market.Provider{
		{ID: "dear", Available: 1, PricePerUnit: 9, Models: []string{"m"}},
		{ID: "cheap", Available: 1, PricePerUnit: 2, Models: []string{"m"}},
		{ID: "middling", Available: 1, PricePerUnit: 5, Models: []string{"m"}},
	}}
	inf := &fakeInference{}
	h := newHandler(t, inf, unsorted, fakeAuth{account: "buyer"})

	post(t, h, ChatCompletionsPath, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)

	if inf.submitted[0].provider != "cheap" {
		t.Fatalf("provider = %q, want cheap", inf.submitted[0].provider)
	}
}

func TestEquallyPricedProvidersTieBreakOnIDSoABillIsExplainable(t *testing.T) {
	tied := fakeRouter{providers: []market.Provider{
		{ID: "c", Available: 1, PricePerUnit: 5, Models: []string{"m"}},
		{ID: "a", Available: 1, PricePerUnit: 5, Models: []string{"m"}},
		{ID: "b", Available: 1, PricePerUnit: 5, Models: []string{"m"}},
	}}
	for i := 0; i < 6; i++ {
		inf := &fakeInference{}
		h := newHandler(t, inf, tied, fakeAuth{account: "buyer"})
		post(t, h, ChatCompletionsPath, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
		if inf.submitted[0].provider != "a" {
			t.Fatalf("call %d picked %q, want a every time", i, inf.submitted[0].provider)
		}
	}
}
