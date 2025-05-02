# Matrix Core

Matrix Core is a powerful, distributed system that transforms any computer (laptop, VPS, or Raspberry Pi) into a full-fledged Matrix OS node. It serves as the kernel, runtime, and control plane combined into a single, efficient Go service.

## 🚀 Features

- **Single Binary Deployment**: One binary (`matrixd`) handles installation, upgrades, and graceful shutdowns
- **Peer-to-Peer Network**: Built-in peer discovery, secure transport, pub/sub messaging, and NAT traversal
- **WebAssembly Support**: Run agents safely in a sandboxed WebAssembly environment
- **Distributed Storage**: Local key-value store with optional CRDT/Raft consensus
- **Modern API Support**: gRPC/REST APIs, Prometheus metrics, and OpenTelemetry tracing
- **Enterprise-Grade Security**: Node-level authentication, agent signatures, and flexible ACLs
- **Production-Ready Observability**: Structured logging, profiling endpoints, and health monitoring

## 📋 Prerequisites

- Go 1.21 or later
- Git

## 🛠 Installation

```bash
# Clone the repository
git clone https://github.com/ecirlabs/matrix-core.git
cd matrix-core

# Build the binary
make build

# Initialize a new node
./matrixd --init
```

## 🏃‍♂️ Quick Start

1. Start the Matrix daemon:
   ```bash
   ./matrixd
   ```

2. Deploy your first agent:
   ```bash
   matrix-ctl deploy myagent.wasm
   ```

3. Check node status:
   ```bash
   matrix-ctl status
   ```

## 🏗 Architecture

Matrix Core is built with a modular architecture, focusing on:

### Core Components

| Component | Description |
|-----------|-------------|
| Process Lifecycle | Manages node initialization, upgrades, and graceful shutdowns |
| Network Fabric | Handles peer discovery, secure communication, and message routing |
| Execution Engine | Provides a secure WebAssembly runtime for agent execution |
| State Management | Manages distributed state and persistent storage |
| API Layer | Exposes gRPC/REST endpoints for node control and monitoring |

### Directory Structure

```
matrix-core/
├── cmd/matrixd/          # Main application entry point
├── internal/             # Private implementation packages
├── pkg/                  # Public API packages
├── proto/                # Protocol definitions
├── scripts/              # Development and maintenance scripts
├── configs/              # Configuration templates
└── testdata/            # Test fixtures
```

## 🔧 Configuration

Matrix Core uses YAML/TOML configuration files. Key settings include:

- Network configuration
- Storage options
- Security parameters
- Resource limits
- Logging levels

Example configuration:

```yaml
network:
  listen_addr: "0.0.0.0:9000"
  bootstrap_peers:
    - "/ip4/1.2.3.4/tcp/9000/p2p/QmExample..."

storage:
  engine: "pebble"
  path: "/var/lib/matrix/data"

security:
  enable_acls: true
  allow_unsigned_agents: false
```

## 🧪 Development

### Building from Source

```bash
# Install dependencies
make deps

# Run tests
make test

# Build binary
make build
```

### Running Tests

```bash
# Run all tests
go test ./...

# Run with race detection
go test -race ./...
```

## 📈 Monitoring

Matrix Core exposes several monitoring endpoints:

- `/metrics` - Prometheus metrics
- `/healthz` - Health check endpoint
- `/debug/pprof` - Performance profiling data

## 🤝 Contributing

We welcome contributions! Please see our [Contributing Guide](CONTRIBUTING.md) for details.

## 📄 License

[License Type] - See [LICENSE](LICENSE) for details.

## 🔗 Dependencies

| Component | Library Used |
|-----------|-------------|
| P2P Transport | `go-libp2p` |
| WebAssembly Runtime | `github.com/tetratelabs/wazero` |
| Key-Value Store | `github.com/cockroachdb/pebble` |
| CLI Framework | `spf13/cobra` + `spf13/viper` |
| Logging | `rs/zerolog` |
| Metrics/Tracing | `prometheus/client_golang` + `opentelemetry.io/otel` |

## 📚 Documentation

For detailed documentation, please visit our [Documentation Site](https://docs.example.com/matrix-core).

## 🎯 Roadmap

- [x] Basic node functionality
- [x] Peer discovery
- [x] WebAssembly runtime
- [ ] Advanced networking features
- [ ] Enhanced security features
- [ ] Production deployment tools

## 💬 Community
- TBD