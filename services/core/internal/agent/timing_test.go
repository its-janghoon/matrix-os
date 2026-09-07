package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/tetratelabs/wazero"
)

// A guest cannot read a clock, and that is the property this file pins.
//
// Every other limit in this package bounds what a guest can DO - how long it
// runs, how much memory it maps, how many bytes it logs, which peers it may
// send to. A clock is different: it bounds what a guest can MEASURE. A guest
// runs in the node's own process, alongside the validator's signing key, the
// ledger and the mempool. Give it a nanosecond timer and it can time its own
// host calls and turn any data-dependent branch in the host into a side
// channel, without ever escaping the sandbox or breaking a single one of the
// other limits.
//
// The runtime denies it by omission: no WASI module is instantiated, and the
// host module exports exactly four functions - log, send, get_memory,
// set_memory - none of which return a value. So a guest has no timer and no
// reply channel to build one from.
//
// Omission is a fragile way to hold a security property, which is why these
// tests exist. wasi.MustInstantiate(ctx, r) is the ordinary one-line way to
// make a wasm guest "just work", and it would hand every guest a clock at
// once. No other test here would fail, because no other fixture asks for one.

// TestAGuestThatAsksForAClockIsRefused. clock.wasm imports
// wasi_snapshot_preview1.clock_time_get, so it must fail to instantiate. The
// failure has to happen at load time rather than at the call, because an
// unresolved import is the whole mechanism: there is nothing to trap on later.
func TestAGuestThatAsksForAClockIsRefused(t *testing.T) {
	code := loadFixture(t, "clock.wasm")

	a, err := New(context.Background(), Config{ID: "clock", Code: code}, DefaultMemoryLimits)
	if err == nil {
		_ = a.Stop(context.Background())
		t.Fatal("a guest importing wasi_snapshot_preview1.clock_time_get was accepted; " +
			"it can now time its own host calls, which makes every data-dependent " +
			"branch in the host a side channel")
	}
	// And it must fail for the right reason. A fixture that failed to compile,
	// or one refused for its memory size, would pass a bare error check while
	// proving nothing about the import table.
	if !strings.Contains(err.Error(), "clock_time_get") &&
		!strings.Contains(err.Error(), "wasi_snapshot_preview1") {
		t.Fatalf("refused, but not for the missing clock import: %v", err)
	}
	t.Logf("refused at instantiation: %v", err)
}

// TestTheGuestImportTableIsFourVoidFunctions. Without a clock a guest can
// still build a crude one if any host call hands back an observable result:
// call, observe, count, repeat. This reads guest.wasm's own import table, which
// is the authoritative answer for two questions at once.
//
// The result types in an import table are what the GUEST declares it expects,
// and wazero refuses to instantiate a module whose declaration disagrees with
// the host's real signature. guest.rs calls all four host functions and the
// agent above instantiated it, so the table below is the host's actual surface,
// not a second copy of it that could drift.
func TestTheGuestImportTableIsFourVoidFunctions(t *testing.T) {
	ctx := context.Background()
	code := loadFixture(t, "guest.wasm")

	// The agent instantiates this fixture, which is what makes the declared
	// signatures binding. Assert it here rather than assuming it.
	a, err := New(ctx, Config{ID: "sig", Code: code}, DefaultMemoryLimits)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = a.Stop(ctx) }()

	r := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig())
	defer func() { _ = r.Close(ctx) }()
	compiled, err := r.CompileModule(ctx, code)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}

	want := map[string]bool{"log": true, "send": true, "get_memory": true, "set_memory": true}
	got := map[string]bool{}
	for _, fn := range compiled.ImportedFunctions() {
		module, name, ok := fn.Import()
		if !ok {
			continue
		}
		// Anything outside "env" is a capability this runtime does not provide
		// and did not choose to: a clock, a socket, a file descriptor.
		if module != "env" {
			t.Fatalf("guest.wasm imports %s.%s; the host module is the whole "+
				"capability surface and it is \"env\" alone", module, name)
		}
		if !want[name] {
			t.Fatalf("guest.wasm imports env.%s, which is not one of the four "+
				"host functions", name)
		}
		if results := fn.ResultTypes(); len(results) != 0 {
			t.Fatalf("env.%s returns %d value(s); a host call that answers is a "+
				"channel a guest can count against, which is the timer this "+
				"runtime withholds", name, len(results))
		}
		got[name] = true
	}
	if len(got) != len(want) {
		t.Fatalf("the fixture exercises %d of the four host functions (%v); it has to "+
			"import all four for this test to say anything about all four", len(got), got)
	}
}
