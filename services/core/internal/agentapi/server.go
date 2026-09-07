// Package agentapi implements the external gRPC surface for deploying and
// running WebAssembly agents on a Matrix OS node (matrix.agent.v1.AgentService).
// It mirrors the internal/marketapi and internal/inferenceapi gRPC server
// pattern: a Server owns a grpc.Server, registers the agent service and a health
// service, and exposes Start(ctx)/Stop(ctx). Authentication is optional and,
// when an *admin.Authenticator is supplied, reuses admin's require-auth
// interceptors while leaving health checks unauthenticated.
//
// It exists because the agent runtime (internal/agent, wazero) could load and
// run a wasm module only from inside the node process; nothing outside could ask
// it to. This service lets an external client submit a module, has the node
// instantiate and run it, persists the deployment in the node's kv store so it
// survives a restart, and meters each run against the marketplace ledger by
// settling a configurable per-run charge THROUGH CONSENSUS (default 0 ==
// unmetered). The Service is a thin gRPC adapter over *Manager, which owns the
// persistence and metering.
package agentapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	agentv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/agent/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ecirlabs/matrix-core/internal/admin"
	"github.com/ecirlabs/matrix-core/internal/agent"
)

// Service implements agentv1.AgentServiceServer over a *Manager. The generated
// stubs are built with require_unimplemented_servers disabled, so every RPC is
// implemented explicitly.
type Service struct {
	mgr *Manager
}

// NewService constructs a Service backed by the given manager, which is
// required.
func NewService(mgr *Manager) (*Service, error) {
	if mgr == nil {
		return nil, fmt.Errorf("agentapi: manager is required")
	}
	return &Service{mgr: mgr}, nil
}

// DeployAgent persists a submitted module, meters the run through consensus, and
// runs the module once, returning the persisted deployment and its run outcome.
func (s *Service) DeployAgent(ctx context.Context, req *agentv1.DeployAgentRequest) (*agentv1.DeployAgentResponse, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, ErrEmptyID.Error())
	}
	if len(req.GetWasmModule()) == 0 {
		return nil, status.Error(codes.InvalidArgument, ErrEmptyModule.Error())
	}
	limits := limitsFromProto(req.GetLimits())
	res, err := s.mgr.Deploy(ctx, req.GetId(), req.GetWasmModule(), limits, req.GetDeployer())
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentv1.DeployAgentResponse{
		Agent:   deploymentToProto(res.Deployment),
		Ran:     res.Ran,
		Charged: res.Charged,
	}, nil
}

// ListAgents returns all persisted deployments, including those reloaded from
// the store after a restart.
func (s *Service) ListAgents(ctx context.Context, req *agentv1.ListAgentsRequest) (*agentv1.ListAgentsResponse, error) {
	deployments := s.mgr.List()
	out := make([]*agentv1.Agent, 0, len(deployments))
	for _, d := range deployments {
		out = append(out, deploymentToProto(d))
	}
	return &agentv1.ListAgentsResponse{Agents: out}, nil
}

// GetAgent returns a single deployment by ID.
func (s *Service) GetAgent(ctx context.Context, req *agentv1.GetAgentRequest) (*agentv1.GetAgentResponse, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, ErrEmptyID.Error())
	}
	d, err := s.mgr.Get(req.GetId())
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentv1.GetAgentResponse{Agent: deploymentToProto(d)}, nil
}

// limitsFromProto maps the optional proto ResourceLimits to agent.ResourceLimits,
// leaving unset fields as zero so the manager applies the runtime defaults.
func limitsFromProto(l *agentv1.ResourceLimits) agent.ResourceLimits {
	if l == nil {
		return agent.ResourceLimits{}
	}
	return agent.ResourceLimits{
		MaxMemoryPages: l.GetMaxMemoryPages(),
		MaxRunTime:     time.Duration(l.GetMaxRunTimeMs()) * time.Millisecond,
	}
}

// statusToProto maps a manager Status to the proto AgentStatus enum.
func statusToProto(s Status) agentv1.AgentStatus {
	switch s {
	case StatusDeployed:
		return agentv1.AgentStatus_AGENT_STATUS_DEPLOYED
	case StatusRunning:
		return agentv1.AgentStatus_AGENT_STATUS_RUNNING
	case StatusFailed:
		return agentv1.AgentStatus_AGENT_STATUS_FAILED
	default:
		return agentv1.AgentStatus_AGENT_STATUS_UNSPECIFIED
	}
}

// deploymentToProto maps a persisted Deployment to the proto Agent the RPCs
// return. The module bytes are never echoed back, only their hash and size.
func deploymentToProto(d Deployment) *agentv1.Agent {
	return &agentv1.Agent{
		Id:         d.ID,
		Status:     statusToProto(d.Status),
		ModuleHash: d.ModuleHash,
		ModuleSize: d.ModuleSize,
		Limits: &agentv1.ResourceLimits{
			MaxMemoryPages: d.MaxMemoryPages,
			MaxRunTimeMs:   d.MaxRunTimeMS,
		},
		LastOutput: d.LastOutput,
		LastError:  d.LastError,
		LastCharge: d.LastCharge,
		CreatedAt:  nanosToTimestamp(d.CreatedAtNS),
		LastRunAt:  nanosToTimestamp(d.LastRunAtNS),
	}
}

// nanosToTimestamp converts an int64 unix-nanosecond timestamp to a protobuf
// Timestamp. A zero value yields nil, matching "unset".
func nanosToTimestamp(ns int64) *timestamppb.Timestamp {
	if ns == 0 {
		return nil
	}
	return timestamppb.New(time.Unix(0, ns).UTC())
}

// mapAgentError translates manager sentinel errors to gRPC status codes.
// Unknown errors map to codes.Internal so unexpected failures are not masked as
// benign. Nil returns nil.
func mapAgentError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrEmptyID), errors.Is(err, ErrEmptyModule):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ErrModuleTooLarge):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ErrAgentNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, ErrNoSigningAccount):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, ErrMeterNotApplied):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

// Server hosts the agent gRPC service on its own listener, following the
// internal/inferenceapi.Server construction pattern.
type Server struct {
	grpcServer *grpc.Server
	healthSvc  *health.Server
	addr       string
	lis        net.Listener
	svc        *Service
}

// Config configures the agent gRPC server.
type Config struct {
	// Addr is the TCP listen address (e.g. "0.0.0.0:9094").
	Addr string
	// Auth, when non-nil, enables authentication using the admin API-key model:
	// every non-health RPC requires a valid key.
	Auth *admin.Authenticator
	// Manager backs the service (required).
	Manager *Manager
}

// NewServer builds an agent gRPC server. It installs the admin auth
// interceptors when cfg.Auth is non-nil, registers the health service, and
// registers the AgentService implementation.
func NewServer(cfg Config) (*Server, error) {
	svc, err := NewService(cfg.Manager)
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
	agentv1.RegisterAgentServiceServer(grpcServer, svc)

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
		return fmt.Errorf("agentapi: failed to listen on %s: %w", s.addr, err)
	}
	s.lis = lis

	go func() {
		if err := s.grpcServer.Serve(lis); err != nil {
			fmt.Printf("agent gRPC server error: %v\n", err)
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

// Service exposes the service implementation for in-process tests and for the
// connectapi binding.
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
