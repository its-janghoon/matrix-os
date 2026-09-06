// Matrix Console transport client.
//
// The console connects to a local `matrixd` node. matrixd exposes its
// marketplace and inference surfaces as gRPC services (MarketService on :9091,
// InferenceService on :9092 by default; see services/core/internal/marketapi and
// internal/inferenceapi). Raw gRPC uses HTTP/2 trailers that browsers cannot
// speak directly, so a browser/WebView client talks to those services through
// the Connect protocol (https://connectrpc.com), which is a plain HTTP POST of
// a JSON (or binary) message to `/{package}.{Service}/{Method}`. matrixd can
// front its gRPC services with a Connect/grpc-web handler; the client below
// speaks that protocol over `fetch`, so it is real wire code rather than a mock.
//
// When no node is reachable (the common case in a fresh checkout with no daemon
// running) the client surfaces a typed error and the UI renders a graceful
// disconnected state. An optional in-memory demo backend (see demo.ts) lets the
// panels be exercised and screenshotted without a live daemon; it implements the
// exact same MatrixClient interface, so switching to a real node is a config
// change, not a code change.

import type {
  Balance,
  InferenceJob,
  Job,
  Provider,
  Transaction,
} from "./types";
import {
  ChatRole,
  InferenceJobStatus,
  JobStatus,
  ProviderOrigin,
} from "./types";

export interface ConnectionConfig {
  /** Base URL of the matrixd MarketService Connect endpoint, e.g. http://127.0.0.1:9091 */
  marketUrl: string;
  /** Base URL of the matrixd InferenceService Connect endpoint, e.g. http://127.0.0.1:9092 */
  inferenceUrl: string;
}

export const DEFAULT_CONFIG: ConnectionConfig = {
  marketUrl: "http://127.0.0.1:9091",
  inferenceUrl: "http://127.0.0.1:9092",
};

export interface SubmitInferenceParams {
  buyer: string;
  provider: string;
  model: string;
  prompt: string;
  maxTokens?: number;
  temperature?: number;
  unitsEstimate?: number;
}

/**
 * MatrixClient is the console-facing abstraction over a matrixd node. The live
 * implementation (ConnectMatrixClient) speaks the Connect protocol to a real
 * daemon; the demo implementation (DemoMatrixClient) is an in-memory stand-in
 * with identical semantics for offline use and screenshots.
 */
export interface MatrixClient {
  ping(): Promise<void>;

  // MarketService
  listProviders(includeRemote: boolean): Promise<Provider[]>;
  registerProvider(id: string, capacity: number, pricePerUnit: number): Promise<Provider>;
  submitJob(buyer: string, provider: string, units: number): Promise<Job>;
  listJobs(buyer?: string): Promise<Job[]>;
  completeJob(id: string): Promise<Job>;
  cancelJob(id: string): Promise<Job>;
  getBalance(account: string): Promise<Balance>;
  listTransactions(startHeight: number, limit: number): Promise<{ transactions: Transaction[]; chainLength: number }>;

  // InferenceService
  submitInference(params: SubmitInferenceParams): Promise<InferenceJob>;
  fulfillInference(id: string): Promise<InferenceJob>;
  getInferenceJob(id: string): Promise<InferenceJob>;
}

/** Error raised when the node cannot be reached or returns a non-OK response. */
export class MatrixClientError extends Error {
  constructor(message: string, readonly cause?: unknown) {
    super(message);
    this.name = "MatrixClientError";
  }
}

// --- Connect protocol helpers -------------------------------------------------

const MARKET_SERVICE = "matrix.market.v1.MarketService";
const INFERENCE_SERVICE = "matrix.inference.v1.InferenceService";

async function connectUnary<TReq extends object, TResp>(
  baseUrl: string,
  service: string,
  method: string,
  request: TReq,
): Promise<TResp> {
  const url = `${baseUrl.replace(/\/$/, "")}/${service}/${method}`;
  let resp: Response;
  try {
    resp = await fetch(url, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Connect-Protocol-Version": "1",
      },
      body: JSON.stringify(request),
    });
  } catch (err) {
    throw new MatrixClientError(
      `unable to reach matrixd at ${baseUrl} (${service}/${method})`,
      err,
    );
  }
  if (!resp.ok) {
    let detail = "";
    try {
      const body = await resp.json();
      detail = typeof body?.message === "string" ? `: ${body.message}` : "";
    } catch {
      // ignore body parse errors
    }
    throw new MatrixClientError(
      `matrixd ${service}/${method} returned ${resp.status}${detail}`,
    );
  }
  return (await resp.json()) as TResp;
}

// --- number coercion ----------------------------------------------------------
// Connect JSON encodes 64-bit ints as strings. Coerce defensively.

function num(v: unknown): number {
  if (typeof v === "number") return v;
  if (typeof v === "string" && v !== "") return Number(v);
  return 0;
}

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

function mapProvider(p: Record<string, unknown>): Provider {
  return {
    id: str(p.id),
    capacity: num(p.capacity),
    pricePerUnit: num(p.pricePerUnit ?? p.price_per_unit),
    available: num(p.available),
    origin: (p.origin as ProviderOrigin) ?? ProviderOrigin.UNSPECIFIED,
    peerId: str(p.peerId ?? p.peer_id),
  };
}

function mapJob(j: Record<string, unknown>): Job {
  return {
    id: str(j.id),
    buyer: str(j.buyer),
    provider: str(j.provider),
    units: num(j.units),
    price: num(j.price),
    status: (j.status as JobStatus) ?? JobStatus.UNSPECIFIED,
    createdAt: j.createdAt as string | undefined,
    updatedAt: j.updatedAt as string | undefined,
  };
}

function mapTransaction(t: Record<string, unknown>): Transaction {
  return {
    height: num(t.height),
    from: str(t.from),
    to: str(t.to),
    amount: num(t.amount),
    nonce: num(t.nonce),
    hash: str(t.hash),
    prevHash: str(t.prevHash ?? t.prev_hash),
    timestamp: t.timestamp as string | undefined,
  };
}

function mapInferenceJob(j: Record<string, unknown>): InferenceJob {
  return {
    id: str(j.id),
    buyer: str(j.buyer),
    provider: str(j.provider),
    model: str(j.model),
    status: (j.status as InferenceJobStatus) ?? InferenceJobStatus.UNSPECIFIED,
    completion: str(j.completion),
    units: num(j.units),
    createdAt: j.createdAt as string | undefined,
    updatedAt: j.updatedAt as string | undefined,
  };
}

/** Live client: speaks the Connect protocol to a running matrixd node. */
export class ConnectMatrixClient implements MatrixClient {
  constructor(private readonly config: ConnectionConfig) {}

  async ping(): Promise<void> {
    // Cheapest read that exercises the market surface.
    await this.listProviders(false);
  }

  async listProviders(includeRemote: boolean): Promise<Provider[]> {
    const resp = await connectUnary<{ includeRemote: boolean }, { providers?: Record<string, unknown>[] }>(
      this.config.marketUrl,
      MARKET_SERVICE,
      "ListProviders",
      { includeRemote },
    );
    return (resp.providers ?? []).map(mapProvider);
  }

  async registerProvider(id: string, capacity: number, pricePerUnit: number): Promise<Provider> {
    const resp = await connectUnary<
      { id: string; capacity: number; pricePerUnit: number },
      { provider: Record<string, unknown> }
    >(this.config.marketUrl, MARKET_SERVICE, "RegisterProvider", { id, capacity, pricePerUnit });
    return mapProvider(resp.provider);
  }

  async submitJob(buyer: string, provider: string, units: number): Promise<Job> {
    const resp = await connectUnary<
      { buyer: string; provider: string; units: number },
      { job: Record<string, unknown> }
    >(this.config.marketUrl, MARKET_SERVICE, "SubmitJob", { buyer, provider, units });
    return mapJob(resp.job);
  }

  async listJobs(buyer?: string): Promise<Job[]> {
    const resp = await connectUnary<{ buyer: string }, { jobs?: Record<string, unknown>[] }>(
      this.config.marketUrl,
      MARKET_SERVICE,
      "ListJobs",
      { buyer: buyer ?? "" },
    );
    return (resp.jobs ?? []).map(mapJob);
  }

  async completeJob(id: string): Promise<Job> {
    const resp = await connectUnary<{ id: string }, { job: Record<string, unknown> }>(
      this.config.marketUrl,
      MARKET_SERVICE,
      "CompleteJob",
      { id },
    );
    return mapJob(resp.job);
  }

  async cancelJob(id: string): Promise<Job> {
    const resp = await connectUnary<{ id: string }, { job: Record<string, unknown> }>(
      this.config.marketUrl,
      MARKET_SERVICE,
      "CancelJob",
      { id },
    );
    return mapJob(resp.job);
  }

  async getBalance(account: string): Promise<Balance> {
    const resp = await connectUnary<{ account: string }, { account: string; balance: unknown }>(
      this.config.marketUrl,
      MARKET_SERVICE,
      "GetBalance",
      { account },
    );
    return { account: str(resp.account), balance: num(resp.balance) };
  }

  async listTransactions(
    startHeight: number,
    limit: number,
  ): Promise<{ transactions: Transaction[]; chainLength: number }> {
    const resp = await connectUnary<
      { startHeight: number; limit: number },
      { transactions?: Record<string, unknown>[]; chainLength: unknown }
    >(this.config.marketUrl, MARKET_SERVICE, "ListTransactions", { startHeight, limit });
    return {
      transactions: (resp.transactions ?? []).map(mapTransaction),
      chainLength: num(resp.chainLength),
    };
  }

  async submitInference(params: SubmitInferenceParams): Promise<InferenceJob> {
    const resp = await connectUnary<Record<string, unknown>, { job: Record<string, unknown> }>(
      this.config.inferenceUrl,
      INFERENCE_SERVICE,
      "SubmitInferenceJob",
      {
        buyer: params.buyer,
        provider: params.provider,
        model: params.model,
        prompt: params.prompt,
        maxTokens: params.maxTokens ?? 0,
        temperature: params.temperature ?? 0,
        unitsEstimate: params.unitsEstimate ?? 0,
      },
    );
    return mapInferenceJob(resp.job);
  }

  async fulfillInference(id: string): Promise<InferenceJob> {
    const resp = await connectUnary<{ id: string }, { job: Record<string, unknown> }>(
      this.config.inferenceUrl,
      INFERENCE_SERVICE,
      "FulfillInferenceJob",
      { id },
    );
    return mapInferenceJob(resp.job);
  }

  async getInferenceJob(id: string): Promise<InferenceJob> {
    const resp = await connectUnary<{ id: string }, { job: Record<string, unknown> }>(
      this.config.inferenceUrl,
      INFERENCE_SERVICE,
      "GetInferenceJob",
      { id },
    );
    return mapInferenceJob(resp.job);
  }
}

export { ChatRole };
