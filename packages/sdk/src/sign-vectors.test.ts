import { describe, expect, it } from 'vitest';
import * as canonical from '@matrix-os/protocol';

import { runAuthorizationSigningBytes, type ChatMessage, type ChatRole } from './index';
import vectors from './sign-vectors.json';

/**
 * The cross-language guard: BOTH TypeScript implementations against bytes GO
 * produced.
 *
 * WHY layout-parity.test.ts DOES NOT COVER THIS. That test compares this package
 * against `@matrix-os/protocol` over randomized inputs, which catches a drift
 * between the two TypeScript copies and says nothing about Go — the side that
 * actually VERIFIES. A signature both copies compute identically and Go rejects
 * arrives in production as "invalid signature", which reads as a key problem and
 * sends somebody looking in the wrong place. On this network a run authorization
 * is what lets a browser buy inference without an API key, so that failure is
 * "nobody can pay", not "one client is odd".
 *
 * Go generates `sign-vectors.json` (`go run ./cmd/signvectors`), a Go test keeps
 * the file matching the Go code, and this test keeps TypeScript matching the file.
 * Three implementations, one source.
 *
 * THE TRAP THESE VECTORS AIM AT. Go `len()` counts BYTES; JavaScript `.length`
 * counts UTF-16 code units. They agree on ASCII and disagree on everything else,
 * so a length-prefixed layout can pass every ASCII test and still produce a
 * different digest for any prompt with an accent in it. The vectors carry Korean,
 * a character outside the BMP (a surrogate PAIR in UTF-16, where the counts differ
 * by more than a constant factor), an empty transcript and empty fields.
 */

interface SignVector {
  name: string;
  why: string;
  publicKeyHex: string;
  provider: string;
  model: string;
  /**
   * A decimal STRING, not a number.
   *
   * It is int64 nanoseconds, which passes 2^53, so a JSON number cannot carry it:
   * `1790059598000000000` parses to a different value and the digest computed from
   * it does not match Go. This test caught that on its first run against a
   * number-typed field, which is the whole reason the guard exists. Proto JSON
   * spells int64 as a string for the same reason.
   */
  timestamp: string;
  messages: {
    role: string;
    content: string;
    toolCallId?: string;
    toolCalls?: { id: string; name: string; arguments: string }[];
  }[];
  /** In the digest since v0.5.8. A JSON number round-trips a float64 exactly. */
  maxTokens: number;
  temperature: number;
  /**
   * In the digest since v0.5.9, as a block emitted ONLY when the request has a tool
   * surface. Absent here means the vector has none, and such a vector must hash
   * exactly as it did before tools existed - which is what catches an
   * implementation that emits a zero count unconditionally.
   *
   * `parameters` is a STRING of raw JSON, not a nested object: the bytes are what is
   * hashed, and a nested object would be re-serialized by each language's JSON
   * writer with its own key order and spacing, so the two sides would hash
   * different bytes from one file.
   */
  tools?: { name: string; description?: string; parameters?: string }[];
  signingBytesHex: string;
}

function fromHex(hex: string): Uint8Array {
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  return out;
}

function toHex(bytes: Uint8Array): string {
  return Array.from(bytes)
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
}

const cases = vectors as SignVector[];

/**
 * Go's roles are the internal spellings the digest is over - "user", "assistant",
 * "system". This package's public vocabulary is the PROTO enum name, which it maps
 * back with roleWireName, and that mapping is part of what a caller relies on. So
 * the vector's role is translated up into the proto name here, which tests the same
 * path `apps/web` uses rather than reaching past it.
 *
 * Worth knowing while reading this: roleWireName used to return "user" for anything
 * it did not recognise, so feeding it an internal spelling did not fail - it
 * silently digested an "assistant" turn as a "user" one, which is how this test
 * first appeared to have found a drift in the library. That default is now an
 * error, because an unknown role producing a valid signature over a different
 * transcript is the one failure the signing layout exists to prevent.
 */
function protoRole(internal: string): ChatRole {
  switch (internal) {
    case 'system':
      return 'CHAT_ROLE_SYSTEM';
    case 'assistant':
      return 'CHAT_ROLE_ASSISTANT';
    case 'user':
      return 'CHAT_ROLE_USER';
    case 'tool':
      return 'CHAT_ROLE_TOOL';
    default:
      throw new Error(`the vectors carry a role this test does not map: ${internal}`);
  }
}

describe('run authorization bytes against the Go-generated vectors', () => {
  it('has vectors to check at all', () => {
    // A file that lost its contents would make every assertion below vacuous.
    expect(cases.length).toBeGreaterThan(0);
  });

  for (const v of cases) {
    it(`this package matches Go for ${v.name}`, async () => {
      const bytes = await runAuthorizationSigningBytes({
        fromPublicKey: fromHex(v.publicKeyHex),
        provider: v.provider,
        model: v.model,
        timestamp: BigInt(v.timestamp),
        messages: v.messages.map(
          (m) =>
            ({
              role: protoRole(m.role),
              content: m.content,
              toolCallId: m.toolCallId,
              toolCalls: m.toolCalls,
            }) as ChatMessage,
        ),
        maxTokens: v.maxTokens,
        temperature: v.temperature,
        tools: v.tools,
      });
      expect(toHex(bytes)).toBe(v.signingBytesHex);
    });

    it(`@matrix-os/protocol matches Go for ${v.name}`, async () => {
      const bytes = await canonical.runAuthorizationSigningBytes({
        fromPublicKey: fromHex(v.publicKeyHex),
        provider: v.provider,
        model: v.model,
        timestamp: BigInt(v.timestamp),
        messages: v.messages.map((m) => ({
          role: m.role,
          content: m.content,
          toolCallId: m.toolCallId,
          toolCalls: m.toolCalls,
        })) as canonical.Message[],
        maxTokens: v.maxTokens,
        temperature: v.temperature,
        tools: v.tools,
      });
      expect(toHex(bytes)).toBe(v.signingBytesHex);
    });
  }

  it('carries every timestamp as a string that round-trips exactly', () => {
    // The assertion that caught the original defect, kept in the form that
    // matters: a decimal string survives JSON whatever its magnitude, and a
    // number does not. If a future vector goes back to a JSON number this fails
    // rather than producing a quietly different digest.
    for (const v of cases) {
      expect(typeof v.timestamp).toBe('string');
      expect(BigInt(v.timestamp).toString()).toBe(v.timestamp);
    }
    // And at least one is past the safe-integer range, so the case that broke it
    // is still covered.
    expect(cases.some((v) => BigInt(v.timestamp) > BigInt(Number.MAX_SAFE_INTEGER))).toBe(true);
  });

  it('still covers the encoding trap, so an ASCII-only file cannot pass for a guard', () => {
    const text = cases.flatMap((v) => [
      v.provider,
      v.model,
      ...v.messages.map((m) => m.content),
      ...(v.tools ?? []).flatMap((t) => [t.name, t.description ?? '', t.parameters ?? '']),
    ]);
    const points = text.flatMap((s) => Array.from(s).map((c) => c.codePointAt(0) ?? 0));
    expect(points.some((c) => c > 0x7f)).toBe(true);
    expect(points.some((c) => c > 0xffff)).toBe(true);
    expect(cases.some((v) => v.messages.length === 0)).toBe(true);
  });

  it('covers tools on both sides of the conditional block', () => {
    // A vector WITH tools, or the block is untested.
    expect(cases.some((v) => (v.tools?.length ?? 0) > 0)).toBe(true);
    // And one WITHOUT, which is what catches an implementation emitting a zero
    // count unconditionally: it hashes eight bytes where Go hashes none, and only
    // a tool-free vector notices. That is also the property keeping every
    // pre-v0.5.9 signature valid.
    expect(cases.some((v) => (v.tools?.length ?? 0) === 0)).toBe(true);
    // A replayed call, so the per-message metadata is exercised.
    expect(cases.some((v) => v.messages.some((m) => (m.toolCalls?.length ?? 0) > 0))).toBe(true);
    // A tool NAME outside the BMP: the tool block has its own length prefixes and
    // meets the byte-versus-UTF-16 trap independently of the transcript's.
    expect(
      cases.some((v) =>
        (v.tools ?? []).some((t) => Array.from(t.name).some((c) => (c.codePointAt(0) ?? 0) > 0xffff)),
      ),
    ).toBe(true);
  });
});
