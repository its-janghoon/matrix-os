package node

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/admin"
)

// TestInitialize_GeneratesAWorkingCredential is the first-run test.
//
// A generated config turns ACLs on. Before this, it also contained no key and
// nothing wrote one, so every RPC on every surface answered "authentication
// required" and the node could not be driven at all - not even by `matrix` on
// the same machine. A default that refuses every call is not a default.
func TestInitialize_GeneratesAWorkingCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Initialize(path); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	n, err := New(context.Background(), path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cfg := n.config

	if !cfg.Security.EnableACLs {
		t.Fatal("generated config has ACLs off; this test is about the case where they are on")
	}
	if len(cfg.Security.APIKeys) != 1 {
		t.Fatalf("generated config has %d api keys, want exactly 1", len(cfg.Security.APIKeys))
	}
	key := cfg.Security.APIKeys[0]
	if key.Role != "admin" {
		t.Fatalf("generated key role = %q, want admin", key.Role)
	}
	// 32 bytes of crypto/rand, hex encoded: long enough that guessing is not a
	// strategy, and printable so it can be pasted into a flag.
	raw, err := hex.DecodeString(key.Key)
	if err != nil {
		t.Fatalf("generated key is not hex: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("generated key is %d bytes, want 32", len(raw))
	}
}

// TestInitialize_ConfigIsNotWorldReadable follows from the above: the file now
// holds a credential.
func TestInitialize_ConfigIsNotWorldReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Initialize(path); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("config mode = %04o, want 0600: it contains an API key", mode)
	}
}

func TestEachInitializeGeneratesADifferentKey(t *testing.T) {
	keys := map[string]struct{}{}
	for i := 0; i < 5; i++ {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := Initialize(path); err != nil {
			t.Fatalf("Initialize: %v", err)
		}
		n, err := New(context.Background(), path)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		k := n.config.Security.APIKeys[0].Key
		if _, dup := keys[k]; dup {
			t.Fatal("two initialized nodes got the same API key")
		}
		keys[k] = struct{}{}
	}
}

func TestRoleFromConfig(t *testing.T) {
	cases := map[string]admin.Role{
		"":         admin.RoleAdmin, // a key with no role stated on a single-operator node
		"admin":    admin.RoleAdmin,
		"ADMIN":    admin.RoleAdmin,
		" admin ":  admin.RoleAdmin,
		"operator": admin.RoleOperator,
		"viewer":   admin.RoleViewer,
		// An unrecognised role must fall to the LEAST privilege, not the most: a
		// typo in a config should cost access, never grant it.
		"amdin":     admin.RoleViewer,
		"superuser": admin.RoleViewer,
	}
	for input, want := range cases {
		if got := roleFromConfig(input); got != want {
			t.Fatalf("roleFromConfig(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestConfiguredKeysSurviveLoading guards the plumbing between the YAML and the
// authenticator: a key written in the file must reach the node.
func TestConfiguredKeysSurviveLoading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `network:
  listen_addr: /ip4/127.0.0.1/tcp/9000
storage:
  engine: pebble
  path: ./data
security:
  enable_acls: true
  api_keys:
    - key: abc123
      role: operator
      name: ci
    - key: def456
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	n, err := New(context.Background(), path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	keys := n.config.Security.APIKeys
	if len(keys) != 2 {
		t.Fatalf("loaded %d keys, want 2", len(keys))
	}
	if keys[0].Key != "abc123" || keys[0].Role != "operator" || keys[0].Name != "ci" {
		t.Fatalf("first key = %+v", keys[0])
	}
	if keys[1].Key != "def456" || keys[1].Role != "" {
		t.Fatalf("second key = %+v; a role-less entry is allowed and means admin", keys[1])
	}
}
