import { describe, expect, it } from 'vitest';

import { fromBase64, messagesDigest, paymentSigningBytes, runAuthorizationSigningBytes, toBase64 } from './signing';

/**
 * These are the SAME constants packages/sdk pins, taken from the Go
 * implementation's own output. They are the only thing keeping three copies of
 * these byte layouts in agreement, and a drift would otherwise surface at
 * runtime as "invalid signature", which reads as a key problem.
 */
const GOLDEN_PAYMENT =
  'AAAAIAABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4fAAAACnByb3ZpZGVyLTEAAAAAB1vNFQAAAAAAAAAHGNL8IrtyxRUAAAAE3q2+7w==';

const GOLDEN_RUN_AUTH =
  'AAAAJW1hdHJpeC9pbmZlcmVuY2UvcnVuLWF1dGhvcml6YXRpb24vdjEAAAAgAAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8AAAAFZ3B1LTEAAAANbGxhbWEtMy4zLTcwYgAAACC5plUi18KqH5VVzrz5BlHYN9SQ1bcVKQ66LcPWFJUKBxjS/CK7csUV';

const KEY = (() => {
  const k = new Uint8Array(32);
  for (let i = 0; i < 32; i++) k[i] = i;
  return k;
})();

describe('payment signing bytes', () => {
  it('matches the node byte for byte', () => {
    const bytes = paymentSigningBytes({
      fromPublicKey: KEY,
      to: 'provider-1',
      amount: 123456789n,
      nonce: 7n,
      timestamp: 1788769228123456789n,
      prevHash: new Uint8Array([0xde, 0xad, 0xbe, 0xef]),
    });
    expect(toBase64(bytes)).toBe(GOLDEN_PAYMENT);
  });

  it('covers the amount and the recipient', () => {
    const base = {
      fromPublicKey: KEY,
      to: 'p',
      amount: 10n,
      nonce: 0n,
      timestamp: 0n,
      prevHash: new Uint8Array(),
    };
    const same = toBase64(paymentSigningBytes(base));
    expect(toBase64(paymentSigningBytes({ ...base, amount: 1n }))).not.toBe(same);
    expect(toBase64(paymentSigningBytes({ ...base, to: 'somebody-else' }))).not.toBe(same);
  });
});

describe('run authorization signing bytes', () => {
  it('matches the node byte for byte', async () => {
    const bytes = await runAuthorizationSigningBytes({
      fromPublicKey: KEY,
      provider: 'gpu-1',
      model: 'llama-3.3-70b',
      timestamp: 1788769228123456789n,
      messages: [{ role: 'user', content: 'hello' }],
    });
    expect(toBase64(bytes)).toBe(GOLDEN_RUN_AUTH);
  });

  it('binds the authorization to the provider, model, prompt and moment', async () => {
    const base = {
      fromPublicKey: KEY,
      provider: 'gpu-1',
      model: 'llama-3.3-70b',
      timestamp: 1n,
      messages: [{ role: 'user' as const, content: 'hello' }],
    };
    const same = toBase64(await runAuthorizationSigningBytes(base));

    for (const changed of [
      { ...base, provider: 'gpu-2' },
      { ...base, model: 'qwen-2.5-72b' },
      { ...base, messages: [{ role: 'user' as const, content: 'an enormously expensive question' }] },
      { ...base, timestamp: 2n },
    ]) {
      expect(toBase64(await runAuthorizationSigningBytes(changed))).not.toBe(same);
    }
  });
});

describe('the transcript digest', () => {
  it('separates fields so two conversations cannot share a hash', async () => {
    const joined = toBase64(await messagesDigest([{ role: 'user', content: 'ab' }]));
    const split = toBase64(
      await messagesDigest([
        { role: 'user', content: 'a' },
        { role: 'user', content: 'b' },
      ]),
    );
    expect(joined).not.toBe(split);
  });

  it('distinguishes who said it', async () => {
    const asUser = toBase64(await messagesDigest([{ role: 'user', content: 'hello' }]));
    const asSystem = toBase64(await messagesDigest([{ role: 'system', content: 'hello' }]));
    expect(asUser).not.toBe(asSystem);
  });
});

describe('base64', () => {
  it('round-trips bytes including the high ones', () => {
    const bytes = new Uint8Array([0x00, 0x7f, 0x80, 0xff, 0x10]);
    expect(Array.from(fromBase64(toBase64(bytes)))).toEqual(Array.from(bytes));
  });
});
