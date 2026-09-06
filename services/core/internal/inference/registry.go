package inference

import (
	"fmt"
	"sort"
	"sync"
)

// BackendKind names a backend implementation for config-driven selection.
type BackendKind string

// Known backend kinds.
const (
	// KindEcho selects the deterministic local stub runner (EchoBackend).
	KindEcho BackendKind = "echo"
	// KindOpenAI selects the OpenAI-compatible provider-API backend.
	KindOpenAI BackendKind = "openai"
	// KindLocalHTTP selects the local Ollama/llama.cpp-style HTTP runner backend.
	KindLocalHTTP BackendKind = "local-http"
)

// BackendConfig is the config-driven description of a backend to build. Only the
// fields relevant to Kind are consulted.
type BackendConfig struct {
	// Kind selects which backend implementation to build.
	Kind BackendKind
	// BaseURL configures the endpoint for KindOpenAI and KindLocalHTTP. For
	// KindOpenAI an empty value defaults to the public OpenAI API.
	BaseURL string
	// APIKeyEnv names the environment variable holding the provider API key for
	// KindOpenAI. Empty defaults to OPENAI_API_KEY.
	APIKeyEnv string
	// EchoPrefix optionally sets the EchoBackend prefix for KindEcho.
	EchoPrefix string
}

// NewBackend is the factory that builds a Backend from config. API keys for
// provider backends are read from the environment inside the concrete
// constructors, never from config, so a key is never carried in a config struct.
func NewBackend(cfg BackendConfig) (Backend, error) {
	switch cfg.Kind {
	case KindEcho:
		return &EchoBackend{Prefix: cfg.EchoPrefix}, nil
	case KindOpenAI:
		return NewOpenAIBackend(OpenAIConfig{BaseURL: cfg.BaseURL, APIKeyEnv: cfg.APIKeyEnv})
	case KindLocalHTTP:
		return NewLocalHTTPBackend(LocalHTTPConfig{BaseURL: cfg.BaseURL})
	default:
		return nil, fmt.Errorf("%w: unknown kind %q", ErrBackendNotFound, cfg.Kind)
	}
}

// Registry maps provider IDs to the Backend each provider fulfills inference
// with. The marketplace looks up a provider's backend by ID when fulfilling an
// inference job, so a node can host several providers (e.g. one local, one
// proxied) each with a distinct backend. It is safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	backends map[string]Backend
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{backends: make(map[string]Backend)}
}

// Register associates a backend with a provider ID, replacing any existing
// entry. A nil backend is rejected.
func (r *Registry) Register(providerID string, b Backend) error {
	if providerID == "" {
		return fmt.Errorf("inference: provider id must not be empty")
	}
	if b == nil {
		return fmt.Errorf("inference: backend must not be nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backends[providerID] = b
	return nil
}

// RegisterFromConfig builds a backend from cfg and registers it for providerID.
func (r *Registry) RegisterFromConfig(providerID string, cfg BackendConfig) (Backend, error) {
	b, err := NewBackend(cfg)
	if err != nil {
		return nil, err
	}
	if err := r.Register(providerID, b); err != nil {
		return nil, err
	}
	return b, nil
}

// Backend returns the backend registered for providerID, or ErrBackendNotFound.
func (r *Registry) Backend(providerID string) (Backend, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.backends[providerID]
	if !ok {
		return nil, fmt.Errorf("%w: provider %q", ErrBackendNotFound, providerID)
	}
	return b, nil
}

// Providers returns the sorted list of provider IDs that have a registered
// inference backend, so callers can enumerate inference-capable providers.
func (r *Registry) Providers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.backends))
	for id := range r.backends {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
