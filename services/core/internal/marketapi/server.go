// Package marketapi implements the external gRPC surface of the Matrix OS
// compute marketplace (matrix.market.v1.MarketService). It is the "external
// API" layer that was previously deferred: it exposes the local order book, the
// remote P2P providers discovered by the marketexchange, the compute-credit
// ledger, the signed-settlement path, and the hash-chained token log to
// external buyers and providers over gRPC.
//
// The service is backed by the real subsystems the node already runs:
//   - *market.Market for the local order book (providers and jobs) and ledger;
//   - *token.Chain plus token.SettledLedger for signed settlement and chain
//     reads;
//   - *marketexchange.Exchange (optional) for remote provider discovery.
//
// It follows the internal/admin gRPC pattern: a Server owns a grpc.Server,
// registers the market service and a health service, and exposes
// Start(ctx)/Stop(ctx). Authentication is optional and mirrors admin's model:
// when an *admin.Authenticator is supplied the server installs the same
// require-auth interceptors, and health checks remain unauthenticated.
package marketapi

import (
	"context"
	"errors"
	"fmt"
	"net"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ecirlabs/matrix-core/internal/admin"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/marketexchange"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// Service implements marketv1.MarketServiceServer over the node's marketplace
// subsystems. The generated stubs are built with require_unimplemented_servers
// disabled, so no embedding is required; every RPC is implemented explicitly.
type Service struct {
	market   *market.Market
	settled  *token.SettledLedger
	chain    *token.Chain
	exchange *marketexchange.Exchange
}

// NewService constructs a Service. market, settled and chain are required;
// exchange may be nil, in which case ListProviders simply omits remote
// providers (a node running without the P2P exchange still serves its local
// order book).
func NewService(m *market.Market, settled *token.SettledLedger, chain *token.Chain, exchange *marketexchange.Exchange) (*Service, error) {
	if m == nil {
		return nil, fmt.Errorf("marketapi: market is required")
	}
	if settled == nil {
		return nil, fmt.Errorf("marketapi: settled ledger is required")
	}
	if chain == nil {
		return nil, fmt.Errorf("marketapi: token chain is required")
	}
	return &Service{market: m, settled: settled, chain: chain, exchange: exchange}, nil
}

// Server hosts the market gRPC service on its own listener, following the
// internal/admin.Server construction pattern (grpc.NewServer, health service,
// Start/Stop). It is a parallel server to the admin server so the marketplace
// API can bind its own address and, when desired, its own auth policy without
// entangling the two surfaces.
type Server struct {
	grpcServer *grpc.Server
	healthSvc  *health.Server
	addr       string
	lis        net.Listener
	svc        *Service
}

// Config configures the market gRPC server.
type Config struct {
	// Addr is the TCP listen address (e.g. "0.0.0.0:9091").
	Addr string
	// Auth, when non-nil, enables authentication using the same API-key model as
	// the admin server: every non-health RPC requires a valid key. When nil the
	// server serves unauthenticated (guarding on nil like the admin services).
	Auth *admin.Authenticator
	// Market, Settled and Chain back the service (required). Exchange is optional.
	Market   *market.Market
	Settled  *token.SettledLedger
	Chain    *token.Chain
	Exchange *marketexchange.Exchange
}

// NewServer builds a market gRPC server. It installs the admin auth
// interceptors when cfg.Auth is non-nil, registers the health service, and
// registers the MarketService implementation.
func NewServer(cfg Config) (*Server, error) {
	svc, err := NewService(cfg.Market, cfg.Settled, cfg.Chain, cfg.Exchange)
	if err != nil {
		return nil, err
	}

	var opts []grpc.ServerOption
	if cfg.Auth != nil {
		opts = append(opts,
			grpc.UnaryInterceptor(requireAuthUnaryInterceptor(cfg.Auth)),
			grpc.StreamInterceptor(requireAuthStreamInterceptor(cfg.Auth)),
		)
	}

	grpcServer := grpc.NewServer(opts...)
	healthSvc := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthSvc)
	marketv1.RegisterMarketServiceServer(grpcServer, svc)

	return &Server{
		grpcServer: grpcServer,
		healthSvc:  healthSvc,
		addr:       cfg.Addr,
		svc:        svc,
	}, nil
}

// Start listens on the configured address and serves in a background goroutine,
// mirroring admin.Server.Start.
func (s *Server) Start(ctx context.Context) error {
	s.healthSvc.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("marketapi: failed to listen on %s: %w", s.addr, err)
	}
	s.lis = lis

	go func() {
		if err := s.grpcServer.Serve(lis); err != nil {
			fmt.Printf("market gRPC server error: %v\n", err)
		}
	}()

	return nil
}

// Stop gracefully stops the gRPC server, mirroring admin.Server.Stop.
func (s *Server) Stop(ctx context.Context) error {
	s.healthSvc.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	s.grpcServer.GracefulStop()
	return nil
}

// Service exposes the market service implementation, which is useful for
// in-process tests and for callers that want direct access.
func (s *Server) Service() *Service { return s.svc }

// Addr returns the actual network address the server is listening on after
// Start has been called. When the configured Addr used a :0 port this reflects
// the OS-assigned port, which is what in-process tests dial. It returns an empty
// string before Start.
func (s *Server) Addr() string {
	if s.lis == nil {
		return ""
	}
	return s.lis.Addr().String()
}

// requireAuthUnaryInterceptor requires authentication for every unary RPC other
// than the health check, reusing the admin authenticator.
func requireAuthUnaryInterceptor(auth *admin.Authenticator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if info.FullMethod == "/grpc.health.v1.Health/Check" {
			return handler(ctx, req)
		}
		if _, err := auth.Authenticate(ctx); err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "authentication required")
		}
		return handler(ctx, req)
	}
}

// requireAuthStreamInterceptor requires authentication for every streaming RPC
// other than the health watch, reusing the admin authenticator.
func requireAuthStreamInterceptor(auth *admin.Authenticator) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if info.FullMethod == "/grpc.health.v1.Health/Watch" {
			return handler(srv, ss)
		}
		if _, err := auth.Authenticate(ss.Context()); err != nil {
			return status.Errorf(codes.Unauthenticated, "authentication required")
		}
		return handler(srv, ss)
	}
}

// mapMarketError translates market/token sentinel errors to gRPC status codes.
// Unknown errors map to codes.Internal so unexpected failures are not masked as
// benign. Nil returns nil.
func mapMarketError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, market.ErrProviderNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, market.ErrJobNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, market.ErrInsufficientFunds):
		// Covers token.ErrUnaffordable, which wraps market.ErrInsufficientFunds.
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, market.ErrInsufficientCapacity):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, market.ErrInvalidJobState):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, market.ErrInvalidProvider):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, market.ErrSelfDealing):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, token.ErrNonceMismatch):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, token.ErrPrevHashMismatch):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, token.ErrHeightOutOfRange):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, token.ErrInvalidSignature),
		errors.Is(err, token.ErrUnsignedTransaction),
		errors.Is(err, token.ErrInvalidTransaction),
		errors.Is(err, token.ErrInvalidPublicKey):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

// toProtoTimestamp converts an int64 unix-nanosecond timestamp to a protobuf
// Timestamp. A zero value yields nil, matching "unset".
func nanosToTimestamp(ns int64) *timestamppb.Timestamp {
	if ns == 0 {
		return nil
	}
	return timestamppb.New(timeUnixNano(ns))
}
