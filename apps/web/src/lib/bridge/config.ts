import { getAddress, type Address } from 'viem';
import { base, baseSepolia, type Chain } from 'viem/chains';

export type SupportedBaseChainId = 8453 | 84532;

export interface BridgeChain {
  id: SupportedBaseChainId;
  name: string;
  network: 'base' | 'base-sepolia';
  nativeCurrency: Chain['nativeCurrency'];
  rpcUrls: readonly string[];
  explorerUrl: string;
}

export interface BridgeConfig {
  chain: BridgeChain;
  wrappedMatrixAddress: Address;
  validatorUrls: readonly string[];
  dexUrl?: string;
}

export interface BridgeConfigState {
  config: BridgeConfig | null;
  error: string | null;
}

export interface BridgePublicEnv {
  NEXT_PUBLIC_BASE_CHAIN_ID?: string;
  NEXT_PUBLIC_WRAPPED_MATRIX_ADDRESS?: string;
  NEXT_PUBLIC_MATRIX_VALIDATOR_URLS?: string;
  NEXT_PUBLIC_MATRIX_DEX_URL?: string;
}

function chainMetadata(chain: Chain, network: BridgeChain['network']): BridgeChain {
  const explorerUrl = chain.blockExplorers?.default.url;
  if (!explorerUrl) throw new Error(`${chain.name} explorer metadata is unavailable`);
  return {
    id: chain.id as SupportedBaseChainId,
    name: chain.name,
    network,
    nativeCurrency: chain.nativeCurrency,
    rpcUrls: chain.rpcUrls.default.http,
    explorerUrl: explorerUrl.replace(/\/$/, ''),
  };
}

export const BASE_CHAINS: Readonly<Record<SupportedBaseChainId, BridgeChain>> = {
  8453: chainMetadata(base, 'base'),
  84532: chainMetadata(baseSepolia, 'base-sepolia'),
};

function required(value: string | undefined, name: string): string {
  const trimmed = value?.trim() ?? '';
  if (!trimmed) throw new Error(`${name} is required to enable the bridge`);
  return trimmed;
}

function endpoint(value: string, name: string, allowHttpLocalhost: boolean): string {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    throw new Error(`${name} must be an absolute URL`);
  }
  if (url.username || url.password) throw new Error(`${name} must not contain credentials`);
  const local = url.hostname === 'localhost' || url.hostname === '127.0.0.1' || url.hostname === '[::1]';
  if (url.protocol !== 'https:' && !(allowHttpLocalhost && local && url.protocol === 'http:')) {
    throw new Error(`${name} must use HTTPS${allowHttpLocalhost ? ' (HTTP is allowed only for localhost)' : ''}`);
  }
  url.hash = '';
  return url.toString().replace(/\/$/, '');
}

/** Validate public deployment configuration. Any missing/invalid value disables the bridge. */
export function parseBridgeConfig(env: BridgePublicEnv): BridgeConfig {
  const rawChainId = required(env.NEXT_PUBLIC_BASE_CHAIN_ID, 'NEXT_PUBLIC_BASE_CHAIN_ID');
  if (rawChainId !== '8453' && rawChainId !== '84532') {
    throw new Error('NEXT_PUBLIC_BASE_CHAIN_ID must be Base (8453) or Base Sepolia (84532)');
  }
  const chainId = Number(rawChainId) as SupportedBaseChainId;

  let wrappedMatrixAddress: Address;
  try {
    wrappedMatrixAddress = getAddress(required(
      env.NEXT_PUBLIC_WRAPPED_MATRIX_ADDRESS,
      'NEXT_PUBLIC_WRAPPED_MATRIX_ADDRESS',
    ));
  } catch (error) {
    if (error instanceof Error && error.message.includes('required')) throw error;
    throw new Error('NEXT_PUBLIC_WRAPPED_MATRIX_ADDRESS must be a valid EVM address');
  }
  if (wrappedMatrixAddress === '0x0000000000000000000000000000000000000000') {
    throw new Error('NEXT_PUBLIC_WRAPPED_MATRIX_ADDRESS must not be the zero address');
  }

  const rawValidators = required(
    env.NEXT_PUBLIC_MATRIX_VALIDATOR_URLS,
    'NEXT_PUBLIC_MATRIX_VALIDATOR_URLS',
  ).split(',');
  const validatorUrls = rawValidators.map((value, index) =>
    endpoint(value.trim(), `NEXT_PUBLIC_MATRIX_VALIDATOR_URLS entry ${index + 1}`, true),
  );
  if (new Set(validatorUrls).size !== validatorUrls.length) {
    throw new Error('NEXT_PUBLIC_MATRIX_VALIDATOR_URLS must not contain duplicates');
  }

  const rawDexUrl = env.NEXT_PUBLIC_MATRIX_DEX_URL?.trim();
  const dexUrl = rawDexUrl ? endpoint(rawDexUrl, 'NEXT_PUBLIC_MATRIX_DEX_URL', false) : undefined;

  return {
    chain: BASE_CHAINS[chainId],
    wrappedMatrixAddress,
    validatorUrls,
    ...(dexUrl ? { dexUrl } : {}),
  };
}

export function resolveBridgeConfig(env: BridgePublicEnv): BridgeConfigState {
  try {
    return { config: parseBridgeConfig(env), error: null };
  } catch (error) {
    return { config: null, error: error instanceof Error ? error.message : String(error) };
  }
}

// Explicit property reads are required for Next to inline NEXT_PUBLIC values in client bundles.
export const bridgeConfigState = resolveBridgeConfig({
  NEXT_PUBLIC_BASE_CHAIN_ID: process.env.NEXT_PUBLIC_BASE_CHAIN_ID,
  NEXT_PUBLIC_WRAPPED_MATRIX_ADDRESS: process.env.NEXT_PUBLIC_WRAPPED_MATRIX_ADDRESS,
  NEXT_PUBLIC_MATRIX_VALIDATOR_URLS: process.env.NEXT_PUBLIC_MATRIX_VALIDATOR_URLS,
  NEXT_PUBLIC_MATRIX_DEX_URL: process.env.NEXT_PUBLIC_MATRIX_DEX_URL,
});
