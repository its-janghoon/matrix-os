# A node image, so deploying one is not "install Go, install buf, build".
#
# The generated protobuf stubs are gitignored, so a build from a clean checkout
# has to run buf before it can compile. Doing that here rather than in a README
# is the difference between an image anyone can build and a set of instructions
# people get wrong.

FROM golang:1.25-bookworm AS build
WORKDIR /src

# buf first, and pinned. An unpinned tool is a build that changes under you.
RUN GOBIN=/usr/local/bin go install github.com/bufbuild/buf/cmd/buf@v1.47.2

# The module files alone, so dependency download is cached separately from the
# source: editing a .go file must not re-download the world.
COPY go.work go.work.sum* ./
COPY proto/go.mod proto/go.sum ./proto/
COPY services/core/go.mod services/core/go.sum ./services/core/
RUN cd services/core && go mod download

COPY . .
RUN cd proto && buf generate
# Trimpath so the binary does not carry this container's paths, and the version
# stamped from the build argument so `matrixd -version` is truthful rather than
# reporting "dev" forever.
ARG VERSION=dev
RUN cd services/core && \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/ecirlabs/matrix-core/internal/version.Version=${VERSION}" \
      -o /out/matrixd ./cmd/matrixd && \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/ecirlabs/matrix-core/internal/version.Version=${VERSION}" \
      -o /out/matrix ./cmd/matrix

# Distroless rather than scratch: the node dials TLS endpoints (a bridge RPC),
# which needs CA certificates, and it resolves DNS peer addresses.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/matrixd /usr/local/bin/matrixd
COPY --from=build /out/matrix /usr/local/bin/matrix

# The data directory is a volume because a node that loses it loses its identity:
# the peer id and consensus key are generated on first start and live there, so a
# container that starts fresh every time is a NEW validator every time.
VOLUME ["/var/lib/matrix"]

# Only the P2P port. Every other listener - admin especially, whose key can move
# funds - is published deliberately or not at all.
EXPOSE 9000

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/matrixd"]
CMD ["-config", "/etc/matrix/config.yaml"]
