package agent

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// DefaultMemoryLimits defines default resource constraints.
var DefaultMemoryLimits = ResourceLimits{
	MaxMemoryPages: 256, // 16MB (256 * 64KB)
	MaxRunTime:     5 * time.Second,
}

// DefaultHostBufferSize is the size of the host-side buffer set_memory writes
// into and get_memory reads from, when Config.MemSize does not say.
const DefaultHostBufferSize = 64 << 10

// Agent represents a WebAssembly agent
type Agent struct {
	ID      string
	module  api.Module
	runtime wazero.Runtime
	// memory is the host-side buffer the guest reaches through set_memory and
	// get_memory. It is deliberately separate from the guest's own linear
	// memory: it survives across calls, and the guest can only move bytes in and
	// out of it through those two functions rather than addressing it directly.
	memory []byte
	// mu guards memory, which the host functions read and write while a guest
	// call is in flight and which a caller may inspect through Memory().
	mu sync.Mutex

	stdout   io.Writer
	stderr   io.Writer
	send     SendFunc
	maxRun   time.Duration
	sendLog  []Message
	logLines int
	// logBytes is what the guest has written through the host logger so far,
	// which is the quantity that fills a disk. See maxLogBytes.
	logBytes int
	// runtimeClosed records that Stop closed the wazero runtime, so Stop is
	// idempotent and a caller can tell the runtime was released.
	runtimeClosed bool
	moduleClosed  bool
	// budgetAnnounced keeps the "budget reached" notice to one line, so the
	// notice cannot itself become the flood.
	budgetAnnounced bool
	// sendBytes is the payload bytes sendLog currently retains, and sendDropped
	// counts the attempts it stopped retaining, so the count is never lost even
	// when the contents are.
	sendBytes   int
	sendDropped int
}

// Message is one send() call a guest made.
type Message struct {
	Target  string
	Payload []byte
}

// SendFunc handles a guest's send() call. Returning an error is reported to the
// guest's stderr; the guest itself gets no return value, because the host ABI
// has none.
//
// There is no default. A host function that can address other agents needs a
// policy - who may be named, and what a name means - and inventing one here
// would be worse than having none, so a Config without a SendFunc refuses every
// send and says so. Refusals are still recorded (see Sent) so a test or an
// operator can see what a module tried to do.
type SendFunc func(target string, payload []byte) error

// Config represents agent configuration.
type Config struct {
	ID     string
	Code   []byte
	Stdout io.Writer
	Stderr io.Writer
	// MemSize is the size of the host-side buffer behind set_memory and
	// get_memory. Zero means DefaultHostBufferSize.
	MemSize uint32
	// Send handles the guest's send() calls. Nil refuses them; see SendFunc.
	Send SendFunc
}

// ResourceLimits defines resource constraints for an agent.
type ResourceLimits struct {
	MaxMemoryPages uint32 // Number of 64KB pages
	// MaxRunTime bounds how long one call into the guest may take before the
	// runtime tears it down.
	//
	// This field replaces a MaxFuel counter that nothing ever read. wazero has
	// no instruction meter to spend such a budget against, so the number was
	// decoration: `for {}` in a guest held the calling goroutine forever and
	// there was no ceiling of any kind on execution. A deadline is a bound the
	// runtime can actually enforce, and it enforces the property that matters -
	// one bad module cannot take the node with it.
	MaxRunTime time.Duration
}

// Validate checks if the resource limits are within acceptable ranges.
func (l ResourceLimits) Validate() error {
	if l.MaxMemoryPages == 0 {
		return fmt.Errorf("MaxMemoryPages must be greater than 0")
	}
	if l.MaxMemoryPages > 65536 {
		return fmt.Errorf("MaxMemoryPages exceeds maximum allowed (65536)")
	}
	if l.MaxRunTime < 0 {
		return fmt.Errorf("MaxRunTime must not be negative")
	}
	return nil
}

// New creates a new Agent instance
func New(ctx context.Context, cfg Config, limits ResourceLimits) (*Agent, error) {
	// Validate resource limits
	if err := limits.Validate(); err != nil {
		return nil, fmt.Errorf("invalid resource limits: %w", err)
	}

	// Create WebAssembly runtime with memory tuning.
	//
	// WithCloseOnContextDone is what makes MaxRunTime enforceable: it lets wazero
	// interrupt a running guest when the call's context is done, so a guest that
	// never returns is torn down instead of holding the goroutine forever.
	rConfig := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(limits.MaxMemoryPages).
		WithCloseOnContextDone(true)

	r := wazero.NewRuntimeWithConfig(ctx, rConfig)

	stdout := cfg.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := cfg.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	memSize := cfg.MemSize
	if memSize == 0 {
		memSize = DefaultHostBufferSize
	}

	// The agent exists before the host module does, because the host functions
	// are closures over it: they need its buffer, its writers and its send
	// policy. They used to be package-level functions with empty bodies, which
	// is what an ABI with nothing behind it looks like - a guest could call
	// log() and get silence.
	a := &Agent{
		ID:      cfg.ID,
		runtime: r,
		memory:  make([]byte, memSize),
		stdout:  stdout,
		stderr:  stderr,
		send:    cfg.Send,
		maxRun:  limits.MaxRunTime,
	}

	builder := r.NewHostModuleBuilder("env")

	builder.NewFunctionBuilder().
		WithFunc(a.hostLog).
		Export("log")

	builder.NewFunctionBuilder().
		WithFunc(a.hostSend).
		Export("send")

	builder.NewFunctionBuilder().
		WithFunc(a.hostGetMemory).
		Export("get_memory")

	builder.NewFunctionBuilder().
		WithFunc(a.hostSetMemory).
		Export("set_memory")

	// Instantiate host module
	if _, err := builder.Instantiate(ctx); err != nil {
		return nil, fmt.Errorf("failed to instantiate host module: %w", err)
	}

	// Compile WebAssembly module
	compiled, err := r.CompileModule(ctx, cfg.Code)
	if err != nil {
		return nil, fmt.Errorf("failed to compile module: %w", err)
	}

	// Configure module. WithStartFunctions is emptied so instantiation does not
	// run _start: Start runs it, under the deadline. Left to instantiation it
	// would run here, with only the caller's context to bound it, and a guest
	// that never returns would hang New rather than Start.
	moduleConfig := wazero.NewModuleConfig().
		WithName(cfg.ID).
		WithStdout(stdout).
		WithStderr(stderr).
		WithStartFunctions()

	// Instantiate module
	module, err := r.InstantiateModule(ctx, compiled, moduleConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate module: %w", err)
	}
	a.module = module

	return a, nil
}

// callCtx applies MaxRunTime to a call into the guest. A zero MaxRunTime means
// no deadline, which is only appropriate for a module the operator wrote.
func (a *Agent) callCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if a.maxRun <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, a.maxRun)
}

// Start runs the module's _start export, if it has one, under MaxRunTime.
//
// A guest that exceeds the deadline is interrupted and the error wraps
// context.DeadlineExceeded, so a caller can tell "this module runs forever"
// from "this module trapped". The module is not usable afterwards - wazero
// closes it on interruption - so treat that error as terminal and Stop.
func (a *Agent) Start(ctx context.Context) error {
	start := a.module.ExportedFunction("_start")
	if start == nil {
		return nil
	}
	callCtx, cancel := a.callCtx(ctx)
	defer cancel()
	if _, err := start.Call(callCtx); err != nil {
		if cerr := callCtx.Err(); cerr != nil {
			return fmt.Errorf("agent %q exceeded its %s run time: %w", a.ID, a.maxRun, cerr)
		}
		return fmt.Errorf("failed to call _start: %w", err)
	}
	return nil
}

// Call invokes an exported function by name under the same deadline as Start,
// and returns whatever the guest returned.
func (a *Agent) Call(ctx context.Context, name string, params ...uint64) ([]uint64, error) {
	fn := a.module.ExportedFunction(name)
	if fn == nil {
		return nil, fmt.Errorf("agent %q exports no function %q", a.ID, name)
	}
	callCtx, cancel := a.callCtx(ctx)
	defer cancel()
	out, err := fn.Call(callCtx, params...)
	if err != nil {
		if cerr := callCtx.Err(); cerr != nil {
			return nil, fmt.Errorf("agent %q exceeded its %s run time in %q: %w", a.ID, a.maxRun, name, cerr)
		}
		return nil, fmt.Errorf("agent %q: call %q: %w", a.ID, name, err)
	}
	return out, nil
}

// Memory returns a copy of the host-side buffer behind set_memory and
// get_memory. A copy, not the buffer, so a caller cannot mutate what a running
// guest is reading.
func (a *Agent) Memory() []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]byte(nil), a.memory...)
}

// SendsDropped returns how many send() attempts were counted but not retained
// because Sent()'s budget was spent. It is what keeps a bounded log honest: a
// caller can tell "this module sent three messages" from "this module sent
// three and then a hundred thousand more".
func (a *Agent) SendsDropped() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sendDropped
}

// Sent returns the send() calls the guest made, in order, whether or not the
// SendFunc accepted them. It is how a caller sees what a module tried to do
// when sends are refused.
//
// It is BOUNDED (see maxSendLog): a guest looping on send() has its later
// attempts counted by SendsDropped rather than retained, because retaining them
// was over a gigabyte of host memory in the default posture.
func (a *Agent) Sent() []Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Message, len(a.sendLog))
	for i, m := range a.sendLog {
		out[i] = Message{Target: m.Target, Payload: append([]byte(nil), m.Payload...)}
	}
	return out
}

// Stop shuts down the agent, closing BOTH the module and the runtime.
//
// It used to close the module and return early on error, which skipped closing
// the runtime. That is not a cosmetic ordering issue: a wazero runtime holds the
// compiled module's memory, and the module is exactly what fails to close after
// a guest was interrupted for exceeding its deadline - wazero has already closed
// it. So a module that reliably times out leaked one runtime per run, and
// Manager.run does `defer a.Stop(ctx)` discarding the error, so nothing
// upstream would ever notice.
//
// The runtime is now closed regardless, both errors are reported, and Stop is
// idempotent: Manager.run defers it and a caller told to treat a deadline error
// as terminal calls it too.
func (a *Agent) Stop(ctx context.Context) error {
	a.mu.Lock()
	moduleDone, runtimeDone := a.moduleClosed, a.runtimeClosed
	a.mu.Unlock()

	var moduleErr, runtimeErr error
	if !moduleDone {
		moduleErr = a.module.Close(ctx)
		a.mu.Lock()
		a.moduleClosed = true
		a.mu.Unlock()
	}
	if !runtimeDone {
		runtimeErr = a.runtime.Close(ctx)
		a.mu.Lock()
		a.runtimeClosed = true
		a.mu.Unlock()
	}

	switch {
	case moduleErr != nil && runtimeErr != nil:
		return fmt.Errorf("failed to close module (%v) and runtime: %w", moduleErr, runtimeErr)
	case moduleErr != nil:
		return fmt.Errorf("failed to close module: %w", moduleErr)
	case runtimeErr != nil:
		return fmt.Errorf("failed to close runtime: %w", runtimeErr)
	}
	return nil
}

// Host functions exposed to WebAssembly modules.
//
// Each takes a pointer and a length into the GUEST's linear memory, the usual
// wasm convention for passing bytes across the boundary. None of them trusts
// those numbers: api.Memory.Read returns ok=false for a range that is not
// wholly inside the guest's memory, so an out-of-range pointer is a refused
// call rather than a read of whatever happens to be next to it. The host ABI
// has no return values, so a refusal is reported to stderr and the guest keeps
// running - it cannot be handed an error it has no way to receive.

// maxHostCallBytes caps one log line or one message. Without a ceiling a guest
// could ask the host to materialise its whole address space per call.
const maxHostCallBytes = 1 << 20

// maxLogLines caps how many lines one agent may write.
const maxLogLines = 10000

// maxLogBytes caps the TOTAL bytes one agent may write through the host logger.
//
// WHY BOTH. The line cap alone claimed to stop "a guest in a loop [filling] the
// node's disk", and it capped the wrong dimension: a line may be up to
// maxHostCallBytes, so lines x bytes-per-line is nearly 10 GiB. Measured with a
// guest logging a 60 KiB line in a loop: 586 MiB written from ONE run, with the
// line cap doing its job the whole time.
//
// 4 MiB is generous for an agent's own diagnostics and 150x smaller than that
// measurement. The line cap stays because the two bound different things: bytes
// bound the disk, lines bound the number of writes to a possibly-slow writer.
const maxLogBytes = 4 << 20

// maxSendLog and maxSendLogBytes bound what Sent() retains.
//
// WHY. Every send() attempt was appended to sendLog, INCLUDING refused ones -
// "Refusals are still recorded (see Sent) so a test or an operator can see what
// a module tried to do", which is a good reason to record something and not a
// reason to record everything. A payload may be maxHostCallBytes, so a guest
// looping on send() grew the log without limit.
//
// Measured in the DEFAULT posture, where every send is refused because no
// policy is configured: 20000 refused sends holding 1172 MiB of host memory.
// The secure default was a node OOM.
//
// The purpose is served by the first attempts plus a count of the rest: an
// operator looking at what a module tried does not need the thousandth copy,
// and for a REFUSED send the payload was never delivered anywhere, so retaining
// a megabyte of it buys nothing at all.
const (
	maxSendLog      = 1000
	maxSendLogBytes = 1 << 20
)

// readGuest copies length bytes at offset out of the guest's memory. It reports
// false, having written the reason to stderr, when the range is not readable.
func (a *Agent) readGuest(m api.Module, what string, offset, length uint32) ([]byte, bool) {
	if length > maxHostCallBytes {
		a.warn("%s: refused %d bytes, over the %d-byte limit", what, length, maxHostCallBytes)
		return nil, false
	}
	mem := m.Memory()
	if mem == nil {
		a.warn("%s: the module exports no memory", what)
		return nil, false
	}
	buf, ok := mem.Read(offset, length)
	if !ok {
		a.warn("%s: refused an out-of-range read of %d bytes at %d", what, length, offset)
		return nil, false
	}
	// Read may alias the guest's memory, so copy before the guest can change it.
	return append([]byte(nil), buf...), true
}

// warn reports a refused host call on the agent's stderr.
func (a *Agent) warn(format string, args ...any) {
	fmt.Fprintf(a.stderrOr(), "agent %s: "+format+"\n", append([]any{a.ID}, args...)...)
}

func (a *Agent) stderrOr() io.Writer {
	if a.stderr == nil {
		return io.Discard
	}
	return a.stderr
}

// hostLog writes a guest's bytes to the agent's stdout, one line per call,
// within both the line and the byte budget.
func (a *Agent) hostLog(_ context.Context, m api.Module, offset, length uint32) {
	buf, ok := a.readGuest(m, "log", offset, length)
	if !ok {
		return
	}

	// The prefix and newline are bytes on the disk too, so they are charged.
	cost := len("[") + len(a.ID) + len("] ") + len(buf) + len("\n")

	a.mu.Lock()
	overLines := a.logLines >= maxLogLines
	overBytes := a.logBytes+cost > maxLogBytes
	if overLines || overBytes {
		// Say it once, then go quiet. Repeating the notice for every dropped
		// call would itself be the flood.
		announce := !a.budgetAnnounced
		a.budgetAnnounced = true
		reason := fmt.Sprintf("byte budget of %d", maxLogBytes)
		if overLines {
			reason = fmt.Sprintf("line limit of %d", maxLogLines)
		}
		a.mu.Unlock()
		if announce {
			fmt.Fprintf(a.stdout, "[%s] log budget reached (%s); further log calls are dropped\n",
				a.ID, reason)
		}
		return
	}
	a.logLines++
	a.logBytes += cost
	a.mu.Unlock()

	fmt.Fprintf(a.stdout, "[%s] %s\n", a.ID, buf)
}

// hostSend hands a guest's message to the configured SendFunc, and records the
// attempt either way.
func (a *Agent) hostSend(_ context.Context, m api.Module, targetOffset, targetLength, msgOffset, msgLength uint32) {
	target, ok := a.readGuest(m, "send target", targetOffset, targetLength)
	if !ok {
		return
	}
	payload, ok := a.readGuest(m, "send payload", msgOffset, msgLength)
	if !ok {
		return
	}

	a.mu.Lock()
	// Bounded. The attempt is always COUNTED; only the retained copy is dropped
	// once the budget is spent. See maxSendLog.
	cost := len(target) + len(payload)
	if len(a.sendLog) < maxSendLog && a.sendBytes+cost <= maxSendLogBytes {
		a.sendLog = append(a.sendLog, Message{Target: string(target), Payload: payload})
		a.sendBytes += cost
	} else {
		a.sendDropped++
	}
	handler := a.send
	a.mu.Unlock()

	if handler == nil {
		a.warn("send to %q refused: no send policy is configured", target)
		return
	}
	if err := handler(string(target), payload); err != nil {
		a.warn("send to %q failed: %v", target, err)
	}
}

// hostGetMemory copies the host-side buffer into the guest's memory at offset.
func (a *Agent) hostGetMemory(_ context.Context, m api.Module, offset, length uint32) {
	if length > maxHostCallBytes {
		a.warn("get_memory: refused %d bytes, over the %d-byte limit", length, maxHostCallBytes)
		return
	}
	a.mu.Lock()
	if int(length) > len(a.memory) {
		size := len(a.memory)
		a.mu.Unlock()
		a.warn("get_memory: refused %d bytes, the host buffer holds %d", length, size)
		return
	}
	out := append([]byte(nil), a.memory[:length]...)
	a.mu.Unlock()

	mem := m.Memory()
	if mem == nil {
		a.warn("get_memory: the module exports no memory")
		return
	}
	if !mem.Write(offset, out) {
		a.warn("get_memory: refused an out-of-range write of %d bytes at %d", length, offset)
	}
}

// hostSetMemory copies bytes out of the guest into the host-side buffer.
func (a *Agent) hostSetMemory(_ context.Context, m api.Module, offset, length uint32) {
	buf, ok := a.readGuest(m, "set_memory", offset, length)
	if !ok {
		return
	}
	a.mu.Lock()
	size := len(a.memory)
	tooBig := len(buf) > size
	if !tooBig {
		copy(a.memory, buf)
		// Zero the tail so a short write cannot leave a previous, longer value
		// visible behind it.
		for i := len(buf); i < size; i++ {
			a.memory[i] = 0
		}
	}
	a.mu.Unlock()

	if tooBig {
		a.warn("set_memory: refused %d bytes, the host buffer holds %d", len(buf), size)
	}
}
