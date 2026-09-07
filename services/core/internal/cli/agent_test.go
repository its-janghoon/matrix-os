package cli

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/agentapi"
	"github.com/ecirlabs/matrix-core/internal/kv"
)

// startAgentServer stands up an in-process agentapi server (metering disabled)
// and returns its bound address. The trivial guest.wasm fixture from the agent
// package's testdata is the module used by the deploy test.
func startAgentServer(t *testing.T) string {
	t.Helper()

	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mgr, err := agentapi.NewManager(agentapi.ManagerConfig{Store: store})
	if err != nil {
		t.Fatalf("agentapi.NewManager: %v", err)
	}
	srv, err := agentapi.NewServer(agentapi.Config{Addr: "127.0.0.1:0", Manager: mgr})
	if err != nil {
		t.Fatalf("agentapi.NewServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	addr := srv.Addr()
	if addr == "" {
		t.Fatal("agent server address empty after Start")
	}
	return addr
}

// TestCLI_AgentDeployAndList drives `matrix agent deploy` and `matrix agent list`
// in-process against a live AgentService: a trivial wasm module is deployed and
// runs, then appears in the deploy list.
func TestCLI_AgentDeployAndList(t *testing.T) {
	addr := startAgentServer(t)

	wasm, err := os.ReadFile("../agent/testdata/guest.wasm")
	if err != nil {
		t.Fatalf("read guest.wasm: %v", err)
	}
	path := t.TempDir() + "/guest.wasm"
	if err := os.WriteFile(path, wasm, 0o600); err != nil {
		t.Fatalf("write temp wasm: %v", err)
	}

	out, err := run(t, "unused:0", "agent", "deploy", "--agent-addr", addr, "--id", "demo", "--wasm", path)
	if err != nil {
		t.Fatalf("agent deploy: %v (%s)", err, out)
	}
	if !strings.Contains(out, "demo") || !strings.Contains(out, "running") {
		t.Fatalf("expected demo running in deploy output: %s", out)
	}

	listOut, err := run(t, "unused:0", "agent", "list", "--agent-addr", addr)
	if err != nil {
		t.Fatalf("agent list: %v (%s)", err, listOut)
	}
	if !strings.Contains(listOut, "demo") {
		t.Fatalf("expected demo in list output: %s", listOut)
	}
}

// TestCLI_AgentDeployRequiresFlags asserts the deploy command validates its
// required flags before dialing.
func TestCLI_AgentDeployRequiresFlags(t *testing.T) {
	if _, err := run(t, "unused:0", "agent", "deploy", "--agent-addr", "127.0.0.1:0", "--id", "x"); err == nil {
		t.Fatal("agent deploy without --wasm should fail")
	}
	if _, err := run(t, "unused:0", "agent", "deploy", "--agent-addr", "127.0.0.1:0", "--wasm", "/nope.wasm"); err == nil {
		t.Fatal("agent deploy without --id should fail")
	}
}
