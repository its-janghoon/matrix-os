/**
 * keccak256, dependency-free.
 *
 * WHY THIS EXISTS. The EIP-712 material in eip712.ts needs
 * keccak256("matrix-os-native-l1") for its domain salt, and this package takes
 * no dependency on purpose: it is the one TypeScript source of the byte layouts
 * every client signs, and a dependency here becomes a dependency of everything
 * that signs. Before this file existed the salt was a precomputed constant
 * transcribed from ethers, and the EIP-712 definitions could not move into this
 * package at all - `apps/web` kept its own copy, which is exactly the drift
 * this package exists to end.
 *
 * This is KECCAK, not SHA-3: the original padding byte 0x01, not FIPS-202's
 * 0x06. Ethereum froze on the pre-standard variant, so `crypto.subtle` (which
 * has no SHA-3 anyway) could never have provided this.
 *
 * The state is 25 64-bit lanes held as BigInt. That is the slow, obvious
 * representation, chosen deliberately: this function hashes a handful of short
 * inputs at module load and in tests, never in a hot path, and the lane
 * arithmetic reading exactly like the specification is worth more here than
 * split 32-bit words would be.
 *
 * Trust comes from the tests, not from this file: keccak.test.ts pins the
 * published empty-string and "abc" vectors, inputs that straddle the 136-byte
 * rate boundary, and values cross-checked against ethers/viem - including the
 * domain salt the first version of eip712.ts got WRONG when it was transcribed
 * from memory. Self-consistency proves nothing; agreement with a real
 * implementation is the only evidence that counts.
 */

const MASK64 = (1n << 64n) - 1n;

/** The 24 round constants of Keccak-f[1600]. */
const ROUND_CONSTANTS: readonly bigint[] = [
  0x0000000000000001n, 0x0000000000008082n, 0x800000000000808an,
  0x8000000080008000n, 0x000000000000808bn, 0x0000000080000001n,
  0x8000000080008081n, 0x8000000000008009n, 0x000000000000008an,
  0x0000000000000088n, 0x0000000080008009n, 0x000000008000000an,
  0x000000008000808bn, 0x800000000000008bn, 0x8000000000008089n,
  0x8000000000008003n, 0x8000000000008002n, 0x8000000000000080n,
  0x000000000000800an, 0x800000008000000an, 0x8000000080008081n,
  0x8000000000008080n, 0x0000000080000001n, 0x8000000080008008n,
];

/** Rotation offsets, indexed [x + 5y] like the state itself. */
const RHO_OFFSETS: readonly bigint[] = [
  0n, 1n, 62n, 28n, 27n,
  36n, 44n, 6n, 55n, 20n,
  3n, 10n, 43n, 25n, 39n,
  41n, 45n, 15n, 21n, 8n,
  18n, 2n, 61n, 56n, 14n,
];

function rotl(value: bigint, by: bigint): bigint {
  if (by === 0n) return value;
  return ((value << by) | (value >> (64n - by))) & MASK64;
}

/** One Keccak-f[1600] permutation, in place. */
function keccakF(state: bigint[]): void {
  for (let round = 0; round < 24; round++) {
    // Theta.
    const c = new Array<bigint>(5);
    for (let x = 0; x < 5; x++) {
      c[x] = state[x]! ^ state[x + 5]! ^ state[x + 10]! ^ state[x + 15]! ^ state[x + 20]!;
    }
    for (let x = 0; x < 5; x++) {
      const d = c[(x + 4) % 5]! ^ rotl(c[(x + 1) % 5]!, 1n);
      for (let y = 0; y < 25; y += 5) state[x + y] = state[x + y]! ^ d;
    }

    // Rho and pi together: lane (x, y) rotates, then moves to (y, 2x+3y).
    const moved = new Array<bigint>(25);
    for (let x = 0; x < 5; x++) {
      for (let y = 0; y < 5; y++) {
        moved[y + 5 * ((2 * x + 3 * y) % 5)] = rotl(state[x + 5 * y]!, RHO_OFFSETS[x + 5 * y]!);
      }
    }

    // Chi.
    for (let y = 0; y < 25; y += 5) {
      for (let x = 0; x < 5; x++) {
        state[x + y] = moved[x + y]! ^ ((~moved[((x + 1) % 5) + y]! & MASK64) & moved[((x + 2) % 5) + y]!);
      }
    }

    // Iota.
    state[0] = state[0]! ^ ROUND_CONSTANTS[round]!;
  }
}

/** The sponge rate for a 256-bit capacity: 1600/8 - 2*32 bytes. */
const RATE = 136;

/** keccak256 over raw bytes, returned as 32 raw bytes. */
export function keccak256(input: Uint8Array): Uint8Array {
  const state: bigint[] = new Array(25).fill(0n);

  // Absorb. The final block always exists: Keccak pads with 0x01 ... 0x80
  // (collapsing to a single 0x81 when one byte remains), so an input that is an
  // exact multiple of the rate absorbs one extra all-padding block.
  const blocks = Math.floor(input.length / RATE) + 1;
  for (let block = 0; block < blocks; block++) {
    const offset = block * RATE;
    const chunk = new Uint8Array(RATE);
    chunk.set(input.subarray(offset, Math.min(offset + RATE, input.length)));
    if (block === blocks - 1) {
      const padAt = input.length - offset;
      chunk[padAt] = chunk[padAt]! | 0x01;
      chunk[RATE - 1] = chunk[RATE - 1]! | 0x80;
    }
    for (let lane = 0; lane < RATE / 8; lane++) {
      let value = 0n;
      // Little-endian lanes, per the specification's byte ordering.
      for (let byte = 7; byte >= 0; byte--) {
        value = (value << 8n) | BigInt(chunk[lane * 8 + byte]!);
      }
      state[lane] = state[lane]! ^ value;
    }
    keccakF(state);
  }

  // Squeeze. 32 bytes is four lanes, well under one rate, so a single read.
  const out = new Uint8Array(32);
  for (let lane = 0; lane < 4; lane++) {
    let value = state[lane]!;
    for (let byte = 0; byte < 8; byte++) {
      out[lane * 8 + byte] = Number(value & 0xffn);
      value >>= 8n;
    }
  }
  return out;
}
