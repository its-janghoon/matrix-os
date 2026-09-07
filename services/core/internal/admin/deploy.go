package admin

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/ecirlabs/matrix-core/internal/agent"
)

// DeployService handles agent and matrix deployment requests.
type DeployService struct {
	deployments map[string]*Deployment
	mu          sync.RWMutex
	auth        *Authenticator
	limits      agent.ResourceLimits
}

// Deployment represents a deployed agent or matrix.
type Deployment struct {
	ID        string
	Type      string // "agent" or "matrix"
	Status    string // "running", "stopped", "error"
	Config    map[string]interface{}
	CreatedAt int64
	// Error carries why a deployment is in "error", so a caller sees the reason
	// rather than only the state.
	Error string

	// runtime is the loaded WebAssembly agent, for Type == "agent". It is what
	// makes Status == "running" mean something: this used to be a map entry that
	// said "running" with no module behind it, so DeployAgent reported success
	// for a deployment that never executed a byte.
	runtime *agent.Agent
}

// NewDeployService creates a new deploy service.
func NewDeployService(auth *Authenticator) *DeployService {
	return &DeployService{
		deployments: make(map[string]*Deployment),
		auth:        auth,
		limits:      agent.DefaultMemoryLimits,
	}
}

// SetAgentLimits overrides the resource limits applied to deployed agents. It
// must be called before any deployment.
func (s *DeployService) SetAgentLimits(limits agent.ResourceLimits) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limits = limits
}

// agentCodeFrom reads a deployment's WebAssembly module out of its config,
// accepting either a path on the node ("wasm_path") or the module inline as
// standard base64 ("wasm_base64").
//
// One of them is required. Reporting a deployment as running with no module
// behind it is the behaviour this replaces, so an unusable config is an error
// now rather than a success.
func agentCodeFrom(config map[string]interface{}) ([]byte, error) {
	if raw, ok := config["wasm_base64"]; ok {
		encoded, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("wasm_base64 must be a string, got %T", raw)
		}
		code, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("wasm_base64 is not valid base64: %w", err)
		}
		if len(code) == 0 {
			return nil, fmt.Errorf("wasm_base64 decoded to nothing")
		}
		return code, nil
	}
	if raw, ok := config["wasm_path"]; ok {
		path, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("wasm_path must be a string, got %T", raw)
		}
		code, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read wasm_path: %w", err)
		}
		if len(code) == 0 {
			return nil, fmt.Errorf("wasm_path %q is empty", path)
		}
		return code, nil
	}
	return nil, fmt.Errorf("no module: set wasm_path to a file on this node, or wasm_base64 to the module itself")
}

// DeployAgent loads a WebAssembly module into this node's agent runtime and
// runs it.
//
// It used to write a map entry that said "running" and execute nothing at all,
// so every deployment "succeeded" and no agent ever ran. Now the module is
// compiled and instantiated against the runtime's four host functions, under
// the service's memory ceiling and run-time deadline, and `_start` is called if
// it exports one. A module that does not compile, does not instantiate, or
// exceeds its deadline leaves the deployment in "error" WITH the reason, and
// DeployAgent returns that error.
//
// "running" means the module is loaded and its exports are callable, which is
// what running means for a WebAssembly module: there is no thread of its own,
// and nothing executes between calls.
func (s *DeployService) DeployAgent(ctx context.Context, id string, config map[string]interface{}) error {
	// Check authorization
	if s.auth != nil {
		if _, err := s.auth.CheckPermission(ctx, PermissionDeployAgent); err != nil {
			return err
		}
	}

	s.mu.Lock()
	if _, exists := s.deployments[id]; exists {
		s.mu.Unlock()
		return fmt.Errorf("deployment with ID %s already exists", id)
	}
	limits := s.limits
	// Claim the id before doing the slow work, so two concurrent deployments of
	// one id cannot both get past the check above.
	record := &Deployment{
		ID:        id,
		Type:      "agent",
		Status:    "starting",
		Config:    config,
		CreatedAt: time.Now().UTC().Unix(),
	}
	s.deployments[id] = record
	s.mu.Unlock()

	fail := func(err error) error {
		s.mu.Lock()
		record.Status = "error"
		record.Error = err.Error()
		s.mu.Unlock()
		return err
	}

	code, err := agentCodeFrom(config)
	if err != nil {
		return fail(fmt.Errorf("deploy agent %q: %w", id, err))
	}

	// The agent's stdout and stderr go to the node's, so an operator sees what a
	// guest logs and every refused host call.
	instance, err := agent.New(ctx, agent.Config{
		ID:     id,
		Code:   code,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}, limits)
	if err != nil {
		return fail(fmt.Errorf("deploy agent %q: %w", id, err))
	}
	if err := instance.Start(ctx); err != nil {
		_ = instance.Stop(ctx)
		return fail(fmt.Errorf("deploy agent %q: %w", id, err))
	}

	s.mu.Lock()
	record.runtime = instance
	record.Status = "running"
	s.mu.Unlock()
	return nil
}

// Agent returns the loaded runtime for an agent deployment, so a caller can
// invoke its exports. It reports an error for a deployment that is not a
// running agent.
func (s *DeployService) Agent(id string) (*agent.Agent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	deployment, exists := s.deployments[id]
	if !exists {
		return nil, fmt.Errorf("deployment with ID %s not found", id)
	}
	if deployment.runtime == nil {
		return nil, fmt.Errorf("deployment %s is a %s in state %q and has no loaded module", id, deployment.Type, deployment.Status)
	}
	return deployment.runtime, nil
}

// DeployMatrix deploys a new matrix
func (s *DeployService) DeployMatrix(ctx context.Context, id string, config map[string]interface{}) error {
	// Check authorization
	if s.auth != nil {
		if _, err := s.auth.CheckPermission(ctx, PermissionDeployMatrix); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.deployments[id]; exists {
		return fmt.Errorf("deployment with ID %s already exists", id)
	}

	// Unlike DeployAgent there is nothing behind this yet: a "matrix" has no
	// runtime in this node, so the record is a record and the status says so
	// rather than claiming something is running.
	s.deployments[id] = &Deployment{
		ID:        id,
		Type:      "matrix",
		Status:    "registered",
		Config:    config,
		CreatedAt: time.Now().UTC().Unix(),
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
	// Check authorization
	if s.auth != nil {
		if _, err := s.auth.CheckPermission(ctx, PermissionStopDeploy); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	deployment, exists := s.deployments[id]
	if !exists {
		return fmt.Errorf("deployment with ID %s not found", id)
	}

	// Actually close the module and its runtime. Flipping the status string
	// alone would leave the compiled module and its memory alive for the life of
	// the process, so "stopped" would have freed nothing.
	if deployment.runtime != nil {
		if err := deployment.runtime.Stop(ctx); err != nil {
			deployment.Status = "error"
			deployment.Error = err.Error()
			return fmt.Errorf("stop deployment %q: %w", id, err)
		}
		deployment.runtime = nil
	}
	deployment.Status = "stopped"
	deployment.Error = ""
	return nil
}

// RemoveDeployment removes a deployment
func (s *DeployService) RemoveDeployment(ctx context.Context, id string) error {
	// Check authorization
	if s.auth != nil {
		if _, err := s.auth.CheckPermission(ctx, PermissionRemoveDeploy); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	deployment, exists := s.deployments[id]
	if !exists {
		return fmt.Errorf("deployment with ID %s not found", id)
	}

	// Close the module before forgetting it, or removing a deployment would leak
	// the runtime it was holding.
	if deployment.runtime != nil {
		if err := deployment.runtime.Stop(ctx); err != nil {
			return fmt.Errorf("remove deployment %q: %w", id, err)
		}
		deployment.runtime = nil
	}
	delete(s.deployments, id)
	return nil
}
