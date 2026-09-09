/** MetaMask signer for Matrix-native EIP-712 operations and Base wallet access. */

import { getAddress, type Address } from 'viem';
import type { BridgeChain } from '@/lib/bridge/config';
import { EIP712_DOMAIN, RUN_AUTHORIZATION_TYPES, TRANSFER_TYPES, bytes32, fromHex, toHex } from './eip712';
import { messagesDigest } from './signing';
import type { Message, PaymentFields, PaymentSignature, RunAuthorization, Signer } from './signer';

export interface Eip1193RequestArguments {
  method: string;
  params?: readonly unknown[] | object;
}

/** Browser-wallet boundary shared by native EIP-712 and viem's custom transport. */
export interface Eip1193Provider {
  request(args: Eip1193RequestArguments): Promise<unknown>;
  on?(event: 'accountsChanged' | 'chainChanged', listener: (...args: unknown[]) => void): void;
  removeListener?(event: 'accountsChanged' | 'chainChanged', listener: (...args: unknown[]) => void): void;
}

declare global {
  interface Window {
    ethereum?: Eip1193Provider;
  }
}

export interface EvmSigner extends Signer {
  readonly kind: 'metamask';
  readonly address: Address;
  readonly provider: Eip1193Provider;
  getChainId(): Promise<number>;
  switchChain(chain: BridgeChain): Promise<void>;
  subscribe(listener: (change: { address?: Address | null; chainId?: number }) => void): () => void;
}

export function metamaskAvailable(): boolean {
  return typeof window !== 'undefined' && typeof window.ethereum !== 'undefined';
}

export function ethAccountID(address: string): string {
  return 'eth:' + getAddress(address).toLowerCase();
}

export async function requestEvmAccounts(provider: Eip1193Provider): Promise<Address[]> {
  const accounts = await provider.request({ method: 'eth_requestAccounts' });
  if (!Array.isArray(accounts)) throw new Error('the wallet returned an invalid account list');
  return accounts.map((account) => {
    if (typeof account !== 'string') throw new Error('the wallet returned an invalid account');
    return getAddress(account);
  });
}

export async function getEvmChainId(provider: Eip1193Provider): Promise<number> {
  const value = await provider.request({ method: 'eth_chainId' });
  if (typeof value !== 'string' || !/^0x[0-9a-f]+$/i.test(value)) {
    throw new Error('the wallet returned an invalid chain id');
  }
  return Number(BigInt(value));
}

function errorCode(error: unknown): number | undefined {
  if (!error || typeof error !== 'object') return undefined;
  const direct = (error as { code?: unknown }).code;
  if (typeof direct === 'number') return direct;
  const nested = (error as { data?: { originalError?: { code?: unknown } } }).data?.originalError?.code;
  return typeof nested === 'number' ? nested : undefined;
}

/** Switch to Base/Base Sepolia, adding the standard chain metadata only after 4902. */
export async function switchEvmChain(provider: Eip1193Provider, chain: BridgeChain): Promise<void> {
  const chainId = `0x${chain.id.toString(16)}`;
  try {
    await provider.request({ method: 'wallet_switchEthereumChain', params: [{ chainId }] });
  } catch (error) {
    if (errorCode(error) !== 4902) throw error;
    await provider.request({
      method: 'wallet_addEthereumChain',
      params: [{
        chainId,
        chainName: chain.name,
        nativeCurrency: chain.nativeCurrency,
        rpcUrls: [...chain.rpcUrls],
        blockExplorerUrls: [chain.explorerUrl],
      }],
    });
    // Some wallets add without selecting it.
    await provider.request({ method: 'wallet_switchEthereumChain', params: [{ chainId }] });
  }
  const selected = await getEvmChainId(provider);
  if (selected !== chain.id) throw new Error(`wallet remained on chain ${selected}; expected ${chain.id}`);
}

export async function connectMetamask(): Promise<EvmSigner> {
  if (!metamaskAvailable()) throw new Error('no browser wallet is installed on this page');
  const provider = window.ethereum as Eip1193Provider;
  const accounts = await requestEvmAccounts(provider);
  if (!accounts[0]) throw new Error('the wallet returned no account');
  return new MetamaskSigner(provider, accounts[0]);
}

export class MetamaskSigner implements EvmSigner {
  readonly kind = 'metamask' as const;
  readonly accountId: string;
  readonly address: Address;

  constructor(
    readonly provider: Eip1193Provider,
    address: string,
  ) {
    this.address = getAddress(address);
    this.accountId = ethAccountID(this.address);
  }

  getChainId(): Promise<number> {
    return getEvmChainId(this.provider);
  }

  switchChain(chain: BridgeChain): Promise<void> {
    return switchEvmChain(this.provider, chain);
  }

  subscribe(listener: (change: { address?: Address | null; chainId?: number }) => void): () => void {
    const accountsChanged = (value: unknown) => {
      const accounts = Array.isArray(value) ? value : [];
      const first = accounts[0];
      listener({ address: typeof first === 'string' ? getAddress(first) : null });
    };
    const chainChanged = (value: unknown) => {
      if (typeof value === 'string' && /^0x[0-9a-f]+$/i.test(value)) {
        listener({ chainId: Number(BigInt(value)) });
      }
    };
    this.provider.on?.('accountsChanged', accountsChanged);
    this.provider.on?.('chainChanged', chainChanged);
    return () => {
      this.provider.removeListener?.('accountsChanged', accountsChanged);
      this.provider.removeListener?.('chainChanged', chainChanged);
    };
  }

  async signRunAuthorization(input: {
    provider: string;
    model: string;
    messages: Message[];
    timestamp: bigint;
  }): Promise<RunAuthorization> {
    const promptDigest = await messagesDigest(input.messages);
    const signature = await this.signTypedData(RUN_AUTHORIZATION_TYPES, {
      buyer: this.address,
      provider: input.provider,
      model: input.model,
      promptDigest: bytes32(promptDigest),
      timestamp: input.timestamp.toString(),
    });
    return {
      publicKey: fromHex(this.address),
      timestamp: input.timestamp,
      signature,
    };
  }

  async signPayment(payment: PaymentFields): Promise<PaymentSignature> {
    const signature = await this.signTypedData(TRANSFER_TYPES, {
      from: this.address,
      to: payment.to,
      amount: payment.amount.toString(),
      nonce: payment.nonce.toString(),
      timestamp: payment.timestamp.toString(),
      prevHash: bytes32(payment.prevHash),
    });
    return { fromPublicKey: fromHex(this.address), signature };
  }

  private async signTypedData(types: object, message: Record<string, string>): Promise<Uint8Array> {
    const payload = JSON.stringify({
      domain: EIP712_DOMAIN,
      types: {
        EIP712Domain: [
          { name: 'name', type: 'string' },
          { name: 'version', type: 'string' },
          { name: 'salt', type: 'bytes32' },
        ],
        ...types,
      },
      primaryType: Object.keys(types)[0],
      message,
    });

    const signature = await this.provider.request({
      method: 'eth_signTypedData_v4',
      params: [this.address, payload],
    });
    if (typeof signature !== 'string') throw new Error('the wallet returned an invalid signature');
    const bytes = fromHex(signature);
    if (bytes.length !== 65) {
      throw new Error(`the wallet returned a ${bytes.length}-byte signature, expected 65`);
    }
    return bytes;
  }
}

export const _internal = { toHex };
