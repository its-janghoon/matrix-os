# Test fixtures for internal/agent

`guest.wasm` and `spin.wasm` are committed as binaries rather than built during
the test run, so the test needs no Rust toolchain and CI stays hermetic. Their
sources are next to them.

Rebuild after editing either source:

```sh
rustup target add wasm32-unknown-unknown
rustc --target wasm32-unknown-unknown -O --crate-type cdylib \
  -C link-arg=--no-entry -o guest.wasm guest.rs
rustc --target wasm32-unknown-unknown -O --crate-type cdylib \
  -C link-arg=--no-entry -o spin.wasm spin.rs
```

The same command builds `flood.wasm`, `spam.wasm`, `escape.wasm` and
`clock.wasm` from their sources.

- `guest.rs` calls all four host functions and exports `scratch_ptr` so the test
  can find where `get_memory` was asked to write.
- `spin.rs` never returns from `_start`. It is what proves the run-time deadline
  is enforced rather than merely configured.
- `flood.rs` logs a 60 KiB line in a loop. It is what measured the log flood:
  586 MiB written from one run while the line cap was doing its job, which is
  how the byte budget came to exist.
- `spam.rs` calls `send()` in a loop. Run with no send policy - the secure
  default, where every send is refused - it held 1172 MiB in the send log,
  because refusals were recorded in full.
- `escape.rs` asks the host functions for memory ranges outside its own linear
  memory: past the end, wrapping u32, and over the per-call cap. Every one is
  refused, which is the sandbox boundary this ABI shape depends on.
- `clock.rs` is the one fixture that is meant to be REJECTED. It imports
  `wasi_snapshot_preview1.clock_time_get`, so it must fail to instantiate. It
  guards the timing boundary rather than the memory one: a guest with a
  nanosecond clock runs in the node's process and can time its own host calls,
  which turns any data-dependent branch in the host into a side channel without
  breaking a single other limit. The regression it exists for is one line -
  `wasi.MustInstantiate(ctx, r)` - and it was verified that adding that line
  leaves every other test in this package passing.

Every fixture except `clock.rs` imports only the four host functions - which is
the property the runtime relies on: a guest cannot open a socket, read a file or
read a clock because nothing else is in its import table. `clock.rs` asks for a
fifth import precisely to prove the table is enforced rather than merely
observed, since a fixture that does not ask for a capability says nothing about
whether it would be granted.
