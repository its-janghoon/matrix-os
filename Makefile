# Matrix OS monorepo convenience targets.
#
# The Protocol Buffers Go stubs (proto/gen/**) are gitignored and NOT committed,
# so they must be generated before building services/core, which now consumes
# the matrix.market.v1 stubs (internal/marketapi). From a fresh checkout run:
#
#     make proto      # buf lint + buf generate into proto/gen
#     make build      # go work sync + build services/core
#
# or simply `make` to do both.

.PHONY: all proto build test vet

all: proto build

# Generate the buf Go stubs into proto/gen (gitignored). Required before
# building or testing services/core from a clean checkout.
proto:
	cd proto && buf lint && buf generate

# Sync the go workspace and build the Go daemon. Depends on `proto` because
# services/core imports the generated matrix-proto stubs.
build: proto
	go work sync
	cd services/core && go build ./...

# Run the Go test suite.
test: proto
	cd services/core && go test ./...

# Vet the Go packages.
vet: proto
	cd services/core && go vet ./...
