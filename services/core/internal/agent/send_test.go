package agent

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// The zero SendPolicy is the secure default: it permits nothing, whether or not
// a name is named, so a node that has not opted in behaves exactly as one with
// a nil SendFunc.
func TestSendPolicyZeroValueRefusesEverything(t *testing.T) {
	var p SendPolicy
	if p.Permits("peer-1") {
		t.Fatal("the zero SendPolicy permitted a target; the default must refuse everything")
	}
	// Enabling alone, with no allowlist, still refuses everything.
	p.Enabled = true
	if p.Permits("peer-1") {
		t.Fatal("an enabled policy with an empty allowlist permitted a target; it must refuse everything")
	}
}

func TestSendPolicyPermitsOnlyAllowlisted(t *testing.T) {
	p := SendPolicy{Enabled: true, Allow: []string{"peer-1", "peer-2"}}
	if !p.Permits("peer-1") {
		t.Fatal("an allowlisted target was not permitted")
	}
	if p.Permits("peer-3") {
		t.Fatal("a target not on the allowlist was permitted; there is no wildcard")
	}
}

// NewSendFunc must return nil (the runtime's "refuse and say so" posture) for
// any policy that permits nothing, so a caller can build a SendFunc
// unconditionally and get the safe default for free.
func TestNewSendFuncNilWhenNothingPermitted(t *testing.T) {
	if NewSendFunc(SendPolicy{}, nil) != nil {
		t.Fatal("NewSendFunc returned a handler for the zero policy; it must be nil (refuse all)")
	}
	if NewSendFunc(SendPolicy{Enabled: true}, nil) != nil {
		t.Fatal("NewSendFunc returned a handler for an enabled policy with an empty allowlist; it must be nil")
	}
	if NewSendFunc(SendPolicy{Allow: []string{"peer-1"}}, nil) != nil {
		t.Fatal("NewSendFunc returned a handler for a disabled policy; it must be nil")
	}
}

func TestNewSendFuncDeliversPermittedAndRefusesRest(t *testing.T) {
	var delivered []Message
	deliver := DelivererFunc(func(target string, payload []byte) error {
		delivered = append(delivered, Message{Target: target, Payload: append([]byte(nil), payload...)})
		return nil
	})
	send := NewSendFunc(SendPolicy{Enabled: true, Allow: []string{"peer-1"}}, deliver)
	if send == nil {
		t.Fatal("NewSendFunc returned nil for a policy that permits a target")
	}
	// A permitted target is delivered and returns nil.
	if err := send("peer-1", []byte("ping")); err != nil {
		t.Fatalf("permitted send returned %v, want nil", err)
	}
	if len(delivered) != 1 || delivered[0].Target != "peer-1" || string(delivered[0].Payload) != "ping" {
		t.Fatalf("delivered = %+v, want one message to peer-1 with payload ping", delivered)
	}
	// A non-permitted target returns an error and is NOT delivered.
	if err := send("peer-9", []byte("ping")); err == nil {
		t.Fatal("a non-permitted send returned nil; it must return an error")
	}
	if len(delivered) != 1 {
		t.Fatalf("a non-permitted send was delivered: %+v", delivered)
	}
}

// A permitted target whose delivery is not configured is a refusal, not a
// permissive fall-through.
func TestNewSendFuncPermittedButNoDeliverer(t *testing.T) {
	send := NewSendFunc(SendPolicy{Enabled: true, Allow: []string{"peer-1"}}, nil)
	if send == nil {
		t.Fatal("NewSendFunc returned nil for a policy that permits a target")
	}
	if err := send("peer-1", []byte("ping")); err == nil {
		t.Fatal("a permitted send with no deliverer returned nil; it must refuse with an error")
	}
}

// End to end through the runtime: the guest.wasm fixture calls send("peer-1",
// "ping"). Under a policy that permits peer-1 it is delivered; the attempt is
// recorded either way.
func TestHostSendDeliversUnderAPermittingPolicy(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var delivered []Message
	send := NewSendFunc(
		SendPolicy{Enabled: true, Allow: []string{"peer-1"}},
		DelivererFunc(func(target string, payload []byte) error {
			delivered = append(delivered, Message{Target: target, Payload: append([]byte(nil), payload...)})
			return nil
		}),
	)
	a, err := New(context.Background(), Config{
		ID:     "sender",
		Code:   loadFixture(t, "guest.wasm"),
		Stdout: &stdout,
		Stderr: &stderr,
		Send:   send,
	}, DefaultMemoryLimits)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer a.Stop(context.Background())
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(delivered) != 1 || delivered[0].Target != "peer-1" || string(delivered[0].Payload) != "ping" {
		t.Fatalf("delivered = %+v, want peer-1/ping", delivered)
	}
	if got := a.Sent(); len(got) != 1 || got[0].Target != "peer-1" {
		t.Fatalf("Sent() = %+v, want the recorded attempt", got)
	}
	if strings.Contains(stderr.String(), "refused") || strings.Contains(stderr.String(), "not permitted") {
		t.Fatalf("stderr reported a refusal for a permitted send: %q", stderr.String())
	}
}

// The guest.wasm fixture sends to "peer-1"; a policy that permits only "peer-9"
// must refuse it, report the refusal to stderr, and still record the attempt.
func TestHostSendRefusesNonPermittedUnderAPolicy(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var delivered []Message
	send := NewSendFunc(
		SendPolicy{Enabled: true, Allow: []string{"peer-9"}},
		DelivererFunc(func(target string, payload []byte) error {
			delivered = append(delivered, Message{Target: target})
			return nil
		}),
	)
	a, err := New(context.Background(), Config{
		ID:     "sender",
		Code:   loadFixture(t, "guest.wasm"),
		Stdout: &stdout,
		Stderr: &stderr,
		Send:   send,
	}, DefaultMemoryLimits)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer a.Stop(context.Background())
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(delivered) != 0 {
		t.Fatalf("a non-permitted send was delivered: %+v", delivered)
	}
	if !strings.Contains(stderr.String(), "not permitted") {
		t.Fatalf("stderr = %q, want a refusal saying the target is not permitted", stderr.String())
	}
	if got := a.Sent(); len(got) != 1 || got[0].Target != "peer-1" {
		t.Fatalf("Sent() = %+v, want the refused attempt recorded", got)
	}
}

// Guard the doc claim that Permits enforces the allowlist exactly.
func TestSendPolicyPermitsIsExact(t *testing.T) {
	p := SendPolicy{Enabled: true, Allow: []string{"peer-1"}}
	for _, bad := range []string{"", "peer", "peer-10", "PEER-1", " peer-1"} {
		if p.Permits(bad) {
			t.Fatalf("Permits(%q) = true, want false (allowlist is exact)", bad)
		}
	}
}
