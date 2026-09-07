# Test fixtures for internal/admin

`guest.wasm` is a copy of `../../agent/testdata/guest.wasm` - a real
WebAssembly module that calls all four host functions. It is copied rather than
referenced across packages so `go test ./internal/admin/` works from a checkout
of this directory alone; see that directory's README for the source and the
rebuild command.

It is here because DeployAgent now loads and runs the module it is given, so a
deployment test needs a module that really runs.
