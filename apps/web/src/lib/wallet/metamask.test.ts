import { describe, expect, it, vi } from 'vitest';
import { BASE_CHAINS } from '@/lib/bridge/config';
import { connectMetamask, getEvmChainId, switchEvmChain, type Eip1193Provider } from './metamask';

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

// Switching account in the extension and pressing Connect again used to hand
// back the first account, every time, with no prompt - because a site that
// already holds a permission gets eth_requestAccounts answered from that
// permission rather than from a fresh choice. There is no way out of that from
// the page except asking for the permission again.
describe('choosing which account connects', () => {
  const FIRST = '0x1111111111111111111111111111111111111111';
  const SECOND = '0x2222222222222222222222222222222222222222';

  function install(request: Eip1193Provider['request']) {
    (window as unknown as { ethereum?: Eip1193Provider }).ethereum = { request };
  }

  it('does not disturb the wallet when no choice was asked for', async () => {
    const request = vi.fn(async (_args: { method: string }) => [FIRST]);
    install(request);
    const signer = await connectMetamask();
    expect(signer.accountId).toBe('eth:' + FIRST);
    expect(request.mock.calls.map(([call]) => call.method)).toEqual(['eth_requestAccounts']);
  });

  it('re-opens the picker first, and connects what came back from it', async () => {
    let chosen = FIRST;
    const request = vi.fn(async ({ method }: { method: string }) => {
      if (method === 'wallet_requestPermissions') {
        chosen = SECOND;
        return [{ parentCapability: 'eth_accounts' }];
      }
      return [chosen];
    });
    install(request);
    const signer = await connectMetamask({ chooseAccount: true });
    expect(request.mock.calls.map(([call]) => call.method)).toEqual([
      'wallet_requestPermissions',
      'eth_requestAccounts',
    ]);
    expect(signer.accountId).toBe('eth:' + SECOND);
  });

  // A wallet without the method is not a wallet we refuse to talk to.
  it('still connects a wallet that does not implement permissions', async () => {
    const request = vi.fn(async ({ method }: { method: string }) => {
      if (method === 'wallet_requestPermissions') throw { code: 4200 };
      return [FIRST];
    });
    install(request);
    await expect(connectMetamask({ chooseAccount: true })).resolves.toMatchObject({
      accountId: 'eth:' + FIRST,
    });
  });

  // Dismissing the picker is an answer. Connecting the old account anyway would
  // be the original complaint with one more click in front of it.
  it('connects nothing when the picker is dismissed', async () => {
    const request = vi.fn(async ({ method }: { method: string }) => {
      if (method === 'wallet_requestPermissions') throw { code: 4001 };
      return [FIRST];
    });
    install(request);
    await expect(connectMetamask({ chooseAccount: true })).rejects.toEqual({ code: 4001 });
    expect(request.mock.calls.map(([call]) => call.method)).toEqual(['wallet_requestPermissions']);
  });
});
