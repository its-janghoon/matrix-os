// Jobs panel: submit compute jobs against a provider, list them, and
// complete/cancel them through MarketService.

import { useCallback, useEffect, useState } from "react";
import { useConnection } from "../state/connection";
import type { Job, Provider } from "../api/types";
import { JobStatus } from "../api/types";
import { Empty, ErrorBox, OkBox, StatusBadge } from "./ui";

export function JobsPanel() {
  const { client } = useConnection();
  const [jobs, setJobs] = useState<Job[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [ok, setOk] = useState<string | null>(null);

  const [buyer, setBuyer] = useState("buyer-alice");
  const [provider, setProvider] = useState("");
  const [units, setUnits] = useState(4);

  const refresh = useCallback(async () => {
    setError(null);
    try {
      const [js, ps] = await Promise.all([client.listJobs(), client.listProviders(true)]);
      setJobs(js);
      setProviders(ps);
      if (!provider && ps.length > 0) setProvider(ps[0]!.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [client, provider]);

  useEffect(() => {
    void refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client]);

  const submit = async () => {
    setError(null);
    setOk(null);
    try {
      const job = await client.submitJob(buyer, provider, units);
      setOk(`Submitted ${job.id}: ${job.units} units @ ${job.price} credits (PENDING)`);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const act = async (fn: () => Promise<Job>, verb: string) => {
    setError(null);
    setOk(null);
    try {
      const job = await fn();
      setOk(`${verb} ${job.id} -> ${job.status}`);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const pending = (j: Job) => j.status === JobStatus.PENDING || j.status === JobStatus.RUNNING;

  return (
    <div className="panel">
      <h2>Jobs</h2>
      <p className="hint">
        Paid compute jobs. Submitting reserves provider capacity (no credits move); completing
        settles the price buyer → provider through the token ledger.
      </p>

      <div className="row" style={{ marginBottom: 16 }}>
        <div className="field">
          <label>Buyer account</label>
          <input value={buyer} onChange={(e) => setBuyer(e.target.value)} />
        </div>
        <div className="field">
          <label>Provider</label>
          <select value={provider} onChange={(e) => setProvider(e.target.value)}>
            {providers.map((p) => (
              <option key={p.id} value={p.id}>
                {p.id} ({p.available} free @ {p.pricePerUnit})
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label>Units</label>
          <input type="number" value={units} onChange={(e) => setUnits(Number(e.target.value))} />
        </div>
        <button className="btn" onClick={submit} disabled={!provider}>
          Submit Job
        </button>
        <button className="btn secondary" onClick={() => void refresh()}>
          Refresh
        </button>
      </div>

      {jobs.length === 0 ? (
        <Empty>No jobs yet. Submit one above.</Empty>
      ) : (
        <table>
          <thead>
            <tr>
              <th>ID</th>
              <th>Buyer</th>
              <th>Provider</th>
              <th>Units</th>
              <th>Price</th>
              <th>Status</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {jobs.map((j) => (
              <tr key={j.id}>
                <td className="mono">{j.id}</td>
                <td className="mono">{j.buyer}</td>
                <td className="mono">{j.provider}</td>
                <td className="mono">{j.units}</td>
                <td className="mono">{j.price}</td>
                <td>
                  <StatusBadge status={j.status} />
                </td>
                <td>
                  {pending(j) && (
                    <span style={{ display: "flex", gap: 6 }}>
                      <button
                        className="btn secondary"
                        onClick={() => void act(() => client.completeJob(j.id), "Completed")}
                      >
                        Complete
                      </button>
                      <button
                        className="btn danger"
                        onClick={() => void act(() => client.cancelJob(j.id), "Cancelled")}
                      >
                        Cancel
                      </button>
                    </span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <OkBox message={ok} />
      <ErrorBox error={error} />
    </div>
  );
}
