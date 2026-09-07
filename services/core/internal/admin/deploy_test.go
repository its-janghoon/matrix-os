package admin

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/agent"
)

// DeployAgent used to write a map entry that said "running" and execute
// nothing, so every deployment succeeded and no agent ever ran. These tests are
// about the difference.

func guestModule(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile("testdata/guest.wasm")
	if err != nil {
		t.Fatalf("read testdata/guest.wasm: %v", err)
	}
	return code
}

func TestDeployAgentRunsTheModule(t *testing.T) {
	svc := NewDeployService(nil)
	ctx := context.Background()

	if err := svc.DeployAgent(ctx, "runner", map[string]interface{}{
		"wasm_base64": base64.StdEncoding.EncodeToString(guestModule(t)),
	}); err != nil {
		t.Fatalf("DeployAgent: %v", err)
	}

	deployment, err := svc.GetDeployment("runner")
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if deployment.Status != "running" {
		t.Fatalf("status = %q (%s), want running", deployment.Status, deployment.Error)
	}
	if deployment.CreatedAt == 0 {
		t.Fatal("CreatedAt is zero; a deployment must record when it happened")
	}

	// "running" has to mean a loaded module, not a string in a map: the guest's
	// exports must be callable.
	instance, err := svc.Agent("runner")
	if err != nil {
		t.Fatalf("Agent: %v", err)
	}
	if _, err := instance.Call(ctx, "scratch_ptr"); err != nil {
		t.Fatalf("calling an export of the deployed module: %v", err)
	}
	// And _start must have run: it calls set_memory with "ping".
	if got := instance.Memory(); string(got[:4]) != "ping" {
		t.Fatalf("host buffer starts with %q, want \"ping\" - _start did not run", got[:4])
	}
}

func TestDeployAgentAcceptsAPathOnTheNode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "guest.wasm")
	if err := os.WriteFile(path, guestModule(t), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	svc := NewDeployService(nil)
	if err := svc.DeployAgent(context.Background(), "from-path", map[string]interface{}{
		"wasm_path": path,
	}); err != nil {
		t.Fatalf("DeployAgent: %v", err)
	}
	if _, err := svc.Agent("from-path"); err != nil {
		t.Fatalf("Agent: %v", err)
	}
}

func TestDeployAgentRefusesWhatItCannotRun(t *testing.T) {
	cases := []struct {
		name   string
		config map[string]interface{}
		want   string
	}{
		{"no module at all", map[string]interface{}{}, "no module"},
		{"empty config value", map[string]interface{}{"wasm_base64": ""}, "decoded to nothing"},
		{"not base64", map[string]interface{}{"wasm_base64": "!!!!"}, "not valid base64"},
		{"wrong type", map[string]interface{}{"wasm_path": 42}, "must be a string"},
		{"missing file", map[string]interface{}{"wasm_path": "/nonexistent/agent.wasm"}, "read wasm_path"},
		{
			"not a wasm module",
			map[string]interface{}{"wasm_base64": base64.StdEncoding.EncodeToString([]byte("nope"))},
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := NewDeployService(nil)
			err := svc.DeployAgent(context.Background(), "bad", c.config)
			if err == nil {
				t.Fatal("DeployAgent reported success for a module it cannot run")
			}
			if c.want != "" && !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want it to mention %q", err, c.want)
			}
			// The record must say error and carry the reason, not sit at running.
			deployment, gerr := svc.GetDeployment("bad")
			if gerr != nil {
				t.Fatalf("GetDeployment: %v", gerr)
			}
			if deployment.Status != "error" {
				t.Fatalf("status = %q, want error", deployment.Status)
			}
			if deployment.Error == "" {
				t.Fatal("the deployment records no reason for being in error")
			}
			if _, aerr := svc.Agent("bad"); aerr == nil {
				t.Fatal("Agent returned a runtime for a failed deployment")
			}
		})
	}
}

// A module that never returns must fail the deployment rather than hang it.
func TestDeployAgentStopsAModuleThatNeverReturns(t *testing.T) {
	spin, err := os.ReadFile("../agent/testdata/spin.wasm")
	if err != nil {
		t.Skipf("spin fixture unavailable: %v", err)
	}

	svc := NewDeployService(nil)
	svc.SetAgentLimits(agent.ResourceLimits{MaxMemoryPages: 16, MaxRunTime: 150 * time.Millisecond})

	done := make(chan error, 1)
	go func() {
		done <- svc.DeployAgent(context.Background(), "spinner", map[string]interface{}{
			"wasm_base64": base64.StdEncoding.EncodeToString(spin),
		})
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("deploying a module that never returns reported success")
		}
		deployment, gerr := svc.GetDeployment("spinner")
		if gerr != nil {
			t.Fatalf("GetDeployment: %v", gerr)
		}
		if deployment.Status != "error" {
			t.Fatalf("status = %q, want error", deployment.Status)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("DeployAgent hung on a module that never returns")
	}
}

func TestStopAndRemoveCloseTheModule(t *testing.T) {
	svc := NewDeployService(nil)
	ctx := context.Background()
	cfg := map[string]interface{}{"wasm_base64": base64.StdEncoding.EncodeToString(guestModule(t))}

	if err := svc.DeployAgent(ctx, "a", cfg); err != nil {
		t.Fatalf("DeployAgent: %v", err)
	}
	if err := svc.StopDeployment(ctx, "a"); err != nil {
		t.Fatalf("StopDeployment: %v", err)
	}
	deployment, _ := svc.GetDeployment("a")
	if deployment.Status != "stopped" {
		t.Fatalf("status = %q, want stopped", deployment.Status)
	}
	// Stopping must actually release the module, not just relabel the record.
	if _, err := svc.Agent("a"); err == nil {
		t.Fatal("a stopped deployment still holds a loaded module")
	}

	if err := svc.DeployAgent(ctx, "b", cfg); err != nil {
		t.Fatalf("DeployAgent: %v", err)
	}
	if err := svc.RemoveDeployment(ctx, "b"); err != nil {
		t.Fatalf("RemoveDeployment: %v", err)
	}
	if _, err := svc.GetDeployment("b"); err == nil {
		t.Fatal("a removed deployment is still listed")
	}
}

// DeployMatrix has no runtime behind it, so it must not claim one.
func TestDeployMatrixDoesNotClaimToBeRunning(t *testing.T) {
	svc := NewDeployService(nil)
	if err := svc.DeployMatrix(context.Background(), "m", map[string]interface{}{}); err != nil {
		t.Fatalf("DeployMatrix: %v", err)
	}
	deployment, err := svc.GetDeployment("m")
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if deployment.Status == "running" {
		t.Fatal("DeployMatrix reports \"running\" for something with no runtime behind it")
	}
	if deployment.Status != "registered" {
		t.Fatalf("status = %q, want registered", deployment.Status)
	}
}
