/**
 * Turning chain amounts into something a person can read, without lying.
 *
 * The ledger counts in base units and the chain has nine decimals, so a stake
 * of three hundred thousand MATRIX reaches this page as 300000000000000. A
 * table full of those is not a table anybody reads: the eye cannot count
 * fourteen digits, so every row looks like the same enormous number and the
 * column stops carrying information at all.
 *
 * The rule everything here follows is that a displayed number is either EXACT
 * or visibly approximate. Formatting is truncation-free and rounding-free by
 * default; the one function that rounds is named for it, and callers pair it
 * with the exact value in a title so the precise figure is always one hover
 * away. A marketplace whose whole claim is that its numbers come from a chain
 * cannot afford a display that quietly rounds them.
 */

/** Base units in one whole MATRIX. The native chain has nine decimals. */
export const BASE_UNITS_PER_MATRIX = 1_000_000_000n;

/** Decimal places the native chain carries. */
export const NATIVE_DECIMALS = 9;

export const SYMBOL = 'MATRIX';

function withThousands(digits: string): string {
  return digits.replace(/\B(?=(\d{3})+(?!\d))/g, ',');
}

/**
 * An exact decimal rendering of a base-unit amount, in whole MATRIX.
 *
 * Nothing is rounded away: trailing zeros in the fraction are dropped because
 * they carry no information, but a non-zero digit is never dropped, however far
 * down it sits. That is why a settled total can come back as
 * `712,800.000000005` - the five base units at the end are real, and a display
 * that hid them would disagree with the chain.
 */
export function formatMatrix(base: bigint): string {
  const negative = base < 0n;
  const abs = negative ? -base : base;
  const whole = abs / BASE_UNITS_PER_MATRIX;
  const frac = abs % BASE_UNITS_PER_MATRIX;

  let out = withThousands(whole.toString());
  if (frac !== 0n) {
    const padded = frac.toString().padStart(NATIVE_DECIMALS, '0').replace(/0+$/, '');
    out += `.${padded}`;
  }
  return negative ? `-${out}` : out;
}

/**
 * A short, deliberately APPROXIMATE rendering for headline figures.
 *
 * Summary tiles have room for about six characters and mainnet totals will not
 * fit in six characters exactly. Callers must show `formatMatrix` alongside -
 * in a title, or on the row itself - because this one does round.
 */
export function compactMatrix(base: bigint): string {
  const abs = base < 0n ? -base : base;
  const whole = abs / BASE_UNITS_PER_MATRIX;
  const sign = base < 0n ? '-' : '';

  const scales: Array<[bigint, string]> = [
    [1_000_000_000_000n, 'T'],
    [1_000_000_000n, 'B'],
    [1_000_000n, 'M'],
    [1_000n, 'K'],
  ];
  for (const [scale, suffix] of scales) {
    if (whole >= scale) {
      // One decimal place, computed in integer arithmetic so the value never
      // passes through a float on the way to the screen.
      const tenths = (whole * 10n) / scale;
      const head = tenths / 10n;
      const tail = tenths % 10n;
      return `${sign}${withThousands(head.toString())}${tail === 0n ? '' : `.${tail}`}${suffix}`;
    }
  }
  // Below a thousand the exact rendering already fits, so nothing is lost.
  return formatMatrix(base);
}

/**
 * The unit prices actually quoted here are a handful of base units, which is a
 * billionth of a MATRIX each. Rendered per unit that is `0.000000005`, a number
 * whose leading zeros defeat comparison - the one thing a price column is for.
 *
 * Quoting per million units moves the decimal point somewhere legible and is
 * how every other inference marketplace prices, so the figure is comparable to
 * the ones a buyer already knows. It stays exact: a million is a power of ten,
 * so the conversion only shifts digits.
 */
export function pricePerMillionUnits(basePerUnit: bigint): string {
  return formatMatrix(basePerUnit * 1_000_000n);
}

/**
 * Reads a human amount back into base units, for an input a person types.
 *
 * Returns null rather than a guess on anything malformed, and specifically
 * refuses MORE precision than the chain carries: `0.0000000001` is not a very
 * small stake, it is an amount this ledger cannot represent, and silently
 * truncating it to zero would stake nothing while reporting success.
 */
export function parseMatrix(text: string): bigint | null {
  const cleaned = text.trim().replace(/,/g, '');
  if (cleaned === '' || !/^\d*(\.\d*)?$/.test(cleaned)) return null;

  const [whole = '', frac = ''] = cleaned.split('.');
  if (whole === '' && frac === '') return null;
  if (frac.length > NATIVE_DECIMALS) return null;

  const padded = frac.padEnd(NATIVE_DECIMALS, '0');
  return BigInt(whole === '' ? '0' : whole) * BASE_UNITS_PER_MATRIX + BigInt(padded === '' ? '0' : padded);
}

/**
 * An account id shortened for a table cell, keeping both ends.
 *
 * Both ends, because the middle is the part nobody checks and the ends are what
 * a person compares against an address they hold. Truncating one side turns two
 * different accounts into the same string on screen.
 */
export function shortAccount(id: string): string {
  const body = id.startsWith('eth:') ? id.slice(4) : id;
  if (body.length <= 16) return body;
  return `${body.slice(0, 8)}...${body.slice(-6)}`;
}

/** The host and port a buyer would dial, without the scheme taking up room. */
export function shortEndpoint(endpoint: string): string {
  return endpoint.replace(/^https?:\/\//, '').replace(/\/$/, '');
}
