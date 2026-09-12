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

// The EIP-712 typed data an eth: account signs (the other two canonical
// payloads), plus the keccak256 its domain salt is computed with. In their own
// files because they are Ethereum's encoding, not the chain's byte layouts -
// but exported from here so a client has one import for everything it signs.
export * from './eip712';
export * from './receipt';
export { keccak256 } from './keccak';


/** Native base units in one whole MATRIX (9 native decimals). */
export const NativeUnit = 1_000_000_000n;

/** ERC-20 base units represented by one native base unit (18 - 9 decimals). */
export const ERC20PerNativeUnit = 1_000_000_000n;

/** Launch anti-dust floor: exactly 100 whole MATRIX in native base units. */
export const MinBridgeLockAmount = 100n * NativeUnit;

const UINT64_MAX = (1n << 64n) - 1n;

/** A lock attestation before or after canonical hex normalization. */
export interface BridgeLockAttestation {
  recipient: string;
  erc20Amount: bigint | number | string;
  lockId: string;
  signature: string;
  attestor: string;
  nativeAmount: bigint | number | string;
}

/** A dependency-free, canonical representation suitable for a contract adapter. */
export interface NormalizedBridgeLockAttestation {
  recipient: string;
  erc20Amount: bigint;
  lockId: string;
  signature: string;
  attestor: string;
  nativeAmount: bigint;
}

function uint64(value: bigint | number, field: string): bigint {
  if (typeof value === 'number') {
    if (!Number.isSafeInteger(value)) {
      throw new RangeError(`${field} must be a safe integer or bigint uint64`);
    }
    value = BigInt(value);
  }
  if (value < 0n || value > UINT64_MAX) {
    throw new RangeError(`${field} must be a uint64 between 0 and 2^64-1`);
  }
  return value;
}

/** Validate the consensus bridge-lock floor and return an exact bigint amount. */
export function validateBridgeLockAmount(value: bigint | number): bigint {
  const amount = uint64(value, 'nativeAmount');
  if (amount < MinBridgeLockAmount) {
    throw new RangeError(`nativeAmount must be at least ${MinBridgeLockAmount} native base units`);
  }
  return amount;
}

function nonNegativeInteger(value: bigint | number | string, field: string): bigint {
  if (typeof value === 'number' && !Number.isSafeInteger(value)) {
    throw new RangeError(`${field} must be a safe integer, bigint, or decimal integer string`);
  }
  if (typeof value === 'string' && !/^(0|[1-9][0-9]*)$/.test(value)) {
    throw new TypeError(`${field} must be a non-negative decimal integer`);
  }
  const out = BigInt(value);
  if (out < 0n) throw new RangeError(`${field} must be non-negative`);
  return out;
}

function normalizedHex(value: string, bytes: number, field: string): string {
  const raw = value.startsWith('0x') || value.startsWith('0X') ? value.slice(2) : value;
  if (!new RegExp(`^[0-9a-fA-F]{${bytes * 2}}$`).test(raw)) {
    throw new TypeError(`${field} must be exactly ${bytes} bytes of hex`);
  }
  return `0x${raw.toLowerCase()}`;
}

/** Parse and normalize an Ethereum address, returning its exact 20 bytes. */
export function ethereumAddressBytes(address: string): Uint8Array {
  const normalized = normalizedHex(address, 20, 'ethereum address');
  const out = new Uint8Array(20);
  for (let i = 0; i < out.length; i++) {
    out[i] = Number.parseInt(normalized.slice(2 + i * 2, 4 + i * 2), 16);
  }
  return out;
}

/** The native account id controlled by an Ethereum address, matching Go EthAccountID. */
export function ethereumAccountId(address: string): string {
  return `eth:${normalizedHex(address, 20, 'ethereum address')}`;
}

/** Derive the canonical native account id from a server-supported sender key. */
export function senderAccountId(senderKey: Uint8Array): string {
  const hex = Array.from(senderKey, (byte) => byte.toString(16).padStart(2, '0')).join('');
  if (senderKey.length === 20) return ethereumAccountId(hex);
  if (senderKey.length === 32) return hex;
  throw new TypeError('senderKey must be a 32-byte ed25519 key or raw 20-byte EVM address');
}

/**
 * Derive the bytes32 id for a committed native bridge lock.
 *
 * This is byte-identical to Go consensus.DeriveLockID:
 * u64be nonce || u32be UTF-8 sender byte length || sender UTF-8 ||
 * 20 recipient bytes || u64be native amount.
 */
export async function deriveBridgeLockId(
  nonce: bigint | number,
  sender: string,
  recipient: string,
  nativeAmount: bigint | number,
): Promise<string> {
  const checkedNonce = uint64(nonce, 'nonce');
  const checkedAmount = uint64(nativeAmount, 'nativeAmount');
  const senderBytes = utf8(sender);
  if (senderBytes.length > 0xffff_ffff) {
    throw new RangeError('sender UTF-8 encoding exceeds uint32 length');
  }
  const payload = concat([
    u64(checkedNonce),
    u32(senderBytes.length),
    senderBytes,
    ethereumAddressBytes(recipient),
    u64(checkedAmount),
  ]);
  const hash = await crypto.subtle.digest('SHA-256', payload as BufferSource);
  return `0x${Array.from(new Uint8Array(hash), (byte) => byte.toString(16).padStart(2, '0')).join('')}`;
}

/** Normalize one attestation's encodings without claiming signature verification. */
export function normalizeBridgeLockAttestation(
  attestation: BridgeLockAttestation,
): NormalizedBridgeLockAttestation {
  return {
    recipient: normalizedHex(attestation.recipient, 20, 'attestation recipient'),
    erc20Amount: nonNegativeInteger(attestation.erc20Amount, 'attestation erc20Amount'),
    lockId: normalizedHex(attestation.lockId, 32, 'attestation lockId'),
    signature: normalizedHex(attestation.signature, 65, 'attestation signature'),
    attestor: normalizedHex(attestation.attestor, 20, 'attestation attestor'),
    nativeAmount: uint64(
      nonNegativeInteger(attestation.nativeAmount, 'attestation nativeAmount'),
      'attestation nativeAmount',
    ),
  };
}

/**
 * Require a set of validators to attest to one identical lock and reject a
 * claimed attestor appearing twice. This validates agreement and exact amount
 * conversion only; WrappedMatrix verifies the signatures cryptographically.
 */
export function validateBridgeLockAttestations(
  attestations: readonly BridgeLockAttestation[],
): NormalizedBridgeLockAttestation[] {
  if (attestations.length === 0) throw new Error('at least one lock attestation is required');
  const normalized = attestations.map(normalizeBridgeLockAttestation);
  const expected = normalized[0] as NormalizedBridgeLockAttestation;
  const seen = new Set<string>();

  for (const attestation of normalized) {
    if (seen.has(attestation.attestor)) {
      throw new Error(`duplicate claimed attestor ${attestation.attestor}`);
    }
    seen.add(attestation.attestor);
    if (
      attestation.lockId !== expected.lockId ||
      attestation.recipient !== expected.recipient ||
      attestation.nativeAmount !== expected.nativeAmount ||
      attestation.erc20Amount !== expected.erc20Amount
    ) {
      throw new Error('lock attestations disagree on lock id, recipient, or amount');
    }
    if (attestation.erc20Amount !== attestation.nativeAmount * ERC20PerNativeUnit) {
      throw new Error('lock attestation ERC-20 amount does not match native amount conversion');
    }
  }
  return normalized;
}
