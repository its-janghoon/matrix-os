import { bridgeConfigState, type BridgeConfig } from '@/lib/bridge/config';

/**
 * The published Base production launch. Every value here is taken from the
 * evidence record at `docs/evidence/base-production-2026-09-12.md`, which is
 * the only thing a reader can independently check. Marketing copy reads these
 * constants instead of restating numbers, because a number retyped into a hero
 * headline is a number that silently goes stale.
 */
export const PRODUCTION_CHAIN_ID = 8453 as const;

export const PRODUCTION_WRAPPED_MATRIX = '0x0ec1C40829B3A5C2349Afa8C4Da6dC8B727fC87c';
export const PRODUCTION_VAULT = '0x7A23b7748162e7BcA02b16C4d236E4A9D122038d';
export const PRODUCTION_WRAPPED_DEPLOY_TX =
  '0x341df3e3d2e2f2f01cb7c8b0bd8b571b13e31c9a4aefd6e300721136aac211a3';
export const PRODUCTION_MINT_TX =
  '0xaba866d6c810a4343e8467dd22a2a9988581a7193bc5c7bf8bd2d40b56f2b145';
export const PRODUCTION_EVIDENCE =
  'https://github.com/savagemanage/matrix-os/blob/main/docs/evidence/base-production-2026-09-12.md';

/** Whole-token figures, for display only. Base-unit maths never reads these. */
export const FOUNDER_ALLOCATION_WHOLE = 50_000_000;
export const MINT_CAP_WHOLE = 60_000_000;
export const NATIVE_MAX_SUPPLY_WHOLE = 1_000_000_000;
export const ATTESTOR_COUNT = 3;
export const ATTESTOR_THRESHOLD = 3;

export interface LaunchFacts {
  config: BridgeConfig;
  explorerUrl: string;
  wrappedMatrixAddress: string;
  vaultAddress: string;
  wrappedDeployTx: string;
  mintTx: string;
  evidenceUrl: string;
  validatorCount: number;
  dexUrl?: string;
}

/**
 * Launch facts for a resolved bridge configuration, or `null` when that
 * configuration is not the published production deployment.
 *
 * The address check is the point of this function. A build could be pointed at
 * Base with a different WrappedMatrix - a redeploy, a fork, a typo in an
 * environment variable - and in that case none of the evidence above describes
 * what the visitor is about to sign. Claiming "live and reconciled" there would
 * be worse than saying nothing, so the whole launch surface disappears instead
 * of half-matching.
 */
export function resolveLaunchFacts(config: BridgeConfig | null): LaunchFacts | null {
  if (!config) return null;
  if (config.chain.id !== PRODUCTION_CHAIN_ID) return null;
  if (config.wrappedMatrixAddress.toLowerCase() !== PRODUCTION_WRAPPED_MATRIX.toLowerCase()) {
    return null;
  }
  return {
    config,
    explorerUrl: config.chain.explorerUrl,
    wrappedMatrixAddress: config.wrappedMatrixAddress,
    vaultAddress: PRODUCTION_VAULT,
    wrappedDeployTx: PRODUCTION_WRAPPED_DEPLOY_TX,
    mintTx: PRODUCTION_MINT_TX,
    evidenceUrl: PRODUCTION_EVIDENCE,
    validatorCount: config.validatorUrls.length,
    ...(config.dexUrl ? { dexUrl: config.dexUrl } : {}),
  };
}

export const launchFacts = resolveLaunchFacts(bridgeConfigState.config);

/** `0x1234…abcd`, for addresses and hashes shown inside running prose. */
export function shortHex(value: string, lead = 6, tail = 4): string {
  if (value.length <= lead + tail + 1) return value;
  return `${value.slice(0, lead)}…${value.slice(-tail)}`;
}

export function addressUrl(explorerUrl: string, address: string): string {
  return `${explorerUrl}/address/${address}`;
}

export function txUrl(explorerUrl: string, hash: string): string {
  return `${explorerUrl}/tx/${hash}`;
}
