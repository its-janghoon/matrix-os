// Connection state and client provider for the Matrix Console.
//
// Holds the configurable matrixd connection target, the active MatrixClient
// (live Connect client or in-memory demo backend), and a probe that pings the
// node to drive the connection status shown in the top bar. Panels consume the
// client through the useConnection hook.

import React, {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
} from "react";
import {
  ConnectMatrixClient,
  DEFAULT_CONFIG,
  MatrixClientError,
  type ConnectionConfig,
  type MatrixClient,
} from "../api/client";
import { DemoMatrixClient } from "../api/demo";
import type { ConnectionState } from "../api/types";

export type BackendMode = "live" | "demo";

interface ConnectionContextValue {
  config: ConnectionConfig;
  setConfig: (c: ConnectionConfig) => void;
  mode: BackendMode;
  setMode: (m: BackendMode) => void;
  client: MatrixClient;
  state: ConnectionState;
  lastError: string | null;
  connect: () => Promise<void>;
}

const ConnectionContext = createContext<ConnectionContextValue | null>(null);

// A single shared demo backend instance so state persists across panels.
const demoClient = new DemoMatrixClient();

export function ConnectionProvider({ children }: { children: React.ReactNode }) {
  const [config, setConfig] = useState<ConnectionConfig>(DEFAULT_CONFIG);
  const [mode, setMode] = useState<BackendMode>("demo");
  const [state, setState] = useState<ConnectionState>("disconnected");
  const [lastError, setLastError] = useState<string | null>(null);

  const client = useMemo<MatrixClient>(() => {
    return mode === "demo" ? demoClient : new ConnectMatrixClient(config);
  }, [mode, config]);

  const connect = useCallback(async () => {
    setState("connecting");
    setLastError(null);
    try {
      await client.ping();
      setState("connected");
    } catch (err) {
      const msg =
        err instanceof MatrixClientError
          ? err.message
          : err instanceof Error
            ? err.message
            : String(err);
      setLastError(msg);
      setState("error");
    }
  }, [client]);

  const value: ConnectionContextValue = {
    config,
    setConfig,
    mode,
    setMode,
    client,
    state,
    lastError,
    connect,
  };

  return <ConnectionContext.Provider value={value}>{children}</ConnectionContext.Provider>;
}

export function useConnection(): ConnectionContextValue {
  const ctx = useContext(ConnectionContext);
  if (!ctx) throw new Error("useConnection must be used within a ConnectionProvider");
  return ctx;
}
