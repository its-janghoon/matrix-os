import { describe, expect, it } from 'vitest';
import { parseBridgeConfig, resolveBridgeConfig } from './config';

const valid = {
  NEXT_PUBLIC_BASE_CHAIN_ID: '84532',
  NEXT_PUBLIC_WRAPPED_MATRIX_ADDRESS: '0x1111111111111111111111111111111111111111',
  NEXT_PUBLIC_MATRIX_VALIDATOR_URLS: 'https://v1.example.test,https://v2.example.test/api',
};

describe('bridge public config', () => {
  it('fails closed when deployment values are absent', () => {
    const state = resolveBridgeConfig({});
    expect(state.config).toBeNull();
    expect(state.error).toContain('NEXT_PUBLIC_BASE_CHAIN_ID');
  });

  it('accepts only Base and Base Sepolia with explicit address and validators', () => {
    const config = parseBridgeConfig(valid);
    expect(config.chain.id).toBe(84532);
    expect(config.chain.explorerUrl).toMatch(/^https:/);
    expect(config.wrappedMatrixAddress).toBe('0x1111111111111111111111111111111111111111');
    expect(config.validatorUrls).toEqual(['https://v1.example.test', 'https://v2.example.test/api']);
  });

  it.each([
    [{ ...valid, NEXT_PUBLIC_BASE_CHAIN_ID: '1' }, '8453'],
    [{ ...valid, NEXT_PUBLIC_WRAPPED_MATRIX_ADDRESS: '0x1234' }, 'valid EVM address'],
    [{ ...valid, NEXT_PUBLIC_WRAPPED_MATRIX_ADDRESS: '0x0000000000000000000000000000000000000000' }, 'zero address'],
    [{ ...valid, NEXT_PUBLIC_MATRIX_VALIDATOR_URLS: 'https://same.test,https://same.test' }, 'duplicates'],
    [{ ...valid, NEXT_PUBLIC_MATRIX_VALIDATOR_URLS: 'http://public.example.test' }, 'HTTPS'],
  ])('rejects unsafe deployment config', (env, message) => {
    expect(() => parseBridgeConfig(env)).toThrow(message);
  });

  it('gates the optional DEX on a trusted explicit HTTPS URL', () => {
    expect(parseBridgeConfig(valid).dexUrl).toBeUndefined();
    expect(parseBridgeConfig({ ...valid, NEXT_PUBLIC_MATRIX_DEX_URL: 'https://dex.example.test/swap' }).dexUrl)
      .toBe('https://dex.example.test/swap');
    expect(() => parseBridgeConfig({ ...valid, NEXT_PUBLIC_MATRIX_DEX_URL: 'http://dex.example.test' }))
      .toThrow('must use HTTPS');
  });
});
