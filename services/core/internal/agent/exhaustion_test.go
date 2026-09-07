package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestTheLogBudgetIsBytesNotLines
//
// THE ATTACK. maxLogLines caps how many lines a guest may write, and its comment
// says that is "so a guest in a loop cannot fill the node's disk through the
// host's logger". It caps the wrong dimension. Each line may be up to
// maxHostCallBytes, so the ceiling is lines x bytes-per-line - almost 10 GiB at
// the constants as written, which is filling the node's disk.
//
// This guest logs a 60 KiB line in a loop. The property is a bound on total
// BYTES written, not on the number of calls.
func TestTheLogBudgetIsBytesNotLines(t *testing.T) {
	code := loadFixture(t, "flood.wasm")

	var out strings.Builder
	a, err := New(context.Background(), Config{
		ID:     "flood",
		Code:   code,
		Stdout: &out,
		Stderr: &out,
	}, ResourceLimits{MaxMemoryPages: 256, MaxRunTime: 30 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = a.Stop(context.Background()) }()

	// The guest may finish or hit the deadline; either is fine. What must hold
	// is the size of what it managed to write.
	_ = a.Start(context.Background())

	written := out.Len()
	if written > 2*maxLogBytes {
		t.Fatalf("a guest wrote %d bytes through the host logger; the budget is %d bytes, and a "+
			"per-line cap alone allows %d", written, maxLogBytes, maxLogLines*maxHostCallBytes)
	}
	if !strings.Contains(out.String(), "log budget") {
		t.Fatalf("the guest was cut off without saying why; an operator seeing truncated output "+
			"needs to know it was a budget:\n%s", tail(out.String(), 300))
	}
}

// TestAnOrdinaryAgentIsNotTruncated. A budget that bites on normal output would
// be its own bug: the fixture that exercises all four host functions writes one
// short line and must come through whole.
func TestAnOrdinaryAgentIsNotTruncated(t *testing.T) {
	code := loadFixture(t, "guest.wasm")

	var out strings.Builder
	a, err := New(context.Background(), Config{
		ID:     "ordinary",
		Code:   code,
		Stdout: &out,
		Stderr: &out,
	}, DefaultMemoryLimits)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = a.Stop(context.Background()) }()

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !strings.Contains(out.String(), "hello from wasm") {
		t.Fatalf("ordinary output was lost:\n%s", out.String())
	}
	if strings.Contains(out.String(), "log budget") {
		t.Fatalf("a one-line agent hit the log budget:\n%s", out.String())
	}
}

// TestStopClosesTheRuntimeEvenWhenTheModuleWillNot
//
// Stop closed the module and returned early on error, which SKIPPED closing the
// runtime. A wazero runtime holds compiled-module memory, so a module that
// reliably fails to close - a guest that always exceeds its deadline is closed
// by the runtime already - leaked one runtime per run. Manager.run does
// `defer a.Stop(ctx)` and discards the error, so nothing upstream would notice.
//
// Calling Stop twice is the cheap way to make the first step fail: the module is
// already gone. The runtime must still be closed.
func TestStopClosesTheRuntimeEvenWhenTheModuleWillNot(t *testing.T) {
	code := loadFixture(t, "guest.wasm")
	a, err := New(context.Background(), Config{ID: "twice", Code: code}, DefaultMemoryLimits)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Close the module out from under Stop, as an interrupted guest does.
	if err := a.module.Close(context.Background()); err != nil {
		t.Fatalf("module.Close: %v", err)
	}

	// Stop must still close the runtime. If it returns early on the module step,
	// the runtime is leaked - and closing it again below would then succeed,
	// which is what this detects.
	_ = a.Stop(context.Background())

	if err := a.runtime.Close(context.Background()); err != nil {
		t.Fatalf("closing the runtime after Stop failed: %v", err)
	}
	if !a.runtimeClosed {
		t.Fatal("Stop did not close the runtime; each agent whose module is already gone leaks one")
	}
}

// TestAStoppedAgentStaysStopped: Stop must be safe to call more than once,
// because Manager.run defers it and a caller that saw a deadline error is told
// to call it too.
func TestAStoppedAgentStaysStopped(t *testing.T) {
	code := loadFixture(t, "guest.wasm")
	a, err := New(context.Background(), Config{ID: "idem", Code: code}, DefaultMemoryLimits)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

// tail returns the last n characters, for an error message that should not dump
// megabytes.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

// TestTheSendLogIsBoundedEvenWhenSendsAreRefused
//
// THE DEFAULT POSTURE WAS A NODE OOM. Every send() attempt was appended to
// sendLog including REFUSED ones - "Refusals are still recorded so a test or an
// operator can see what a module tried to do", which is a good reason to record
// something and not a reason to record everything. A payload may be
// maxHostCallBytes.
//
// Measured with no send policy configured, which is the secure default where
// every send is refused: 20000 refused sends holding 1172 MiB of host memory.
// Nothing was delivered anywhere; the node just held it.
func TestTheSendLogIsBoundedEvenWhenSendsAreRefused(t *testing.T) {
	code := loadFixture(t, "spam.wasm")

	var out strings.Builder
	a, err := New(context.Background(), Config{
		ID:     "spam",
		Code:   code,
		Stdout: &out,
		Stderr: &out,
		// No Send: the default, where every send is refused.
	}, ResourceLimits{MaxMemoryPages: 256, MaxRunTime: 30 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = a.Stop(context.Background()) }()

	_ = a.Start(context.Background())

	sent := a.Sent()
	var held int
	for _, m := range sent {
		held += len(m.Target) + len(m.Payload)
	}
	if len(sent) > maxSendLog {
		t.Fatalf("sendLog retained %d entries, cap is %d", len(sent), maxSendLog)
	}
	if held > 2*maxSendLogBytes {
		t.Fatalf("sendLog holds %d bytes, budget is %d: a guest whose sends are all REFUSED can "+
			"still allocate the node's memory", held, maxSendLogBytes)
	}
	// The count must survive even though the contents do not, or a bounded log
	// would hide what the module did - which is the whole reason refusals are
	// recorded.
	if a.SendsDropped() == 0 {
		t.Fatal("no attempts were counted as dropped; the bound would be hiding what the module tried")
	}
	if total := len(sent) + a.SendsDropped(); total < 20000 {
		t.Fatalf("counted %d attempts in total, want the 20000 the guest made: a bounded log must "+
			"still be able to say how many there were", total)
	}
}

// TestAnOrdinaryAgentsSendsAreAllRetained. The bound must not lose the sends of
// a normal module: the fixture that sends once must show up whole, contents
// included.
func TestAnOrdinaryAgentsSendsAreAllRetained(t *testing.T) {
	code := loadFixture(t, "guest.wasm")
	a, err := New(context.Background(), Config{ID: "one-send", Code: code}, DefaultMemoryLimits)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = a.Stop(context.Background()) }()
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	sent := a.Sent()
	if len(sent) != 1 {
		t.Fatalf("retained %d sends, want 1", len(sent))
	}
	if sent[0].Target != "peer-1" || string(sent[0].Payload) != "ping" {
		t.Fatalf("the retained send lost its contents: %+v", sent[0])
	}
	if a.SendsDropped() != 0 {
		t.Fatalf("a single send reported %d drops", a.SendsDropped())
	}
}

// TestAGuestCannotReadOutsideItsOwnMemory
//
// The host functions take (offset, length) into the GUEST's linear memory. If
// they trusted those numbers, a guest would name a range outside its memory and
// the host would hand it back whatever was next to it - the classic sandbox
// escape for this ABI shape.
//
// This fixture asks for ranges past the end of its memory, a range that wraps
// u32 arithmetic, and a length over the per-call cap, through all four host
// functions. Every one must be refused, the guest must keep running (the ABI
// has no way to return an error to it), and nothing must be handed across.
func TestAGuestCannotReadOutsideItsOwnMemory(t *testing.T) {
	code := loadFixture(t, "escape.wasm")

	var stdout, stderr strings.Builder
	sends := 0
	a, err := New(context.Background(), Config{
		ID:     "escape",
		Code:   code,
		Stdout: &stdout,
		Stderr: &stderr,
		Send: func(string, []byte) error {
			sends++
			return nil
		},
	}, DefaultMemoryLimits)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = a.Stop(context.Background()) }()

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("the guest should keep running after refused calls, not trap: %v", err)
	}

	// Nothing crossed the boundary: no log line, no send, and the host buffer
	// untouched.
	if stdout.Len() != 0 {
		t.Fatalf("an out-of-range log produced output, so something was read that should not "+
			"have been:\n%s", tail(stdout.String(), 300))
	}
	if sends != 0 {
		t.Fatalf("%d out-of-range sends were delivered", sends)
	}
	for i, b := range a.Memory() {
		if b != 0 {
			t.Fatalf("the host buffer holds a non-zero byte at %d after only refused calls", i)
		}
	}

	// And every refusal was reported, so an operator can see what the module
	// tried rather than the calls vanishing.
	refusals := strings.Count(stderr.String(), "refused")
	if refusals < 6 {
		t.Fatalf("only %d refusals were reported for 7 out-of-range calls:\n%s",
			refusals, stderr.String())
	}
	// The attempts are still recorded, including the ones that never resolved.
	if len(a.Sent()) != 0 {
		t.Fatalf("an out-of-range send was recorded as a message: %+v", a.Sent())
	}
}
