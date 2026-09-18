package openaiapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/market"
)

type fakeDirectory struct{ sellers []RemoteSeller }

func (f fakeDirectory) RemoteSellers() []RemoteSeller { return f.sellers }

// A validator: it hosts nothing itself, and it is the address a newcomer is
// given. Both facts together are what made the old answers wrong.
func hostsNothing() fakeRouter { return fakeRouter{providers: nil} }

func theMarketSells() fakeDirectory {
	return fakeDirectory{sellers: []RemoteSeller{
		{
			ProviderID:   "gpu-box-1",
			Endpoint:     "https://gpu.example.com",
			Models:       []string{"qwen3-32b", "qwen3.6-27b"},
			PricePerUnit: 7,
			Available:    900,
		},
		{
			ProviderID:   "gpu-box-2",
			Endpoint:     "https://cheaper.example.com",
			Models:       []string{"qwen3-32b"},
			PricePerUnit: 3,
			Available:    500,
		},
		// Announcing but with nothing left to sell, and one with nowhere to be
		// reached. Neither is somewhere a caller can be sent.
		{ProviderID: "full", Endpoint: "https://full.example.com", Models: []string{"llama-3.3-70b"}, PricePerUnit: 1, Available: 0},
		{ProviderID: "unreachable", Endpoint: "", Models: []string{"secret-model"}, PricePerUnit: 1, Available: 10},
	}}
}

func directoryHandler(t *testing.T, router Router, dir Directory) http.Handler {
	t.Helper()
	h, err := NewHandler(Config{
		Inference: &fakeInference{},
		Router:    router,
		Directory: dir,
		Auth:      fakeAuth{account: "buyer-1"},
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

func listModels(t *testing.T, mux http.Handler) []modelInfo {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ModelsPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", ModelsPath, rec.Code, rec.Body.String())
	}
	var body struct {
		Data []modelInfo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return body.Data
}

// A node that hosts nothing used to answer "this network has no models", which
// is indistinguishable from the true form of that sentence and a very
// discouraging thing to be told wrongly.
func TestANodeThatHostsNothingStillDescribesTheMarket(t *testing.T) {
	models := listModels(t, directoryHandler(t, hostsNothing(), theMarketSells()))

	byID := map[string]modelInfo{}
	for _, m := range models {
		byID[m.ID] = m
	}
	if _, ok := byID["qwen3-32b"]; !ok {
		t.Fatalf("the market's models are missing from the listing: %+v", models)
	}
	m := byID["qwen3-32b"]
	if m.Providers != 2 {
		t.Errorf("qwen3-32b has %d sellers, want 2", m.Providers)
	}
	if m.PricePerUnit != 3 {
		t.Errorf("cheapest price is %d, want the cheaper seller's 3", m.PricePerUnit)
	}
	if m.Servable {
		t.Error("this node claims it can serve a model it does not host")
	}
	// Listing a model without saying where to buy it would be worse than hiding
	// it: a name that 404s on use is a dead end.
	if m.Endpoint != "https://cheaper.example.com" {
		t.Errorf("endpoint is %q, want the cheapest seller's address", m.Endpoint)
	}
}

// A seller a caller cannot actually reach is not somewhere to send them.
func TestAModelNobodyCanBeSentToIsNotListed(t *testing.T) {
	models := listModels(t, directoryHandler(t, hostsNothing(), theMarketSells()))
	for _, m := range models {
		switch m.ID {
		case "llama-3.3-70b":
			t.Error("a seller with no capacity left was listed as somewhere to go")
		case "secret-model":
			t.Error("a seller with no published address was listed as somewhere to go")
		}
	}
}

// What this node serves is marked servable, and carries no redirect: the caller
// is already in the right place.
func TestAModelThisNodeServesIsMarkedServable(t *testing.T) {
	local := fakeRouter{providers: []market.Provider{{
		ID: "local-1", Capacity: 100, Available: 100, PricePerUnit: 5,
		Models: []string{"qwen3-32b"}, QuoteID: "q", QuoteVersion: 1,
		ObservedAt: time.Now(), ValidUntil: time.Now().Add(time.Hour),
	}}}
	models := listModels(t, directoryHandler(t, local, theMarketSells()))

	for _, m := range models {
		if m.ID != "qwen3-32b" {
			continue
		}
		if !m.Servable {
			t.Error("a model this node serves is not marked servable")
		}
		if m.Endpoint != "" {
			t.Errorf("a servable model carries a redirect to %q", m.Endpoint)
		}
		// Local and remote sellers of the same model are counted together,
		// because the question was how many sellers the market has.
		if m.Providers != 3 {
			t.Errorf("qwen3-32b has %d sellers, want 3 (one local, two remote)", m.Providers)
		}
		return
	}
	t.Fatal("the locally served model is missing from the listing")
}

// The refusal used to be a claim about the network made after reading one node's
// own shelf, and it was wrong exactly when it mattered.
func TestAskingForAModelThisNodeLacksNamesWhoHasIt(t *testing.T) {
	mux := directoryHandler(t, hostsNothing(), theMarketSells())

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, ChatCompletionsPath,
		strings.NewReader(`{"model":"qwen3-32b","messages":[{"role":"user","content":"hi"}]}`)))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "no provider on this network") {
		t.Error("the refusal still claims the network has no such model")
	}
	if !strings.Contains(body, "https://cheaper.example.com") {
		t.Errorf("the refusal does not name where to buy it: %s", body)
	}
	if !strings.Contains(body, "cannot buy on your behalf") {
		t.Errorf("the refusal does not say this node will not broker it: %s", body)
	}
}

// A model nobody at all is serving gets the plain answer, and it is careful to
// claim only what this node can know.
func TestAModelNobodyServesGetsAnHonestlyScopedRefusal(t *testing.T) {
	mux := directoryHandler(t, hostsNothing(), theMarketSells())

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, ChatCompletionsPath,
		strings.NewReader(`{"model":"no-such-model","messages":[{"role":"user","content":"hi"}]}`)))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "no seller this node has heard of") {
		t.Errorf("the refusal overclaims what this node can know: %s", body)
	}
}

// Model names are case-insensitive across the vendors we proxy, so the redirect
// has to match the way the order book matches.
func TestTheRedirectMatchesAModelNameCaseInsensitively(t *testing.T) {
	mux := directoryHandler(t, hostsNothing(), theMarketSells())

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, ChatCompletionsPath,
		strings.NewReader(`{"model":"Qwen3-32B","messages":[{"role":"user","content":"hi"}]}`)))

	if !strings.Contains(rec.Body.String(), "https://cheaper.example.com") {
		t.Errorf("a differently-cased model name was not matched to its seller: %s", rec.Body.String())
	}
}

// A node with no exchange has no directory to consult, and its local-only answer
// is then the whole truth it has. It must not crash for want of one.
func TestANodeWithNoDirectoryStillAnswers(t *testing.T) {
	mux := directoryHandler(t, hostsNothing(), nil)

	if got := listModels(t, mux); len(got) != 0 {
		t.Errorf("a node with nothing to sell and nothing to report listed %d models", len(got))
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, ChatCompletionsPath,
		strings.NewReader(`{"model":"qwen3-32b","messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no seller this node has heard of") {
		t.Errorf("unexpected refusal: %s", rec.Body.String())
	}
}
