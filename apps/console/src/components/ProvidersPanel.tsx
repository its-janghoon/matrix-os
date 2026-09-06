// Providers panel: list local + remote providers from MarketService and
// register new local compute capacity on the order book.

import { useCallback, useEffect, useState } from "react";
import { useConnection } from "../state/connection";
import type { Provider } from "../api/types";
import { Empty, ErrorBox, OkBox, OriginBadge, truncate } from "./ui";

export function ProvidersPanel() {
  const { client } = useConnection();
  const [providers, setProviders] = useState<Provider[]>([]);
  const [includeRemote, setIncludeRemote] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [ok, setOk] = useState<string | null>(null);

  const [id, setId] = useState("gpu-local-02");
  const [capacity, setCapacity] = useState(100);
  const [price, setPrice] = useState(5);

  const refresh = useCallback(async () => {
    setError(null);
    try {
      setProviders(await client.listProviders(includeRemote));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [client, includeRemote]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const register = async () => {
    setError(null);
    setOk(null);
    try {
      const p = await client.registerProvider(id, capacity, price);
      setOk(`Registered provider ${p.id} (capacity ${p.capacity}, ${p.pricePerUnit}/unit)`);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  return (
    <div className="panel">
      <h2>Providers</h2>
      <p className="hint">
        Compute providers on the marketplace. Local providers are on this node's order book; remote
        providers are discovered over the libp2p gossip network.
      </p>

      <div className="toolbar">
        <button className="btn secondary" onClick={() => void refresh()}>
          Refresh
        </button>
        <label className="check">
          <input
            type="checkbox"
            checked={includeRemote}
            onChange={(e) => setIncludeRemote(e.target.checked)}
          />
          Include remote (P2P) providers
        </label>
      </div>

      {providers.length === 0 ? (
        <Empty>No providers registered. Register local capacity below or connect to a node.</Empty>
      ) : (
        <table>
          <thead>
            <tr>
              <th>ID</th>
              <th>Origin</th>
              <th>Capacity</th>
              <th>Available</th>
              <th>Price / unit</th>
              <th>Peer ID</th>
            </tr>
          </thead>
          <tbody>
            {providers.map((p) => (
              <tr key={p.id}>
                <td className="mono">{p.id}</td>
                <td>
                  <OriginBadge origin={p.origin} />
                </td>
                <td className="mono">{p.capacity}</td>
                <td className="mono">{p.available}</td>
                <td className="mono">{p.pricePerUnit}</td>
                <td className="mono">{truncate(p.peerId, 20)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <h2 style={{ marginTop: 24, fontSize: 14 }}>Register local capacity</h2>
      <div className="row">
        <div className="field">
          <label>Provider ID</label>
          <input value={id} onChange={(e) => setId(e.target.value)} />
        </div>
        <div className="field">
          <label>Capacity</label>
          <input
            type="number"
            value={capacity}
            onChange={(e) => setCapacity(Number(e.target.value))}
          />
        </div>
        <div className="field">
          <label>Price / unit</label>
          <input type="number" value={price} onChange={(e) => setPrice(Number(e.target.value))} />
        </div>
        <button className="btn" onClick={register}>
          Register
        </button>
      </div>

      <OkBox message={ok} />
      <ErrorBox error={error} />
    </div>
  );
}
