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

.PHONY: all proto build test vet fmt

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

# Fail if any Go file is not gofmt-clean, naming the files.
#
# gofmt is not just cosmetic here: it realigns struct literals and const blocks
# when a longer name is added, so an unformatted tree produces diffs full of
# whitespace changes to lines nobody touched, and a reviewer cannot see the
# actual change. Five files had drifted before this target existed.
fmt:
	@unformatted=$$(cd services/core && gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "These files are not gofmt-clean (run gofmt -w on them):"; \
		echo "$$unformatted" | sed 's|^|  services/core/|'; \
		exit 1; \
	fi
