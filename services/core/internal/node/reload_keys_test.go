package node

import (
	"os"
	"path/filepath"
	"testing"
)

// apiKeysFromConfig is what BOTH start and reload use to decide which
// credentials a node accepts. These pin the parts that would silently diverge
// if the two ever grew separate copies.
func TestAPIKeysFromConfig(t *testing.T) {
	t.Run("acls off means no keys at all", func(t *testing.T) {
		cfg := &Config{}
		cfg.Security.EnableACLs = false
		cfg.Security.APIKeys = []APIKeyConfig{{Key: "ignored"}}
		keys, err := apiKeysFromConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(keys) != 0 {
			t.Fatalf("got %d keys with ACLs off, want none", len(keys))
		}
	})

	t.Run("an entry with no credential is an error, not a skip", func(t *testing.T) {
		cfg := &Config{}
		cfg.Security.EnableACLs = true
		cfg.Security.APIKeys = []APIKeyConfig{{Key: "good"}, {Key: ""}}
		if _, err := apiKeysFromConfig(cfg); err == nil {
			t.Fatal("a keyless entry was accepted")
		}
	})

	t.Run("an unset role means admin, and unnamed entries get stable names", func(t *testing.T) {
		cfg := &Config{}
		cfg.Security.EnableACLs = true
		cfg.Security.APIKeys = []APIKeyConfig{{Key: "k1"}, {Key: "k2", Role: "viewer", Name: "buyer"}}
		keys, err := apiKeysFromConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(keys) != 2 {
			t.Fatalf("got %d keys, want 2", len(keys))
		}
		if keys[0].Role != "admin" || keys[0].Name != "config-key-0" {
			t.Fatalf("first key: %+v", keys[0])
		}
		if keys[1].Role != "viewer" || keys[1].Name != "buyer" {
			t.Fatalf("second key: %+v", keys[1])
		}
	})

	t.Run("the environment key is additive", func(t *testing.T) {
		t.Setenv("MATRIX_ADMIN_API_KEY", "from-env")
		cfg := &Config{}
		cfg.Security.EnableACLs = true
		cfg.Security.APIKeys = []APIKeyConfig{{Key: "from-file"}}
		keys, err := apiKeysFromConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(keys) != 2 {
			t.Fatalf("got %d keys, want the file key plus the env key", len(keys))
		}
		// It has to survive a RELOAD too, or an operator who keeps secrets out
		// of files loses their own access on a signal they expected to be safe.
		if keys[1].Name != "env-admin" {
			t.Fatalf("env key not appended: %+v", keys[1])
		}
	})
}

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// A reload reads the same file the node started from, through the same parser.
func TestLoadConfig_AppliesTheSameDefaultsAsStart(t *testing.T) {
	path := writeConfig(t, t.TempDir(), "security:\n  enable_acls: true\n  api_keys:\n    - key: abc\n")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Admin.Addr != "0.0.0.0:9090" || cfg.Connect.Addr != "0.0.0.0:9093" {
		t.Fatalf("defaults were not applied: admin=%q connect=%q", cfg.Admin.Addr, cfg.Connect.Addr)
	}
	if cfg.Storage.Path != "./data" {
		t.Fatalf("storage default not applied: %q", cfg.Storage.Path)
	}
	if len(cfg.Security.APIKeys) != 1 || cfg.Security.APIKeys[0].Key != "abc" {
		t.Fatalf("keys not parsed: %+v", cfg.Security.APIKeys)
	}
}

// ReloadAPIKeys must refuse rather than half-apply, and must say what is still
// in force. A node that reports an error AND quietly disarmed itself is the
// outcome this whole path exists to avoid.
func TestReloadAPIKeys_RefusesWithoutAStartedNode(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "security:\n  enable_acls: true\n  api_keys:\n    - key: abc\n")
	n, err := New(t.Context(), path)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := n.ReloadAPIKeys(); err == nil {
		t.Fatal("reloading an unstarted node was accepted")
	}
	if n.configPath != path {
		t.Fatalf("the node did not remember the file it was loaded from: %q", n.configPath)
	}
}
