import { describe, expect, it } from 'vitest';

import {
  ERC20PerNativeUnit,
  MinBridgeLockAmount,
  NativeUnit,
  deriveBridgeLockId,
  validateBridgeLockAmount,
  validateBridgeLockAttestations,
} from './index';

const RECIPIENT = '0x000102030405060708090a0b0c0d0e0f10111213';
const SENDER = 'eth:0x1234567890abcdef1234567890abcdef12345678/한글';
const NONCE = 0x0102030405060708n;
const AMOUNT = 100_000_000_000n;
const GO_LOCK_ID = '0x1b297c3fb312c7fdc94d140615750e16ec4a1544f7c8da8e30b539b967b8a35f';

function attestation(attestor: string) {
  return {
    recipient: RECIPIENT,
    erc20Amount: AMOUNT * ERC20PerNativeUnit,
    lockId: GO_LOCK_ID,
    signature: `0x${'ab'.repeat(65)}`,
    attestor,
    nativeAmount: AMOUNT,
  };
}

describe('canonical bridge protocol', () => {
  it('pins monetary constants and the exact minimum boundary', () => {
    expect(NativeUnit).toBe(1_000_000_000n);
    expect(ERC20PerNativeUnit).toBe(1_000_000_000n);
    expect(MinBridgeLockAmount).toBe(100n * NativeUnit);
    expect(validateBridgeLockAmount(MinBridgeLockAmount)).toBe(MinBridgeLockAmount);
    expect(() => validateBridgeLockAmount(MinBridgeLockAmount - 1n)).toThrow(/at least/);
  });

  it('matches a vector printed by Go consensus.DeriveLockID, including UTF-8 byte length', async () => {
    await expect(deriveBridgeLockId(NONCE, SENDER, RECIPIENT, AMOUNT)).resolves.toBe(GO_LOCK_ID);
  });

  it('changes when any lock-id field changes', async () => {
    const base = await deriveBridgeLockId(NONCE, SENDER, RECIPIENT, AMOUNT);
    const variants = await Promise.all([
      deriveBridgeLockId(NONCE + 1n, SENDER, RECIPIENT, AMOUNT),
      deriveBridgeLockId(NONCE, `${SENDER}!`, RECIPIENT, AMOUNT),
      deriveBridgeLockId(NONCE, SENDER, '0x100102030405060708090a0b0c0d0e0f10111213', AMOUNT),
      deriveBridgeLockId(NONCE, SENDER, RECIPIENT, AMOUNT + 1n),
    ]);
    expect(new Set([base, ...variants]).size).toBe(5);
  });

  it('strictly rejects non-uint64 values and malformed Ethereum addresses', async () => {
    await expect(deriveBridgeLockId(-1n, SENDER, RECIPIENT, AMOUNT)).rejects.toThrow(/uint64/);
    await expect(deriveBridgeLockId(2n ** 64n, SENDER, RECIPIENT, AMOUNT)).rejects.toThrow(/uint64/);
    await expect(deriveBridgeLockId(Number.MAX_SAFE_INTEGER + 1, SENDER, RECIPIENT, AMOUNT)).rejects.toThrow(/safe integer/);
    await expect(deriveBridgeLockId(NONCE, SENDER, '0x1234', AMOUNT)).rejects.toThrow(/20 bytes/);
  });

  it('normalizes agreeing attestations and rejects duplicate claimed attestors', () => {
    const first = attestation(`0x${'11'.repeat(20)}`);
    const second = attestation(`${'22'.repeat(20).toUpperCase()}`);
    const normalized = validateBridgeLockAttestations([first, second]);
    expect(normalized[1]!.attestor).toBe(`0x${'22'.repeat(20)}`);
    expect(normalized[0]!.signature).toBe(`0x${'ab'.repeat(65)}`);

    expect(() => validateBridgeLockAttestations([
      first,
      { ...second, attestor: first.attestor.toUpperCase().replace('0X', '0x') },
    ])).toThrow(/duplicate claimed attestor/);
  });

  it('rejects validator disagreement and an invalid native-to-ERC20 conversion', () => {
    const first = attestation(`0x${'11'.repeat(20)}`);
    const second = attestation(`0x${'22'.repeat(20)}`);
    expect(() => validateBridgeLockAttestations([
      first,
      { ...second, recipient: `0x${'ff'.repeat(20)}` },
    ])).toThrow(/disagree/);
    expect(() => validateBridgeLockAttestations([
      first,
      { ...second, erc20Amount: second.erc20Amount + 1n },
    ])).toThrow(/disagree/);
    expect(() => validateBridgeLockAttestations([
      { ...first, erc20Amount: first.erc20Amount + 1n },
    ])).toThrow(/conversion/);
  });
});
