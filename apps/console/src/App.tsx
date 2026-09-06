// Matrix Console root component.
//
// Renders the top bar (brand + live connection status), a tab strip, and the
// active panel. All panels talk to matrixd through the MatrixClient provided by
// ConnectionProvider, so the same UI drives a live node or the in-memory demo
// backend depending on the connection configuration.

import { useState } from "react";
import { ConnectionProvider, useConnection } from "./state/connection";
import { NodeConnectionPanel } from "./components/NodeConnectionPanel";
import { ProvidersPanel } from "./components/ProvidersPanel";
import { JobsPanel } from "./components/JobsPanel";
import { WalletPanel } from "./components/WalletPanel";
import { ConsensusPanel } from "./components/ConsensusPanel";
import { InferencePanel } from "./components/InferencePanel";

type TabId = "connection" | "providers" | "jobs" | "wallet" | "consensus" | "inference";

const TABS: { id: TabId; label: string }[] = [
  { id: "connection", label: "Connection" },
  { id: "providers", label: "Providers" },
  { id: "jobs", label: "Jobs" },
  { id: "wallet", label: "Wallet & Token" },
  { id: "consensus", label: "Consensus" },
  { id: "inference", label: "Inference" },
];

function ConnectionPill() {
  const { state, mode } = useConnection();
  return (
    <span className={`conn-pill ${state}`}>
      <span className="status-dot" />
      {mode} · {state}
    </span>
  );
}

function Shell() {
  const [tab, setTab] = useState<TabId>("connection");

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <span className="dot" />
          <span>Matrix Console</span>
          <small>P2P compute · consensus settlement · LLM inference</small>
        </div>
        <ConnectionPill />
      </header>

      <nav className="tabs">
        {TABS.map((t) => (
          <button
            key={t.id}
            className={`tab ${tab === t.id ? "active" : ""}`}
            onClick={() => setTab(t.id)}
          >
            {t.label}
          </button>
        ))}
      </nav>

      <main className="content">
        {tab === "connection" && <NodeConnectionPanel />}
        {tab === "providers" && <ProvidersPanel />}
        {tab === "jobs" && <JobsPanel />}
        {tab === "wallet" && <WalletPanel />}
        {tab === "consensus" && <ConsensusPanel />}
        {tab === "inference" && <InferencePanel />}
      </main>
    </div>
  );
}

export function App() {
  return (
    <ConnectionProvider>
      <Shell />
    </ConnectionProvider>
  );
}
