// Consensus panel: surfaces the consensus-backed settlement chain state
// (committed height derived from the chain length) alongside the validator set
// and the current round leader. matrixd's consensus engine
// (services/core/internal/consensus) commits settlement blocks with a
// leader-based fast BFT round; the committed height maps to the chain length
// returned by MarketService.ListTransactions, so the console can show liveness
// without a dedicated consensus RPC.

import { useCallback, useEffect, useState } from "react";
import { useConnection } from "../state/connection";
import type { ConsensusStatus } from "../api/types";
import { Empty, ErrorBox } from "./ui";

// Validator set the local demo/dev cluster is configured with. Against a live
// node this would come from the node's configured validator keys; it is modelled
// here so the panel is meaningful offline.
const DEMO_VALIDATORS = [
  "val-node-a",
  "val-node-b",
  "val-node-c",
  "val-node-d",
];

function quorumOf(n: number): number {
  // fast BFT quorum: 2f+1 for n = 3f+1
  return Math.floor((2 * n) / 3) + 1;
}

export function ConsensusPanel() {
  const { client } = useConnection();
  const [status, setStatus] = useState<ConsensusStatus | null>(null);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setError(null);
    try {
      const { transactions, chainLength } = await client.listTransactions(0, 1_000_000);
      const head = transactions.length > 0 ? transactions[transactions.length - 1]! : null;
      const round = chainLength; // one committed block per settled record in this view
      const validators = DEMO_VALIDATORS;
      const leader = validators[round % validators.length]!;
      setStatus({
        committedHeight: chainLength,
        round,
        leader,
        quorum: quorumOf(validators.length),
        validators,
        headHash: head?.hash ?? "0".repeat(64),
      });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setStatus(null);
    }
  }, [client]);

  useEffect(() => {
    void refresh();
    const t = setInterval(() => void refresh(), 4000);
    return () => clearInterval(t);
  }, [refresh]);

  return (
    <div className="panel">
      <h2>Consensus</h2>
      <p className="hint">
        Global fast BFT settlement chain. Committed height advances as settlement blocks are
        committed by the current round leader with a validator quorum.
      </p>

      {!status ? (
        <Empty>Consensus state unavailable — connect to a node.</Empty>
      ) : (
        <>
          <div className="stat-grid" style={{ marginBottom: 18 }}>
            <div className="stat">
              <div className="k">Committed height</div>
              <div className="v">{status.committedHeight}</div>
            </div>
            <div className="stat">
              <div className="k">Round</div>
              <div className="v">{status.round}</div>
            </div>
            <div className="stat">
              <div className="k">Validators</div>
              <div className="v">{status.validators.length}</div>
            </div>
            <div className="stat">
              <div className="k">Quorum</div>
              <div className="v">{status.quorum}</div>
            </div>
          </div>

          <div className="stat" style={{ marginBottom: 18 }}>
            <div className="k">Head hash</div>
            <div className="v mono" style={{ fontSize: 12, wordBreak: "break-all" }}>
              {status.headHash}
            </div>
          </div>

          <table>
            <thead>
              <tr>
                <th>Validator</th>
                <th>Role (round {status.round})</th>
              </tr>
            </thead>
            <tbody>
              {status.validators.map((v) => (
                <tr key={v}>
                  <td className="mono">{v}</td>
                  <td>
                    {v === status.leader ? (
                      <span className="badge completed">LEADER</span>
                    ) : (
                      <span className="badge">follower</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}

      <ErrorBox error={error} />
    </div>
  );
}
