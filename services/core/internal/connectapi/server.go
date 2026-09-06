package connectapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Server hosts the Connect endpoint on its own listener, following the
// Start/Stop shape of the admin, market and inference servers.
type Server struct {
	http *http.Server
	addr string
	lis  net.Listener
}

// NewServer builds the endpoint from the same Config the handler takes, plus
// the address to listen on.
func NewServer(addr string, cfg Config) (*Server, error) {
	if addr == "" {
		return nil, errors.New("connectapi: listen address is required")
	}
	handler, err := NewHandler(cfg)
	if err != nil {
		return nil, err
	}
	return &Server{
		addr: addr,
		http: &http.Server{
			Handler: handler,
			// A browser-facing surface gets timeouts: an idle or slow client must
			// not be able to hold a connection open indefinitely.
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
		},
	}, nil
}

// Start listens and serves in a background goroutine.
func (s *Server) Start(ctx context.Context) error {
	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("connectapi: failed to listen on %s: %w", s.addr, err)
	}
	s.lis = lis

	go func() {
		if err := s.http.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Printf("connect endpoint error: %v\n", err)
		}
	}()
	return nil
}

// Addr returns the address actually bound, which differs from the configured
// one when port 0 was requested.
func (s *Server) Addr() string {
	if s.lis == nil {
		return s.addr
	}
	return s.lis.Addr().String()
}

// Stop shuts the endpoint down, waiting briefly for in-flight requests.
func (s *Server) Stop(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.http.Shutdown(shutdownCtx)
}
