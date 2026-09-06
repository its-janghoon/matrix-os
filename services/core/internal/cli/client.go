package cli

import (
	"context"
	"fmt"
	"time"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// dialOptions builds the gRPC dial options for connecting to a node's market
// server. The local market API is served over an insecure transport (matching
// internal/marketapi/integration_test.go which dials with
// insecure.NewCredentials), so the CLI uses the same credentials. When apiKey
// is non-empty it is attached to every RPC as the "authorization" metadata key,
// which is exactly what admin.Authenticator reads server-side.
func dialOptions(apiKey string) []grpc.DialOption {
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	if apiKey != "" {
		opts = append(opts, grpc.WithUnaryInterceptor(authUnaryInterceptor(apiKey)))
	}
	return opts
}

// authUnaryInterceptor injects the API key as the "authorization" metadata
// header on every unary RPC, matching the key admin.Authenticator extracts via
// metadata.Get("authorization"). A bare token is sent (the authenticator also
// accepts a "Bearer " prefix, but the market server expects the raw key).
func authUnaryInterceptor(apiKey string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", apiKey)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// clientConn is the set of gRPC clients the CLI commands use against a single
// node endpoint, plus the underlying connection so callers can Close it.
type clientConn struct {
	conn   *grpc.ClientConn
	market marketv1.MarketServiceClient
	health healthpb.HealthClient
}

// Close closes the underlying gRPC connection.
func (c *clientConn) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// dial establishes a gRPC connection to addr using the options implied by opts
// (endpoint, API key). It does not block on connectivity; the first RPC surfaces
// any connection error (mapped to a readable message by mapErr).
func dial(opts *globalOptions) (*clientConn, error) {
	conn, err := grpc.NewClient(opts.Addr, dialOptions(opts.APIKey)...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", opts.Addr, err)
	}
	return &clientConn{
		conn:   conn,
		market: marketv1.NewMarketServiceClient(conn),
		health: healthpb.NewHealthClient(conn),
	}, nil
}

// callContext derives a context with the configured timeout from the parent.
func callContext(ctx context.Context, opts *globalOptions) (context.Context, context.CancelFunc) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// mapErr turns a gRPC error into a concise, human-readable message. It unwraps
// the gRPC status so users see the message text (and code name) rather than the
// verbose "rpc error: code = ..." wrapper, and gives connection failures a clear
// hint that the node may not be running.
func mapErr(addr string, err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	code := st.Code()
	msg := st.Message()
	switch code.String() {
	case "Unavailable":
		return fmt.Errorf("cannot reach node at %s: %s (is the node running and is --addr correct?)", addr, msg)
	case "Unauthenticated":
		return fmt.Errorf("authentication failed: %s (set --api-key if the node requires ACLs)", msg)
	default:
		return fmt.Errorf("%s: %s", code.String(), msg)
	}
}

// defaultTimeout is the fallback per-RPC timeout when --timeout is unset.
const defaultTimeout = 10 * time.Second
