// Node connection panel: configure the matrixd address(es), pick live vs demo
// backend, and connect. Drives the connection status shown in the top bar.

import { useConnection } from "../state/connection";
import { ErrorBox } from "./ui";

export function NodeConnectionPanel() {
  const { config, setConfig, mode, setMode, state, lastError, connect } = useConnection();

  return (
    <div className="panel">
      <h2>Node Connection</h2>
      <p className="hint">
        Configure the local <span className="mono">matrixd</span> endpoints. The console speaks the
        Connect protocol to the MarketService (default <span className="mono">:9091</span>) and
        InferenceService (default <span className="mono">:9092</span>). Use the built-in demo backend
        to explore the console without a running daemon.
      </p>

      <div className="row" style={{ marginBottom: 14 }}>
        <div className="field">
          <label>Backend</label>
          <select value={mode} onChange={(e) => setMode(e.target.value as "live" | "demo")}>
            <option value="demo">Demo (in-memory)</option>
            <option value="live">Live matrixd</option>
          </select>
        </div>
        <div className="field grow">
          <label>MarketService URL</label>
          <input
            value={config.marketUrl}
            disabled={mode === "demo"}
            onChange={(e) => setConfig({ ...config, marketUrl: e.target.value })}
            placeholder="http://127.0.0.1:9091"
          />
        </div>
        <div className="field grow">
          <label>InferenceService URL</label>
          <input
            value={config.inferenceUrl}
            disabled={mode === "demo"}
            onChange={(e) => setConfig({ ...config, inferenceUrl: e.target.value })}
            placeholder="http://127.0.0.1:9092"
          />
        </div>
        <button className="btn" onClick={connect} disabled={state === "connecting"}>
          {state === "connecting" ? "Connecting…" : "Connect"}
        </button>
      </div>

      <div className="stat-grid">
        <div className="stat">
          <div className="k">Status</div>
          <div className="v" style={{ fontSize: 18 }}>
            {state}
          </div>
        </div>
        <div className="stat">
          <div className="k">Backend</div>
          <div className="v" style={{ fontSize: 18 }}>
            {mode}
          </div>
        </div>
        <div className="stat">
          <div className="k">Market Endpoint</div>
          <div className="v mono" style={{ fontSize: 13 }}>
            {mode === "demo" ? "in-memory" : config.marketUrl}
          </div>
        </div>
      </div>

      <ErrorBox error={state === "error" ? lastError : null} />
    </div>
  );
}
