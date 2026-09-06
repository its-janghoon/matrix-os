package connectapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/connectapi"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/marketapi"
	"github.com/ecirlabs/matrix-core/internal/token"
	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
)

// TestBrowserCanDriveTheRealMarketService runs the REAL market service behind
// the endpoint and completes a whole job over HTTP: register a provider, fund a
// buyer, submit a job, complete it, and read both balances back.
//
// It is deliberately end to end. The handler dispatches through the generated
// descriptors, so a unit test with a fake implementation proves the routing but
// not that the actual services decode, execute and encode correctly through
// this path - which is the thing that was missing when the console shipped a
// client for a protocol the daemon did not serve.
func TestBrowserCanDriveTheRealMarketService(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mkt, err := market.NewMarket(store)
	if err != nil {
		t.Fatalf("market.NewMarket: %v", err)
	}
	chain := token.NewChain(store)
	settled := token.NewSettledLedger(mkt.Ledger(), chain)

	svc, err := marketapi.NewService(mkt, settled, chain, nil, nil)
	if err != nil {
		t.Fatalf("marketapi.NewService: %v", err)
	}

	handler, err := connectapi.NewHandler(connectapi.Config{
		Bindings:       []connectapi.Binding{{Desc: &marketv1.MarketService_ServiceDesc, Impl: svc}},
		AllowedOrigins: []string{"*"},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	call := func(method, body string) map[string]any {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost,
			srv.URL+"/matrix.market.v1.MarketService/"+method, strings.NewReader(body))
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Connect-Protocol-Version", "1")
		// An Origin, as a browser would send.
		req.Header.Set("Origin", "http://localhost:5173")

		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d body %s", method, resp.StatusCode, raw)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
			t.Fatalf("%s: allow-origin %q, so a browser would discard this response", method, got)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s: decode %s: %v", method, raw, err)
		}
		return out
	}

	// Fund the buyer directly on the ledger: FundAccount is admin-gated and this
	// test is about the transport, not the treasury.
	if err := mkt.Ledger().Credit("buyer", 1_000); err != nil {
		t.Fatalf("credit: %v", err)
	}

	provider := call("RegisterProvider", `{"id":"gpu-1","capacity":"10","price_per_unit":"7"}`)
	if provider["provider"] == nil {
		t.Fatalf("RegisterProvider returned no provider: %v", provider)
	}

	listed := call("ListProviders", `{}`)
	providers, _ := listed["providers"].([]any)
	if len(providers) != 1 {
		t.Fatalf("ListProviders returned %d providers, want 1: %v", len(providers), listed)
	}

	submitted := call("SubmitJob", `{"buyer":"buyer","provider":"gpu-1","units":"3"}`)
	job, _ := submitted["job"].(map[string]any)
	jobID, _ := job["id"].(string)
	if jobID == "" {
		t.Fatalf("SubmitJob returned no job id: %v", submitted)
	}

	completed := call("CompleteJob", `{"id":`+quote(jobID)+`}`)
	doneJob, _ := completed["job"].(map[string]any)
	if status, _ := doneJob["status"].(string); !strings.Contains(status, "COMPLETED") {
		t.Fatalf("job status = %q, want completed: %v", status, completed)
	}

	// 3 units at 7 each: the buyer paid 21, the provider earned it.
	if got := balance(t, call, "buyer"); got != "979" {
		t.Fatalf("buyer balance = %q, want 979 (1000 - 3*7)", got)
	}
	if got := balance(t, call, "gpu-1"); got != "21" {
		t.Fatalf("provider balance = %q, want 21", got)
	}
}

func balance(t *testing.T, call func(string, string) map[string]any, account string) string {
	t.Helper()
	out := call("GetBalance", `{"account":`+quote(account)+`}`)
	// protojson encodes 64-bit fields as strings, which is what a JS client sees.
	s, _ := out["balance"].(string)
	return s
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
