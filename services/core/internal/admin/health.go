package admin

import (
	"context"
	"sync"

	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// HealthService provides health check functionality
// Note: The gRPC health service is already registered in server.go
// This file provides additional health checking utilities

// ComponentHealth represents the health status of a component
type ComponentHealth struct {
	Name   string
	Status healthpb.HealthCheckResponse_ServingStatus
	Error  string
}

// HealthChecker checks the health of various components
type HealthChecker struct {
	components map[string]ComponentHealth
	mu         sync.RWMutex
}

// NewHealthChecker creates a new health checker
func NewHealthChecker() *HealthChecker {
	return &HealthChecker{
		components: make(map[string]ComponentHealth),
	}
}

// UpdateComponentHealth updates the health status of a component
func (h *HealthChecker) UpdateComponentHealth(name string, status healthpb.HealthCheckResponse_ServingStatus, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	health := ComponentHealth{
		Name:   name,
		Status: status,
	}

	if err != nil {
		health.Error = err.Error()
	}

	h.components[name] = health
}

// GetComponentHealth retrieves the health status of a component
func (h *HealthChecker) GetComponentHealth(name string) (ComponentHealth, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	health, exists := h.components[name]
	return health, exists
}

// GetAllComponentHealth returns health status of all components
func (h *HealthChecker) GetAllComponentHealth() map[string]ComponentHealth {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make(map[string]ComponentHealth)
	for k, v := range h.components {
		result[k] = v
	}

	return result
}

// CheckOverallHealth checks the overall health of the system
func (h *HealthChecker) CheckOverallHealth(ctx context.Context) healthpb.HealthCheckResponse_ServingStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.components) == 0 {
		return healthpb.HealthCheckResponse_SERVING
	}

	// If any critical component is not serving, return NOT_SERVING
	criticalComponents := []string{"p2p", "kv", "agent"}
	for _, name := range criticalComponents {
		if health, exists := h.components[name]; exists {
			if health.Status != healthpb.HealthCheckResponse_SERVING {
				return healthpb.HealthCheckResponse_NOT_SERVING
			}
		}
	}

	return healthpb.HealthCheckResponse_SERVING
}
