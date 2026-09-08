package connectapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
)

// /healthz used to answer "ok" unconditionally, and that is not a neutral
// default: during the ledger deadlock the gRPC health service said SERVING and
// this said ok while every balance read hung. A probe that cannot report a sick
// node actively tells the supervisor not to act.

func healthzHarness(t *testing.T, healthy func() (bool, string)) *httptest.ResponseRecorder {
	t.Helper()
	h, err := NewHandler(Config{
		Bindings: []Binding{{Desc: &marketv1.MarketService_ServiceDesc, Impl: &fakeMarket{}}},
		Healthy:  healthy,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	return rec
}

func TestHealthzReportsASickNode(t *testing.T) {
	rec := healthzHarness(t, func() (bool, string) { return false, "ledger stalled: no block is being applied" })
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: a load balancer keeps a node in rotation on 200",
			rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ledger stalled") {
		t.Fatalf("body = %q, want the reason: an operator running curl gets nothing from a "+
			"bare status code", rec.Body.String())
	}
}

func TestHealthzSaysOkWhenTheNodeIsWell(t *testing.T) {
	rec := healthzHarness(t, func() (bool, string) { return true, "" })
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Fatalf("body = %q, want ok", rec.Body.String())
	}
}

// Nil means nothing is watching, which is the honest answer for an embedded or
// test handler: the process is up and that is all it can truthfully claim.
func TestHealthzWithNoWatcherStillAnswers(t *testing.T) {
	rec := healthzHarness(t, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 when no health source is configured", rec.Code)
	}
}
