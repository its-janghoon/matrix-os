/**
 * matrix-os-sdk - a TypeScript client for a Matrix OS node.
 *
 * A node serves its compute-marketplace and LLM-inference APIs over HTTP using
 * the Connect protocol's unary JSON form (see
 * services/core/internal/connectapi): an ordinary POST to
 * `/{package}.{Service}/{Method}` with the request message as JSON. That is
 * what this client speaks, so it works from a browser, a dApp front end, Node,
 * Deno, Bun, or a WebView - anywhere `fetch` exists. It has no dependencies.
 *
 * Amounts are `bigint`, not `number`, and that is deliberate. Native MATRIX has
 * 9 decimals and a cap of 1,000,000,000 whole coins, so a balance can reach
 * 1e18 base units - two orders of magnitude past `Number.MAX_SAFE_INTEGER`.
 * Coercing to `number` would silently round real money. The wire format already
 * carries 64-bit fields as strings, which is what makes the exact conversion
 * possible.
 */

export const DEFAULT_ENDPOINT = 'http://127.0.0.1:9093';

const MARKET = 'matrix.market.v1.MarketService';
const INFERENCE = 'matrix.inference.v1.InferenceService';
const AGENT = 'matrix.agent.v1.AgentService';

/** Connect error codes, as the endpoint reports them. */
export type MatrixErrorCode =
  | 'canceled'
  | 'invalid_argument'
  | 'deadline_exceeded'
  | 'not_found'
  | 'already_exists'
  | 'permission_denied'
  | 'resource_exhausted'
  | 'failed_precondition'
  | 'aborted'
  | 'out_of_range'
  | 'unimplemented'
  | 'internal'
  | 'unavailable'
  | 'data_loss'
  | 'unauthenticated'
  | 'unreachable';

/**
 * An RPC that did not succeed.
 *
 * `code` is the reason, so a caller can branch on it rather than matching on
 * message text: `not_found` for a provider that does not exist,
 * `unauthenticated` when the node has ACLs on and the key is missing,
 * `unreachable` when no node answered at all.
 */
export class MatrixError extends Error {
  readonly code: MatrixErrorCode;
  readonly method: string;
  readonly cause?: unknown;

  constructor(code: MatrixErrorCode, method: string, message: string, cause?: unknown) {
    super(message);
    this.name = 'MatrixError';
    this.code = code;
    this.method = method;
    if (cause !== undefined) this.cause = cause;
  }
}

export type ProviderOrigin = 'PROVIDER_ORIGIN_UNSPECIFIED' | 'PROVIDER_ORIGIN_LOCAL' | 'PROVIDER_ORIGIN_REMOTE';

export type JobStatus =
  | 'JOB_STATUS_UNSPECIFIED'
  | 'JOB_STATUS_PENDING'
  | 'JOB_STATUS_RUNNING'
  | 'JOB_STATUS_COMPLETED'
  | 'JOB_STATUS_CANCELLED'
  | 'JOB_STATUS_FAILED';

export type InferenceJobStatus =
  | 'INFERENCE_JOB_STATUS_UNSPECIFIED'
  | 'INFERENCE_JOB_STATUS_PENDING'
  | 'INFERENCE_JOB_STATUS_RUNNING'
  | 'INFERENCE_JOB_STATUS_COMPLETED'
  | 'INFERENCE_JOB_STATUS_FAILED'
  | 'INFERENCE_JOB_STATUS_SETTLING';

export type ChatRole = 'CHAT_ROLE_UNSPECIFIED' | 'CHAT_ROLE_SYSTEM' | 'CHAT_ROLE_USER' | 'CHAT_ROLE_ASSISTANT';

export type AgentStatus =
  | 'AGENT_STATUS_UNSPECIFIED'
  | 'AGENT_STATUS_DEPLOYED'
  | 'AGENT_STATUS_RUNNING'
  | 'AGENT_STATUS_FAILED';

export interface Provider {
  id: string;
  /** Total units the provider advertises. */
  capacity: bigint;
  /** Price in native MATRIX base units per unit of compute. */
  pricePerUnit: bigint;
  /** Units not currently reserved by a job. */
  available: bigint;
  origin: ProviderOrigin;
  /** libp2p peer id, empty for a local provider. */
  peerId: string;
}

export interface Job {
  id: string;
  buyer: string;
  provider: string;
  units: bigint;
  /** units * pricePerUnit, fixed when the job was submitted. */
  price: bigint;
  status: JobStatus;
  /** RFC 3339. */
  createdAt: string;
  updatedAt: string;
}

/**
 * A committed value transfer read back from the consensus transaction history.
 *
 * Native MATRIX transfers settle through consensus now: a signed transfer is
 * ordered and applied by a quorum, so the history is the ordered sequence of
 * committed transfers (the same on every node) rather than a per-node hash
 * chain. `index` is the transfer's stable position in that sequence and
 * `blockHeight` is the committed block it landed in.
 */
export interface Transaction {
  /** Stable zero-based position in the consensus transaction history. */
  index: bigint;
  from: string;
  to: string;
  /** Gross amount, before any protocol fee. */
  amount: bigint;
  /** Per-sender uniquifier (consensus dedups committed transfers for replay). */
  nonce: bigint;
  /** Committed block height the transfer landed in. */
  blockHeight: bigint;
  timestamp: string;
}

/** The outcome of settling a signed transfer through consensus. */
export interface SettledTransfer {
  /** The settled transfer, with its committed index and block height. */
  transaction: Transaction;
  /** Whether the transfer was ordered into a committed block. */
  committed: boolean;
  /** Whether the transfer actually moved credits (false = skipped as unaffordable). */
  applied: boolean;
}

export interface Balance {
  account: string;
  balance: bigint;
}

export interface ChatMessage {
  role: ChatRole;
  content: string;
}

export interface InferenceJob {
  id: string;
  buyer: string;
  provider: string;
  model: string;
  status: InferenceJobStatus;
  completion: string;
  units: bigint;
  createdAt: string;
  updatedAt: string;
}

/** A deployed WebAssembly agent and the outcome of its most recent run. */
export interface Agent {
  id: string;
  status: AgentStatus;
  /** Hex-encoded sha256 of the deployed module bytes. */
  moduleHash: string;
  /** Size of the deployed module in bytes. */
  moduleSize: bigint;
  /** Anything the module wrote to stdout during its most recent run. */
  lastOutput: string;
  /** The error from the most recent run, empty when the last run succeeded. */
  lastError: string;
  /** Credits charged for the most recent run through consensus (0 when unmetered). */
  lastCharge: bigint;
  createdAt: string;
  lastRunAt: string;
}

/** The result of deploying an agent: the persisted record, whether it ran, and the metered charge. */
export interface AgentDeployment {
  agent: Agent;
  ran: boolean;
  /** Credits settled through consensus for this run (0 when unmetered). */
  charged: bigint;
}

export interface ClientOptions {
  /** Base URL of the node's HTTP endpoint. Defaults to DEFAULT_ENDPOINT. */
  endpoint?: string;
  /** API key, sent as `Authorization: Bearer <key>`. Required when the node runs with ACLs enabled. */
  apiKey?: string;
  /** Abort a call that takes longer than this. Defaults to 30s; 0 disables it. */
  timeoutMs?: number;
  /** Injected for tests, or to add retries or tracing around the transport. */
  fetch?: typeof globalThis.fetch;
}

/** Coerce a wire value to bigint. 64-bit fields arrive as strings. */
function big(value: unknown): bigint {
  if (typeof value === 'bigint') return value;
  if (typeof value === 'number') return BigInt(Math.trunc(value));
  if (typeof value === 'string' && value !== '') return BigInt(value);
  return 0n;
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function decodeProvider(raw: Record<string, unknown>): Provider {
  return {
    id: str(raw.id),
    capacity: big(raw.capacity),
    pricePerUnit: big(raw.pricePerUnit),
    available: big(raw.available),
    origin: (str(raw.origin) || 'PROVIDER_ORIGIN_UNSPECIFIED') as ProviderOrigin,
    peerId: str(raw.peerId),
  };
}

function decodeJob(raw: Record<string, unknown>): Job {
  return {
    id: str(raw.id),
    buyer: str(raw.buyer),
    provider: str(raw.provider),
    units: big(raw.units),
    price: big(raw.price),
    status: (str(raw.status) || 'JOB_STATUS_UNSPECIFIED') as JobStatus,
    createdAt: str(raw.createdAt),
    updatedAt: str(raw.updatedAt),
  };
}

function decodeTransaction(raw: Record<string, unknown>): Transaction {
  return {
    index: big(raw.index),
    from: str(raw.from),
    to: str(raw.to),
    amount: big(raw.amount),
    nonce: big(raw.nonce),
    blockHeight: big(raw.blockHeight),
    timestamp: str(raw.timestamp),
  };
}

function decodeInferenceJob(raw: Record<string, unknown>): InferenceJob {
  return {
    id: str(raw.id),
    buyer: str(raw.buyer),
    provider: str(raw.provider),
    model: str(raw.model),
    status: (str(raw.status) || 'INFERENCE_JOB_STATUS_UNSPECIFIED') as InferenceJobStatus,
    completion: str(raw.completion),
    units: big(raw.units),
    createdAt: str(raw.createdAt),
    updatedAt: str(raw.updatedAt),
  };
}

function decodeAgent(raw: Record<string, unknown>): Agent {
  return {
    id: str(raw.id),
    status: (str(raw.status) || 'AGENT_STATUS_UNSPECIFIED') as AgentStatus,
    moduleHash: str(raw.moduleHash),
    moduleSize: big(raw.moduleSize),
    lastOutput: str(raw.lastOutput),
    lastError: str(raw.lastError),
    lastCharge: big(raw.lastCharge),
    createdAt: str(raw.createdAt),
    lastRunAt: str(raw.lastRunAt),
  };
}

const BASE64_ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';

/**
 * Base64-encode bytes for a proto `bytes` field.
 *
 * Written out rather than delegating to `btoa` or `Buffer`, because one of
 * those is missing in any given runtime and depending on either would make the
 * SDK care where it is running. This works the same in a browser, in Node, and
 * in a worker, with no dependency and no @types/node.
 */
export function toBase64(bytes: Uint8Array): string {
  let out = '';
  for (let i = 0; i < bytes.length; i += 3) {
    const b0 = bytes[i] as number;
    const b1 = bytes[i + 1];
    const b2 = bytes[i + 2];
    out += BASE64_ALPHABET[b0 >> 2];
    out += BASE64_ALPHABET[((b0 & 0x03) << 4) | ((b1 ?? 0) >> 4)];
    out += b1 === undefined ? '=' : BASE64_ALPHABET[((b1 & 0x0f) << 2) | ((b2 ?? 0) >> 6)];
    out += b2 === undefined ? '=' : BASE64_ALPHABET[b2 & 0x3f];
  }
  return out;
}

/**
 * A client for one node.
 *
 * Every method maps to one RPC. Nothing is cached and nothing is polled: the
 * client is a transport, so a caller decides when to ask.
 */
export class MatrixClient {
  readonly endpoint: string;
  private readonly apiKey: string | undefined;
  private readonly timeoutMs: number;
  private readonly doFetch: typeof globalThis.fetch;

  constructor(options: ClientOptions = {}) {
    this.endpoint = (options.endpoint ?? DEFAULT_ENDPOINT).replace(/\/$/, '');
    this.apiKey = options.apiKey;
    this.timeoutMs = options.timeoutMs ?? 30_000;
    const injected = options.fetch ?? globalThis.fetch;
    if (typeof injected !== 'function') {
      throw new TypeError('matrix-os-sdk: no fetch available; pass one in options.fetch');
    }
    this.doFetch = injected;
  }

  /** Liveness check against the endpoint's /healthz, which needs no protocol knowledge. */
  async ping(): Promise<void> {
    let response: Response;
    try {
      response = await this.doFetch(`${this.endpoint}/healthz`, { method: 'GET' });
    } catch (cause) {
      throw new MatrixError('unreachable', 'healthz', `no node answered at ${this.endpoint}`, cause);
    }
    if (!response.ok) {
      throw new MatrixError('unavailable', 'healthz', `node at ${this.endpoint} is not serving (${response.status})`);
    }
  }

  // --- MarketService ---------------------------------------------------------

  async registerProvider(input: { id: string; capacity: bigint | number; pricePerUnit: bigint | number }): Promise<Provider> {
    const out = await this.call(MARKET, 'RegisterProvider', {
      id: input.id,
      capacity: String(input.capacity),
      pricePerUnit: String(input.pricePerUnit),
    });
    return decodeProvider(record(out.provider));
  }

  async listProviders(input: { includeRemote?: boolean } = {}): Promise<Provider[]> {
    const out = await this.call(MARKET, 'ListProviders', { includeRemote: input.includeRemote ?? false });
    return list(out.providers).map(decodeProvider);
  }

  async submitJob(input: { buyer: string; provider: string; units: bigint | number }): Promise<Job> {
    const out = await this.call(MARKET, 'SubmitJob', {
      buyer: input.buyer,
      provider: input.provider,
      units: String(input.units),
    });
    return decodeJob(record(out.job));
  }

  async getJob(id: string): Promise<Job> {
    const out = await this.call(MARKET, 'GetJob', { id });
    return decodeJob(record(out.job));
  }

  async listJobs(input: { buyer?: string } = {}): Promise<Job[]> {
    const out = await this.call(MARKET, 'ListJobs', { buyer: input.buyer ?? '' });
    return list(out.jobs).map(decodeJob);
  }

  /**
   * Settles a job: the buyer pays the provider and the job becomes COMPLETED.
   *
   * The payment goes through consensus as a transfer signed by the buyer, so
   * the NODE has to hold the buyer's signing key - it resolves one from the
   * wallet files under ~/.matrix. A job whose buyer it holds no key for is
   * refused with a `failed_precondition` MatrixError and the reservation left
   * intact, so it can be cancelled. Paying out of an account without its
   * owner's signature is what that refusal exists to prevent.
   */
  async completeJob(id: string): Promise<Job> {
    const out = await this.call(MARKET, 'CompleteJob', { id });
    return decodeJob(record(out.job));
  }

  async cancelJob(id: string): Promise<Job> {
    const out = await this.call(MARKET, 'CancelJob', { id });
    return decodeJob(record(out.job));
  }

  async getBalance(account: string): Promise<Balance> {
    const out = await this.call(MARKET, 'GetBalance', { account });
    return { account: str(out.account), balance: big(out.balance) };
  }

  /** Read one committed transfer from the consensus history by its index. */
  async getTransaction(index: bigint | number): Promise<Transaction> {
    const out = await this.call(MARKET, 'GetTransaction', { index: String(index) });
    return decodeTransaction(record(out.transaction));
  }

  /**
   * List committed transfers from the consensus transaction history in
   * ascending index (commit) order. `total` is the number of committed
   * transfers; two nodes return the identical sequence.
   */
  async listTransactions(
    input: { startIndex?: bigint | number; limit?: bigint | number } = {},
  ): Promise<{ transactions: Transaction[]; total: bigint }> {
    const out = await this.call(MARKET, 'ListTransactions', {
      startIndex: String(input.startIndex ?? 0),
      limit: String(input.limit ?? 0),
    });
    return {
      transactions: list(out.transactions).map(decodeTransaction),
      total: big(out.total),
    };
  }

  /**
   * Broadcast a transfer signed by the sender's ed25519 key. It settles through
   * consensus: the signed transfer is ordered into a committed block by a quorum
   * and applied by every node, so all nodes agree on the resulting balances and
   * it pays the protocol fee (when configured) like every other committed
   * transfer.
   *
   * The node verifies the signature; it never sees a private key. Sign with
   * whatever ed25519 implementation you already trust and pass the bytes. The
   * nonce is a per-sender uniquifier (consensus dedups committed transfers for
   * replay protection); prevHash is unused for linkage and may be empty, though
   * it must match what the signature covers.
   *
   * The returned SettledTransfer reports whether the transfer committed and
   * applied; a committed-but-unaffordable transfer surfaces as a
   * FailedPrecondition error rather than a successful result.
   */
  async submitSignedTransfer(input: {
    fromPublicKey: Uint8Array;
    to: string;
    amount: bigint | number;
    nonce: bigint | number;
    prevHash: Uint8Array;
    signature: Uint8Array;
    timestamp?: bigint | number;
  }): Promise<SettledTransfer> {
    const out = await this.call(MARKET, 'SubmitSignedTransfer', {
      fromPublicKey: toBase64(input.fromPublicKey),
      to: input.to,
      amount: String(input.amount),
      nonce: String(input.nonce),
      prevHash: toBase64(input.prevHash),
      signature: toBase64(input.signature),
      timestamp: String(input.timestamp ?? 0),
    });
    return {
      transaction: decodeTransaction(record(out.transaction)),
      committed: out.committed === true,
      applied: out.applied === true,
    };
  }

  /**
   * Move MATRIX from the genesis reward pool to an account.
   *
   * Admin-gated: it needs an API key, and a node with ACLs disabled refuses it
   * outright. It is a development faucet, not a market.
   */
  async fundAccount(input: { account: string; amount: bigint | number }): Promise<Balance> {
    const out = await this.call(MARKET, 'FundAccount', {
      account: input.account,
      amount: String(input.amount),
    });
    return { account: str(out.account), balance: big(out.balance) };
  }

  // --- InferenceService ------------------------------------------------------

  async submitInferenceJob(input: {
    buyer: string;
    provider: string;
    model: string;
    prompt?: string;
    messages?: ChatMessage[];
    maxTokens?: number;
    temperature?: number;
    unitsEstimate?: bigint | number;
  }): Promise<InferenceJob> {
    const out = await this.call(INFERENCE, 'SubmitInferenceJob', {
      buyer: input.buyer,
      provider: input.provider,
      model: input.model,
      prompt: input.prompt ?? '',
      messages: input.messages ?? [],
      maxTokens: input.maxTokens ?? 0,
      temperature: input.temperature ?? 0,
      unitsEstimate: String(input.unitsEstimate ?? 0),
    });
    return decodeInferenceJob(record(out.job));
  }

  async fulfillInferenceJob(id: string): Promise<InferenceJob> {
    const out = await this.call(INFERENCE, 'FulfillInferenceJob', { id });
    return decodeInferenceJob(record(out.job));
  }

  async getInferenceJob(id: string): Promise<InferenceJob> {
    const out = await this.call(INFERENCE, 'GetInferenceJob', { id });
    return decodeInferenceJob(record(out.job));
  }

  // --- AgentService ----------------------------------------------------------

  /**
   * Deploy a WebAssembly module to the node: it is persisted (surviving a
   * restart), instantiated, and run once. When the node meters agent runs,
   * `deployer` names the paying account; an unaffordable or keyless metered
   * deploy is refused rather than run for free.
   */
  async deployAgent(input: {
    id: string;
    wasmModule: Uint8Array;
    limits?: { maxMemoryPages?: number; maxRunTimeMs?: bigint | number };
    deployer?: string;
  }): Promise<AgentDeployment> {
    const request: Record<string, unknown> = {
      id: input.id,
      wasmModule: toBase64(input.wasmModule),
      deployer: input.deployer ?? '',
    };
    if (input.limits) {
      request.limits = {
        maxMemoryPages: input.limits.maxMemoryPages ?? 0,
        maxRunTimeMs: String(input.limits.maxRunTimeMs ?? 0),
      };
    }
    const out = await this.call(AGENT, 'DeployAgent', request);
    return {
      agent: decodeAgent(record(out.agent)),
      ran: out.ran === true,
      charged: big(out.charged),
    };
  }

  /** List the node's deployed agents and their status. */
  async listAgents(): Promise<Agent[]> {
    const out = await this.call(AGENT, 'ListAgents', {});
    return list(out.agents).map(decodeAgent);
  }

  /** Fetch a single deployed agent by ID. */
  async getAgent(id: string): Promise<Agent> {
    const out = await this.call(AGENT, 'GetAgent', { id });
    return decodeAgent(record(out.agent));
  }

  /**
   * Post one request and return the decoded response body.
   *
   * Public because the typed methods above cannot cover a method added to a
   * proto after this version of the SDK was published; `call` reaches it
   * without waiting for a release.
   */
  async call(service: string, method: string, request: unknown): Promise<Record<string, unknown>> {
    const url = `${this.endpoint}/${service}/${method}`;
    const label = `${service}/${method}`;
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      'Connect-Protocol-Version': '1',
    };
    if (this.apiKey) headers.Authorization = `Bearer ${this.apiKey}`;

    const controller = this.timeoutMs > 0 ? new AbortController() : undefined;
    const timer =
      controller && this.timeoutMs > 0 ? setTimeout(() => controller.abort(), this.timeoutMs) : undefined;

    let response: Response;
    try {
      response = await this.doFetch(url, {
        method: 'POST',
        headers,
        body: JSON.stringify(request ?? {}),
        ...(controller ? { signal: controller.signal } : {}),
      });
    } catch (cause) {
      const aborted = controller?.signal.aborted ?? false;
      throw new MatrixError(
        aborted ? 'deadline_exceeded' : 'unreachable',
        label,
        aborted ? `${label} timed out after ${this.timeoutMs}ms` : `could not reach ${this.endpoint} for ${label}`,
        cause,
      );
    } finally {
      if (timer !== undefined) clearTimeout(timer);
    }

    const text = await response.text();
    if (!response.ok) {
      let code: MatrixErrorCode = 'internal';
      let message = `${label} failed with HTTP ${response.status}`;
      try {
        const body = JSON.parse(text) as { code?: string; message?: string };
        if (body.code) code = body.code as MatrixErrorCode;
        if (body.message) message = body.message;
      } catch {
        // A non-JSON body means something other than the node answered; keep
        // the HTTP status as the whole story rather than inventing a code.
      }
      throw new MatrixError(code, label, message);
    }

    if (text === '') return {};
    try {
      return JSON.parse(text) as Record<string, unknown>;
    } catch (cause) {
      throw new MatrixError('internal', label, `${label} returned a body that is not JSON`, cause);
    }
  }
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' ? (value as Record<string, unknown>) : {};
}

function list(value: unknown): Record<string, unknown>[] {
  return Array.isArray(value) ? value.map(record) : [];
}
