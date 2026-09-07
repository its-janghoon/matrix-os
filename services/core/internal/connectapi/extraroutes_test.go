package connectapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
)

// TestAnExtraRouteSharesTheEndpointAndItsCORS is why extra routes exist at all:
// the OpenAI-compatible surface is HTTP/JSON for the same node, so it should use
// one listener and one origin list rather than a port of its own.
func TestAnExtraRouteSharesTheEndpointAndItsCORS(t *testing.T) {
	h, err := NewHandler(Config{
		Bindings: []Binding{{Desc: &marketv1.MarketService_ServiceDesc, Impl: &fakeMarket{}}},
		ExtraRoutes: map[string]http.Handler{
			"/v1/chat/completions": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, `{"ok":true}`)
			}),
		},
		AllowedOrigins: []string{"https://app.test"},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", http.NoBody)
	req.Header.Set("Origin", "https://app.test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.test" {
		t.Fatalf("CORS header = %q, want the configured origin: an extra route that "+
			"did not inherit the policy would be unreachable from the browser", got)
	}
}

// TestAnExtraRouteIsNotBehindTheBlanketMethodAuth: the route authenticates
// itself, because it needs the account a key spends from rather than only its
// role. If the handler's blanket check ran first, the route could never see the
// credential it has to resolve.
func TestAnExtraRouteIsNotBehindTheBlanketMethodAuth(t *testing.T) {
	reached := false
	h, err := NewHandler(Config{
		Bindings: []Binding{{Desc: &marketv1.MarketService_ServiceDesc, Impl: &fakeMarket{}}},
		ExtraRoutes: map[string]http.Handler{
			"/v1/models": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			}),
		},
		Auth:           denyAll{},
		AllowedOrigins: []string{"*"},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", http.NoBody))

	if !reached {
		t.Fatal("the extra route was not reached; it must apply its own auth")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestExtraRoutesAreValidated(t *testing.T) {
	bindings := []Binding{{Desc: &marketv1.MarketService_ServiceDesc, Impl: &fakeMarket{}}}
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	if _, err := NewHandler(Config{
		Bindings:    bindings,
		ExtraRoutes: map[string]http.Handler{"v1/models": noop},
	}); err == nil {
		t.Error("a path with no leading slash should be rejected, not silently unrouted")
	}
	if _, err := NewHandler(Config{
		Bindings:    bindings,
		ExtraRoutes: map[string]http.Handler{"/v1/models": nil},
	}); err == nil {
		t.Error("a nil handler should be rejected rather than panicking on the first request")
	}
}
