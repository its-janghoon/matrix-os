import { describe, expect, it, vi } from 'vitest';

import {
  ERC20PerNativeUnit,
  MatrixClient,
  MatrixError,
  MinBridgeLockAmount,
  bridgeLockRecipient,
  collectLockAttestations,
  ethereumAccountId,
  toBase64,
} from './index';

interface Reply {
  status?: number;
  body: Record<string, unknown>;
}

function clientWithReplies(replies: Reply[]) {
  const calls: Array<{ url: string; body: Record<string, unknown> }> = [];
  let index = 0;
  const fetch = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
    const reply = replies[Math.min(index++, replies.length - 1)] as Reply;
    calls.push({ url: String(url), body: JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown> });
    return {
      ok: (reply.status ?? 200) < 400,
      status: reply.status ?? 200,
      text: async () => JSON.stringify(reply.body),
    } as Response;
  }) as unknown as typeof globalThis.fetch;
  return {
    calls,
    client: new MatrixClient({ endpoint: 'http://validator.test:9093', fetch }),
  };
}

const senderAddress = `0x${'11'.repeat(20)}`;
const sender = ethereumAccountId(senderAddress);
const recipient = `0x${'22'.repeat(20)}`;
const lockId = `0x${'ab'.repeat(32)}`;

function attestation(attestorByte: string, overrides: Record<string, unknown> = {}) {
  return {
    recipient: recipient.toUpperCase().replace('0X', '0x'),
    erc20Amount: String(MinBridgeLockAmount * ERC20PerNativeUnit),
    lockId: lockId.slice(2).toUpperCase(),
    signature: 'cd'.repeat(65),
    attestor: `0x${attestorByte.repeat(20)}`,
    nativeAmount: String(MinBridgeLockAmount),
    ...overrides,
  };
}

describe('bridge readiness RPC', () => {
  it('encodes a 32-byte challenge and decodes deployment fields and proof bytes', async () => {
    const challenge = new Uint8Array(32).fill(0xa5);
    const signature = new Uint8Array(65).fill(0x44);
    const { client, calls } = clientWithReplies([{ body: {
      chainId: '84532',
      contract: recipient,
      attestor: senderAddress,
      minLockNative: String(MinBridgeLockAmount),
      challenge: toBase64(challenge),
      signature: toBase64(signature),
    } }]);
    const proof = await client.bridgeReadiness(challenge);
    expect(proof).toMatchObject({ chainId: 84532n, contract: recipient, attestor: senderAddress, minLockNative: MinBridgeLockAmount });
    expect(proof.challenge).toEqual(challenge);
    expect(proof.signature).toEqual(signature);
    expect(calls[0]!.url).toContain('/GetBridgeReadiness');
    expect(calls[0]!.body).toEqual({ challenge: toBase64(challenge) });
  });

  it('rejects malformed challenges before making an RPC', async () => {
    const { client, calls } = clientWithReplies([{ body: {} }]);
    await expect(client.bridgeReadiness(new Uint8Array(31))).rejects.toThrow('32 bytes');
    expect(calls).toHaveLength(0);
  });
});

describe('signed bridge lock submission', () => {
  it('supports a raw 20-byte EVM sender and returns a committed lock id', async () => {
    const to = bridgeLockRecipient(recipient);
    const fromPublicKey = new Uint8Array(20).fill(0x11);
    const signature = new Uint8Array(65).fill(0x55);
    const { client, calls } = clientWithReplies([
      {
        body: {
          transaction: {
            index: '7',
            from: sender,
            to,
            amount: String(MinBridgeLockAmount),
            nonce: '9',
            blockHeight: '12',
            timestamp: '2026-01-01T00:00:00Z',
          },
          committed: true,
          applied: true,
        },
      },
    ]);

    const result = await client.submitBridgeLock({
      sender,
      fromPublicKey,
      recipient,
      nativeAmount: MinBridgeLockAmount,
      nonce: 9n,
      prevHash: new Uint8Array(),
      signature,
      timestamp: 123n,
    });

    expect(result.lockId).toMatch(/^0x[0-9a-f]{64}$/);
    expect(result.settlement.committed).toBe(true);
    expect(calls[0]!.body).toMatchObject({
      fromPublicKey: toBase64(fromPublicKey),
      to,
      amount: String(MinBridgeLockAmount),
      nonce: '9',
      signature: toBase64(signature),
      timestamp: '123',
    });
  });

  it('rejects below-minimum locks before making an RPC', async () => {
    const { client, calls } = clientWithReplies([{ body: {} }]);
    await expect(client.submitBridgeLock({
      sender,
      fromPublicKey: new Uint8Array(20),
      recipient,
      nativeAmount: MinBridgeLockAmount - 1n,
      nonce: 1n,
      prevHash: new Uint8Array(),
      signature: new Uint8Array(65),
    })).rejects.toThrow(/at least/);
    expect(calls).toHaveLength(0);
  });

  it('rejects a sender/public-key mismatch before making an RPC', async () => {
    const { client, calls } = clientWithReplies([{ body: {} }]);
    await expect(client.submitBridgeLock({
      sender: ethereumAccountId(`0x${'99'.repeat(20)}`),
      fromPublicKey: new Uint8Array(20).fill(0x11),
      recipient,
      nativeAmount: MinBridgeLockAmount,
      nonce: 1n,
      prevHash: new Uint8Array(),
      signature: new Uint8Array(65),
    })).rejects.toThrow(/sender must match fromPublicKey/);
    expect(calls).toHaveLength(0);
  });

  it('requires both committed and applied', async () => {
    const to = bridgeLockRecipient(recipient);
    const base = {
      transaction: {
        from: sender,
        to,
        amount: String(MinBridgeLockAmount),
        nonce: '1',
      },
      committed: true,
      applied: false,
    };
    const { client } = clientWithReplies([{ body: base }]);
    await expect(client.submitBridgeLock({
      sender,
      fromPublicKey: new Uint8Array(20).fill(0x11),
      recipient,
      nativeAmount: MinBridgeLockAmount,
      nonce: 1n,
      prevHash: new Uint8Array(),
      signature: new Uint8Array(65),
    })).rejects.toMatchObject({ code: 'failed_precondition' });
  });

  it('rejects a successful response whose signed fields do not match', async () => {
    const { client } = clientWithReplies([{
      body: {
        transaction: {
          from: sender,
          to: bridgeLockRecipient(recipient),
          amount: String(MinBridgeLockAmount),
          nonce: '2',
        },
        committed: true,
        applied: true,
      },
    }]);
    await expect(client.submitBridgeLock({
      sender,
      fromPublicKey: new Uint8Array(20).fill(0x11),
      recipient,
      nativeAmount: MinBridgeLockAmount,
      nonce: 1n,
      prevHash: new Uint8Array(),
      signature: new Uint8Array(65),
    })).rejects.toMatchObject({ code: 'data_loss' });
  });

  it('rejects sender key byte lengths the server cannot parse', async () => {
    const { client } = clientWithReplies([{ body: {} }]);
    await expect(client.submitSignedTransfer({
      fromPublicKey: new Uint8Array(21),
      to: 'receiver',
      amount: 1n,
      nonce: 1n,
      prevHash: new Uint8Array(),
      signature: new Uint8Array(),
    })).rejects.toThrow(/20-byte EVM/);
  });
});

describe('lock attestation collection', () => {
  it('retries only not_found, then normalizes agreeing validator responses', async () => {
    const first = clientWithReplies([
      { status: 404, body: { code: 'not_found', message: 'not committed yet' } },
      { body: attestation('33') },
    ]);
    const second = clientWithReplies([{ body: attestation('44') }]);

    const result = await collectLockAttestations({
      lockId,
      validators: [first.client, second.client],
      attempts: 2,
      delayMs: 0,
    });

    expect(first.calls).toHaveLength(2);
    expect(second.calls).toHaveLength(1);
    expect(result.map((item) => item.attestor)).toEqual([
      `0x${'33'.repeat(20)}`,
      `0x${'44'.repeat(20)}`,
    ]);
    expect(result[0]!.lockId).toBe(lockId);
    expect(result[0]!.recipient).toBe(recipient);
  });

  it('does not retry failed_precondition or other errors', async () => {
    const validator = clientWithReplies([
      { status: 412, body: { code: 'failed_precondition', message: 'no attestor key' } },
      { body: attestation('33') },
    ]);
    const error = await collectLockAttestations({
      lockId,
      validators: [validator.client],
      attempts: 5,
      delayMs: 0,
    }).catch((caught: unknown) => caught);
    expect(error).toBeInstanceOf(MatrixError);
    expect(error).toMatchObject({ code: 'failed_precondition' });
    expect(validator.calls).toHaveLength(1);
  });

  it('cancels sibling not_found polling after a validator hard-fails', async () => {
    const polling = clientWithReplies([
      { status: 404, body: { code: 'not_found', message: 'not committed yet' } },
    ]);
    const hardFailure = clientWithReplies([
      { status: 503, body: { code: 'unavailable', message: 'validator unavailable' } },
    ]);

    await expect(collectLockAttestations({
      lockId,
      validators: [polling.client, hardFailure.client],
      attempts: 5,
      delayMs: 5,
    })).rejects.toMatchObject({ code: 'unavailable' });
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(polling.calls).toHaveLength(1);
  });

  it('rejects duplicate claimed attestors, disagreement, and bad conversion', async () => {
    const duplicateA = clientWithReplies([{ body: attestation('33') }]);
    const duplicateB = clientWithReplies([{ body: attestation('33') }]);
    await expect(collectLockAttestations({
      lockId,
      validators: [duplicateA.client, duplicateB.client],
      attempts: 1,
    })).rejects.toThrow(/duplicate claimed attestor/);

    const agreeing = clientWithReplies([{ body: attestation('33') }]);
    const disagreeing = clientWithReplies([
      { body: attestation('44', { recipient: `0x${'ff'.repeat(20)}` }) },
    ]);
    await expect(collectLockAttestations({
      lockId,
      validators: [agreeing.client, disagreeing.client],
      attempts: 1,
    })).rejects.toThrow(/disagree/);

    const badConversion = clientWithReplies([
      { body: attestation('55', { erc20Amount: String(MinBridgeLockAmount * ERC20PerNativeUnit + 1n) }) },
    ]);
    await expect(collectLockAttestations({
      lockId,
      validators: [badConversion.client],
      attempts: 1,
    })).rejects.toThrow(/conversion/);
  });
});
