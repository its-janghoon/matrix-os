import { describe, expect, it } from 'vitest';

import { DOMAIN_SALT, RUN_AUTHORIZATION_TYPES, TRANSFER_TYPES, bytes32, fromHex, toHex } from './eip712';

/**
 * These constants come from ethers v6, which is what MetaMask's
 * eth_signTypedData_v4 computes, and they are the same values the node's Go
 * tests are pinned to.
 *
 * This is the check that matters here. Everything else in this directory can be
 * self-consistent and still wrong: only agreeing with a real Ethereum library
 * proves a wallet will produce a signature the node accepts. The first version
 * of DOMAIN_SALT was written from memory and was wrong, which is exactly the
 * failure this catches - and it would otherwise have surfaced as every signature
 * being rejected as invalid.
 *
 * Regenerate from contracts/, where ethers is installed:
 *   node -e "const {ethers}=require('ethers'); console.log(ethers.id('matrix-os-native-l1'))"
 */
describe('the EIP-712 domain', () => {
  it('uses keccak256("matrix-os-native-l1") as its salt', () => {
    expect(DOMAIN_SALT).toBe('0xfd6bb8e2a58b73b6611d1d283696815d27b85d953f24023fc186a4630338e211');
  });
});

describe('the typed data schemas', () => {
  // A wallet hashes the type string built from these names and types, so a
  // rename or a reorder produces a digest the node does not compute.
  it('matches the Transfer type on the node', () => {
    expect(TRANSFER_TYPES.Transfer.map((f) => `${f.type} ${f.name}`)).toEqual([
      'address from',
      'string to',
      'uint256 amount',
      'uint256 nonce',
      'int64 timestamp',
      'bytes32 prevHash',
    ]);
  });

  it('matches the RunAuthorization type on the node', () => {
    expect(RUN_AUTHORIZATION_TYPES.RunAuthorization.map((f) => `${f.type} ${f.name}`)).toEqual([
      'address buyer',
      'string provider',
      'string model',
      'bytes32 promptDigest',
      'int64 timestamp',
    ]);
  });
});

describe('bytes32', () => {
  it('accepts exactly 32 bytes', () => {
    const b = new Uint8Array(32).fill(0xab);
    expect(bytes32(b)).toBe('0x' + 'ab'.repeat(32));
  });

  it('reads empty as the zero value', () => {
    expect(bytes32(new Uint8Array())).toBe('0x' + '00'.repeat(32));
  });

  // The padding trap: Ethereum tooling right-pads a short bytes32 while every
  // integer is left-padded, so a short value would encode one way here and the
  // other way in the wallet, and the digests would silently differ.
  it('refuses a short value rather than padding it ambiguously', () => {
    for (const n of [1, 4, 31, 33]) {
      expect(() => bytes32(new Uint8Array(n))).toThrow(/32 bytes or empty/);
    }
  });
});

describe('hex', () => {
  it('round-trips including the high bytes', () => {
    const b = new Uint8Array([0x00, 0x0f, 0x7f, 0x80, 0xff]);
    expect(Array.from(fromHex(toHex(b)))).toEqual(Array.from(b));
  });

  it('accepts hex with or without the 0x', () => {
    expect(Array.from(fromHex('0xdead'))).toEqual([0xde, 0xad]);
    expect(Array.from(fromHex('dead'))).toEqual([0xde, 0xad]);
  });
});
