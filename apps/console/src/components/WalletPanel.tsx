// Wallet panel: read native MATRIX balances and view the settled transaction
// chain from MarketService. Native MATRIX is the source of truth; the ERC-20
// (wMATRIX) is only a bridged wrapped mirror.

import { useCallback, useEffect, useState } from "react";
import { useConnection } from "../state/connection";
import type { Transaction } from "../api/types";
import { Empty, ErrorBox, truncate } from "./ui";

export function WalletPanel() {
  const { client } = useConnection();
  const [account, setAccount] = useState("buyer-alice");
  const [balance, setBalance] = useState<number | null>(null);
  const [txs, setTxs] = useState<Transaction[]>([]);
  const [chainLength, setChainLength] = useState(0);
  const [error, setError] = useState<string | null>(null);

  const refreshTxs = useCallback(async () => {
    setError(null);
    try {
      const { transactions, chainLength: len } = await client.listTransactions(0, 50);
      setTxs(transactions);
      setChainLength(len);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [client]);

  useEffect(() => {
    void refreshTxs();
  }, [refreshTxs]);

  const lookup = async () => {
    setError(null);
    try {
      const b = await client.getBalance(account);
      setBalance(b.balance);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setBalance(null);
    }
  };

  return (
    <div className="panel">
      <h2>Wallet &amp; Token</h2>
      <p className="hint">
        Native MATRIX balances and the hash-chained settlement ledger. Native MATRIX is the coin of
        the Matrix L1 and the single source of truth for balances; the ERC-20 (wMATRIX) is a bridged
        wrapped mirror for exchange listing, backed 1:1 by locked native. Balances change as jobs
        settle buyer → provider in native MATRIX through consensus.
      </p>

      <div className="row" style={{ marginBottom: 16 }}>
        <div className="field grow">
          <label>Account</label>
          <input value={account} onChange={(e) => setAccount(e.target.value)} />
        </div>
        <button className="btn" onClick={lookup}>
          Get Balance
        </button>
      </div>

      <div className="stat-grid" style={{ marginBottom: 20 }}>
        <div className="stat">
          <div className="k">Balance ({account})</div>
          <div className="v">{balance === null ? "—" : balance}</div>
        </div>
        <div className="stat">
          <div className="k">Chain length</div>
          <div className="v">{chainLength}</div>
        </div>
      </div>

      <div className="toolbar">
        <strong style={{ fontSize: 13 }}>Settled transactions</strong>
        <button className="btn secondary" onClick={() => void refreshTxs()}>
          Refresh
        </button>
      </div>

      {txs.length === 0 ? (
        <Empty>No settled transactions yet. Complete a job to move native MATRIX.</Empty>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Height</th>
              <th>From</th>
              <th>To</th>
              <th>Amount</th>
              <th>Nonce</th>
              <th>Hash</th>
            </tr>
          </thead>
          <tbody>
            {txs.map((t) => (
              <tr key={t.height}>
                <td className="mono">{t.height}</td>
                <td className="mono">{truncate(t.from, 14)}</td>
                <td className="mono">{truncate(t.to, 14)}</td>
                <td className="mono">{t.amount}</td>
                <td className="mono">{t.nonce}</td>
                <td className="mono">{truncate(t.hash, 18)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <ErrorBox error={error} />
    </div>
  );
}
