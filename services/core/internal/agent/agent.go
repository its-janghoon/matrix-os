package agent

import (
	"context"
	"fmt"
	"io"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// DefaultMemoryLimits defines default resource constraints
var DefaultMemoryLimits = ResourceLimits{
	MaxMemoryPages: 256, // 16MB (256 * 64KB)
	MaxFuel:        1000000,
}

// Agent represents a WebAssembly agent
type Agent struct {
	ID      string
	module  api.Module
	runtime wazero.Runtime
	memory  []byte
}

// Config represents agent configuration
type Config struct {
	ID      string
	Code    []byte
	Stdout  io.Writer
	Stderr  io.Writer
	MemSize uint32
}

// ResourceLimits defines resource constraints for an agent
type ResourceLimits struct {
	MaxMemoryPages uint32 // Number of 64KB pages
	MaxFuel        uint64
}

// Validate checks if the resource limits are within acceptable ranges
func (l ResourceLimits) Validate() error {
	if l.MaxMemoryPages == 0 {
		return fmt.Errorf("MaxMemoryPages must be greater than 0")
	}
	if l.MaxMemoryPages > 65536 {
		return fmt.Errorf("MaxMemoryPages exceeds maximum allowed (65536)")
	}
	return nil
}

// New creates a new Agent instance
func New(ctx context.Context, cfg Config, limits ResourceLimits) (*Agent, error) {
	// Validate resource limits
	if err := limits.Validate(); err != nil {
		return nil, fmt.Errorf("invalid resource limits: %w", err)
	}

	// Create WebAssembly runtime with memory tuning
	rConfig := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(limits.MaxMemoryPages)

	r := wazero.NewRuntimeWithConfig(ctx, rConfig)

	// Configure module
	builder := r.NewHostModuleBuilder("env")

	// Add host functions
	builder.NewFunctionBuilder().
		WithFunc(hostLog).
		Export("log")

	builder.NewFunctionBuilder().
		WithFunc(hostSend).
		Export("send")

	builder.NewFunctionBuilder().
		WithFunc(hostGetMemory).
		Export("get_memory")

	builder.NewFunctionBuilder().
		WithFunc(hostSetMemory).
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

	// Configure module
	moduleConfig := wazero.NewModuleConfig().
		WithName(cfg.ID).
		WithStdout(cfg.Stdout).
		WithStderr(cfg.Stderr)

	// Instantiate module
	module, err := r.InstantiateModule(ctx, compiled, moduleConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate module: %w", err)
	}

	// Initialize agent memory buffer
	memSize := cfg.MemSize
	if memSize == 0 {
		memSize = uint32(limits.MaxMemoryPages) * 65536 // Default to max WebAssembly memory
	}

	return &Agent{
		ID:      cfg.ID,
		module:  module,
		runtime: r,
		memory:  make([]byte, memSize),
	}, nil
}

// Start initializes and starts the agent
func (a *Agent) Start(ctx context.Context) error {
	// Call _start function if it exists
	start := a.module.ExportedFunction("_start")
	if start != nil {
		if _, err := start.Call(ctx); err != nil {
			return fmt.Errorf("failed to call _start: %w", err)
		}
	}
	return nil
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

// Host functions exposed to WebAssembly modules

func hostLog(ctx context.Context, m api.Module, offset, length uint32) {
	// Implementation for logging from WebAssembly
}

func hostSend(ctx context.Context, m api.Module, targetOffset, targetLength, msgOffset, msgLength uint32) {
	// Implementation for sending messages between agents
}

func hostGetMemory(ctx context.Context, m api.Module, offset, length uint32) {
	// Implementation for reading from agent memory
}

func hostSetMemory(ctx context.Context, m api.Module, offset, length uint32) {
	// Implementation for writing to agent memory
}
