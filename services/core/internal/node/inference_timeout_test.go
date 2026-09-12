package node

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestInferenceBackendRequestTimeoutParsesFromYAML pins that an operator can
// actually write `request_timeout: 10m` in the node config. A duration field
// that silently decodes to zero would read as configured while leaving the
// backend on the 60s default, which is precisely the failure a GPU provider
// would only discover when a long completion was cut off mid-answer.
func TestInferenceBackendRequestTimeoutParsesFromYAML(t *testing.T) {
	const doc = `
inference:
  backends:
    - id: gpu-box-1
      kind: openai
      base_url: http://127.0.0.1:8000
      api_key_env: MATRIX_VLLM_API_KEY
      request_timeout: 10m
      quote_ttl: 12h
      capacity: 200000
      price_per_unit: 4
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(doc), &cfg); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if len(cfg.Inference.Backends) != 1 {
		t.Fatalf("backends = %d, want 1", len(cfg.Inference.Backends))
	}
	b := cfg.Inference.Backends[0]
	if b.RequestTimeout != 10*time.Minute {
		t.Fatalf("request_timeout = %s, want 10m", b.RequestTimeout)
	}
	if b.QuoteTTL != 12*time.Hour {
		t.Fatalf("quote_ttl = %s, want 12h", b.QuoteTTL)
	}
}

// TestConfiguredRequestTimeoutReachesTheBackend walks the whole registration
// path a GPU provider uses, so the config field is proven to survive into the
// installed backend rather than just into the struct.
func TestConfiguredRequestTimeoutReachesTheBackend(t *testing.T) {
	n, registry := backendTestNode(t, InferenceConfig{
		Backends: []InferenceBackendConfig{{
			ID:             "gpu-box-1",
			Kind:           "local-http",
			BaseURL:        "http://127.0.0.1:11434",
			RequestTimeout: 9 * time.Minute,
			Models:         []string{"qwen3-32b"},
			Capacity:       200000,
			PricePerUnit:   4,
		}},
	})
	if err := n.registerConfiguredInferenceBackends(registry); err != nil {
		t.Fatalf("registerConfiguredInferenceBackends: %v", err)
	}
	if _, err := registry.Backend("gpu-box-1"); err != nil {
		t.Fatalf("backend not registered: %v", err)
	}
}
