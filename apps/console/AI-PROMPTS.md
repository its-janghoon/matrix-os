## 🟨 `matrix-console` — Prompt & Architecture

### 🎯 Prompt

```
You’re building `matrix-console`, the graphical UI for Matrix OS.

Matrix OS lets users create Souls (AI agents that mirror them), run Matrices (simulations with rules), and observe how autonomous agents behave when placed into dynamic environments.

The console must:
- Allow users to create/edit Souls (memories, persona, values)
- Let users drag-and-drop Souls into Matrices
- Visualize the interaction graph of agents (nodes ↔ edges)
- Stream live chat with a Soul in a rich GUI
- Display emergent events and simulation outputs (timeline, signal overlays)
- Connect to local `matrixd` nodes over WebSocket/gRPC
- Bundle into a Tauri desktop app for Windows, macOS, and Linux

Your job is to make this interaction story real:
→ “My Soul speaks and evolves like me.”
→ “My Matrix helps me understand social and technical systems.”
→ “I can watch, interact with, and steer the simulation.”
```

### 📁 Folder Structure

```
matrix-console/
├── src/
│   ├── components/
│   │   ├── SoulChatPanel.tsx         # Chat interface with a Soul
│   │   ├── MatrixGraph.tsx           # Graph of agent interactions
│   │   ├── EmergenceTimeline.tsx     # Heatmap of emergent events
│   │   └── ScenarioEditor.tsx        # Build new Matrices (drag Souls, define rules)
│   ├── api/
│   │   ├── grpc.ts                   # Admin RPC client
│   │   └── websocket.ts              # Live event stream
│   └── App.tsx
├── public/
├── tauri.conf.json
├── vite.config.ts
└── package.json
```

### 🧱 Architectural Overview

* **Frontend stack**: React + TypeScript + Vite + Tauri
* **Transport**: WebSocket → matrixd `/stream` endpoint (for MatrixEvent, SoulLogs)
* **UX Goals**:

  * Minimize clicks to deploy a Soul and start simulation
  * Real-time observability (chat, logs, signals)
  * Editable policy interface for sandbox control

---

## ✅ Summary Table

| Repo             | Purpose                                    | Key Value Prop Supported                      |
| ---------------- | ------------------------------------------ | --------------------------------------------- |
| `matrix-proto`   | Wire contracts: Souls, Matrices, transport | “Interoperable, extensible, API-based agents” |
| `matrix-core`    | Runtime: Wasm, P2P, Matrix logic           | “Decentralized autonomy and simulation”       |
| `matrix-console` | GUI for creation, observation, control     | “Mirror yourself, simulate your world”        |
