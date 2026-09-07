// Package inferenceapi implements the external gRPC surface for LLM inference in
// the Matrix OS compute marketplace (matrix.inference.v1.InferenceService). It
// mirrors the internal/marketapi and internal/admin gRPC server pattern: a
// Server owns a grpc.Server, registers the inference service and a health
// service, and exposes Start(ctx)/Stop(ctx). Authentication is optional and, when
// an *admin.Authenticator is supplied, reuses admin's require-auth interceptors
// while leaving health checks unauthenticated.
//
// The service is backed by *inference.Service, which reserves capacity through
// the compute marketplace, fulfills jobs via a provider's pluggable inference
// Backend, and settles the computed units buyer -> provider in the token through
// the consensus-backed settlement path.
package inferenceapi

import (
	"context"
	"errors"
	"fmt"
	"net"

	inferencev1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/inference/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/ecirlabs/matrix-core/internal/admin"
	"github.com/ecirlabs/matrix-core/internal/inference"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// Service implements inferencev1.InferenceServiceServer over an
// *inference.Service. The generated stubs are built with
// require_unimplemented_servers disabled, so every RPC is implemented explicitly.
type Service struct {
	inf *inference.Service
}

// NewService constructs a Service backed by the given inference service, which
// is required.
func NewService(inf *inference.Service) (*Service, error) {
	if inf == nil {
		return nil, fmt.Errorf("inferenceapi: inference service is required")
	}
	return &Service{inf: inf}, nil
}

// Server hosts the inference gRPC service on its own listener, following the
// internal/marketapi.Server construction pattern.
type Server struct {
	grpcServer *grpc.Server
	healthSvc  *health.Server
	addr       string
	lis        net.Listener
	svc        *Service
}

// Config configures the inference gRPC server.
type Config struct {
	// Addr is the TCP listen address (e.g. "0.0.0.0:9092").
	Addr string
	// Auth, when non-nil, enables authentication using the admin API-key model.
	Auth *admin.Authenticator
	// Inference backs the service (required).
	Inference *inference.Service
}

// NewServer builds an inference gRPC server. It installs the admin auth
// interceptors when cfg.Auth is non-nil, registers the health service, and
// registers the InferenceService implementation.
func NewServer(cfg Config) (*Server, error) {
	svc, err := NewService(cfg.Inference)
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
	inferencev1.RegisterInferenceServiceServer(grpcServer, svc)

	return &Server{
		grpcServer: grpcServer,
		healthSvc:  healthSvc,
		addr:       cfg.Addr,
		svc:        svc,
	}, nil
}

// Start listens on the configured address and serves in a background goroutine.
func (s *Server) Start(ctx context.Context) error {
	s.healthSvc.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("inferenceapi: failed to listen on %s: %w", s.addr, err)
	}
	s.lis = lis

	go func() {
		if err := s.grpcServer.Serve(lis); err != nil {
			fmt.Printf("inference gRPC server error: %v\n", err)
		}
	}()

	return nil
}

// Stop gracefully stops the gRPC server.
func (s *Server) Stop(ctx context.Context) error {
	s.healthSvc.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	s.grpcServer.GracefulStop()
	return nil
}

// Service exposes the service implementation for in-process tests.
func (s *Server) Service() *Service { return s.svc }

// Addr returns the actual listen address after Start, reflecting the OS-assigned
// port when the configured Addr used :0. It returns an empty string before Start.
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

// mapInferenceError translates inference/market sentinel errors to gRPC status
// codes. Unknown errors map to codes.Internal. Nil returns nil.
func mapInferenceError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, inference.ErrJobNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, inference.ErrNoBackend), errors.Is(err, inference.ErrBackendNotFound):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, inference.ErrEmptyPrompt):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, inference.ErrStreamNotSupported):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, inference.ErrNotAwaitingPayment):
		// The job is at the wrong point in its lifecycle - not run yet, already
		// paid, or expired - which is a precondition, not a bad argument.
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, inference.ErrPaymentMismatch), errors.Is(err, inference.ErrPaymentUnsigned):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, market.ErrProviderNotFound), errors.Is(err, market.ErrJobNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, market.ErrInsufficientFunds), errors.Is(err, market.ErrInsufficientCapacity):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, market.ErrSelfDealing):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, token.ErrInvalidSignature),
		errors.Is(err, token.ErrUnsignedTransaction),
		errors.Is(err, token.ErrInvalidTransaction),
		errors.Is(err, token.ErrInvalidPublicKey):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
