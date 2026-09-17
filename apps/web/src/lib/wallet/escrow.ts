/**
 * The escrowed path from the browser: the only one where a reader keeps their
 * own key AND watches the answer arrive.
 *
 * Four calls, and the order is the design:
 *
 *   1. RESERVE   the node names an escrow account whose NAME carries the terms,
 *                and hands back the deposit to sign. Nothing has run.
 *   2. FUND      the signed deposit, and the node waits for it to COMMIT AND
 *                APPLY. The seller now holds the most this job can cost.
 *   3. STREAM    the answer, as it is produced. There is nothing left to
 *                withhold, which is the whole point.
 *   4. SETTLE    the buyer's side names what it really cost; consensus pays that
 *                and returns the change.
 *
 * WHAT HAPPENS IF THE READER CLOSES THE TAB AT STEP 3. The provider claims the
 * whole reservation once its expiry passes. That is what made streaming safe to
 * offer, and it means settling honestly is always the cheaper move - so step 4
 * is not a courtesy, it is how the reader gets their change back.
 *
 * THE SETTLEMENT IS CHECKED BEFORE IT IS SIGNED. See ceiling.ts: signing the
 * number the node handed over would give back the one piece of leverage moving
 * settlement to the buyer was meant to create.
 */

import { checkSettlement } from './ceiling';
import { INFERENCE, big, num, obj, rpc, sellerFor, str, type SellerChoice, type Settled } from './node';
import { fromBase64, toBase64 } from './signing';
import type { Message, Signer } from './signer';

/** How far a job has got, for a caller that wants to show it. */
export type EscrowPhase = 'reserving' | 'funding' | 'streaming' | 'settling';

export interface EscrowChatOptions {
  model: string;
  messages: Message[];
  minBond?: bigint;
  chosen?: SellerChoice;
  /** Called with each piece of the answer as it arrives. */
  onDelta?: (delta: string) => void;
  /** Called when the job moves from one step to the next. */
  onPhase?: (phase: EscrowPhase) => void;
}

/** Thrown when the node asks for more than the answer can account for. */
export class SettlementRefused extends Error {
  constructor(
    readonly reason: string,
    readonly jobId: string,
    readonly claimableAt: Date,
  ) {
    super(
      `refused to settle job ${jobId}: ${reason}. The reservation is the seller's to claim ` +
        `after ${claimableAt.toISOString()} if this is not settled before then.`,
    );
    this.name = 'SettlementRefused';
  }
}

export async function chatEscrowed(
  endpoint: string,
  signer: Signer,
  input: EscrowChatOptions,
): Promise<Settled> {
  const seller = await sellerFor(endpoint, input.model, { minBond: input.minBond, chosen: input.chosen });
  // Every call goes to the SELLER's node. Inference is served by the node that
  // owns the provider, so a request sent anywhere else is refused by a node that
  // has never heard of the job.
  const serving = seller.endpoint;
  const wire = input.messages.map((m) => ({ role: `CHAT_ROLE_${m.role.toUpperCase()}`, content: m.content }));

  input.onPhase?.('reserving');
  const auth = await signer.signRunAuthorization({
    provider: seller.id,
    model: input.model,
    messages: input.messages,
    timestamp: BigInt(Date.now()) * 1_000_000n,
  });
  // The SAME authorization object is sent twice: once to reserve, and once to
  // stream. Streaming otherwise takes a job id and nothing else, and a job id is
  // not a secret.
  const authorization = {
    publicKey: toBase64(auth.publicKey),
    timestamp: String(auth.timestamp),
    signature: toBase64(auth.signature),
  };

  const reserved = await rpc(serving, INFERENCE, 'ReserveInferenceEscrow', {
    buyer: signer.accountId,
    provider: seller.id,
    model: input.model,
    messages: wire,
    unitsEstimate: '4096',
    authorization,
  });

  const deposit = obj(reserved.payment);
  const jobId = str(deposit.jobId);
  const claimableAt = new Date(Number(big(reserved.claimableAt)) * 1000);
  const reservedAmount = big(deposit.amount);

  input.onPhase?.('funding');
  const signedDeposit = await signer.signPayment({
    to: str(deposit.to),
    amount: reservedAmount,
    nonce: big(deposit.nonce),
    timestamp: big(deposit.timestamp),
    prevHash: fromBase64(str(deposit.prevHash)),
  });
  await rpc(serving, INFERENCE, 'FundInferenceEscrow', {
    id: jobId,
    fromPublicKey: toBase64(signedDeposit.fromPublicKey),
    to: str(deposit.to),
    amount: String(reservedAmount),
    nonce: String(big(deposit.nonce)),
    timestamp: String(big(deposit.timestamp)),
    prevHash: str(deposit.prevHash),
    signature: toBase64(signedDeposit.signature),
  });

  input.onPhase?.('streaming');
  let completion = '';
  let last: Record<string, unknown> = {};
  for await (const frame of streamRpc(serving, INFERENCE, 'StreamEscrowedInferenceJob', {
    id: jobId,
    authorization,
  })) {
    const delta = str(frame.delta);
    if (delta !== '') {
      completion += delta;
      input.onDelta?.(delta);
    }
    if (frame.payment !== undefined || frame.job !== undefined) last = frame;
  }

  const settlement = obj(last.payment);
  const job = obj(last.job);
  const amount = big(settlement.amount);

  // The check that makes the buyer's signature mean something. Refused BEFORE
  // signing, and loudly: the reader still owes the reservation at the expiry, so
  // a silent refusal would cost them more than a wrong bill.
  const verdict = checkSettlement({
    amount,
    reserved: reservedAmount,
    pricePerUnit: seller.pricePerUnit ?? 0n,
    messages: input.messages,
    completion,
    reasoning: str(job.reasoning),
  });
  if (!verdict.ok) throw new SettlementRefused(verdict.reason, jobId, claimableAt);

  input.onPhase?.('settling');
  const signedSettlement = await signer.signPayment({
    to: str(settlement.to),
    amount,
    nonce: big(settlement.nonce),
    timestamp: big(settlement.timestamp),
    prevHash: fromBase64(str(settlement.prevHash)),
  });
  const done = await rpc(serving, INFERENCE, 'SettleEscrowedInferenceJob', {
    id: jobId,
    fromPublicKey: toBase64(signedSettlement.fromPublicKey),
    to: str(settlement.to),
    amount: String(amount),
    nonce: String(big(settlement.nonce)),
    timestamp: String(big(settlement.timestamp)),
    prevHash: str(settlement.prevHash),
    signature: toBase64(signedSettlement.signature),
  });

  const settled = obj(done.job);
  const usage = obj(settled.usage);
  return {
    // The completion came down the stream, not out of the settled job: the
    // reader has been watching it, and the job's copy is only the node's record.
    completion,
    reasoning: str(settled.reasoning),
    units: big(settled.units),
    provider: str(settled.provider),
    model: str(settled.model) || input.model,
    promptTokens: num(usage.promptTokens),
    completionTokens: num(usage.completionTokens),
    receipt: decodeReceiptField(settled.receipt),
    seller,
  };
}

function decodeReceiptField(raw: unknown): string {
  if (typeof raw !== 'string' || raw === '') return '';
  try {
    return new TextDecoder().decode(fromBase64(raw));
  } catch {
    return '';
  }
}

/**
 * Reads a Connect server-streaming response.
 *
 * The wire format is one enveloped frame per message on an ordinary HTTP
 * response: byte 0 is flags - 0 for a message, 0x02 for the end-of-stream frame
 * that carries any error - then a four-byte big-endian length, then the JSON.
 * Written out here rather than pulled in as a dependency because it is fifteen
 * lines and the alternative is a client library in a page that already has one
 * job.
 *
 * A frame can arrive split across chunks and several can arrive in one, so the
 * buffer is drained by length rather than by chunk boundary. Assuming one frame
 * per chunk works on a fast local node and corrupts the first long answer over a
 * real network.
 */
export async function* streamRpc(
  endpoint: string,
  service: string,
  method: string,
  body: unknown,
): AsyncGenerator<Record<string, unknown>> {
  const res = await fetch(`${endpoint.replace(/\/$/, '')}/${service}/${method}`, {
    method: 'POST',
    headers: { 'content-type': 'application/json', 'connect-protocol-version': '1' },
    body: JSON.stringify(body),
  });
  if (!res.ok || res.body === null) {
    throw new Error(`${method} failed: ${res.status} ${await res.text().catch(() => '')}`.trim());
  }

  const reader = res.body.getReader();
  let buf = new Uint8Array(0);
  for (;;) {
    const { done, value } = await reader.read();
    if (value !== undefined && value.length > 0) {
      const next = new Uint8Array(buf.length + value.length);
      next.set(buf);
      next.set(value, buf.length);
      buf = next;
    }

    // Drain every COMPLETE frame the buffer now holds, not one per chunk.
    for (;;) {
      if (buf.length < 5) break;
      const view = new DataView(buf.buffer, buf.byteOffset, buf.byteLength);
      const flags = view.getUint8(0);
      const size = view.getUint32(1, false);
      if (buf.length < 5 + size) break;

      const payload = new TextDecoder().decode(buf.subarray(5, 5 + size));
      buf = buf.subarray(5 + size);

      if ((flags & 0x02) !== 0) {
        // The end-of-stream frame. It carries an error when there was one, and
        // that error is the only way a failure after the first byte can reach
        // the caller - so it must not be swallowed as "the stream ended".
        const end = payload === '' ? {} : (JSON.parse(payload) as Record<string, unknown>);
        const err = end.error as { message?: string; code?: string } | undefined;
        if (err) throw new Error(err.message ?? err.code ?? `${method} failed mid-stream`);
        return;
      }
      yield JSON.parse(payload) as Record<string, unknown>;
    }

    if (done) return;
  }
}
