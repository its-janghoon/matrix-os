import { afterEach, describe, expect, it, vi } from 'vitest';
import { beforeEach } from 'vitest';

const verifyBridgeReadinessMock = vi.hoisted(() => vi.fn(async () => {}));
vi.mock('./evm', () => ({ verifyBridgeReadiness: verifyBridgeReadinessMock }));

import {
  ERC20PerNativeUnit,
  MinBridgeLockAmount,
  NativeUnit,
  bridgeLockRecipient,
  deriveBridgeLockId,
  validateBridgeLockAttestations,
} from '@matrix-os/protocol';
import { parseBridgeConfig } from './config';
import {
  PENDING_LOCK_STORAGE_KEY,
  PendingLockContextError,
  collectLockAttestations,
  loadPendingLock,
  submitBridgeLock,
  wholeMatrixToNativeBaseUnits,
} from './lock';
import type { EvmSigner } from '@/lib/wallet/metamask';

const address = '0x1111111111111111111111111111111111111111' as const;
const config = parseBridgeConfig({
  NEXT_PUBLIC_BASE_CHAIN_ID: '84532',
  NEXT_PUBLIC_WRAPPED_MATRIX_ADDRESS: '0x2222222222222222222222222222222222222222',
  NEXT_PUBLIC_MATRIX_VALIDATOR_URLS: 'https://validator.example.test',
});

function wallet(signPayment = vi.fn(async () => ({
  fromPublicKey: new Uint8Array(20).fill(0x11),
  signature: new Uint8Array(65).fill(0x22),
}))): EvmSigner {
  return {
    kind: 'metamask',
    address,
    accountId: `eth:${address}`,
    provider: { request: vi.fn() },
    getChainId: vi.fn(async () => 84532),
    switchChain: vi.fn(async () => {}),
    subscribe: vi.fn(() => () => {}),
    signRunAuthorization: vi.fn(),
    signPayment,
  };
}

function response(body: unknown, ok = true): Response {
  return { ok, status: ok ? 200 : 404, text: async () => JSON.stringify(body) } as Response;
}

beforeEach(() => {
  verifyBridgeReadinessMock.mockReset();
  verifyBridgeReadinessMock.mockResolvedValue(undefined);
});

afterEach(() => {
  localStorage.clear();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('bridge lock orchestration', () => {
  it('converts whole MATRIX exactly and enforces the native range', () => {
    expect(wholeMatrixToNativeBaseUnits('100')).toBe(MinBridgeLockAmount);
    expect(wholeMatrixToNativeBaseUnits('0002')).toBe(2n * NativeUnit);
    expect(() => wholeMatrixToNativeBaseUnits('1.5')).toThrow('whole number');
    expect(() => wholeMatrixToNativeBaseUnits('999999999999999999999')).toThrow('uint64');
  });

  it('requires the protocol lock minimum before asking the signer', async () => {
    const signer = wallet();
    await expect(submitBridgeLock({
      wallet: signer,
      config,
      validatorEndpoint: config.validatorUrls[0] as string,
      nativeAmount: 99n * NativeUnit,
      nonce: 7n,
    })).rejects.toThrow('at least');
    expect(signer.signPayment).not.toHaveBeenCalled();
  });

  it('fails readiness before payment signing, storage, or native submission', async () => {
    const signer = wallet();
    const fetchMock = vi.fn();
    const storageMock = vi.spyOn(Storage.prototype, 'setItem');
    vi.stubGlobal('fetch', fetchMock);
    verifyBridgeReadinessMock.mockRejectedValueOnce(new Error('WrappedMatrix mint cap has insufficient headroom for this lock'));

    await expect(submitBridgeLock({
      wallet: signer,
      config,
      validatorEndpoint: config.validatorUrls[0] as string,
      nativeAmount: MinBridgeLockAmount,
      nonce: 7n,
    })).rejects.toThrow('mint cap');

    expect(signer.signPayment).not.toHaveBeenCalled();
    expect(storageMock).not.toHaveBeenCalled();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('re-checks the live wallet chain immediately before payment signing', async () => {
    const signer = wallet();
    vi.mocked(signer.getChainId).mockResolvedValueOnce(1);
    const fetchMock = vi.fn();
    const storageMock = vi.spyOn(Storage.prototype, 'setItem');
    vi.stubGlobal('fetch', fetchMock);

    await expect(submitBridgeLock({
      wallet: signer,
      config,
      validatorEndpoint: config.validatorUrls[0] as string,
      nativeAmount: MinBridgeLockAmount,
      nonce: 8n,
    })).rejects.toThrow('wrong wallet chain 1');

    expect(verifyBridgeReadinessMock).toHaveBeenCalledOnce();
    expect(signer.signPayment).not.toHaveBeenCalled();
    expect(storageMock).not.toHaveBeenCalled();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('signs exact payment fields, requires applied settlement, derives the id, and persists only metadata', async () => {
    const nonce = 42n;
    const amount = 100n * NativeUnit;
    const signer = wallet();
    vi.stubGlobal('fetch', vi.fn(async () => response({
      transaction: {
        index: '3',
        from: signer.accountId,
        to: bridgeLockRecipient(address),
        amount: amount.toString(),
        nonce: nonce.toString(),
        blockHeight: '9',
      },
      committed: true,
      applied: true,
    })));

    const pending = await submitBridgeLock({
      wallet: signer,
      config,
      validatorEndpoint: config.validatorUrls[0] as string,
      nativeAmount: amount,
      nonce,
    });
    const expected = await deriveBridgeLockId(nonce, signer.accountId, address, amount);
    expect(pending.lockId).toBe(expected);
    expect(pending.erc20Amount).toBe((amount * ERC20PerNativeUnit).toString());
    expect(signer.signPayment).toHaveBeenCalledWith(expect.objectContaining({
      to: bridgeLockRecipient(address), amount, nonce, prevHash: new Uint8Array(),
    }));
    const stored = localStorage.getItem(PENDING_LOCK_STORAGE_KEY) as string;
    expect(stored).toContain(expected);
    expect(stored).not.toContain('signature');
    await expect(loadPendingLock(config, address)).resolves.toMatchObject({ lockId: expected, nonce: '42' });
  });

  it('preserves the signed intent when settlement is not applied', async () => {
    const signer = wallet();
    vi.stubGlobal('fetch', vi.fn(async () => response({ transaction: {}, committed: true, applied: false })));
    await expect(submitBridgeLock({
      wallet: signer,
      config,
      validatorEndpoint: config.validatorUrls[0] as string,
      nativeAmount: MinBridgeLockAmount,
      nonce: 1n,
    })).rejects.toThrow('commit and apply');
    expect(localStorage.getItem(PENDING_LOCK_STORAGE_KEY)).toContain('"settlement":"submitted"');
  });

  it('preserves recovery metadata when the submission response is lost', async () => {
    const signer = wallet();
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('connection lost'); }));
    await expect(submitBridgeLock({
      wallet: signer,
      config,
      validatorEndpoint: config.validatorUrls[0] as string,
      nativeAmount: MinBridgeLockAmount,
      nonce: 2n,
    })).rejects.toThrow('could not reach');
    await expect(loadPendingLock(config, address)).resolves.toMatchObject({ nonce: '2', settlement: 'submitted' });
  });

  it('never deletes a valid recovery record when another account connects', async () => {
    const signer = wallet();
    vi.stubGlobal('fetch', vi.fn(async () => response({
      transaction: { from: signer.accountId, to: bridgeLockRecipient(address), amount: MinBridgeLockAmount.toString(), nonce: '3' },
      committed: true,
      applied: true,
    })));
    await submitBridgeLock({
      wallet: signer,
      config,
      validatorEndpoint: config.validatorUrls[0] as string,
      nativeAmount: MinBridgeLockAmount,
      nonce: 3n,
    });
    await expect(loadPendingLock(config, '0x4444444444444444444444444444444444444444'))
      .rejects.toBeInstanceOf(PendingLockContextError);
    expect(localStorage.getItem(PENDING_LOCK_STORAGE_KEY)).not.toBeNull();
  });
});

describe('attestation field validation', () => {
  const lockId = `0x${'ab'.repeat(32)}`;
  const signature = `0x${'cd'.repeat(65)}`;
  const base = {
    recipient: address,
    nativeAmount: MinBridgeLockAmount,
    erc20Amount: MinBridgeLockAmount * ERC20PerNativeUnit,
    lockId,
    signature,
    attestor: '0x3333333333333333333333333333333333333333',
  };

  it('rejects field disagreement', () => {
    expect(() => validateBridgeLockAttestations([
      base,
      { ...base, attestor: '0x4444444444444444444444444444444444444444', nativeAmount: base.nativeAmount + 1n },
    ])).toThrow('disagree');
  });

  it('rejects duplicate claimed attestors', () => {
    expect(() => validateBridgeLockAttestations([base, base])).toThrow('duplicate claimed attestor');
  });
});


describe('validator availability', () => {
  it('keeps a usable attestation when another explicit validator fails without retrying it', async () => {
    const pending = {
      version: 1 as const,
      lockId: `0x${'ab'.repeat(32)}` as const,
      sender: `eth:${address}`,
      recipient: address,
      nativeAmount: MinBridgeLockAmount.toString(),
      erc20Amount: (MinBridgeLockAmount * ERC20PerNativeUnit).toString(),
      nonce: '9',
      timestamp: '10',
      validatorEndpoint: 'https://good.example',
      chainId: config.chain.id,
      contract: config.wrappedMatrixAddress,
      createdAt: new Date().toISOString(),
      settlement: 'committed' as const,
    };
    const fetchMock = vi.fn(async (url: string) => {
      if (url.startsWith('https://down.example')) {
        return response({ code: 'failed_precondition', message: 'attestor disabled' }, false);
      }
      return response({
        recipient: address,
        nativeAmount: pending.nativeAmount,
        erc20Amount: pending.erc20Amount,
        lockId: pending.lockId,
        signature: `0x${'cd'.repeat(65)}`,
        attestor: '0x3333333333333333333333333333333333333333',
      });
    });
    vi.stubGlobal('fetch', fetchMock);
    const collected = await collectLockAttestations({
      pending,
      validatorUrls: ['https://down.example', 'https://good.example'],
      attempts: 3,
      delayMs: 0,
    });
    expect(collected).toHaveLength(1);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
