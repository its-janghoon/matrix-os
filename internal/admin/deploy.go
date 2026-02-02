package admin

import (
	"context"
	"fmt"
	"sync"
)

// DeployService handles agent and matrix deployment requests
type DeployService struct {
	deployments map[string]*Deployment
	mu          sync.RWMutex
}

// Deployment represents a deployed agent or matrix
type Deployment struct {
	ID        string
	Type      string // "agent" or "matrix"
	Status    string // "running", "stopped", "error"
	Config    map[string]interface{}
	CreatedAt int64
}

// NewDeployService creates a new deploy service
func NewDeployService() *DeployService {
	return &DeployService{
		deployments: make(map[string]*Deployment),
	}
}

// DeployAgent deploys a new agent
func (s *DeployService) DeployAgent(ctx context.Context, id string, config map[string]interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.deployments[id]; exists {
		return fmt.Errorf("deployment with ID %s already exists", id)
	}

	s.deployments[id] = &Deployment{
		ID:        id,
		Type:      "agent",
		Status:    "running",
		Config:    config,
		CreatedAt: 0, // TODO: Use actual timestamp
	}

	return nil
}

// DeployMatrix deploys a new matrix
func (s *DeployService) DeployMatrix(ctx context.Context, id string, config map[string]interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.deployments[id]; exists {
		return fmt.Errorf("deployment with ID %s already exists", id)
	}

	s.deployments[id] = &Deployment{
		ID:        id,
		Type:      "matrix",
		Status:    "running",
		Config:    config,
		CreatedAt: 0, // TODO: Use actual timestamp
	}

	return nil
}

// GetDeployment retrieves a deployment by ID
func (s *DeployService) GetDeployment(id string) (*Deployment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	deployment, exists := s.deployments[id]
	if !exists {
		return nil, fmt.Errorf("deployment with ID %s not found", id)
	}

	return deployment, nil
}

// ListDeployments returns all deployments
func (s *DeployService) ListDeployments() []*Deployment {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Deployment, 0, len(s.deployments))
	for _, deployment := range s.deployments {
		result = append(result, deployment)
	}

	return result
}

// StopDeployment stops a deployment
func (s *DeployService) StopDeployment(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	deployment, exists := s.deployments[id]
	if !exists {
		return fmt.Errorf("deployment with ID %s not found", id)
	}

	deployment.Status = "stopped"
	return nil
}

// RemoveDeployment removes a deployment
func (s *DeployService) RemoveDeployment(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.deployments[id]; !exists {
		return fmt.Errorf("deployment with ID %s not found", id)
	}

	delete(s.deployments, id)
	return nil
}
