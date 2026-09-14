/**
 * The node calls a self-custody page makes, and nothing else.
 *
 * Everything here is the CLIENT-SIGNED path: the page's own key authorises the
 * spend and the node never holds it. That is the whole reason this file is a
 * handful of RPCs rather than a port of the SDK.
 *
 * A node has to be configured for this. It needs `connect.public_reads` so a
 * keyless page can read a balance, and `connect.signed_writes` so it can reach
 * the three methods whose authority is a signature rather than an API key. A
 * node without them answers "authentication required", and reportProblem below
 * says so in those words rather than leaving a blank screen.
 */

import { fromBase64, toBase64 } from './signing';
import type { Message, Signer } from './signer';

const MARKET = 'matrix.market.v1.MarketService';
const INFERENCE = 'matrix.inference.v1.InferenceService';

/**
 * A node on this machine. Offered as a choice, never assumed.
 */
export const LOCAL_ENDPOINT = 'http://127.0.0.1:9093';

/**
 * The node a page talks to by default.
 *
 * It was LOCAL_ENDPOINT, and that is the address of a node the reader does not
 * have. Someone arriving at /market has no matrixd on their laptop, so the page
 * asked a host that was not listening and rendered an empty table - and an empty
 * marketplace does not read as "you are not connected to one", it reads as a
 * marketplace with no sellers in it. The one audience who would have had a node
 * running is the one who did not need the page.
 *
 * So it points at a public node. A read is answered the same way by any node -
 * what comes back is that node's own view of the order book, which is the honest
 * thing for a page to show and the same thing a buyer's own node would tell them
 * - and an operator running their own can still type its address into the field.
 *
 * NEXT_PUBLIC_MATRIX_VALIDATOR_URLS is the same comma-separated list the bridge
 * config reads, so there is one place to change when the public face moves
 * (a sentry, a load balancer) rather than two that can disagree.
 *
 * INFERENCE IS DIFFERENT and this default does not cover it: a prompt is served
 * by the node that OWNS the provider, not by whichever node answered the read.
 * A page that runs inference has to use the seller's own endpoint, which arrives
 * in the announcement and is carried on each Seller row.
 */
function firstPublicNode(): string {
  const configured = (process.env.NEXT_PUBLIC_MATRIX_VALIDATOR_URLS ?? '')
    .split(',')
    .map((url) => url.trim())
    .filter((url) => url !== '');
  return configured[0] ?? 'https://validator-1.ecirlabs.com';
}

export const DEFAULT_ENDPOINT = firstPublicNode();

export class NodeError extends Error {
  constructor(
    readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = 'NodeError';
  }
}

/** A model the network is serving, with what it costs. */
export interface ModelOffer {
  id: string;
  providers: number;
  pricePerUnit: bigint;
}

/**
 * One seller: a node, the terms it offers, and what the reading node's own
 * chain says about it.
 *
 * `endpoint` is the field that makes this usable. Inference is served by the
 * node that OWNS the provider, so a buyer that picked a seller announced by
 * somebody else and then sent the request to its own node would be refused -
 * the order book it asked holds local providers only. The endpoint is where the
 * request has to go, and it is signed by the announcing node, so a relaying
 * peer cannot point a buyer's prompt at a host of its choosing.
 *
 * `bonded`, `settledPayments` and `settledPayers` come from the CHAIN the
 * answering node holds, never from the seller's announcement. They are the only
 * things here a seller cannot simply type: an announcement carries no
 * self-reported uptime or rating, deliberately.
 */
/**
 * Which seller a buyer picked, as an identity rather than an address.
 *
 * Both halves are needed: one node announces every backend it runs, and two
 * nodes may settle into the same payout account, so neither id alone names one
 * offer.
 */
export interface SellerChoice {
  nodeId: string;
  providerId: string;
}

export interface Seller {
  id: string;
  nodeId: string;
  endpoint: string;
  models: string[];
  pricePerUnit: bigint;
  available: bigint;
  bonded: bigint;
  settledPayments: bigint;
  settledPayers: bigint;
  /**
   * Base units this account has been paid, totalled over the settled transfers
   * the answering node's chain holds.
   *
   * A count of payments says a seller has been used; an amount says how much
   * anyone was willing to spend on it, which is the harder of the two to
   * manufacture and the one a headline figure should carry.
   */
  settledReceived: bigint;
  origin: 'local' | 'remote';
  /**
   * Whether the node being ASKED verified a maintainer signature saying it
   * operates this seller.
   *
   * Computed by that node against the maintainer its own chain names, never
   * taken from the announcement - a seller can put any bytes in one, and the
   * only thing that makes them mean anything is a signature check against
   * consensus state.
   *
   * It is an identity claim and nothing more. A reader who does not trust that
   * maintainer account learns nothing from it, which is the correct outcome.
   */
  operatorAttested: boolean;
  /** Who the attestation names. Empty unless operatorAttested. */
  operatorName: string;
}

/** What a completed exchange cost, so a UI can show the bill it just paid. */
export interface Settled {
  completion: string;
  /**
   * A reasoning model's working, empty for a model that produced none.
   *
   * The buyer paid for these tokens - a reasoning model spends most of them
   * here - and the receipt's digest covers them, so a page that drops this
   * field bills for text it never shows and then cannot verify the receipt it
   * was handed.
   */
  reasoning: string;
  units: bigint;
  provider: string;
  model: string;
  promptTokens: number;
  completionTokens: number;
  /**
   * The serving node's signed account of what it charged and for what, as the
   * exact bytes it signed.
   *
   * Kept verbatim rather than parsed into fields, because re-encoding it would
   * invalidate the signature and the whole value of a receipt is that the buyer
   * holds the bytes. Empty when the seller's node has no signing key.
   */
  receipt: string;
  /** Who served it, so a UI can name the seller rather than just the account. */
  seller: Seller;
}

export interface NativeTransaction {
  index: bigint;
  from: string;
  to: string;
  amount: bigint;
  nonce: bigint;
  blockHeight: bigint;
}

export interface SignedTransferInput {
  fromPublicKey: Uint8Array;
  to: string;
  amount: bigint;
  nonce: bigint;
  prevHash: Uint8Array;
  signature: Uint8Array;
  timestamp: bigint;
}

export interface SettledTransfer {
  transaction: NativeTransaction;
  committed: boolean;
  applied: boolean;
}

export interface LockAttestation {
  recipient: string;
  erc20Amount: bigint;
  lockId: string;
  signature: string;
  attestor: string;
  nativeAmount: bigint;
}

export interface BridgeReadiness {
  chainId: bigint;
  contract: string;
  attestor: string;
  minLockNative: bigint;
  challenge: Uint8Array;
  signature: Uint8Array;
}

export interface BridgeReconciliation {
  lockedNative: bigint;
  unlockedNative: bigint;
  outstandingNative: bigint;
  escrowBalance: bigint;
  outstandingErc20: bigint;
  blockHeight: bigint;
}

async function rpc(
  endpoint: string,
  service: string,
  method: string,
  body: unknown,
  options: { timeoutMs?: number } = {},
): Promise<Record<string, unknown>> {
  const url = `${endpoint.replace(/\/$/, '')}/${service}/${method}`;
  const controller = options.timeoutMs ? new AbortController() : undefined;
  const timer = controller ? setTimeout(() => controller.abort(), options.timeoutMs) : undefined;
  let response: Response;
  try {
    response = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Connect-Protocol-Version': '1' },
      body: JSON.stringify(body ?? {}),
      ...(controller ? { signal: controller.signal } : {}),
    });
  } catch {
    if (controller?.signal.aborted) {
      throw new NodeError('deadline_exceeded', `${method} timed out after ${options.timeoutMs}ms`);
    }
    throw new NodeError('unreachable', `could not reach ${endpoint}. Is a node running there?`);
  } finally {
    if (timer !== undefined) clearTimeout(timer);
  }

  const text = await response.text();
  if (!response.ok) {
    let code = 'internal';
    let message = `${method} failed with HTTP ${response.status}`;
    try {
      const parsed = JSON.parse(text) as { code?: string; message?: string };
      if (parsed.code) code = parsed.code;
      if (parsed.message) message = parsed.message;
    } catch {
      // Not a Connect error body; the status is what we have.
    }
    throw new NodeError(code, message);
  }
  return text === '' ? {} : (JSON.parse(text) as Record<string, unknown>);
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function big(value: unknown): bigint {
  if (typeof value === 'bigint') return value;
  if (typeof value === 'string' && value !== '') return BigInt(value);
  if (typeof value === 'number') return BigInt(Math.trunc(value));
  return 0n;
}

function num(value: unknown): number {
  return typeof value === 'number' ? value : Number(value ?? 0) || 0;
}

function obj(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' ? (value as Record<string, unknown>) : {};
}

/**
 * How long a READ may take before a page gives up on a node.
 *
 * rpc() has no timeout unless one is given, which is right for running a model -
 * that legitimately takes as long as it takes - and wrong for anything a page
 * blocks its own render on. A node that accepts a connection and never answers,
 * or an address with nothing behind it, otherwise leaves a page loading forever
 * with no error to explain it and nothing for the reader to do.
 *
 * Generous enough that a slow node on a slow link still answers.
 */
const READ_TIMEOUT_MS = 10_000;

/** Reads a balance. Needs `connect.public_reads` on a node with ACLs. */
export async function getBalance(endpoint: string, account: string): Promise<bigint> {
  const out = await rpc(endpoint, MARKET, 'GetBalance', { account }, { timeoutMs: READ_TIMEOUT_MS });
  return big(out.balance);
}

/** True only for the one bridge polling condition that may resolve after commit propagation. */
export function isNotFoundNodeError(error: unknown): error is NodeError {
  return error instanceof NodeError && error.code === 'not_found';
}

/** Submit a public caller-signed native transfer; no API key or server-side key is used. */
export async function submitSignedTransfer(
  endpoint: string,
  input: SignedTransferInput,
): Promise<SettledTransfer> {
  if (input.fromPublicKey.length !== 20 && input.fromPublicKey.length !== 32) {
    throw new Error('fromPublicKey must be a 20-byte EVM address or 32-byte ed25519 key');
  }
  const out = await rpc(endpoint, MARKET, 'SubmitSignedTransfer', {
    fromPublicKey: toBase64(input.fromPublicKey),
    to: input.to,
    amount: String(input.amount),
    nonce: String(input.nonce),
    prevHash: toBase64(input.prevHash),
    signature: toBase64(input.signature),
    timestamp: String(input.timestamp),
  });
  const transaction = obj(out.transaction);
  return {
    transaction: {
      index: big(transaction.index),
      from: str(transaction.from),
      to: str(transaction.to),
      amount: big(transaction.amount),
      nonce: big(transaction.nonce),
      blockHeight: big(transaction.blockHeight),
    },
    committed: out.committed === true,
    applied: out.applied === true,
  };
}

/** True only for endpoint transport failures that a threshold may outvote. */
export function isBridgeReadinessTransportError(error: unknown): error is NodeError {
  return error instanceof NodeError && (error.code === 'unreachable' || error.code === 'deadline_exceeded');
}

/** Request one validator's challenge-bound deployment-readiness proof. */
export async function getBridgeReadiness(
  endpoint: string,
  challenge: Uint8Array,
  timeoutMs = 10_000,
): Promise<BridgeReadiness> {
  if (challenge.length !== 32) throw new Error(`bridge readiness challenge must be 32 bytes, got ${challenge.length}`);
  const out = await rpc(endpoint, MARKET, 'GetBridgeReadiness', { challenge: toBase64(challenge) }, { timeoutMs });
  return {
    chainId: big(out.chainId),
    contract: str(out.contract),
    attestor: str(out.attestor),
    minLockNative: big(out.minLockNative),
    challenge: fromBase64(str(out.challenge)),
    signature: fromBase64(str(out.signature)),
  };
}

/** Fetch one validator's attestation for an already-committed lock. */
export async function getLockAttestation(
  endpoint: string,
  lockId: string,
  timeoutMs = 10_000,
): Promise<LockAttestation> {
  const out = await rpc(endpoint, MARKET, 'GetLockAttestation', { lockId }, { timeoutMs });
  return {
    recipient: str(out.recipient),
    erc20Amount: big(out.erc20Amount),
    lockId: str(out.lockId),
    signature: str(out.signature),
    attestor: str(out.attestor),
    nativeAmount: big(out.nativeAmount),
  };
}

/** Read a public, reproducible bridge escrow/supply reconciliation snapshot. */
export async function getBridgeReconciliation(endpoint: string): Promise<BridgeReconciliation> {
  const out = await rpc(endpoint, MARKET, 'GetBridgeReconciliation', {});
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
 * Lists the models the network serves, cheapest price first among the providers
 * offering each. It reads the order book rather than /v1/models, because that
 * route sits behind an API key and this page has none.
 */
export async function listModels(endpoint: string): Promise<ModelOffer[]> {
  const out = await rpc(endpoint, MARKET, 'ListProviders', { includeRemote: true }, { timeoutMs: READ_TIMEOUT_MS });
  const providers = Array.isArray(out.providers) ? out.providers : [];

  const byModel = new Map<string, { providers: number; cheapest: bigint }>();
  for (const raw of providers) {
    const p = obj(raw);
    if (big(p.available) === 0n) continue;
    const price = big(p.pricePerUnit);
    const models = Array.isArray(p.models) ? p.models : [];
    for (const m of models) {
      if (typeof m !== 'string') continue;
      const seen = byModel.get(m);
      if (!seen) byModel.set(m, { providers: 1, cheapest: price });
      else {
        seen.providers += 1;
        if (price < seen.cheapest) seen.cheapest = price;
      }
    }
  }

  return Array.from(byModel.entries())
    .map(([id, v]) => ({ id, providers: v.providers, pricePerUnit: v.cheapest }))
    .sort((a, b) => a.id.localeCompare(b.id));
}

/**
 * Lists the sellers of a model, with the address each is reached at and what the
 * chain says about them.
 *
 * Reading the directory of ONE node, which is a buyer's real position: that node
 * heard these announcements and holds the chain the bond and settled history are
 * read from. A different node may have heard others.
 */
export async function listSellers(endpoint: string, model?: string): Promise<Seller[]> {
  const out = await rpc(
    endpoint,
    MARKET,
    'ListProviders',
    { includeRemote: true, ...(model ? { model } : {}) },
    { timeoutMs: READ_TIMEOUT_MS },
  );
  const providers = Array.isArray(out.providers) ? out.providers : [];

  const sellers: Seller[] = [];
  for (const raw of providers) {
    const p = obj(raw);
    const id = str(p.id);
    if (id === '') continue;
    const remote = str(p.origin) === 'PROVIDER_ORIGIN_REMOTE';
    sellers.push({
      id,
      nodeId: str(p.nodeId),
      // A local provider is served by the node being asked, so its address is
      // the one the caller already has.
      endpoint: remote ? str(p.endpoint) : endpoint,
      models: (Array.isArray(p.models) ? p.models : []).filter((m): m is string => typeof m === 'string'),
      pricePerUnit: big(p.pricePerUnit),
      available: big(p.available),
      bonded: big(p.bonded),
      settledPayments: big(p.settledPayments),
      settledPayers: big(p.settledPayers),
      settledReceived: big(p.settledReceived),
      origin: remote ? 'remote' : 'local',
      operatorAttested: p.operatorAttested === true,
      operatorName: str(p.operatorName),
    });
  }
  return sellers;
}

/**
 * Picks a seller for a model: cheapest that can actually be reached and can
 * still take the work.
 *
 * REACHABLE IS NOT A DETAIL. Inference is served by the node that OWNS the
 * provider, and this used to return a bare id and let the caller send the
 * request to its own node. That was invisible only because nothing announced -
 * every registry was empty, so no remote seller was ever the cheapest. The
 * moment discovery started working, the cheapest seller was routinely somebody
 * else's, and the buy failed with "provider not found" from a node that had
 * never heard of it.
 *
 * `minBond` is the buyer's own floor. A bond does not make a seller honest -
 * nothing can - but it makes a LISTING cost capital, which is what stops one
 * attacker from filling the directory with cheap fake sellers. A buyer who
 * wants to refuse strangers sets it.
 */
export async function sellerFor(
  endpoint: string,
  model: string,
  opts: { minBond?: bigint; chosen?: SellerChoice } = {},
): Promise<Seller> {
  const minBond = opts.minBond ?? 0n;
  const offered = await listSellers(endpoint, model);

  // A CHOSEN seller is resolved from the live directory, never taken on the
  // caller's word.
  //
  // The choice travels as an IDENTITY - which node, which payout account - and
  // the address to send a prompt to is read back from the directory here. It has
  // to be: a choice that carried its own endpoint would mean a link someone was
  // handed could route their prompt at a host of the sender's choosing, with the
  // page showing the seller they thought they picked. The identity is safe to
  // carry because it is only a lookup key; the endpoint is not, because it is
  // where the request goes.
  if (opts.chosen) {
    const { nodeId, providerId } = opts.chosen;
    const match = offered.find((s) => s.nodeId === nodeId && s.id === providerId);
    if (!match) {
      throw new NodeError(
        'not_found',
        `the seller you picked is not offering ${model} here any more. It may have stopped announcing, ` +
          'run out of capacity, or never been heard by this node.',
      );
    }
    if (match.endpoint === '') {
      throw new NodeError('not_found', 'the seller you picked publishes no address, so it cannot take a prompt');
    }
    return match;
  }

  const candidates = offered.filter((s) => s.available > 0n && s.endpoint !== '' && s.bonded >= minBond);

  let best: Seller | null = null;
  for (const s of candidates) {
    // Cheapest, ties on id, so two identical asks reach the same seller.
    if (!best || s.pricePerUnit < best.pricePerUnit || (s.pricePerUnit === best.pricePerUnit && s.id < best.id)) {
      best = s;
    }
  }
  if (!best) {
    throw new NodeError(
      'not_found',
      minBond > 0n
        ? `nobody serving ${model} has ${minBond} bonded and capacity to spare`
        : `nobody on this network is serving ${model} with capacity to spare`,
    );
  }
  return best;
}

/**
 * Runs an inference and pays for it with the page's own key.
 *
 * It takes a Signer rather than a key, so the same code path serves a
 * browser-held ed25519 key and MetaMask. MetaMask cannot sign arbitrary bytes -
 * only EIP-712 typed data - so the decision of WHAT to sign has to sit inside
 * the signer, which is also what lets its prompt say "Transfer: 26 to gpu-1"
 * instead of showing a hex blob.
 *
 * Two signatures, in order, because they answer different questions:
 *
 *   1. a RUN AUTHORIZATION, before any work happens, proving this page controls
 *      the buyer account and is asking for this exact prompt. Without it the
 *      node would have to take `buyer` on trust, and anyone could make a
 *      provider work for free against someone else's funded account.
 *   2. a PAYMENT, after the model has run, over the invoice the node returns.
 *      The completion is withheld until this is signed - that is the only thing
 *      holding the buyer to the bargain, since the provider has already worked.
 *
 * Streaming is not available here, and that is a consequence rather than an
 * omission: streaming the answer out before the payment is signed would hand
 * over the very thing being withheld.
 */
export async function chat(
  endpoint: string,
  signer: Signer,
  input: { model: string; messages: Message[]; minBond?: bigint; chosen?: SellerChoice },
): Promise<Settled> {
  const seller = await sellerFor(endpoint, input.model, { minBond: input.minBond, chosen: input.chosen });
  const provider = seller.id;
  // Every call below goes to the SELLER's node, not to the one the directory was
  // read from. Inference is served by the node that owns the provider, so a
  // request sent anywhere else is refused by a node that has never heard of it.
  const serving = seller.endpoint;
  const timestamp = BigInt(Date.now()) * 1_000_000n;

  const auth = await signer.signRunAuthorization({
    provider,
    model: input.model,
    messages: input.messages,
    timestamp,
  });

  const ran = await rpc(serving, INFERENCE, 'RunInferenceJob', {
    buyer: signer.accountId,
    provider,
    model: input.model,
    messages: input.messages.map((m) => ({ role: `CHAT_ROLE_${m.role.toUpperCase()}`, content: m.content })),
    unitsEstimate: '4096',
    authorization: {
      publicKey: toBase64(auth.publicKey),
      timestamp: String(auth.timestamp),
      signature: toBase64(auth.signature),
    },
  });

  const payment = obj(ran.payment);
  const signed = await signer.signPayment({
    to: str(payment.to),
    amount: big(payment.amount),
    nonce: big(payment.nonce),
    timestamp: big(payment.timestamp),
    prevHash: fromBase64(str(payment.prevHash)),
  });

  const settled = await rpc(serving, INFERENCE, 'SettleInferenceJob', {
    id: str(payment.jobId),
    fromPublicKey: toBase64(signed.fromPublicKey),
    to: str(payment.to),
    amount: String(big(payment.amount)),
    nonce: String(big(payment.nonce)),
    timestamp: String(big(payment.timestamp)),
    prevHash: str(payment.prevHash),
    signature: toBase64(signed.signature),
  });

  const job = obj(settled.job);
  const usage = obj(job.usage);
  return {
    completion: str(job.completion),
    reasoning: str(job.reasoning),
    units: big(job.units),
    provider: str(job.provider),
    model: str(job.model) || input.model,
    promptTokens: num(usage.promptTokens),
    completionTokens: num(usage.completionTokens),
    receipt: decodeReceipt(job.receipt),
    seller,
  };
}

/**
 * Reads the receipt out of a settled job.
 *
 * It arrives as base64 over Connect's JSON encoding, and what comes back out has
 * to be the EXACT bytes the node signed - a re-encode would invalidate the
 * signature and the whole point is that the buyer holds those bytes.
 */
function decodeReceipt(raw: unknown): string {
  if (typeof raw !== 'string' || raw === '') return '';
  try {
    return new TextDecoder().decode(fromBase64(raw));
  } catch {
    return '';
  }
}

/**
 * Turns a node error into something worth showing. The two configuration
 * mistakes that produce a dead page are worth naming outright, because
 * "unauthenticated" on its own sends a reader looking for a login.
 */
export function reportProblem(err: unknown): string {
  if (err instanceof NodeError) {
    if (err.code === 'unauthenticated') {
      return (
        `${err.message}. A page cannot hold an API key, so this node needs ` +
        '`connect.public_reads: true` and `connect.signed_writes: true` in its config.'
      );
    }
    if (err.code === 'failed_precondition' && /insufficient funds/i.test(err.message)) {
      return `${err.message}. Fund this account first - the address is above.`;
    }
    return err.message;
  }
  return err instanceof Error ? err.message : String(err);
}
