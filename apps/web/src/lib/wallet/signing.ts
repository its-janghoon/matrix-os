/**
 * The canonical bytes the Matrix chain verifies, for the two signatures a
 * self-custody client produces.
 *
 * WHY THIS IS DUPLICATED. The node is the source of truth
 * (`token.Transaction.SigningBytes` and `inference.RunAuthorization.
 * SigningBytes`), and `packages/sdk` already carries a TypeScript copy. This
 * repo has no workspace linkage, so `apps/web` cannot import that package
 * without a build pipeline for it, and three copies of a byte layout is exactly
 * how a signature quietly stops verifying.
 *
 * What keeps them honest is the golden vectors in signing.test.ts: the same
 * base64 constants the SDK's tests use, taken from the Go implementation's own
 * output. A one-byte drift in any copy fails those tests. Without them a drift
 * would surface as "invalid signature" at runtime, which reads as a key problem
 * and sends you looking in the wrong place entirely.
 */

/** Must match inference.runAuthDomain on the node. */
const RUN_AUTH_DOMAIN = 'matrix/inference/run-authorization/v1';

/** The chat roles, in the wire spelling the node's digest uses. */
export type Role = 'system' | 'user' | 'assistant';

export interface Message {
  role: Role;
  content: string;
}

function utf8(value: string): Uint8Array {
  return new TextEncoder().encode(value);
}

/** Concatenates, so the length-prefixed pieces can be assembled in order. */
function concat(parts: Uint8Array[]): Uint8Array {
  let size = 0;
  for (const p of parts) size += p.length;
  const out = new Uint8Array(size);
  let at = 0;
  for (const p of parts) {
    out.set(p, at);
    at += p.length;
  }
  return out;
}

function u32(value: number): Uint8Array {
  const out = new Uint8Array(4);
  new DataView(out.buffer).setUint32(0, value, false);
  return out;
}

function u64(value: bigint): Uint8Array {
  const out = new Uint8Array(8);
  new DataView(out.buffer).setBigUint64(0, value, false);
  return out;
}

function i64(value: bigint): Uint8Array {
  const out = new Uint8Array(8);
  new DataView(out.buffer).setBigInt64(0, value, false);
  return out;
}

/** Length-prefixed, which is what stops two distinct field sets colliding. */
function lp(bytes: Uint8Array): Uint8Array {
  return concat([u32(bytes.length), bytes]);
}

/**
 * The bytes a buyer signs to authorise a PAYMENT. Mirrors
 * token.Transaction.SigningBytes:
 *
 *     uint32(len(from)) | from | uint32(len(to)) | to |
 *     uint64(amount) | uint64(nonce) | int64(timestamp) |
 *     uint32(len(prevHash)) | prevHash
 */
export function paymentSigningBytes(input: {
  fromPublicKey: Uint8Array;
  to: string;
  amount: bigint;
  nonce: bigint;
  timestamp: bigint;
  prevHash: Uint8Array;
}): Uint8Array {
  return concat([
    lp(input.fromPublicKey),
    lp(utf8(input.to)),
    u64(input.amount),
    u64(input.nonce),
    i64(input.timestamp),
    lp(input.prevHash),
  ]);
}

/**
 * The bytes a buyer signs to authorise a RUN. Mirrors
 * inference.RunAuthorization.SigningBytes.
 *
 * This is a different signature from the payment, answering a different
 * question at a different time: this one says "I am the buyer and I am asking
 * for this work", and it is what lets a node serve RunInferenceJob without an
 * API key, which is what a page needs because it cannot hold one.
 */
export async function runAuthorizationSigningBytes(input: {
  fromPublicKey: Uint8Array;
  provider: string;
  model: string;
  timestamp: bigint;
  messages: Message[];
}): Promise<Uint8Array> {
  const digest = await messagesDigest(input.messages);
  return concat([
    lp(utf8(RUN_AUTH_DOMAIN)),
    lp(input.fromPublicKey),
    lp(utf8(input.provider)),
    lp(utf8(input.model)),
    lp(digest),
    i64(input.timestamp),
  ]);
}

/**
 * SHA-256 over the count followed by the length-prefixed role/content pairs.
 * The prefixes are why "ab" and "a"+"b" do not hash the same, which would let
 * one signature authorise either conversation.
 */
export async function messagesDigest(messages: Message[]): Promise<Uint8Array> {
  const parts: Uint8Array[] = [u64(BigInt(messages.length))];
  for (const m of messages) {
    parts.push(lp(utf8(m.role)), lp(utf8(m.content)));
  }
  const hash = await crypto.subtle.digest('SHA-256', concat(parts) as BufferSource);
  return new Uint8Array(hash);
}

/** Standard base64, which is how proto JSON carries a `bytes` field. */
export function toBase64(bytes: Uint8Array): string {
  let binary = '';
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary);
}

/** Decodes standard base64. */
export function fromBase64(value: string): Uint8Array {
  const binary = atob(value);
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i);
  return out;
}
