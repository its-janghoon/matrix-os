import { describe, expect, it } from 'vitest';
import { parseBridgeConfig } from './bridge/config';
import {
  PRODUCTION_CHAIN_ID,
  PRODUCTION_WRAPPED_MATRIX,
  resolveLaunchFacts,
  shortHex,
} from './launch';

const validators = 'https://v1.example.test,https://v2.example.test,https://v3.example.test';

function config(overrides: Record<string, string> = {}) {
  return parseBridgeConfig({
    NEXT_PUBLIC_BASE_CHAIN_ID: String(PRODUCTION_CHAIN_ID),
    NEXT_PUBLIC_WRAPPED_MATRIX_ADDRESS: PRODUCTION_WRAPPED_MATRIX,
    NEXT_PUBLIC_MATRIX_VALIDATOR_URLS: validators,
    ...overrides,
  });
}

describe('launch facts', () => {
  it('describes the published deployment when chain and contract both match', () => {
    const facts = resolveLaunchFacts(config());
    expect(facts).not.toBeNull();
    expect(facts?.wrappedMatrixAddress.toLowerCase()).toBe(PRODUCTION_WRAPPED_MATRIX.toLowerCase());
    expect(facts?.validatorCount).toBe(3);
    expect(facts?.explorerUrl).toMatch(/^https:/);
    expect(facts?.mintTx).toMatch(/^0x[0-9a-f]{64}$/);
    expect(facts?.evidenceUrl).toMatch(/^https:/);
  });

  it('announces nothing without a resolved bridge config', () => {
    expect(resolveLaunchFacts(null)).toBeNull();
  });

  it('announces nothing on a testnet build', () => {
    const sepolia = config({
      NEXT_PUBLIC_BASE_CHAIN_ID: '84532',
      NEXT_PUBLIC_WRAPPED_MATRIX_ADDRESS: PRODUCTION_WRAPPED_MATRIX,
    });
    expect(sepolia.chain.id).toBe(84532);
    expect(resolveLaunchFacts(sepolia)).toBeNull();
  });

  // The dangerous case: right chain, wrong contract. The published evidence
  // describes one deployment, so a different address on Base must not inherit
  // its "live and reconciled" claim.
  it('announces nothing when Base carries a different WrappedMatrix', () => {
    const other = config({
      NEXT_PUBLIC_WRAPPED_MATRIX_ADDRESS: '0x1111111111111111111111111111111111111111',
    });
    expect(other.chain.id).toBe(PRODUCTION_CHAIN_ID);
    expect(resolveLaunchFacts(other)).toBeNull();
  });

  it('matches the production address regardless of checksum casing', () => {
    const lowercased = config({
      NEXT_PUBLIC_WRAPPED_MATRIX_ADDRESS: PRODUCTION_WRAPPED_MATRIX.toLowerCase(),
    });
    expect(resolveLaunchFacts(lowercased)).not.toBeNull();
  });

  it('exposes a DEX link only when the deployment configured one', () => {
    expect(resolveLaunchFacts(config())?.dexUrl).toBeUndefined();
    expect(
      resolveLaunchFacts(config({ NEXT_PUBLIC_MATRIX_DEX_URL: 'https://dex.example.test/swap' }))
        ?.dexUrl,
    ).toBe('https://dex.example.test/swap');
  });
});

describe('shortHex', () => {
  it('elides the middle of a long value and keeps both ends', () => {
    const short = shortHex(PRODUCTION_WRAPPED_MATRIX);
    expect(short.startsWith(PRODUCTION_WRAPPED_MATRIX.slice(0, 6))).toBe(true);
    expect(short.endsWith(PRODUCTION_WRAPPED_MATRIX.slice(-4))).toBe(true);
    expect(short).toContain('…');
  });

  it('leaves a value that is already short enough untouched', () => {
    expect(shortHex('0xabcd')).toBe('0xabcd');
  });
});
