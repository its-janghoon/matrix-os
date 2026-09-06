package connectapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
)

// fakeMarket implements just enough of MarketService to exercise the handler:
// one method that succeeds, one that fails with a specific code.
type fakeMarket struct {
	marketv1.UnimplementedMarketServiceServer
	lastBalanceAccount string
	sawAuthHeader      string
}

func (f *fakeMarket) GetBalance(ctx context.Context, req *marketv1.GetBalanceRequest) (*marketv1.GetBalanceResponse, error) {
	f.lastBalanceAccount = req.GetAccount()
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get("authorization"); len(v) > 0 {
			f.sawAuthHeader = v[0]
		}
	}
	if req.GetAccount() == "missing" {
		return nil, status.Error(codes.NotFound, "no such account")
	}
	return &marketv1.GetBalanceResponse{Account: req.GetAccount(), Balance: 42}, nil
}

func newTestHandler(t *testing.T, auth Authenticator) (http.Handler, *fakeMarket) {
	t.Helper()
	impl := &fakeMarket{}
	h, err := NewHandler(Config{
		Bindings:       []Binding{{Desc: &marketv1.MarketService_ServiceDesc, Impl: impl}},
		Auth:           auth,
		AllowedOrigins: []string{"*"},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h, impl
}

func post(t *testing.T, h http.Handler, path, body string, headers map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestUnaryCallRoundTrip(t *testing.T) {
	h, impl := newTestHandler(t, nil)

	// The path and body shape are the Connect protocol's, which is what the
	// console's client already speaks: POST /{package}.{Service}/{Method} with
	// the request message as JSON.
	resp := post(t, h, "/matrix.market.v1.MarketService/GetBalance", `{"account":"alice"}`, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, body)
	}
	if impl.lastBalanceAccount != "alice" {
		t.Fatalf("service saw account %q, want alice", impl.lastBalanceAccount)
	}
	var decoded struct {
		Account string `json:"account"`
		Balance any    `json:"balance"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Account != "alice" {
		t.Fatalf("response account = %q, want alice", decoded.Account)
	}
	// 64-bit fields come back as JSON strings, which is protojson's contract and
	// what a client must be ready for.
	if got, want := decoded.Balance, "42"; got != want {
		t.Fatalf("amount = %#v, want %q (64-bit ints are strings in proto JSON)", got, want)
	}
}

func TestEveryMethodOfTheServiceIsRouted(t *testing.T) {
	h, _ := newTestHandler(t, nil)

	// The point of building on the generated descriptor is that nothing has to
	// be listed twice. Every method on MarketService must answer - with an
	// Unimplemented from the embedded base, but never with a 404.
	for _, m := range marketv1.MarketService_ServiceDesc.Methods {
		resp := post(t, h, "/matrix.market.v1.MarketService/"+m.MethodName, `{}`, nil)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			t.Fatalf("method %s is not routed (404): %s", m.MethodName, body)
		}
	}
}

func TestUnknownMethodIs404(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	resp := post(t, h, "/matrix.market.v1.MarketService/NoSuchMethod", `{}`, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestErrorCodesAreMapped(t *testing.T) {
	h, _ := newTestHandler(t, nil)

	cases := []struct {
		name       string
		path       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "service error keeps its code",
			path:       "/matrix.market.v1.MarketService/GetBalance",
			body:       `{"account":"missing"}`,
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "malformed json is invalid_argument",
			path:       "/matrix.market.v1.MarketService/GetBalance",
			body:       `{"account":`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_argument",
		},
		{
			name:       "unimplemented method reports 501",
			path:       "/matrix.market.v1.MarketService/RegisterProvider",
			body:       `{}`,
			wantStatus: http.StatusNotImplemented,
			wantCode:   "unimplemented",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := post(t, h, tc.path, tc.body, nil)
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			var e connectError
			if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if e.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", e.Code, tc.wantCode)
			}
			if e.Message == "" {
				t.Fatal("error body has no message")
			}
		})
	}
}

// denyAll rejects everything, standing in for an authenticator with no matching
// key.
type denyAll struct{}

func (denyAll) Authenticate(context.Context) (string, error) { return "", errors.New("nope") }

// requireKey accepts one key, and only through the Authorization header, which
// is how the admin authenticator reads it.
type requireKey struct{ key string }

func (r requireKey) Authenticate(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", errors.New("no metadata")
	}
	for _, v := range md.Get("authorization") {
		if v == "Bearer "+r.key || v == r.key {
			return "admin", nil
		}
	}
	return "", errors.New("bad key")
}

func TestAuthenticationIsEnforcedWhenConfigured(t *testing.T) {
	h, _ := newTestHandler(t, denyAll{})
	resp := post(t, h, "/matrix.market.v1.MarketService/GetBalance", `{"account":"alice"}`, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	var e connectError
	_ = json.NewDecoder(resp.Body).Decode(&e)
	if e.Code != "unauthenticated" {
		t.Fatalf("code = %q, want unauthenticated", e.Code)
	}
}

func TestHTTPHeadersReachTheServiceAsMetadata(t *testing.T) {
	h, impl := newTestHandler(t, requireKey{key: "s3cret"})

	resp := post(t, h, "/matrix.market.v1.MarketService/GetBalance", `{"account":"alice"}`,
		map[string]string{"Authorization": "Bearer s3cret"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	// The same credential the endpoint checked must be visible to the service, so
	// one auth policy can cover both this surface and gRPC.
	if impl.sawAuthHeader != "Bearer s3cret" {
		t.Fatalf("service saw authorization %q, want the header value", impl.sawAuthHeader)
	}
}

func TestPreflightIsAnswered(t *testing.T) {
	h, _ := newTestHandler(t, nil)

	req := httptest.NewRequest(http.MethodOptions, "/matrix.market.v1.MarketService/GetBalance", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Fatalf("allow-origin = %q, want the requesting origin", got)
	}
	if !strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "Connect-Protocol-Version") {
		t.Fatal("preflight does not allow the Connect protocol header, so a browser call would be blocked")
	}
}

func TestOriginNotOnTheListGetsNoCORSHeaders(t *testing.T) {
	impl := &fakeMarket{}
	h, err := NewHandler(Config{
		Bindings:       []Binding{{Desc: &marketv1.MarketService_ServiceDesc, Impl: impl}},
		AllowedOrigins: []string{"https://console.example"},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	resp := post(t, h, "/matrix.market.v1.MarketService/GetBalance", `{"account":"alice"}`,
		map[string]string{"Origin": "https://evil.example"})
	defer resp.Body.Close()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("allow-origin = %q, want empty for an origin that is not allowed", got)
	}
}

func TestGetIsRejected(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/matrix.market.v1.MarketService/GetBalance", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 for a GET", rec.Code)
	}
}

func TestHealthzNeedsNoProtocolKnowledge(t *testing.T) {
	h, _ := newTestHandler(t, denyAll{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("healthz = %d %q", rec.Code, rec.Body.String())
	}
}

func TestHandlerRefusesBadConfig(t *testing.T) {
	if _, err := NewHandler(Config{}); err == nil {
		t.Fatal("a handler with no bindings should be refused")
	}
	if _, err := NewHandler(Config{Bindings: []Binding{{Desc: &marketv1.MarketService_ServiceDesc}}}); err == nil {
		t.Fatal("a binding with no implementation should be refused")
	}
	streaming := grpc.ServiceDesc{
		ServiceName: "test.Streaming",
		Streams:     []grpc.StreamDesc{{StreamName: "Watch"}},
	}
	if _, err := NewHandler(Config{Bindings: []Binding{{Desc: &streaming, Impl: &fakeMarket{}}}}); err == nil {
		t.Fatal("a streaming service should be refused rather than half-served")
	}
}
