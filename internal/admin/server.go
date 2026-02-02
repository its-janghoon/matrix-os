package admin

import (
	"context"
	"fmt"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// Server represents the admin gRPC server
type Server struct {
	grpcServer *grpc.Server
	healthSvc  *health.Server
	addr       string
	deploySvc  *DeployService
	logsSvc    *LogsService
}

// Config represents admin server configuration
type Config struct {
	Addr string
}

// NewServer creates a new admin gRPC server
func NewServer(cfg Config) *Server {
	grpcServer := grpc.NewServer()
	healthSvc := health.NewServer()

	// Register health service
	healthpb.RegisterHealthServer(grpcServer, healthSvc)

	// Create and register custom services
	deploySvc := NewDeployService()
	logsSvc := NewLogsService()

	// Register services (proto definitions would be used here)
	// For now, we'll create the structure that can be extended

	return &Server{
		grpcServer: grpcServer,
		healthSvc:  healthSvc,
		addr:       cfg.Addr,
		deploySvc:  deploySvc,
		logsSvc:    logsSvc,
	}
}

// Start starts the gRPC server
func (s *Server) Start(ctx context.Context) error {
	// Set health status to serving
	s.healthSvc.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	// Listen on the configured address
	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.addr, err)
	}

	// Start serving in a goroutine
	go func() {
		if err := s.grpcServer.Serve(lis); err != nil {
			// Log error but don't return it since we're in a goroutine
			fmt.Printf("gRPC server error: %v\n", err)
		}
	}()

	return nil
}

// Stop gracefully stops the gRPC server
func (s *Server) Stop(ctx context.Context) error {
	s.healthSvc.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	s.grpcServer.GracefulStop()
	return nil
}

// GetDeployService returns the deploy service instance
func (s *Server) GetDeployService() *DeployService {
	return s.deploySvc
}

// GetLogsService returns the logs service instance
func (s *Server) GetLogsService() *LogsService {
	return s.logsSvc
}
