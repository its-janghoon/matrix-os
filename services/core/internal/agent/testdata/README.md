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

- `guest.rs` calls all four host functions and exports `scratch_ptr` so the test
  can find where `get_memory` was asked to write.
- `spin.rs` never returns from `_start`. It is what proves the run-time deadline
  is enforced rather than merely configured.

Both are `#![no_std]`, so the only imports in the module are the four host
functions - which is the property the runtime relies on: a guest cannot open a
socket or read a file because nothing else is in its import table.
