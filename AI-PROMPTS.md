## 🟩 `matrix-core` — Prompt & Architecture

### 🎯 Prompt

```
You’re working on `matrix-core`, the runtime engine for Matrix OS—a peer-to-peer system for training and orchestrating AI agents ("Souls") in decentralized simulation environments ("Matrices").

Souls are Wasm-based agents trained over time from user input, API requests, and agent-to-agent conversations. Matrices are simulated environments that allow these agents to interact and generate emergent signals.

Your job is to:
- Build the core runtime for launching, orchestrating, and networking agents
- Use libp2p for peer discovery and agent message transport
- Execute Wasm agents in resource-limited sandboxes
- Run matrices with custom rule engines and expose events
- Emit structured logs and Prometheus metrics
- Maintain gRPC services for control and deployment
- Integrate Souls, Matrices, and P2P into a coherent event-driven architecture

Everything must be testable, observable, and minimal in dependencies. Peer-to-peer design is essential: agents on different nodes must be able to interact as if local.
```

### 📁 Folder Structure

```
matrix-core/
├── cmd/
│   └── matrixd/              # Entry point for the node daemon
├── internal/
│   ├── agent/                # Wasm execution, training loop, hostcalls
│   ├── soul/                 # Memory, persona, values, training logic
│   ├── matrix/               # Matrix rule engine, sandbox orchestration
│   ├── p2p/                  # libp2p host, peer discovery, stream handling
│   ├── transport/            # MatrixMessage routing, event bus
│   ├── admin/                # gRPC handlers: Health, Deploy, Logs
│   ├── kv/                   # Raft + Pebble-based CRDT store
│   ├── metrics/              # Prometheus counters + OpenTelemetry
│   └── node/                 # Config, startup, shutdown, graceful exit
└── go.mod
```

### 🧱 Architectural Overview

* **Daemon (`matrixd`)**: Bootstraps a full Matrix OS node
* **Souls**: Are Wasm agents executed with per-agent resource caps
* **Matrices**: Run rulesets and expose `MatrixEvent`s via the event bus
* **P2P**: Enables decentralized agent communication (libp2p stream: `/matrix/1.0.0`)
* **Event Bus**: All subsystems (P2P, Soul, Matrix, Trainer) publish to a common pub/sub system
* **KV Store**: Replicates data and agent state across nodes using Raft or CRDT

## ✅ Summary Table

| Repo             | Purpose                                    | Key Value Prop Supported                      |
| ---------------- | ------------------------------------------ | --------------------------------------------- |
| `matrix-proto`   | Wire contracts: Souls, Matrices, transport | “Interoperable, extensible, API-based agents” |
| `matrix-core`    | Runtime: Wasm, P2P, Matrix logic           | “Decentralized autonomy and simulation”       |
| `matrix-console` | GUI for creation, observation, control     | “Mirror yourself, simulate your world”        |