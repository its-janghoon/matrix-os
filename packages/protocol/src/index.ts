/**
 * The canonical byte layouts a Matrix OS client signs.
 *
 * WHY THIS PACKAGE EXISTS. The node is the source of truth for these layouts
 * (`token.Transaction.SigningBytes` and `inference.RunAuthorization.
 * SigningBytes` in services/core), and every client has to reproduce them
 * byte-for-byte or its signature simply does not verify. There used to be two
 * independent TypeScript transcriptions - one in `packages/sdk`, one in
 * `apps/web` - each with its own helper functions and its own chance of a
 * one-byte mistake.
 *
 * Golden vectors caught a drift in whichever layout a vector covered. They did
 * not stop a second copy existing, and a drift surfaces at runtime as "invalid
 * signature", which reads as a key problem and sends you looking in the wrong
 * place entirely.
 *
 * `apps/web` now imports these. `packages/sdk` keeps a self-contained
 * implementation because it is PUBLISHED and must not depend on a private
 * workspace package - but its copy is held byte-identical to this one by a
 * differential test over randomized inputs, not only by the three pinned
 * vectors. See packages/sdk/src/layout-parity.test.ts.
 *
 * Nothing here has a dependency, and nothing here touches a key. These
 * functions produce the message; how it is signed - a wallet, WebCrypto, a
 * hardware signer - is the caller's business.
 */

/** Must match inference.runAuthDomain on the node. */
export const RUN_AUTH_DOMAIN = 'matrix/inference/run-authorization/v1';

/** A chat turn's role, spelled exactly as it goes into the signed digest. */
export type Role = 'system' | 'user' | 'assistant';

/** One chat turn. The role spelling is inside the signature. */
export interface Message {
  role: Role;
  content: string;
}

export function utf8(value: string): Uint8Array {
  return new TextEncoder().encode(value);
}

/** Concatenates, so the length-prefixed pieces can be assembled in order. */
export function concat(parts: Uint8Array[]): Uint8Array {
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

/** Big-endian uint32, matching Go's binary.BigEndian.PutUint32. */
export function u32(value: number): Uint8Array {
  const out = new Uint8Array(4);
  new DataView(out.buffer).setUint32(0, value, false);
  return out;
}

/** Big-endian uint64. */
export function u64(value: bigint): Uint8Array {
  const out = new Uint8Array(8);
  new DataView(out.buffer).setBigUint64(0, value, false);
  return out;
}

/**
 * Big-endian int64. Separate from u64 on purpose: a timestamp is signed, and
 * setBigInt64 writes the same two's complement Go does for a negative value.
 */
export function i64(value: bigint): Uint8Array {
  const out = new Uint8Array(8);
  new DataView(out.buffer).setBigInt64(0, value, false);
  return out;
}

/**
 * Length-prefixed, which is what stops two distinct field sets colliding into
 * the same signed payload. Without the prefixes, to="ab" with an empty next
 * field and to="a" with next field "b" produce identical bytes, so one
 * signature would authorize either.
 */
export function lp(bytes: Uint8Array): Uint8Array {
  return concat([u32(bytes.length), bytes]);
}

/** The fields a payment signature covers. */
export interface PaymentSigningInput {
  fromPublicKey: Uint8Array;
  to: string;
  amount: bigint;
  nonce: bigint;
  timestamp: bigint;
  prevHash: Uint8Array;
}

/**
 * The bytes a buyer signs to authorize a PAYMENT. Mirrors
 * token.Transaction.SigningBytes:
 *
 *     uint32(len(from)) | from | uint32(len(to)) | to |
 *     uint64(amount) | uint64(nonce) | int64(timestamp) |
 *     uint32(len(prevHash)) | prevHash
 */
export function paymentSigningBytes(input: PaymentSigningInput): Uint8Array {
  return concat([
    lp(input.fromPublicKey),
    lp(utf8(input.to)),
    u64(input.amount),
    u64(input.nonce),
    i64(input.timestamp),
    lp(input.prevHash),
  ]);
}

/** The fields a run authorization covers. */
export interface RunAuthorizationSigningInput {
  fromPublicKey: Uint8Array;
  provider: string;
  model: string;
  timestamp: bigint;
  messages: Message[];
}

/**
 * The bytes a buyer signs to authorize a RUN. Mirrors
 * inference.RunAuthorization.SigningBytes:
 *
 *     uint32(len(domain)) | domain | uint32(len(from)) | from |
 *     uint32(len(provider)) | provider | uint32(len(model)) | model |
 *     uint32(len(promptDigest)) | promptDigest | int64(timestamp)
 *
 * A different signature from the payment, answering a different question at a
 * different time: this one says "I am the buyer and I am asking for this work",
 * and it is what lets a node serve an inference run without an API key - which
 * is what a browser page needs, because it cannot hold one.
 *
 * The domain prefix is what stops a run authorization being replayed as some
 * other signature over the same key.
 */
export async function runAuthorizationSigningBytes(
  input: RunAuthorizationSigningInput,
): Promise<Uint8Array> {
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
 * SHA-256 over the turn count followed by the length-prefixed role/content
 * pairs. The prefixes are why "ab" and "a"+"b" do not hash the same, which
 * would let one signature authorize either conversation.
 */
export async function messagesDigest(messages: Message[]): Promise<Uint8Array> {
  const parts: Uint8Array[] = [u64(BigInt(messages.length))];
  for (const m of messages) {
    parts.push(lp(utf8(m.role)), lp(utf8(m.content)));
  }
  const hash = await crypto.subtle.digest('SHA-256', concat(parts) as BufferSource);
  return new Uint8Array(hash);
}

/**
 * The reserved recipient that locks native MATRIX for an Ethereum address.
 *
 * Locking is an ordinary signed transfer whose RECIPIENT encodes the intent, so
 * a client that gets this string wrong does not get an error - it makes a
 * transfer to a different reserved namespace, or to an account id that does not
 * exist. That is the class of bug this package exists to prevent, which is why
 * the format lives here rather than being spelled out in each client.
 *
 * It must match Go's `consensus.BridgeLockRecipient` byte for byte, including
 * the LOWERCASE hex: consensus refuses a mixed-case address, because two
 * spellings of one address would be two different locks against one escrow.
 *
 * The amount is the transfer's value, not part of the recipient. This is the one
 * reserved recipient besides a stake bond that legitimately carries money.
 */
export function bridgeLockRecipient(ethAddress: string): string {
  const raw = ethAddress.startsWith('0x') || ethAddress.startsWith('0X')
    ? ethAddress.slice(2)
    : ethAddress;
  if (!/^[0-9a-fA-F]{40}$/.test(raw)) {
    throw new Error(
      `bridgeLockRecipient: ${ethAddress} is not a 20-byte ethereum address`,
    );
  }
  return `bridge/lock/${raw.toLowerCase()}`;
}

/** The prefix `bridgeLockRecipient` produces. Exported so a client can classify
 *  a recipient it reads back without re-implementing the check. */
export const BRIDGE_LOCK_PREFIX = 'bridge/lock/';
