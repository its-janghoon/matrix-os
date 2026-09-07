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

import { fromBase64, paymentSigningBytes, runAuthorizationSigningBytes, toBase64, type Message } from './signing';
import type { Wallet } from './wallet';

const MARKET = 'matrix.market.v1.MarketService';
const INFERENCE = 'matrix.inference.v1.InferenceService';

/** The node a page talks to. Defaults to the reader's own node. */
export const DEFAULT_ENDPOINT = 'http://127.0.0.1:9093';

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

/** What a completed exchange cost, so a UI can show the bill it just paid. */
export interface Settled {
  completion: string;
  units: bigint;
  provider: string;
  model: string;
  promptTokens: number;
  completionTokens: number;
}

async function rpc(
  endpoint: string,
  service: string,
  method: string,
  body: unknown,
): Promise<Record<string, unknown>> {
  const url = `${endpoint.replace(/\/$/, '')}/${service}/${method}`;
  let response: Response;
  try {
    response = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Connect-Protocol-Version': '1' },
      body: JSON.stringify(body ?? {}),
    });
  } catch {
    throw new NodeError('unreachable', `could not reach ${endpoint}. Is a node running there?`);
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

/** Reads a balance. Needs `connect.public_reads` on a node with ACLs. */
export async function getBalance(endpoint: string, account: string): Promise<bigint> {
  const out = await rpc(endpoint, MARKET, 'GetBalance', { account });
  return big(out.balance);
}

/**
 * Lists the models the network serves, cheapest price first among the providers
 * offering each. It reads the order book rather than /v1/models, because that
 * route sits behind an API key and this page has none.
 */
export async function listModels(endpoint: string): Promise<ModelOffer[]> {
  const out = await rpc(endpoint, MARKET, 'ListProviders', { includeRemote: true });
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

/** Picks the cheapest provider serving a model that still has capacity. */
export async function providerFor(endpoint: string, model: string): Promise<string> {
  const out = await rpc(endpoint, MARKET, 'ListProviders', { includeRemote: true, model });
  const providers = Array.isArray(out.providers) ? out.providers : [];
  let best: { id: string; price: bigint } | null = null;
  for (const raw of providers) {
    const p = obj(raw);
    const id = str(p.id);
    if (id === '' || big(p.available) === 0n) continue;
    const price = big(p.pricePerUnit);
    // Cheapest, ties on id, so two identical asks reach the same provider.
    if (!best || price < best.price || (price === best.price && id < best.id)) best = { id, price };
  }
  if (!best) throw new NodeError('not_found', `nobody on this network is serving ${model} with capacity to spare`);
  return best.id;
}

/**
 * Runs an inference and pays for it with the page's own key.
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
  wallet: Wallet,
  input: { model: string; messages: Message[] },
): Promise<Settled> {
  const provider = await providerFor(endpoint, input.model);
  const timestamp = BigInt(Date.now()) * 1_000_000n;

  const authBytes = await runAuthorizationSigningBytes({
    fromPublicKey: wallet.publicKey,
    provider,
    model: input.model,
    timestamp,
    messages: input.messages,
  });
  const authSignature = await wallet.sign(authBytes);

  const ran = await rpc(endpoint, INFERENCE, 'RunInferenceJob', {
    buyer: wallet.accountId,
    provider,
    model: input.model,
    messages: input.messages.map((m) => ({ role: `CHAT_ROLE_${m.role.toUpperCase()}`, content: m.content })),
    unitsEstimate: '4096',
    authorization: {
      publicKey: toBase64(wallet.publicKey),
      timestamp: String(timestamp),
      signature: toBase64(authSignature),
    },
  });

  const payment = obj(ran.payment);
  const prevHash = fromBase64(str(payment.prevHash));
  const paymentBytes = paymentSigningBytes({
    fromPublicKey: wallet.publicKey,
    to: str(payment.to),
    amount: big(payment.amount),
    nonce: big(payment.nonce),
    timestamp: big(payment.timestamp),
    prevHash,
  });
  const paymentSignature = await wallet.sign(paymentBytes);

  const settled = await rpc(endpoint, INFERENCE, 'SettleInferenceJob', {
    id: str(payment.jobId),
    fromPublicKey: toBase64(wallet.publicKey),
    to: str(payment.to),
    amount: String(big(payment.amount)),
    nonce: String(big(payment.nonce)),
    timestamp: String(big(payment.timestamp)),
    prevHash: str(payment.prevHash),
    signature: toBase64(paymentSignature),
  });

  const job = obj(settled.job);
  const usage = obj(job.usage);
  return {
    completion: str(job.completion),
    units: big(job.units),
    provider: str(job.provider),
    model: str(job.model) || input.model,
    promptTokens: num(usage.promptTokens),
    completionTokens: num(usage.completionTokens),
  };
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
