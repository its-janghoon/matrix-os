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

The same command builds `flood.wasm`, `spam.wasm` and `escape.wasm` from their
sources.

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

All of them are `#![no_std]`, so the only imports in each module are the four
host functions - which is the property the runtime relies on: a guest cannot open a
socket or read a file because nothing else is in its import table.
