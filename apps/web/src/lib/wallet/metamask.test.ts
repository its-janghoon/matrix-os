import { describe, expect, it, vi } from 'vitest';
import { BASE_CHAINS } from '@/lib/bridge/config';
import { getEvmChainId, switchEvmChain, type Eip1193Provider } from './metamask';

function provider(request: Eip1193Provider['request']): Eip1193Provider {
  return { request };
}

describe('EIP-1193 Base chain boundary', () => {
  it('reads a hexadecimal chain id', async () => {
    await expect(getEvmChainId(provider(vi.fn(async () => '0x14a34')))).resolves.toBe(84532);
  });

  it('switches an already-known Base chain without adding it', async () => {
    const request = vi.fn(async ({ method }: { method: string }) => method === 'eth_chainId' ? '0x2105' : null);
    await switchEvmChain(provider(request), BASE_CHAINS[8453]);
    expect(request.mock.calls.map(([call]) => call.method)).toEqual(['wallet_switchEthereumChain', 'eth_chainId']);
  });

  it('uses wallet_addEthereumChain only for 4902 and then verifies selection', async () => {
    let switches = 0;
    const request = vi.fn(async ({ method }: { method: string }) => {
      if (method === 'wallet_switchEthereumChain' && switches++ === 0) throw { code: 4902 };
      if (method === 'eth_chainId') return '0x14a34';
      return null;
    });
    await switchEvmChain(provider(request), BASE_CHAINS[84532]);
    expect(request.mock.calls.map(([call]) => call.method)).toEqual([
      'wallet_switchEthereumChain',
      'wallet_addEthereumChain',
      'wallet_switchEthereumChain',
      'eth_chainId',
    ]);
  });

  it('fails when the wallet remains on the wrong chain', async () => {
    const request = vi.fn(async ({ method }: { method: string }) => method === 'eth_chainId' ? '0x1' : null);
    await expect(switchEvmChain(provider(request), BASE_CHAINS[8453])).rejects.toThrow('wallet remained on chain 1');
  });

  it('does not add a chain after user rejection', async () => {
    const request = vi.fn(async () => { throw { code: 4001 }; });
    await expect(switchEvmChain(provider(request), BASE_CHAINS[8453])).rejects.toEqual({ code: 4001 });
    expect(request).toHaveBeenCalledTimes(1);
  });
});
