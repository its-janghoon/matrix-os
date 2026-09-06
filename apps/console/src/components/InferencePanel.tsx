// Inference console: submit an LLM inference job to an inference-capable
// provider, fulfill it (runs the backend + settles units buyer → provider), and
// view the completion. Uses matrix.inference.v1 InferenceService.

import { useCallback, useEffect, useState } from "react";
import { useConnection } from "../state/connection";
import type { InferenceJob, Provider } from "../api/types";
import { InferenceJobStatus } from "../api/types";
import { ErrorBox, StatusBadge } from "./ui";

export function InferencePanel() {
  const { client } = useConnection();
  const [providers, setProviders] = useState<Provider[]>([]);
  const [buyer, setBuyer] = useState("buyer-alice");
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("llama-3.1-8b-instruct");
  const [prompt, setPrompt] = useState("Explain what the Matrix OS compute marketplace does in one sentence.");
  const [job, setJob] = useState<InferenceJob | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadProviders = useCallback(async () => {
    try {
      const ps = await client.listProviders(true);
      setProviders(ps);
      if (!provider && ps.length > 0) setProvider(ps[0]!.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [client, provider]);

  useEffect(() => {
    void loadProviders();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client]);

  const run = async () => {
    setError(null);
    setBusy(true);
    setJob(null);
    try {
      const submitted = await client.submitInference({
        buyer,
        provider,
        model,
        prompt,
        unitsEstimate: 1,
      });
      setJob(submitted);
      const fulfilled = await client.fulfillInference(submitted.id);
      setJob(fulfilled);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="panel">
      <h2>Inference Console</h2>
      <p className="hint">
        Submit an LLM prompt to an inference-capable provider. On fulfillment the provider runs its
        backend (local model runner or proxied provider API) and settles the billed units buyer →
        provider through consensus-backed token settlement.
      </p>

      <div className="row" style={{ marginBottom: 12 }}>
        <div className="field">
          <label>Buyer</label>
          <input value={buyer} onChange={(e) => setBuyer(e.target.value)} />
        </div>
        <div className="field">
          <label>Provider</label>
          <select value={provider} onChange={(e) => setProvider(e.target.value)}>
            {providers.map((p) => (
              <option key={p.id} value={p.id}>
                {p.id}
              </option>
            ))}
          </select>
        </div>
        <div className="field grow">
          <label>Model</label>
          <input value={model} onChange={(e) => setModel(e.target.value)} />
        </div>
      </div>

      <div className="field" style={{ marginBottom: 12 }}>
        <label>Prompt</label>
        <textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} />
      </div>

      <button className="btn" onClick={run} disabled={busy || !provider}>
        {busy ? "Running…" : "Submit & Fulfill"}
      </button>

      {job && (
        <div style={{ marginTop: 18 }}>
          <div className="row" style={{ alignItems: "center", gap: 10 }}>
            <span className="mono">{job.id}</span>
            <StatusBadge status={job.status} />
            <span className="mono" style={{ color: "var(--text-dim)" }}>
              {job.units} unit(s) · {job.model}
            </span>
          </div>
          {job.status === InferenceJobStatus.COMPLETED && (
            <div className="completion">{job.completion}</div>
          )}
        </div>
      )}

      <ErrorBox error={error} />
    </div>
  );
}
