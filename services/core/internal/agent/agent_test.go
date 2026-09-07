package agent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// The four host functions used to be package-level stubs with empty bodies, and
// this package had no tests at all. So a guest could call log() and get
// silence, set_memory() and store nothing, get_memory() and read nothing - and
// the docs described the ABI as though it worked. These tests run a real
// WebAssembly module against the real runtime and check what actually crosses
// the boundary.

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	code, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return code
}

func TestHostFunctionsActuallyDoSomething(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var sent []Message

	a, err := New(context.Background(), Config{
		ID:     "fixture",
		Code:   loadFixture(t, "guest.wasm"),
		Stdout: &stdout,
		Stderr: &stderr,
		Send: func(target string, payload []byte) error {
			sent = append(sent, Message{Target: target, Payload: payload})
			return nil
		},
	}, DefaultMemoryLimits)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer a.Stop(context.Background())

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// log: the guest's bytes reach the host's stdout.
	if got := stdout.String(); !strings.Contains(got, "hello from wasm") {
		t.Fatalf("log() produced %q, want it to contain the guest's string", got)
	}
	if got := stdout.String(); !strings.Contains(got, "[fixture]") {
		t.Fatalf("log() produced %q, want it tagged with the agent id", got)
	}

	// set_memory: the guest's bytes reach the host-side buffer.
	if got := a.Memory(); !bytes.HasPrefix(got, []byte("ping")) {
		t.Fatalf("set_memory() left %q in the host buffer, want it to start with \"ping\"", got[:min(8, len(got))])
	}

	// get_memory: the host-side buffer reaches back into the guest. The guest
	// asked for it to be written into its SCRATCH array, so read that back out
	// of the guest's own memory at the offset the guest reports.
	out, err := a.Call(context.Background(), "scratch_ptr")
	if err != nil {
		t.Fatalf("scratch_ptr: %v", err)
	}
	scratch, ok := a.module.Memory().Read(uint32(out[0]), 4)
	if !ok {
		t.Fatalf("could not read the guest's scratch at %d", out[0])
	}
	if string(scratch) != "ping" {
		t.Fatalf("get_memory() left %q in the guest, want \"ping\" - the round trip through the host buffer is broken", scratch)
	}

	// send: the guest's message reaches the configured handler.
	if len(sent) != 1 {
		t.Fatalf("send() delivered %d messages, want 1", len(sent))
	}
	if sent[0].Target != "peer-1" || string(sent[0].Payload) != "ping" {
		t.Fatalf("send() delivered %+v, want target peer-1 payload ping", sent[0])
	}
	if got := a.Sent(); len(got) != 1 || got[0].Target != "peer-1" {
		t.Fatalf("Sent() = %+v, want the one recorded message", got)
	}
}

// With no policy configured, send must be refused and said so - not silently
// dropped, and not delivered to some invented default.
func TestSendIsRefusedWithoutAPolicy(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a, err := New(context.Background(), Config{
		ID:     "no-policy",
		Code:   loadFixture(t, "guest.wasm"),
		Stdout: &stdout,
		Stderr: &stderr,
	}, DefaultMemoryLimits)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer a.Stop(context.Background())

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := stderr.String(); !strings.Contains(got, "no send policy is configured") {
		t.Fatalf("stderr = %q, want a refusal naming the missing policy", got)
	}
	// The attempt is still on the record, which is how an operator sees what a
	// module tried to do.
	if got := a.Sent(); len(got) != 1 || got[0].Target != "peer-1" {
		t.Fatalf("Sent() = %+v, want the refused attempt recorded", got)
	}
}

// The property that matters: one bad module must not take the node with it.
// MaxFuel used to express this and was read by nothing, so `for {}` in a guest
// held the calling goroutine forever.
func TestAGuestThatNeverReturnsIsStopped(t *testing.T) {
	a, err := New(context.Background(), Config{
		ID:   "spin",
		Code: loadFixture(t, "spin.wasm"),
	}, ResourceLimits{MaxMemoryPages: 16, MaxRunTime: 150 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer a.Stop(context.Background())

	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- a.Start(context.Background()) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a guest that never returns reported success")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Start error = %v, want it to wrap context.DeadlineExceeded", err)
		}
		if elapsed := time.Since(started); elapsed > 5*time.Second {
			t.Fatalf("the guest ran for %s before being stopped", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a guest that never returns was never stopped; the run-time limit is not enforced")
	}
}

// New must not run _start itself, or the deadline would not apply to it and a
// spinning guest would hang construction instead of Start.
func TestNewDoesNotRunTheGuest(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		a, err := New(context.Background(), Config{
			ID:   "spin-at-new",
			Code: loadFixture(t, "spin.wasm"),
		}, ResourceLimits{MaxMemoryPages: 16, MaxRunTime: time.Second})
		if err != nil {
			t.Errorf("New: %v", err)
			return
		}
		_ = a.Stop(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("New ran the guest's _start; construction must not execute guest code")
	}
}

func TestResourceLimitsValidate(t *testing.T) {
	cases := []struct {
		name  string
		lim   ResourceLimits
		valid bool
	}{
		{"zero pages", ResourceLimits{MaxMemoryPages: 0}, false},
		{"too many pages", ResourceLimits{MaxMemoryPages: 65537}, false},
		{"negative run time", ResourceLimits{MaxMemoryPages: 1, MaxRunTime: -time.Second}, false},
		{"no deadline is allowed", ResourceLimits{MaxMemoryPages: 1}, true},
		{"defaults", DefaultMemoryLimits, true},
	}
	for _, c := range cases {
		err := c.lim.Validate()
		if c.valid && err != nil {
			t.Errorf("%s: Validate() = %v, want nil", c.name, err)
		}
		if !c.valid && err == nil {
			t.Errorf("%s: Validate() = nil, want an error", c.name)
		}
	}
}

func TestBadCodeIsRejected(t *testing.T) {
	if _, err := New(context.Background(), Config{
		ID:   "garbage",
		Code: []byte("this is not a wasm module"),
	}, DefaultMemoryLimits); err == nil {
		t.Fatal("New accepted bytes that are not a WebAssembly module")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
