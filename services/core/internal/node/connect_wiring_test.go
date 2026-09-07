package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitialize_WritesTheConnectEndpointWithNarrowOrigins pins the shape of a
// fresh node's browser-facing surface.
//
// The endpoint is on by default because the Console and any web front end need
// it, but its CORS policy is not wide open: a daemon on localhost that lets any
// origin call it can be driven by whatever page the operator happens to have
// open, which on a node without ACLs means spending their MATRIX. The generated
// config therefore names the origins that actually need to work.
func TestInitialize_WritesTheConnectEndpointWithNarrowOrigins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Initialize(path); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), "connect:") {
		t.Fatalf("generated config has no connect section, so the endpoint is undocumented:\n%s", raw)
	}

	n, err := New(context.Background(), path)
	if err != nil {
		t.Fatalf("New on the generated config: %v", err)
	}
	cfg := n.config

	if cfg.Connect.Addr != "0.0.0.0:9093" {
		t.Fatalf("connect addr = %q, want 0.0.0.0:9093", cfg.Connect.Addr)
	}
	if len(cfg.Connect.AllowedOrigins) == 0 {
		t.Fatal("generated config allows no browser origin, so the Console cannot connect out of the box")
	}
	for _, origin := range cfg.Connect.AllowedOrigins {
		if origin == "*" {
			t.Fatalf("generated config allows any origin (%q); a localhost daemon must not be callable "+
				"by every page the operator visits", origin)
		}
		if !strings.HasPrefix(origin, "http://127.0.0.1") && !strings.HasPrefix(origin, "http://localhost") {
			t.Fatalf("generated config allows %q, which is not a local development origin", origin)
		}
	}
}

// TestLoadingDoesNotWidenAnEmptyOriginList is the security half: an operator who
// writes `allowed_origins: []` means it, and loading must not turn that into
// "any origin". Deny-by-default only ever affects browsers - curl, the Go client
// and the SDK under Node send no Origin header and need no CORS.
func TestLoadingDoesNotWidenAnEmptyOriginList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `network:
  listen_addr: /ip4/127.0.0.1/tcp/9000
storage:
  engine: pebble
  path: ./data
connect:
  addr: 127.0.0.1:9093
  allowed_origins: []
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	n, err := New(context.Background(), path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := n.config.Connect.AllowedOrigins; len(got) != 0 {
		t.Fatalf("allowed_origins = %v, want it left empty as written", got)
	}
}

// TestConnectEndpointCanBeTurnedOff covers the escape hatch: an operator who
// wants no HTTP surface at all should not have to firewall a port.
func TestConnectEndpointCanBeTurnedOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "off-config.yaml")
	body := `network:
  listen_addr: /ip4/127.0.0.1/tcp/9000
storage:
  engine: pebble
  path: ./data
connect:
  addr: off
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	n, err := New(context.Background(), path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if n.config.Connect.Addr != "off" {
		t.Fatalf("connect addr = %q, want \"off\" preserved so Start skips the endpoint", n.config.Connect.Addr)
	}
}
