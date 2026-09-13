import { describe, expect, it } from 'vitest';

import {
  compactMatrix,
  formatMatrix,
  parseMatrix,
  pricePerMillionUnits,
  shortAccount,
  shortEndpoint,
} from './format';

describe('formatMatrix', () => {
  it('renders whole amounts with thousands separators', () => {
    // The stake a devnet seller actually posts, and the number this page used
    // to print as fourteen undifferentiated digits.
    expect(formatMatrix(300000000000000n)).toBe('300,000');
    expect(formatMatrix(0n)).toBe('0');
    expect(formatMatrix(1000000000n)).toBe('1');
  });

  it('keeps every non-zero digit, however small', () => {
    // A real settled total. The trailing five base units are the protocol fee
    // remainder, and a display that dropped them would disagree with the chain
    // a reader can go and check.
    expect(formatMatrix(712800000000005n)).toBe('712,800.000000005');
    expect(formatMatrix(1n)).toBe('0.000000001');
  });

  it('drops only trailing zeros, which carry nothing', () => {
    expect(formatMatrix(1500000000n)).toBe('1.5');
    expect(formatMatrix(1050000000n)).toBe('1.05');
  });
});

describe('compactMatrix', () => {
  it('shortens headline figures without pretending to be exact', () => {
    expect(compactMatrix(300000000000000n)).toBe('300K');
    expect(compactMatrix(1500000000000000n)).toBe('1.5M');
    expect(compactMatrix(1000000000000000000n)).toBe('1B');
  });

  it('falls back to the exact rendering when it already fits', () => {
    expect(compactMatrix(1500000000n)).toBe('1.5');
    expect(compactMatrix(0n)).toBe('0');
  });

  it('never passes a value through a float', () => {
    // 2^53 + 1 whole MATRIX. A float would round this; integer arithmetic does
    // not, and the tenth digit below is the proof.
    expect(compactMatrix(9007199254740993n * 1000000000n)).toBe('9,007.1T');
  });
});

describe('pricePerMillionUnits', () => {
  it('moves a per-unit price somewhere a buyer can compare it', () => {
    // Five base units per unit is 0.000000005 MATRIX, which no one can rank by
    // eye. Per million units it is a price.
    expect(pricePerMillionUnits(5n)).toBe('0.005');
    expect(pricePerMillionUnits(1000n)).toBe('1');
    expect(pricePerMillionUnits(0n)).toBe('0');
  });
});

describe('parseMatrix', () => {
  it('reads what a person types', () => {
    expect(parseMatrix('300000')).toBe(300000000000000n);
    expect(parseMatrix('1.5')).toBe(1500000000n);
    expect(parseMatrix(' 1,000 ')).toBe(1000000000000n);
    expect(parseMatrix('0.000000001')).toBe(1n);
  });

  it('refuses precision the chain cannot hold', () => {
    // Not a tiny stake: an amount this ledger cannot represent. Truncating it
    // to zero would stake nothing and report success.
    expect(parseMatrix('0.0000000001')).toBeNull();
  });

  it('refuses anything malformed rather than guessing', () => {
    expect(parseMatrix('')).toBeNull();
    expect(parseMatrix('abc')).toBeNull();
    expect(parseMatrix('1.2.3')).toBeNull();
    expect(parseMatrix('-5')).toBeNull();
  });

  it('round-trips through formatMatrix', () => {
    for (const raw of [0n, 1n, 5n, 1500000000n, 300000000000000n, 712800000000005n]) {
      expect(parseMatrix(formatMatrix(raw))).toBe(raw);
    }
  });
});

describe('shortAccount', () => {
  it('keeps both ends, because the ends are what anyone checks', () => {
    const id = 'eth:0x8e05638c6f0e4a1b2c3d4e5f60718293a4b5c6d7';
    const short = shortAccount(id);
    expect(short.startsWith('0x8e0563')).toBe(true);
    expect(short.endsWith('b5c6d7')).toBe(true);
  });

  it('leaves a short id alone', () => {
    expect(shortAccount('eth:0x1234')).toBe('0x1234');
  });
});

describe('shortEndpoint', () => {
  it('drops the scheme and a trailing slash', () => {
    expect(shortEndpoint('http://127.0.0.1:19004/')).toBe('127.0.0.1:19004');
    expect(shortEndpoint('https://gpu.example.com')).toBe('gpu.example.com');
  });
});
