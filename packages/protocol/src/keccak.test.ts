import { describe, expect, it } from 'vitest';

import { toHex } from './eip712';
import { keccak256 } from './keccak';

const utf8 = (s: string) => new TextEncoder().encode(s);
const hex = (b: Uint8Array) => toHex(b);

/**
 * Every constant here was generated with viem's keccak256 (apps/web's Ethereum
 * library, the one a wallet's eth_signTypedData_v4 agrees with) and the first
 * three are also the published Keccak-256 reference vectors. Self-consistency
 * proves nothing for a hash: only agreement with a real implementation does,
 * and both times a constant in this repo's EIP-712 material was written from
 * memory it was wrong.
 *
 * Regenerate from apps/web, where viem is installed:
 *   node -e "const {keccak256,stringToBytes}=require('viem'); console.log(keccak256(stringToBytes('...')))"
 */
describe('keccak256', () => {
  it('matches the published vectors, including Keccak (not SHA-3) padding', () => {
    // FIPS-202 SHA3-256('') is a7ffc6f8...: a match there would mean the wrong
    // padding byte, which is the one mistake this function invites.
    expect(hex(keccak256(utf8('')))).toBe(
      '0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470',
    );
    expect(hex(keccak256(utf8('abc')))).toBe(
      '0x4e03657aea45a94fc7d47ba826c8d667c0d1e6e33a64a036ec44f58fa12d6c45',
    );
    expect(hex(keccak256(utf8('The quick brown fox jumps over the lazy dog')))).toBe(
      '0x4d741b6f1eb29cb2a9b9911c82f56fa8d73b04959d3d9d222895df6c0b28aa15',
    );
  });

  it('matches ethers.id on the domain-salt input', () => {
    expect(hex(keccak256(utf8('matrix-os-native-l1')))).toBe(
      '0xfd6bb8e2a58b73b6611d1d283696815d27b85d953f24023fc186a4630338e211',
    );
  });

  // The sponge absorbs 136 bytes per permutation, and the padding collapses to
  // a single 0x81 byte when exactly one byte of the block remains. 135, 136 and
  // 137 are the three inputs that take different branches through that logic; a
  // fixed short vector exercises none of them.
  it('is exact on either side of the 136-byte rate boundary', () => {
    expect(hex(keccak256(utf8('a'.repeat(135))))).toBe(
      '0x34367dc248bbd832f4e3e69dfaac2f92638bd0bbd18f2912ba4ef454919cf446',
    );
    expect(hex(keccak256(utf8('a'.repeat(136))))).toBe(
      '0xa6c4d403279fe3e0af03729caada8374b5ca54d8065329a3ebcaeb4b60aa386e',
    );
    expect(hex(keccak256(utf8('a'.repeat(137))))).toBe(
      '0xd869f639c7046b4929fc92a4d988a8b22c55fbadb802c0c66ebcd484f1915f39',
    );
  });

  it('absorbs a multi-block input', () => {
    expect(hex(keccak256(utf8('a'.repeat(1000))))).toBe(
      '0xb6a4ac1f51884d71f30fa397a5e155de3099e11fc0edef5d08b646e621e19de9',
    );
  });

  it('hashes raw bytes, not text: every byte value 0x00-0xff appears', () => {
    const b = new Uint8Array(300);
    for (let i = 0; i < b.length; i++) b[i] = i & 0xff;
    expect(hex(keccak256(b))).toBe(
      '0xa679e749a6af300c36e7ff2255d220864eab27b382f9cfdc5aa4d13563ba36ff',
    );
  });

  it('returns 32 raw bytes', () => {
    expect(keccak256(utf8('anything'))).toBeInstanceOf(Uint8Array);
    expect(keccak256(utf8('anything')).length).toBe(32);
  });
});
