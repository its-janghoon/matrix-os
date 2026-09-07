package openaiapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/inference"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/openaiapi"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// directSettler applies the transfer straight to the market ledger instead of
// going through consensus, so this test exercises the whole route - auth,
// routing, the real inference Service, a real backend, real pricing - without
// standing up an engine.
type directSettler struct{ ledger *market.Ledger }

func (d directSettler) SubmitAccountTransfer(from *token.Account, recipient string, amount, nonce uint64) (*token.Transaction, error) {
	if err := d.ledger.Transfer(from.AccountID(), recipient, amount); err != nil {
		return nil, err
	}
	return &token.Transaction{To: recipient, Amount: amount, Nonce: nonce}, nil
}

func (d directSettler) WaitForSettlement(context.Context, *token.Transaction) (bool, bool, error) {
	return true, true, nil
}

type memAccounts map[string]*token.Account

func (m memAccounts) Account(id string) (*token.Account, bool) {
	a, ok := m[id]
	return a, ok
}

type keyAuth struct{ account string }

func (k keyAuth) AccountFor(r *http.Request) (string, error) {
	if r.Header.Get("Authorization") != "Bearer mx-test" {
		return "", http.ErrNoCookie
	}
	return k.account, nil
}

// TestARealRequestIsRoutedRunAndPaidFor is the end-to-end claim: an
// OpenAI-shaped POST reaches a provider by model, runs on its backend, and moves
// native MATRIX from the buyer's on-chain balance to the provider's, all with
// nothing in the request but a model and messages.
func TestARealRequestIsRoutedRunAndPaidFor(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mkt, err := market.NewMarket(store)
	if err != nil {
		t.Fatalf("market.NewMarket: %v", err)
	}

	buyer, _ := token.GenerateAccount()
	buyerID := buyer.AccountID()
	if err := mkt.Ledger().Credit(buyerID, 1_000_000); err != nil {
		t.Fatalf("Credit: %v", err)
	}

	// Two providers serving the same model at different prices, so the route has
	// a real choice to make.
	registry := inference.NewRegistry()
	for _, p := range []market.Provider{
		{ID: "dear", Capacity: 100_000, PricePerUnit: 9, Models: []string{"llama-3.3-70b"}},
		{ID: "cheap", Capacity: 100_000, PricePerUnit: 2, Models: []string{"llama-3.3-70b"}},
	} {
		if err := mkt.RegisterProvider(p); err != nil {
			t.Fatalf("RegisterProvider %s: %v", p.ID, err)
		}
		if err := registry.Register(p.ID, inference.NewEchoBackend()); err != nil {
			t.Fatalf("registry.Register %s: %v", p.ID, err)
		}
	}

	svc, err := inference.NewService(inference.Config{
		Market:   mkt,
		Registry: registry,
		Settler:  directSettler{ledger: mkt.Ledger()},
		Accounts: memAccounts{buyerID: buyer},
	})
	if err != nil {
		t.Fatalf("inference.NewService: %v", err)
	}

	handler, err := openaiapi.NewHandler(openaiapi.Config{
		Inference: svc,
		Router:    mkt,
		Auth:      keyAuth{account: buyerID},
	})
	if err != nil {
		t.Fatalf("openaiapi.NewHandler: %v", err)
	}
	mux := http.NewServeMux()
	for path, h := range handler.Routes() {
		mux.Handle(path, h)
	}

	before, err := mkt.Ledger().Balance(buyerID)
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, openaiapi.ChatCompletionsPath, strings.NewReader(
		`{"model":"llama-3.3-70b","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Authorization", "Bearer mx-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Object   string `json:"object"`
		Model    string `json:"model"`
		Provider string `json:"provider"`
		Choices  []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not a chat completion: %v", err)
	}
	if got.Object != "chat.completion" {
		t.Errorf("object = %q, want chat.completion", got.Object)
	}
	if got.Provider != "cheap" {
		t.Errorf("provider = %q, want cheap: the model routes to the cheapest", got.Provider)
	}
	if len(got.Choices) != 1 || got.Choices[0].Message.Content == "" {
		t.Fatalf("choices = %+v, want one non-empty completion", got.Choices)
	}
	if got.Usage.TotalTokens == 0 {
		t.Error("usage.total_tokens = 0, want the backend's reported count")
	}

	// The money actually moved, and it moved to the provider the route chose.
	after, err := mkt.Ledger().Balance(buyerID)
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if after >= before {
		t.Fatalf("buyer balance %d -> %d, want it to fall: nothing was charged", before, after)
	}
	paid, err := mkt.Ledger().Balance("cheap")
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if paid != before-after {
		t.Fatalf("provider was paid %d but the buyer lost %d", paid, before-after)
	}

	// The reservation is released, not left held: a route that leaked capacity
	// would silently take a provider off the market after enough requests.
	prov, _ := mkt.GetProvider("cheap")
	if prov.Available != prov.Capacity {
		t.Fatalf("Available = %d of %d after a completed job, want the reservation released",
			prov.Available, prov.Capacity)
	}
}

func TestARequestWithNoCredentialPaysNothing(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mkt, err := market.NewMarket(store)
	if err != nil {
		t.Fatalf("market.NewMarket: %v", err)
	}
	buyer, _ := token.GenerateAccount()
	if err := mkt.Ledger().Credit(buyer.AccountID(), 1_000_000); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if err := mkt.RegisterProvider(market.Provider{
		ID: "p", Capacity: 100, PricePerUnit: 1, Models: []string{"m"},
	}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	registry := inference.NewRegistry()
	if err := registry.Register("p", inference.NewEchoBackend()); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}
	svc, err := inference.NewService(inference.Config{
		Market:   mkt,
		Registry: registry,
		Settler:  directSettler{ledger: mkt.Ledger()},
		Accounts: memAccounts{buyer.AccountID(): buyer},
	})
	if err != nil {
		t.Fatalf("inference.NewService: %v", err)
	}
	handler, err := openaiapi.NewHandler(openaiapi.Config{
		Inference: svc, Router: mkt, Auth: keyAuth{account: buyer.AccountID()},
	})
	if err != nil {
		t.Fatalf("openaiapi.NewHandler: %v", err)
	}
	mux := http.NewServeMux()
	for path, h := range handler.Routes() {
		mux.Handle(path, h)
	}

	req := httptest.NewRequest(http.MethodPost, openaiapi.ChatCompletionsPath, strings.NewReader(
		`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	// No Authorization header.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	balance, err := mkt.Ledger().Balance(buyer.AccountID())
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if balance != 1_000_000 {
		t.Fatalf("balance = %d, want it untouched at 1000000", balance)
	}
	prov, _ := mkt.GetProvider("p")
	if prov.Available != prov.Capacity {
		t.Fatalf("Available = %d, want no capacity reserved by a refused request", prov.Available)
	}
}
