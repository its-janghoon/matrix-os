import { describe, expect, it, vi } from 'vitest';
import { afterEach } from 'vitest';
import { privateKeyToAccount } from 'viem/accounts';
import { hexToBytes, type Address, type Hex } from 'viem';
import { ERC20PerNativeUnit, MinBridgeLockAmount, type NormalizedBridgeLockAttestation } from '@matrix-os/protocol';
import { parseBridgeConfig } from './config';
import {
  assertBridgeContractReadiness,
  bridgeAttestationDigest,
  bridgeReadinessDigest,
  collectBridgeReadinessResponses,
  requireAttestorThreshold,
  simulateAndWriteBurn,
  validateBurnAmount,
  verifyAttestationSigners,
  verifyBridgeReadiness,
  verifyBridgeReadinessResponders,
} from './evm';
import { NodeError, type BridgeReadiness } from '@/lib/wallet/node';
import type { EvmSigner } from '@/lib/wallet/metamask';

const config = parseBridgeConfig({
  NEXT_PUBLIC_BASE_CHAIN_ID: '84532',
  NEXT_PUBLIC_WRAPPED_MATRIX_ADDRESS: '0x2222222222222222222222222222222222222222',
  NEXT_PUBLIC_MATRIX_VALIDATOR_URLS: 'https://validator.example.test',
});
const recipient = '0x1111111111111111111111111111111111111111' as Address;
const lockId = `0x${'ab'.repeat(32)}` as Hex;
const amount = MinBridgeLockAmount * ERC20PerNativeUnit;

function attestation(attestor: Address, signature: Hex): NormalizedBridgeLockAttestation {
  return {
    recipient,
    erc20Amount: amount,
    lockId,
    signature,
    attestor,
    nativeAmount: MinBridgeLockAmount,
  };
}

function wrongChainWallet(): EvmSigner {
  return {
    kind: 'metamask',
    address: recipient,
    accountId: `eth:${recipient}`,
    provider: { request: vi.fn() },
    getChainId: vi.fn(async () => 1),
    switchChain: vi.fn(),
    subscribe: vi.fn(() => () => {}),
    signRunAuthorization: vi.fn(),
    signPayment: vi.fn(),
  };
}

function walletWithProvider(request: EvmSigner['provider']['request']): EvmSigner {
  return {
    kind: 'metamask',
    address: recipient,
    accountId: `eth:${recipient}`,
    provider: { request },
    getChainId: vi.fn(async () => config.chain.id),
    switchChain: vi.fn(),
    subscribe: vi.fn(() => () => {}),
    signRunAuthorization: vi.fn(),
    signPayment: vi.fn(),
  };
}

function readiness(attestor: Address, signature: Hex, challenge: Uint8Array): BridgeReadiness {
  return {
    chainId: BigInt(config.chain.id),
    contract: config.wrappedMatrixAddress,
    attestor,
    minLockNative: MinBridgeLockAmount,
    challenge: challenge.slice(),
    signature: hexToBytes(signature),
  };
}

afterEach(() => vi.unstubAllGlobals());

describe('bridge deployment readiness', () => {
  it('rejects no code and contract read failures before querying validators', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    const noCodeRequest = vi.fn(async ({ method }: { method: string }) => {
      if (method === 'eth_getCode') return '0x';
      throw new Error(`unexpected ${method}`);
    });
    await expect(verifyBridgeReadiness(walletWithProvider(noCodeRequest), config, MinBridgeLockAmount))
      .rejects.toThrow('no deployed code');
    expect(fetchMock).not.toHaveBeenCalled();

    const readFailure = vi.fn(async ({ method }: { method: string }) => {
      if (method === 'eth_getCode') return '0x6000';
      if (method === 'eth_call') throw new Error('contract read failed');
      throw new Error(`unexpected ${method}`);
    });
    await expect(verifyBridgeReadiness(walletWithProvider(readFailure), config, MinBridgeLockAmount))
      .rejects.toThrow();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('checks threshold, conversion, and cap headroom', () => {
    const ready = {
      code: '0x6000' as Hex,
      threshold: 2n,
      attestorCount: 3n,
      mintCap: amount + 1n,
      totalSupply: 0n,
      erc20PerNativeUnit: ERC20PerNativeUnit,
    };
    expect(() => assertBridgeContractReadiness(ready, MinBridgeLockAmount)).not.toThrow();
    expect(() => assertBridgeContractReadiness({ ...ready, code: '0x' }, MinBridgeLockAmount)).toThrow('no deployed code');
    expect(() => assertBridgeContractReadiness({ ...ready, attestorCount: 1n }, MinBridgeLockAmount)).toThrow('below threshold');
    expect(() => assertBridgeContractReadiness({ ...ready, erc20PerNativeUnit: 1n }, MinBridgeLockAmount)).toThrow('conversion');
    expect(() => assertBridgeContractReadiness({ ...ready, mintCap: amount - 1n }, MinBridgeLockAmount)).toThrow('insufficient headroom');
  });

  it('tolerates only transport failures and leaves threshold enforcement to verified responders', () => {
    const placeholder = {} as BridgeReadiness;
    expect(collectBridgeReadinessResponses([
      { status: 'rejected', reason: new NodeError('unreachable', 'down') },
      { status: 'fulfilled', value: placeholder },
    ], ['https://down.example', 'https://live.example'])).toEqual([placeholder]);
    expect(() => collectBridgeReadinessResponses([
      { status: 'rejected', reason: new NodeError('failed_precondition', 'wrong deployment') },
    ], ['https://bad.example'])).toThrow('wrong deployment');
  });

  it('accepts unique registered responders and rejects domain mismatch, malformed, duplicate, unregistered, and below-threshold proofs', async () => {
    const challenge = new Uint8Array(32).fill(0xa5);
    const accounts = [
      privateKeyToAccount(`0x${'11'.repeat(32)}`),
      privateKeyToAccount(`0x${'12'.repeat(32)}`),
    ];
    const make = async (account: typeof accounts[number], chainId = BigInt(config.chain.id)) => {
      const digest = bridgeReadinessDigest({
        challenge: `0x${Buffer.from(challenge).toString('hex')}` as Hex,
        chainId,
        contract: config.wrappedMatrixAddress,
        attestor: account.address,
        minLockNative: MinBridgeLockAmount,
      });
      return readiness(account.address, await account.sign({ hash: digest }), challenge);
    };
    const valid = await Promise.all(accounts.map((account) => make(account)));
    await expect(verifyBridgeReadinessResponders({
      responses: valid,
      challenge,
      chainId: config.chain.id,
      contract: config.wrappedMatrixAddress,
      threshold: 2n,
      isRegistered: async () => true,
    })).resolves.toHaveLength(2);

    const wrongDomain = await make(accounts[0]!, 1n);
    await expect(verifyBridgeReadinessResponders({
      responses: [wrongDomain], challenge, chainId: config.chain.id, contract: config.wrappedMatrixAddress, threshold: 1n, isRegistered: async () => true,
    })).rejects.toThrow('does not match recovered signer');
    await expect(verifyBridgeReadinessResponders({
      responses: [{ ...valid[0]!, signature: new Uint8Array(64) }], challenge, chainId: config.chain.id, contract: config.wrappedMatrixAddress, threshold: 1n, isRegistered: async () => true,
    })).rejects.toThrow('65 bytes');
    await expect(verifyBridgeReadinessResponders({
      responses: [valid[0]!, valid[0]!], challenge, chainId: config.chain.id, contract: config.wrappedMatrixAddress, threshold: 2n, isRegistered: async () => true,
    })).rejects.toThrow('duplicate readiness signer');
    await expect(verifyBridgeReadinessResponders({
      responses: [valid[0]!], challenge, chainId: config.chain.id, contract: config.wrappedMatrixAddress, threshold: 1n, isRegistered: async () => false,
    })).rejects.toThrow('not a registered attestor');
    await expect(verifyBridgeReadinessResponders({
      responses: [valid[0]!], challenge, chainId: config.chain.id, contract: config.wrappedMatrixAddress, threshold: 2n, isRegistered: async () => true,
    })).rejects.toThrow('threshold not met');
  });
});

describe('WrappedMatrix signature verification', () => {
  it('recovers from the exact packed contract digest and sorts by numeric address', async () => {
    const accounts = [
      privateKeyToAccount(`0x${'01'.repeat(32)}`),
      privateKeyToAccount(`0x${'02'.repeat(32)}`),
    ];
    const digest = bridgeAttestationDigest({
      recipient,
      amount,
      lockId,
      chainId: config.chain.id,
      contract: config.wrappedMatrixAddress,
    });
    const signed = await Promise.all(accounts.map(async (account) => ({
      account,
      signature: await account.sign({ hash: digest }),
    })));
    signed.reverse();

    const verified = await verifyAttestationSigners({
      attestations: signed.map(({ account, signature }) => attestation(account.address, signature)),
      chainId: config.chain.id,
      contract: config.wrappedMatrixAddress,
    }, async () => true);

    expect(verified.map((entry) => BigInt(entry.signer))).toEqual(
      [...accounts].map((account) => BigInt(account.address)).sort((a, b) => a < b ? -1 : 1),
    );
  });

  it('rejects claimed/recovered mismatch before contract submission', async () => {
    const signer = privateKeyToAccount(`0x${'03'.repeat(32)}`);
    const claimed = privateKeyToAccount(`0x${'04'.repeat(32)}`);
    const digest = bridgeAttestationDigest({ recipient, amount, lockId, chainId: config.chain.id, contract: config.wrappedMatrixAddress });
    const signature = await signer.sign({ hash: digest });
    await expect(verifyAttestationSigners({
      attestations: [attestation(claimed.address, signature)],
      chainId: config.chain.id,
      contract: config.wrappedMatrixAddress,
    }, async () => true)).rejects.toThrow('does not match recovered signer');
  });

  it('enforces the contract threshold after sorting and registration checks', () => {
    expect(() => requireAttestorThreshold(1, 2n)).toThrow('got 1, need 2');
    expect(() => requireAttestorThreshold(2, 2n)).not.toThrow();
  });
});

describe('burn safety', () => {
  it('requires positive exact ERC20-per-native multiples', () => {
    expect(validateBurnAmount(ERC20PerNativeUnit)).toBe(ERC20PerNativeUnit);
    expect(() => validateBurnAmount(0n)).toThrow('positive');
    expect(() => validateBurnAmount(ERC20PerNativeUnit + 1n)).toThrow('exact multiple');
  });

  it('refuses a wrong-chain write before simulation or wallet RPC', async () => {
    const wallet = wrongChainWallet();
    await expect(simulateAndWriteBurn(wallet, config, ERC20PerNativeUnit, 'native-account'))
      .rejects.toThrow('wrong wallet chain 1');
    expect(wallet.provider.request).not.toHaveBeenCalled();
  });
});
