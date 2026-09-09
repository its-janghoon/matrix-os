/**
 * The EIP-712 typed data a MetaMask user signs, in the exact shape the node
 * verifies.
 *
 * These objects are the signature. A wallet hashes the type strings and the
 * field order, so a renamed field or a reordered one produces a digest the node
 * does not compute - and the failure reads as "invalid signature", which sends
 * you looking at the key rather than at the schema.
 *
 * THIS USED TO BE A COPY in apps/web, the last one of the four canonical
 * payloads still living outside this package. The blocker was that the domain
 * salt is a keccak256 value and this package takes no dependency; keccak.ts
 * removes it, so the definitions live here now and apps/web re-exports them.
 *
 * The node's own definitions are in services/core/internal/token/eth.go. Both
 * sides are pinned to the same ethers-generated vectors - here in
 * eip712.test.ts and in apps/web's test of its re-export - because only
 * agreeing with a real Ethereum library proves a wallet will produce a
 * signature the node accepts. Every self-consistency test passes just as
 * happily with a wrong digest.
 */

import { keccak256 } from './keccak';

/**
 * keccak256("matrix-os-native-l1"), the domain salt.
 *
 * COMPUTED, no longer transcribed. The first version of this constant was
 * written from memory and was wrong, and nothing but a check against a real
 * library caught it before every signature failed. It is still pinned against
 * ethers' own `ethers.id` in eip712.test.ts, which is now a check on the
 * keccak implementation rather than on a human's clipboard.
 */
export const DOMAIN_SALT: string = toHex(keccak256(new TextEncoder().encode('matrix-os-native-l1')));

/** Must match eip712DomainName / Version / Salt on the node. */
export const EIP712_DOMAIN = {
  name: 'Matrix OS',
  version: '1',
  salt: DOMAIN_SALT,
} as const;

/**
 * A native MATRIX transfer. `to` is a string because a recipient may be an
 * ed25519 id, an eth: id, or a reserved account like "bridge/escrow", none of
 * which an `address` type could express.
 */
export const TRANSFER_TYPES = {
  Transfer: [
    { name: 'from', type: 'address' },
    { name: 'to', type: 'string' },
    { name: 'amount', type: 'uint256' },
    { name: 'nonce', type: 'uint256' },
    { name: 'timestamp', type: 'int64' },
    { name: 'prevHash', type: 'bytes32' },
  ],
} as const;

/**
 * The buyer's proof it is asking for a specific inference. The prompt travels as
 * a digest so the wallet prompt stays readable while the signature is still
 * bound to the exact transcript.
 */
export const RUN_AUTHORIZATION_TYPES = {
  RunAuthorization: [
    { name: 'buyer', type: 'address' },
    { name: 'provider', type: 'string' },
    { name: 'model', type: 'string' },
    { name: 'promptDigest', type: 'bytes32' },
    { name: 'timestamp', type: 'int64' },
  ],
} as const;

/** Hex, 0x-prefixed, of a byte array. */
export function toHex(bytes: Uint8Array): string {
  let out = '0x';
  for (const b of bytes) out += b.toString(16).padStart(2, '0');
  return out;
}

/** Bytes from 0x-prefixed hex. */
export function fromHex(hex: string): Uint8Array {
  const clean = hex.startsWith('0x') ? hex.slice(2) : hex;
  const out = new Uint8Array(clean.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(clean.slice(i * 2, i * 2 + 2), 16);
  return out;
}

/**
 * A bytes32 for typed data: exactly 32 bytes, or the zero value when empty.
 *
 * A short value is refused rather than padded, matching the node. Ethereum
 * tooling right-pads a short bytes32 while integers are left-padded, so a
 * 4-byte value would encode one way here and another way there, and the digests
 * would differ for no visible reason.
 */
export function bytes32(bytes: Uint8Array): string {
  if (bytes.length === 0) return '0x' + '00'.repeat(32);
  if (bytes.length !== 32) {
    throw new Error(
      `a bytes32 must be 32 bytes or empty, got ${bytes.length}: a shorter value would be padded ` +
        'differently here and in the wallet',
    );
  }
  return toHex(bytes);
}
