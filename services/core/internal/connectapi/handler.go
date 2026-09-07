// Package connectapi exposes the node's gRPC services over plain HTTP so a
// browser can call them.
//
// The market and inference APIs are gRPC. Raw gRPC rides on HTTP/2 trailers,
// which browsers cannot produce: a web app, a dApp front end, or the Matrix
// Console running in a WebView cannot speak to matrixd at all, however open the
// port is. That is why the console shipped with a client that spoke the Connect
// protocol against a daemon that did not serve it - the live path could never
// have worked, only the in-memory demo backend.
//
// This package closes that gap with the Connect protocol's unary JSON form
// (https://connectrpc.com/docs/protocol): an ordinary HTTP POST to
// `/{package}.{Service}/{Method}` whose body is the request message as JSON and
// whose response body is the response message as JSON. It is deliberately built
// on the generated grpc.ServiceDesc rather than on hand-written per-method
// code, so every method of every bound service is served, and a method added to
// a proto file is served the moment it is registered - there is no second list
// to forget to update.
//
// What it is not: streaming. Every RPC in this tree is unary today; a streaming
// method would need the Connect streaming framing, and this handler rejects one
// rather than pretending.
package connectapi

//go:generate sh -c "go run ../../cmd/rpcmanifest > ../../../../packages/sdk/src/rpc-manifest.json"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// maxRequestBytes bounds a request body. The messages here are small (an
// account id, a prompt, a signature); a cap keeps a hostile caller from making
// the node allocate.
const maxRequestBytes = 4 << 20

// Binding pairs a generated service descriptor with the implementation that
// serves it.
type Binding struct {
	// Desc is the generated descriptor, e.g. marketv1.MarketService_ServiceDesc.
	Desc *grpc.ServiceDesc
	// Impl is the server implementation registered for that service.
	Impl any
}

// Authenticator is the subset of admin.Authenticator this handler needs. It is
// an interface so the package does not depend on the admin package, and so a
// test can supply its own policy.
type Authenticator interface {
	// Authenticate reports whether the request context carries a valid key. The
	// role it returns is not used here: authorisation per method is the gRPC
	// services' own business, and this surface applies the same
	// authentication-required rule the market and inference servers apply.
	Authenticate(ctx context.Context) (role string, err error)
}

// Config configures the handler.
type Config struct {
	// Bindings are the services to serve. At least one is required.
	Bindings []Binding
	// Auth, when non-nil, requires a valid API key on every call, exactly as the
	// gRPC servers do when ACLs are enabled. The key travels in the
	// Authorization header and is passed through to the authenticator as gRPC
	// incoming metadata, so one policy covers both surfaces.
	Auth Authenticator
	// ExtraRoutes are additional exact paths served on the same mux, so a surface
	// that is not a gRPC service - the OpenAI-compatible /v1/chat/completions -
	// shares this endpoint's listener and CORS policy instead of needing a port
	// and an origin list of its own. Each handler applies its own
	// authentication, because the policy that fits a protocol-shaped route is
	// not always the blanket "a valid key on every method" this handler applies.
	ExtraRoutes map[string]http.Handler
	// AllowedOrigins lists the browser origins allowed to call this endpoint.
	// A single "*" allows any origin, which is the right default for a local
	// daemon a user's own page talks to and the wrong one for a public
	// deployment; an operator narrows it in config.
	AllowedOrigins []string
}

var (
	marshaler   = protojson.MarshalOptions{EmitUnpopulated: true}
	unmarshaler = protojson.UnmarshalOptions{DiscardUnknown: true}
)

// NewHandler builds the HTTP handler. It returns an error when no bindings are
// given or a descriptor declares a streaming method, which this protocol form
// cannot serve.
func NewHandler(cfg Config) (http.Handler, error) {
	if len(cfg.Bindings) == 0 {
		return nil, errors.New("connectapi: at least one binding is required")
	}

	mux := http.NewServeMux()
	for _, b := range cfg.Bindings {
		if b.Desc == nil || b.Impl == nil {
			return nil, errors.New("connectapi: binding needs both a descriptor and an implementation")
		}
		if len(b.Desc.Streams) > 0 {
			return nil, fmt.Errorf("connectapi: service %s declares streaming methods, which this handler cannot serve",
				b.Desc.ServiceName)
		}
		for i := range b.Desc.Methods {
			method := b.Desc.Methods[i]
			impl := b.Impl
			path := "/" + b.Desc.ServiceName + "/" + method.MethodName
			fullMethod := "/" + b.Desc.ServiceName + "/" + method.MethodName
			mux.Handle(path, &rpcHandler{
				method:     method,
				impl:       impl,
				fullMethod: fullMethod,
				auth:       cfg.Auth,
			})
		}
	}

	for path, h := range cfg.ExtraRoutes {
		if path == "" || h == nil {
			return nil, errors.New("connectapi: an extra route needs both a path and a handler")
		}
		if !strings.HasPrefix(path, "/") {
			return nil, fmt.Errorf("connectapi: extra route %q must start with /", path)
		}
		mux.Handle(path, h)
	}

	// A liveness probe that needs no protocol knowledge, so an operator (or a
	// load balancer) can check the endpoint with curl.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})

	return withCORS(cfg.AllowedOrigins, mux), nil
}

// rpcHandler serves one method. It holds the generated grpc.MethodDesc rather
// than just its Handler func: the handler's type is unexported by grpc, so the
// descriptor is the only way to carry it around.
type rpcHandler struct {
	method     grpc.MethodDesc
	impl       any
	fullMethod string
	auth       Authenticator
}

func (h *rpcHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, status.Error(codes.Unimplemented, "connect unary calls are POST"))
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
		writeError(w, status.Errorf(codes.InvalidArgument,
			"unsupported content type %q: this endpoint serves the Connect JSON protocol", ct))
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		writeError(w, status.Error(codes.InvalidArgument, "could not read request body"))
		return
	}
	// An empty body is a message with every field defaulted, which is what a
	// no-argument RPC looks like from a browser.
	if len(body) == 0 {
		body = []byte("{}")
	}

	// Carry the HTTP headers into the context as gRPC incoming metadata, so the
	// same authenticator the gRPC servers use sees the same credentials.
	ctx := metadata.NewIncomingContext(r.Context(), metadataFromHeader(r.Header))
	if h.auth != nil {
		if _, err := h.auth.Authenticate(ctx); err != nil {
			writeError(w, status.Error(codes.Unauthenticated, "authentication required"))
			return
		}
	}

	resp, err := h.method.Handler(h.impl, ctx, func(target any) error {
		msg, ok := target.(proto.Message)
		if !ok {
			return status.Error(codes.Internal, "request type is not a protobuf message")
		}
		if err := unmarshaler.Unmarshal(body, msg); err != nil {
			return status.Errorf(codes.InvalidArgument, "malformed request json: %v", err)
		}
		return nil
	}, nil)
	if err != nil {
		writeError(w, err)
		return
	}

	msg, ok := resp.(proto.Message)
	if !ok {
		writeError(w, status.Error(codes.Internal, "response type is not a protobuf message"))
		return
	}
	out, err := marshaler.Marshal(msg)
	if err != nil {
		writeError(w, status.Errorf(codes.Internal, "could not encode response: %v", err))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// metadataFromHeader converts HTTP headers to gRPC metadata, lowercasing keys
// as gRPC does.
func metadataFromHeader(h http.Header) metadata.MD {
	md := metadata.MD{}
	for key, values := range h {
		md[strings.ToLower(key)] = append([]string(nil), values...)
	}
	return md
}

// connectError is the Connect protocol's error body.
type connectError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeError maps a gRPC status to the Connect code name and HTTP status the
// protocol prescribes, so a client can tell "you are not authenticated" from
// "that provider does not exist" without parsing prose.
func writeError(w http.ResponseWriter, err error) {
	st, _ := status.FromError(err)
	name, httpStatus := connectCode(st.Code())
	body, mErr := json.Marshal(connectError{Code: name, Message: st.Message()})
	if mErr != nil {
		body = []byte(`{"code":"internal","message":"error encoding error"}`)
		httpStatus = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpStatus)
	_, _ = w.Write(body)
}

// connectCode returns the Connect error code name and the HTTP status for a
// gRPC code, per the Connect protocol's mapping table.
func connectCode(c codes.Code) (string, int) {
	switch c {
	case codes.OK:
		return "", http.StatusOK
	case codes.Canceled:
		return "canceled", 499
	case codes.InvalidArgument:
		return "invalid_argument", http.StatusBadRequest
	case codes.DeadlineExceeded:
		return "deadline_exceeded", http.StatusGatewayTimeout
	case codes.NotFound:
		return "not_found", http.StatusNotFound
	case codes.AlreadyExists:
		return "already_exists", http.StatusConflict
	case codes.PermissionDenied:
		return "permission_denied", http.StatusForbidden
	case codes.ResourceExhausted:
		return "resource_exhausted", http.StatusTooManyRequests
	case codes.FailedPrecondition:
		return "failed_precondition", http.StatusBadRequest
	case codes.Aborted:
		return "aborted", http.StatusConflict
	case codes.OutOfRange:
		return "out_of_range", http.StatusBadRequest
	case codes.Unimplemented:
		return "unimplemented", http.StatusNotImplemented
	case codes.Unavailable:
		return "unavailable", http.StatusServiceUnavailable
	case codes.DataLoss:
		return "data_loss", http.StatusInternalServerError
	case codes.Unauthenticated:
		return "unauthenticated", http.StatusUnauthorized
	default:
		return "internal", http.StatusInternalServerError
	}
}

// withCORS answers preflights and echoes the allowed origin, without which a
// browser refuses to show the response even though the call succeeded.
func withCORS(allowed []string, next http.Handler) http.Handler {
	allowAny := false
	set := make(map[string]struct{}, len(allowed))
	for _, origin := range allowed {
		if origin == "*" {
			allowAny = true
		}
		set[origin] = struct{}{}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			_, ok := set[origin]
			if allowAny || ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Headers",
					"Content-Type, Authorization, Connect-Protocol-Version, Connect-Timeout-Ms")
				w.Header().Set("Access-Control-Expose-Headers", "Content-Type")
				w.Header().Set("Access-Control-Max-Age", "3600")
			}
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
