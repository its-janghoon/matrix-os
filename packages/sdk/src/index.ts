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
  /**
   * Model identifiers this provider serves, lowercased and sorted by the node.
   * This is what a request naming a model is routed on; a compute-only provider
   * advertises none and is reachable only by naming its id.
   */
  models: string[];
}

/**
 * The exact transfer a buyer must sign to settle an inference job.
 *
 * The price of an inference is not knowable until the work is done - it comes
 * from the tokens the backend reported - so a buyer cannot pre-sign for it the
 * way `submitSignedTransfer` allows. `runInferenceJob` runs the model and hands
 * back this invoice instead; signing it authorises this recipient and this
 * amount and nothing else. The completion is withheld until you settle.
 */
/** One message of a streamed completion. */
export interface InferenceChunk {
  /** The next piece of the completion. Concatenating every delta gives the whole. */
  delta: string;
  /** Present on every chunk, so a caller can correlate without waiting for the end. */
  jobId: string;
  /**
   * Set on the final chunk only: the settled job. Do not treat a stream as paid
   * for until you have this.
   */
  job?: InferenceJob;
  /**
   * True on the final chunk when the provider's backend could not stream and
   * the whole completion arrived as one delta, so a UI can stop pretending
   * there is a typing effect.
   */
  streamedOneShot: boolean;
}

export interface PaymentRequest {
  jobId: string;
  /** The buyer's account, which must be the signer. */
  from: string;
  /** The provider being paid. */
  to: string;
  /** The charge in native MATRIX base units. */
  amount: bigint;
  nonce: bigint;
  timestamp: bigint;
  prevHash: Uint8Array;
  /** What the charge is for, so a wallet can show a reason with the number. */
  usage: TokenUsage;
  model: string;
  /** When an unsigned request stops being settleable (RFC 3339). */
  expiresAt: string;
}

export interface TokenUsage {
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
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

/**
 * A lock-and-mint bridge backing snapshot: proof that the native MATRIX held in
 * escrow equals the wrapped supply the Ethereum WrappedMatrix contract should
 * show. It is a read of the node's own escrow accounting at a stated committed
 * height, so it is reproducible from public data rather than a manual query.
 */
export interface BridgeReconciliation {
  /** Cumulative native base units ever locked into escrow. */
  lockedNative: bigint;
  /** Cumulative native base units ever unlocked (released on a processed burn). */
  unlockedNative: bigint;
  /** Native base units currently in escrow (lockedNative - unlockedNative). */
  outstandingNative: bigint;
  /** The on-ledger bridge escrow balance; equals outstandingNative or the node errors. */
  escrowBalance: bigint;
  /**
   * outstandingNative converted to ERC-20 base units (18 decimals): the wrapped
   * total supply the Ethereum contract must show. It is a bigint because the
   * value (up to 1e27) does not fit a 64-bit integer.
   */
  outstandingErc20: bigint;
  /** The committed consensus block height the snapshot reflects. */
  blockHeight: bigint;
}

/**
 * One validator's signature authorizing the wrapped mint for a committed lock.
 *
 * A mint needs a THRESHOLD of these, and each node holds only its own attestor
 * key, so a caller collects them by asking several validators and passes the
 * set to `WrappedMatrix.mint`. The contract does the counting; gathering them is
 * the client's job.
 *
 * The authorization is not a bearer token. It mints to the Ethereum address the
 * LOCKER named when they locked, so collecting one gains nothing but the ability
 * to complete somebody's bridge transfer for them.
 */
/**
 * The reserved recipient that locks native MATRIX for an Ethereum address.
 *
 * Locking is an ordinary signed transfer whose RECIPIENT encodes the intent, so
 * getting this string wrong does not produce an error - it produces a transfer
 * to a different reserved namespace, or to an account id nobody holds. Build it
 * with this rather than by hand.
 *
 * The lowercase is load-bearing: consensus refuses a mixed-case address, because
 * two spellings of one address would be two different locks against one escrow.
 *
 * The amount is the transfer's value, not part of the recipient.
 *
 * This is a transcription of `@matrix-os/protocol`'s `bridgeLockRecipient`, kept
 * here because this package is published and must stay dependency-free, and held
 * byte-identical by the differential test in layout-parity.test.ts.
 */
export function bridgeLockRecipient(ethAddress: string): string {
  const raw =
    ethAddress.startsWith('0x') || ethAddress.startsWith('0X')
      ? ethAddress.slice(2)
      : ethAddress;
  if (!/^[0-9a-fA-F]{40}$/.test(raw)) {
    throw new Error(`bridgeLockRecipient: ${ethAddress} is not a 20-byte ethereum address`);
  }
  return `bridge/lock/${raw.toLowerCase()}`;
}

export interface LockAttestation {
  /** Ethereum address the wrapped tokens mint to, 0x hex. */
  recipient: string;
  /** Wrapped amount in ERC-20 base units (18 decimals); a bigint because it exceeds 2^64. */
  erc20Amount: bigint;
  /** The lock this authorizes, 32 bytes of hex. */
  lockId: string;
  /** This node's 65-byte r||s||v signature, hex. */
  signature: string;
  /** The attestor address the signature recovers to, 0x hex. */
  attestor: string;
  /** Native base units locked, so the conversion is checkable without another call. */
  nativeAmount: bigint;
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

function strList(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value.filter((v): v is string => typeof v === 'string');
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
    models: strList(raw.models),
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

function decodeUsage(raw: unknown): TokenUsage {
  const u = record(raw);
  return {
    promptTokens: Number(u.promptTokens ?? 0),
    completionTokens: Number(u.completionTokens ?? 0),
    totalTokens: Number(u.totalTokens ?? 0),
  };
}

function decodePaymentRequest(raw: Record<string, unknown>): PaymentRequest {
  return {
    jobId: str(raw.jobId),
    from: str(raw.from),
    to: str(raw.to),
    amount: big(raw.amount),
    nonce: big(raw.nonce),
    timestamp: big(raw.timestamp),
    prevHash: fromBase64(str(raw.prevHash)),
    usage: decodeUsage(raw.usage),
    model: str(raw.model),
    expiresAt: str(raw.expiresAt),
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
/**
 * The exact bytes a buyer signs to authorise a payment.
 *
 * This is `token.Transaction.SigningBytes` on the node side: a length-prefixed,
 * big-endian layout so no two distinct field combinations can collide into the
 * same signed payload.
 *
 *     uint32(len(from)) | from | uint32(len(to)) | to |
 *     uint64(amount) | uint64(nonce) | int64(timestamp) |
 *     uint32(len(prevHash)) | prevHash
 *
 * Signing is deliberately left to the caller rather than done here. A wallet,
 * a passkey-derived key, WebCrypto's Ed25519 and a hardware signer all hold the
 * key differently, and pinning one of them would make this package care about
 * key custody. It only produces the message.
 *
 * ```ts
 * const bytes = paymentSigningBytes(payment, publicKey);
 * const signature = await crypto.subtle.sign('Ed25519', key, bytes);
 * const job = await client.settleInferenceJob({
 *   payment, fromPublicKey: publicKey, signature: new Uint8Array(signature),
 * });
 * ```
 */
export function paymentSigningBytes(
  payment: Pick<PaymentRequest, 'to' | 'amount' | 'nonce' | 'timestamp' | 'prevHash'>,
  fromPublicKey: Uint8Array,
): Uint8Array {
  const to = utf8(payment.to);
  const out = new Uint8Array(4 + fromPublicKey.length + 4 + to.length + 8 + 8 + 8 + 4 + payment.prevHash.length);
  const view = new DataView(out.buffer);
  let o = 0;

  view.setUint32(o, fromPublicKey.length, false);
  o += 4;
  out.set(fromPublicKey, o);
  o += fromPublicKey.length;

  view.setUint32(o, to.length, false);
  o += 4;
  out.set(to, o);
  o += to.length;

  view.setBigUint64(o, BigInt(payment.amount), false);
  o += 8;
  view.setBigUint64(o, BigInt(payment.nonce), false);
  o += 8;
  // int64: setBigInt64 so a negative timestamp encodes the same two's
  // complement Go writes.
  view.setBigInt64(o, BigInt(payment.timestamp), false);
  o += 8;

  view.setUint32(o, payment.prevHash.length, false);
  o += 4;
  out.set(payment.prevHash, o);

  return out;
}

function utf8(value: string): Uint8Array {
  if (typeof TextEncoder !== 'undefined') return new TextEncoder().encode(value);
  // A minimal fallback so this stays dependency-free on a runtime with no
  // TextEncoder. Account ids and provider ids are ASCII in practice.
  const out: number[] = [];
  for (const ch of value) {
    const cp = ch.codePointAt(0) as number;
    if (cp < 0x80) out.push(cp);
    else if (cp < 0x800) out.push(0xc0 | (cp >> 6), 0x80 | (cp & 0x3f));
    else if (cp < 0x10000) out.push(0xe0 | (cp >> 12), 0x80 | ((cp >> 6) & 0x3f), 0x80 | (cp & 0x3f));
    else
      out.push(
        0xf0 | (cp >> 18),
        0x80 | ((cp >> 12) & 0x3f),
        0x80 | ((cp >> 6) & 0x3f),
        0x80 | (cp & 0x3f),
      );
  }
  return new Uint8Array(out);
}

/**
 * The exact bytes a buyer signs to authorise a RUN, as opposed to a payment.
 *
 * These are two different signatures answering two different questions at two
 * different times. This one says "I am the buyer and I am asking for this
 * work", and it is what makes `runInferenceJob` safe for a node to serve
 * without an API key - a browser cannot hold one, and without this proof
 * `buyer` would be just a string, so anyone could name someone else's funded
 * account, have a provider do the work, and never sign for it. The payment
 * signature (`paymentSigningBytes`) then says "I accept this bill".
 *
 * The layout is a domain-separated, length-prefixed, big-endian serialization
 * of the public key, provider, model, a SHA-256 digest of the effective chat
 * messages, and the timestamp, so an authorization is bound to one provider,
 * one prompt and one moment.
 *
 * The digest is computed here, which is why this function is async: it needs
 * WebCrypto. It uses the EFFECTIVE messages, so sending a bare `prompt` and
 * sending the equivalent one-message transcript produce the same digest.
 */
export async function runAuthorizationSigningBytes(input: {
  fromPublicKey: Uint8Array;
  provider: string;
  model: string;
  timestamp: bigint | number;
  prompt?: string;
  messages?: ChatMessage[];
}): Promise<Uint8Array> {
  const messages: ChatMessage[] =
    input.messages && input.messages.length > 0
      ? input.messages
      : input.prompt && input.prompt.trim() !== ''
        ? [{ role: 'CHAT_ROLE_USER', content: input.prompt }]
        : [];

  const digest = await messagesDigest(messages);

  const domain = utf8(RUN_AUTH_DOMAIN);
  const provider = utf8(input.provider);
  const model = utf8(input.model);
  const total =
    4 + domain.length + 4 + input.fromPublicKey.length + 4 + provider.length + 4 + model.length + 4 + digest.length + 8;

  const out = new Uint8Array(total);
  const view = new DataView(out.buffer);
  let o = 0;
  const put = (bytes: Uint8Array) => {
    view.setUint32(o, bytes.length, false);
    o += 4;
    out.set(bytes, o);
    o += bytes.length;
  };

  put(domain);
  put(input.fromPublicKey);
  put(provider);
  put(model);
  put(digest);
  view.setBigInt64(o, BigInt(input.timestamp), false);

  return out;
}

/** Must match inference.runAuthDomain on the node. */
const RUN_AUTH_DOMAIN = 'matrix/inference/run-authorization/v1';

/**
 * SHA-256 over the length-prefixed role/content pairs, preceded by the count.
 * The prefixes are what stop two different transcripts hashing the same, which
 * would let one signature cover both.
 */
async function messagesDigest(messages: ChatMessage[]): Promise<Uint8Array> {
  const parts: Uint8Array[] = [];
  const count = new Uint8Array(8);
  new DataView(count.buffer).setBigUint64(0, BigInt(messages.length), false);
  parts.push(count);

  for (const m of messages) {
    for (const field of [utf8(roleWireName(m.role)), utf8(m.content)]) {
      const len = new Uint8Array(4);
      new DataView(len.buffer).setUint32(0, field.length, false);
      parts.push(len, field);
    }
  }

  let size = 0;
  for (const p of parts) size += p.length;
  const flat = new Uint8Array(size);
  let at = 0;
  for (const p of parts) {
    flat.set(p, at);
    at += p.length;
  }

  const hash = await crypto.subtle.digest('SHA-256', flat);
  return new Uint8Array(hash);
}

/**
 * The node's internal role strings are "system" / "user" / "assistant", not the
 * proto enum names, and the digest is over those. Mapping here rather than
 * asking a caller to know it is the difference between a signature that
 * verifies and one that mysteriously does not.
 */
function roleWireName(role: ChatRole): string {
  switch (role) {
    case 'CHAT_ROLE_SYSTEM':
      return 'system';
    case 'CHAT_ROLE_ASSISTANT':
      return 'assistant';
    default:
      return 'user';
  }
}

/** Decodes standard base64 (what proto JSON uses for a `bytes` field). */
export function fromBase64(value: string): Uint8Array {
  const clean = value.replace(/[^A-Za-z0-9+/]/g, '');
  const out = new Uint8Array(Math.floor((clean.length * 3) / 4));
  let o = 0;
  for (let i = 0; i < clean.length; i += 4) {
    const c0 = BASE64_ALPHABET.indexOf(clean[i] as string);
    const c1 = BASE64_ALPHABET.indexOf(clean[i + 1] as string);
    const c2 = clean[i + 2] === undefined ? -1 : BASE64_ALPHABET.indexOf(clean[i + 2] as string);
    const c3 = clean[i + 3] === undefined ? -1 : BASE64_ALPHABET.indexOf(clean[i + 3] as string);
    out[o++] = (c0 << 2) | (c1 >> 4);
    if (c2 >= 0) out[o++] = ((c1 & 0x0f) << 4) | (c2 >> 2);
    if (c3 >= 0) out[o++] = ((c2 & 0x03) << 6) | c3;
  }
  return out.subarray(0, o);
}

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

  /**
   * Registers a provider on the order book. Pass `models` for an
   * inference-capable provider so a request naming one of those models can be
   * routed here; omit it for compute-only capacity.
   */
  async registerProvider(input: {
    id: string;
    capacity: bigint | number;
    pricePerUnit: bigint | number;
    models?: string[];
  }): Promise<Provider> {
    const out = await this.call(MARKET, 'RegisterProvider', {
      id: input.id,
      capacity: String(input.capacity),
      pricePerUnit: String(input.pricePerUnit),
      ...(input.models && input.models.length > 0 ? { models: input.models } : {}),
    });
    return decodeProvider(record(out.provider));
  }

  /**
   * Lists providers. Passing `model` returns only those advertising it that
   * still have capacity to reserve, cheapest first.
   */
  async listProviders(input: { includeRemote?: boolean; model?: string } = {}): Promise<Provider[]> {
    const out = await this.call(MARKET, 'ListProviders', {
      includeRemote: input.includeRemote ?? false,
      ...(input.model ? { model: input.model } : {}),
    });
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

  /**
   * Fetch the lock-and-mint bridge reconciliation snapshot: the native MATRIX
   * locked/unlocked/outstanding in escrow, the escrow balance, the equivalent
   * wrapped ERC-20 supply, and the committed height the snapshot reflects.
   *
   * It is a read-only report, reproducible from public data. A node with no
   * bridge configured refuses it with a `failed_precondition` MatrixError; an
   * escrow/accounting mismatch (a broken 1:1 backing invariant) surfaces as an
   * `internal` MatrixError rather than a snapshot.
   */
  async bridgeReconciliation(): Promise<BridgeReconciliation> {
    const out = await this.call(MARKET, 'GetBridgeReconciliation', {});
    return {
      lockedNative: big(out.lockedNative),
      unlockedNative: big(out.unlockedNative),
      outstandingNative: big(out.outstandingNative),
      escrowBalance: big(out.escrowBalance),
      outstandingErc20: big(out.outstandingErc20),
      blockHeight: big(out.blockHeight),
    };
  }

  /**
   * Fetch this node's signature authorizing the wrapped mint for a committed
   * lock.
   *
   * Call it against SEVERAL validators and collect a threshold of signatures;
   * `WrappedMatrix.mint` verifies the set. One node's answer is one signature by
   * design - gossiping partial signatures would be a second consensus for
   * something the contract already checks.
   *
   * The lock must be committed first: locking is a signed transfer to the
   * reserved `bridge/lock/<address>` recipient, and only once a block carries it
   * is there anything to attest to. Asking earlier gives a `not_found`
   * MatrixError, which is the honest answer - signing for value that is not yet
   * escrowed is what the 1:1 backing forbids. A node with no bridge or no
   * attestor key refuses with `failed_precondition`.
   */
  async lockAttestation(lockId: string): Promise<LockAttestation> {
    const out = await this.call(MARKET, 'GetLockAttestation', { lockId });
    return {
      recipient: str(out.recipient),
      erc20Amount: big(out.erc20Amount),
      lockId: str(out.lockId),
      signature: str(out.signature),
      attestor: str(out.attestor),
      nativeAmount: big(out.nativeAmount),
    };
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

  /**
   * Runs an inference and returns the payment to sign, WITHOUT the completion.
   *
   * This is the client-signed path, and it is the one to use when the node
   * should not hold your key: a public endpoint, where signing on your behalf
   * makes its operator a custodian of your balance, or a dApp, where it would
   * make wallet login decorative. Sign the returned payment with
   * `paymentSigningBytes` and pass the signature to `settleInferenceJob` to get
   * the completion.
   *
   * The job holds a reservation while it waits, released at the payment's
   * `expiresAt` if you never sign.
   */
  async runInferenceJob(input: {
    buyer: string;
    provider: string;
    model: string;
    prompt?: string;
    messages?: ChatMessage[];
    maxTokens?: number;
    temperature?: number;
    unitsEstimate?: bigint | number;
    /**
     * The buyer's proof that it controls `buyer` and is asking for this work,
     * signed over `runAuthorizationSigningBytes`. Required by a node that serves
     * this method without an API key, which is what a browser needs.
     */
    authorization?: { publicKey: Uint8Array; timestamp: bigint | number; signature: Uint8Array };
  }): Promise<{ payment: PaymentRequest; job: InferenceJob }> {
    const out = await this.call(INFERENCE, 'RunInferenceJob', {
      buyer: input.buyer,
      provider: input.provider,
      model: input.model,
      prompt: input.prompt ?? '',
      messages: input.messages ?? [],
      maxTokens: input.maxTokens ?? 0,
      temperature: input.temperature ?? 0,
      unitsEstimate: String(input.unitsEstimate ?? 0),
      ...(input.authorization
        ? {
            authorization: {
              publicKey: toBase64(input.authorization.publicKey),
              timestamp: String(input.authorization.timestamp),
              signature: toBase64(input.authorization.signature),
            },
          }
        : {}),
    });
    return {
      payment: decodePaymentRequest(record(out.payment)),
      job: decodeInferenceJob(record(out.job)),
    };
  }

  /**
   * Submits the buyer's signed payment for a job awaiting one, and returns the
   * completion.
   *
   * Every signable field must match the payment request exactly. A valid
   * signature is not enough on its own: a transfer of one base unit to an
   * account you control would verify perfectly, and is refused.
   */
  async settleInferenceJob(input: {
    payment: PaymentRequest;
    fromPublicKey: Uint8Array;
    signature: Uint8Array;
  }): Promise<InferenceJob> {
    const out = await this.call(INFERENCE, 'SettleInferenceJob', {
      id: input.payment.jobId,
      fromPublicKey: toBase64(input.fromPublicKey),
      to: input.payment.to,
      amount: String(input.payment.amount),
      nonce: String(input.payment.nonce),
      timestamp: String(input.payment.timestamp),
      prevHash: toBase64(input.payment.prevHash),
      signature: toBase64(input.signature),
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
  /**
   * Submits an inference job and yields the completion as it is produced, then
   * the settled job.
   *
   * This is the hosted path: the node signs the payment with a key it holds for
   * the buyer once the model has finished. Streaming is deliberately
   * unavailable on the client-signed path, where the completion is withheld
   * until the buyer signs the invoice - streaming the answer out and then asking
   * to be paid would give the whole thing away first.
   *
   * ```ts
   * for await (const chunk of client.streamInferenceJob({ ... })) {
   *   if (chunk.job) console.log('settled', chunk.job.units);
   *   else process.stdout.write(chunk.delta);
   * }
   * ```
   *
   * Breaking out of the loop cancels the request, which aborts the run so the
   * provider stops generating tokens nobody will read.
   */
  async *streamInferenceJob(input: {
    buyer: string;
    provider: string;
    model: string;
    prompt?: string;
    messages?: ChatMessage[];
    maxTokens?: number;
    temperature?: number;
    unitsEstimate?: bigint | number;
  }): AsyncGenerator<InferenceChunk, void, undefined> {
    const frames = this.callStreaming(INFERENCE, 'StreamInferenceJob', {
      buyer: input.buyer,
      provider: input.provider,
      model: input.model,
      prompt: input.prompt ?? '',
      messages: input.messages ?? [],
      maxTokens: input.maxTokens ?? 0,
      temperature: input.temperature ?? 0,
      unitsEstimate: String(input.unitsEstimate ?? 0),
    });
    for await (const raw of frames) {
      const hasJob = raw.job !== undefined && raw.job !== null;
      yield {
        delta: str(raw.delta),
        jobId: str(raw.jobId),
        streamedOneShot: raw.streamedOneShot === true,
        ...(hasJob ? { job: decodeInferenceJob(record(raw.job)) } : {}),
      };
    }
  }

  /**
   * Reads a Connect server-streaming response: enveloped frames of
   * `[1 flag byte][4 big-endian length bytes][payload]`, ending with a frame
   * whose flag bit 0x02 is set. That end frame is not decoration - an HTTP
   * response that has already begun cannot change its status code, so a failure
   * halfway through a stream travels in it. A reader that stopped at
   * end-of-body would read a truncated stream as a complete one.
   */
  private async *callStreaming(
    service: string,
    method: string,
    request: unknown,
  ): AsyncGenerator<Record<string, unknown>, void, undefined> {
    const url = `${this.endpoint}/${service}/${method}`;
    const label = `${service}/${method}`;
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      'Connect-Protocol-Version': '1',
    };
    if (this.apiKey) headers.Authorization = `Bearer ${this.apiKey}`;

    let response: Response;
    try {
      // No timeout on a stream: this.timeoutMs bounds a unary round trip, and a
      // long generation is not a stalled request. A caller that wants to give up
      // breaks out of the loop, which cancels the body.
      response = await this.doFetch(url, { method: 'POST', headers, body: JSON.stringify(request ?? {}) });
    } catch (cause) {
      throw new MatrixError('unreachable', label, `could not reach ${this.endpoint} for ${label}`, cause);
    }

    if (!response.ok) {
      // A failure before the first frame is still an ordinary HTTP error.
      const text = await response.text();
      let code: MatrixErrorCode = 'internal';
      let message = `${label} failed with HTTP ${response.status}`;
      try {
        const body = JSON.parse(text) as { code?: string; message?: string };
        if (body.code) code = body.code as MatrixErrorCode;
        if (body.message) message = body.message;
      } catch {
        // Not a Connect error body; the status and label are what we have.
      }
      throw new MatrixError(code, label, message);
    }
    if (!response.body) {
      throw new MatrixError('internal', label, `${label} returned no body to stream`);
    }

    const reader = response.body.getReader();
    let buffer = new Uint8Array();
    let sawEnd = false;

    try {
      for (;;) {
        const { done, value } = await reader.read();
        if (value && value.length > 0) {
          const next = new Uint8Array(buffer.length + value.length);
          next.set(buffer);
          next.set(value, buffer.length);
          buffer = next;
        }

        // Drain whole frames out of the buffer.
        for (;;) {
          if (buffer.length < 5) break;
          const view = new DataView(buffer.buffer, buffer.byteOffset, buffer.byteLength);
          const flags = buffer[0] as number;
          const length = view.getUint32(1, false);
          if (buffer.length < 5 + length) break;
          const payload = buffer.subarray(5, 5 + length);
          buffer = buffer.subarray(5 + length);

          const text = new TextDecoder().decode(payload);
          const parsed = text === '' ? {} : (JSON.parse(text) as Record<string, unknown>);

          if ((flags & 0x02) !== 0) {
            sawEnd = true;
            const err = parsed.error;
            if (err) {
              const body = record(err);
              throw new MatrixError(
                (str(body.code) || 'internal') as MatrixErrorCode,
                label,
                str(body.message) || `${label} failed mid-stream`,
              );
            }
            return;
          }
          yield parsed;
        }

        if (done) break;
      }
    } finally {
      // Cancel on any exit, including a caller breaking out of the loop, so the
      // provider stops generating.
      await reader.cancel().catch(() => {});
    }

    if (!sawEnd) {
      // The body ended without an end frame, so the stream was cut off. Saying
      // so is the whole reason the end frame exists.
      throw new MatrixError('internal', label,
        `${label} ended without an end-of-stream frame, so the response was truncated`);
    }
  }

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
