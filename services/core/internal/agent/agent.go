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

// Sent returns the send() calls the guest made, in order, whether or not the
// SendFunc accepted them. It is how a caller sees what a module tried to do
// when sends are refused.
func (a *Agent) Sent() []Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Message, len(a.sendLog))
	for i, m := range a.sendLog {
		out[i] = Message{Target: m.Target, Payload: append([]byte(nil), m.Payload...)}
	}
	return out
}

// Stop gracefully shuts down the agent
func (a *Agent) Stop(ctx context.Context) error {
	if err := a.module.Close(ctx); err != nil {
		return fmt.Errorf("failed to close module: %w", err)
	}
	if err := a.runtime.Close(ctx); err != nil {
		return fmt.Errorf("failed to close runtime: %w", err)
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

// maxLogLines caps how many lines one agent may write, so a guest in a loop
// cannot fill the node's disk through the host's logger.
const maxLogLines = 10000

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

// hostLog writes a guest's bytes to the agent's stdout, one line per call.
func (a *Agent) hostLog(_ context.Context, m api.Module, offset, length uint32) {
	buf, ok := a.readGuest(m, "log", offset, length)
	if !ok {
		return
	}
	a.mu.Lock()
	if a.logLines >= maxLogLines {
		a.mu.Unlock()
		return
	}
	a.logLines++
	last := a.logLines == maxLogLines
	a.mu.Unlock()

	fmt.Fprintf(a.stdout, "[%s] %s\n", a.ID, buf)
	if last {
		fmt.Fprintf(a.stdout, "[%s] log limit of %d lines reached; further log calls are dropped\n", a.ID, maxLogLines)
	}
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
	a.sendLog = append(a.sendLog, Message{Target: string(target), Payload: payload})
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
