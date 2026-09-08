import { describe, expect, it } from 'vitest';
import * as canonical from '@matrix-os/protocol';
import {
  bridgeLockRecipient,
  paymentSigningBytes,
  runAuthorizationSigningBytes,
  type ChatMessage,
  type ChatRole,
} from './index';

/**
 * Differential test: this package's signing layouts against the canonical ones
 * in `@matrix-os/protocol`, over randomized inputs.
 *
 * WHY A SECOND IMPLEMENTATION IS ALLOWED TO EXIST HERE. This package is
 * PUBLISHED. It cannot depend at runtime on a private workspace package, and it
 * must stay dependency-free so it works unchanged in a browser, in Node and in
 * a worker. So it keeps its own transcription of the layouts.
 *
 * WHY THAT USED TO BE DANGEROUS AND NOW IS NOT. The only guard was three golden
 * vectors. A vector pins the exact input it covers and says nothing about any
 * other: a copy could be wrong for an empty recipient, a zero-length prevHash,
 * a negative timestamp, a multi-byte character, or a nonce above 2^53, and
 * every vector would still pass. A drift then shows up in production as
 * "invalid signature", which reads as a key problem and sends you looking in
 * the wrong place entirely.
 *
 * These tests compare the two implementations field-for-field over hundreds of
 * randomized inputs including exactly those edges. `apps/web` no longer has a
 * copy at all - it imports the canonical package - so this is the last copy,
 * and it is pinned to the one source rather than to three examples.
 */

// A small deterministic PRNG, so a failure is reproducible from the seed rather
// than being a flake nobody can rerun.
function rng(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0;
    return s / 0x100000000;
  };
}

const ROLES: ChatRole[] = [
  'CHAT_ROLE_UNSPECIFIED',
  'CHAT_ROLE_SYSTEM',
  'CHAT_ROLE_USER',
  'CHAT_ROLE_ASSISTANT',
];

// The mapping this package applies before hashing. The node's digest is over
// these wire names, not the proto enum names.
const WIRE_NAME: Record<ChatRole, canonical.Role> = {
  CHAT_ROLE_UNSPECIFIED: 'user',
  CHAT_ROLE_SYSTEM: 'system',
  CHAT_ROLE_USER: 'user',
  CHAT_ROLE_ASSISTANT: 'assistant',
};

// Strings chosen to break a naive length prefix: multi-byte characters mean
// byte length differs from string length, and an empty string means a
// zero-length field.
const STRINGS = [
  '',
  'a',
  'provider-one',
  'gpt-4o-mini',
  '한국어 문자열',
  'emoji and spaces',
  'x'.repeat(300),
];

/**
 * Picks one element. Written out because this tsconfig has
 * noUncheckedIndexedAccess, so an index expression is `T | undefined` and the
 * test should fail loudly on a bad index rather than silently signing over
 * undefined.
 */
function pick<T>(next: () => number, items: readonly T[]): T {
  const at = Math.floor(next() * items.length);
  const value = items[at];
  if (value === undefined) throw new Error(`pick: index ${at} out of range for ${items.length}`);
  return value;
}

function pickBytes(next: () => number, len: number): Uint8Array {
  const out = new Uint8Array(len);
  for (let i = 0; i < len; i++) out[i] = Math.floor(next() * 256);
  return out;
}

describe('signing layouts match the canonical implementation', () => {
  it('paymentSigningBytes agrees over randomized inputs', () => {
    const next = rng(20260907);
    for (let i = 0; i < 400; i++) {
      const fromPublicKey = pickBytes(next, pick(next, [0, 1, 20, 32, 64]));
      const prevHash = pickBytes(next, pick(next, [0, 1, 32]));
      const to = pick(next, STRINGS);
      // Includes values above 2^53, where a Number would silently lose
      // precision, and the unsigned edges.
      const amounts = [0n, 1n, 250n, 2n ** 53n + 1n, 2n ** 64n - 1n];
      const amount = pick(next, amounts);
      const nonce = pick(next, amounts);
      // A negative timestamp is the case that distinguishes int64 from uint64.
      const timestamps = [0n, 1n, 1757250000000000000n, -1n, -(2n ** 63n)];
      const timestamp = pick(next, timestamps);

      const mine = paymentSigningBytes({ to, amount, nonce, timestamp, prevHash }, fromPublicKey);
      const theirs = canonical.paymentSigningBytes({
        fromPublicKey,
        to,
        amount,
        nonce,
        timestamp,
        prevHash,
      });
      expect(
        Array.from(mine),
        `case ${i}: to=${JSON.stringify(to)} amount=${amount} nonce=${nonce} ts=${timestamp}`,
      ).toEqual(Array.from(theirs));
    }
  });

  it('runAuthorizationSigningBytes agrees over randomized inputs', async () => {
    const next = rng(31337);
    for (let i = 0; i < 120; i++) {
      const fromPublicKey = pickBytes(next, pick(next, [20, 32]));
      const provider = pick(next, STRINGS);
      const model = pick(next, STRINGS);
      const timestamp = pick(next, [0n, 1n, 1757250000000000000n, -1n]);

      const turns = Math.floor(next() * 4);
      const messages: ChatMessage[] = [];
      for (let t = 0; t < turns; t++) {
        messages.push({
          role: pick(next, ROLES),
          content: pick(next, STRINGS),
        });
      }

      const mine = await runAuthorizationSigningBytes({
        fromPublicKey,
        provider,
        model,
        timestamp,
        messages,
      });
      const theirs = await canonical.runAuthorizationSigningBytes({
        fromPublicKey,
        provider,
        model,
        timestamp,
        messages: messages.map((m) => ({ role: WIRE_NAME[m.role], content: m.content })),
      });
      expect(
        Array.from(mine),
        `case ${i}: provider=${JSON.stringify(provider)} turns=${turns}`,
      ).toEqual(Array.from(theirs));
    }
  });

  it('agrees on an empty transcript, which is its own layout case', async () => {
    const fromPublicKey = new Uint8Array(32).fill(7);
    const mine = await runAuthorizationSigningBytes({
      fromPublicKey,
      provider: 'p',
      model: 'm',
      timestamp: 1n,
      messages: [],
    });
    const theirs = await canonical.runAuthorizationSigningBytes({
      fromPublicKey,
      provider: 'p',
      model: 'm',
      timestamp: 1n,
      messages: [],
    });
    expect(Array.from(mine)).toEqual(Array.from(theirs));
  });

  it('uses the same run-authorization domain string', async () => {
    // The domain is inside the signature, so a difference here is a signature
    // that never verifies - and it is a string constant transcribed twice,
    // which is the easiest kind of thing to get wrong.
    const fromPublicKey = new Uint8Array(32);
    const bytes = await canonical.runAuthorizationSigningBytes({
      fromPublicKey,
      provider: '',
      model: '',
      timestamp: 0n,
      messages: [],
    });
    const domain = new TextDecoder().decode(bytes.slice(4, 4 + canonical.RUN_AUTH_DOMAIN.length));
    expect(domain).toBe('matrix/inference/run-authorization/v1');

    const mine = await runAuthorizationSigningBytes({
      fromPublicKey,
      provider: '',
      model: '',
      timestamp: 0n,
      messages: [],
    });
    expect(Array.from(mine)).toEqual(Array.from(bytes));
  });

  it('maps every proto role name, so no role falls through to a wrong default', async () => {
    // CHAT_ROLE_UNSPECIFIED deliberately maps to "user". This pins that: a role
    // silently becoming a different one changes the digest, so a signature
    // would cover a conversation the buyer did not authorize.
    for (const role of ROLES) {
      const fromPublicKey = new Uint8Array(32).fill(3);
      const mine = await runAuthorizationSigningBytes({
        fromPublicKey,
        provider: 'p',
        model: 'm',
        timestamp: 5n,
        messages: [{ role, content: 'hello' }],
      });
      const theirs = await canonical.runAuthorizationSigningBytes({
        fromPublicKey,
        provider: 'p',
        model: 'm',
        timestamp: 5n,
        messages: [{ role: WIRE_NAME[role], content: 'hello' }],
      });
      expect(Array.from(mine), `role ${role}`).toEqual(Array.from(theirs));
    }
  });
});

describe('bridgeLockRecipient against the canonical implementation', () => {
  const HEX = '0123456789abcdefABCDEF'.split('');
  const addr = (next: () => number): string => {
    let out = '';
    for (let i = 0; i < 40; i++) out += pick(next, HEX);
    return out;
  };

  it('agrees on randomized addresses, in both 0x and bare form', () => {
    const next = rng(0xb12d6e);
    for (let i = 0; i < 200; i++) {
      const bare = addr(next);
      for (const input of [bare, `0x${bare}`, `0X${bare}`]) {
        expect(bridgeLockRecipient(input)).toBe(canonical.bridgeLockRecipient(input));
      }
    }
  });

  it('lowercases, because consensus refuses a mixed-case address', () => {
    const got = bridgeLockRecipient('0xAbCdEf0123456789AbCdEf0123456789AbCdEf01');
    expect(got).toBe('bridge/lock/abcdef0123456789abcdef0123456789abcdef01');
    expect(got).toBe(canonical.bridgeLockRecipient('0xAbCdEf0123456789AbCdEf0123456789AbCdEf01'));
  });

  it('refuses anything that is not a 20-byte address, in both implementations', () => {
    for (const bad of ['', '0x', 'abc', `0x${'a'.repeat(39)}`, `0x${'a'.repeat(41)}`, `0x${'z'.repeat(40)}`]) {
      expect(() => bridgeLockRecipient(bad)).toThrow();
      expect(() => canonical.bridgeLockRecipient(bad)).toThrow();
    }
  });
});
