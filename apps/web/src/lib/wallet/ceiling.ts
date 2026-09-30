/**
 * What a completed inference can honestly have cost, computed HERE.
 *
 * WHY THE CLIENT HAS TO DO THIS. Until now the browser signed whatever invoice
 * the node returned: it computed nothing and compared nothing. The node's own
 * `MaxUnitsFor` runs on the SELLER's machine, which makes it a bound on that
 * seller's model server rather than a protection for a buyer from its operator -
 * an operator who wants to overcharge owns the process running the check.
 *
 * So the only thing that has ever stood between a buyer and an inflated bill is
 * the buyer's power to refuse. On the escrowed path the buyer signs the
 * settlement, which is that power made explicit - and a buyer who signs the
 * number they were handed has given it straight back. This is the check that
 * makes the signature mean something.
 *
 * WHAT IT IS AND IS NOT. An upper bound from the text that actually crossed the
 * wire, not an estimate of the real count. It cannot know the provider's
 * tokeniser and does not need to: no tokeniser turns five characters into a
 * thousand tokens. It is roughly four times looser than the truth on English
 * prose, deliberately, because it must never refuse an honest bill. The
 * overcharge it exists to stop is two orders of magnitude, not a few percent.
 *
 * THIS MUST MATCH THE GO EXACTLY. It is the same arithmetic as
 * `inference.MaxUnitsFor`, and a client that computed a tighter bound than the
 * node would refuse bills the node considers honest - which reads to a buyer as
 * the seller cheating. There is a test on each side asserting the same numbers
 * for the same inputs.
 */

/**
 * The divisor turning observed text into an upper bound on tokens.
 *
 * One, because a byte-level BPE token encodes at least one byte, so a piece of
 * text can never have more tokens than it has bytes.
 */
const BYTES_PER_TOKEN_CEILING = 1;

/**
 * What a chat template adds around the text a buyer can see: role markers, turn
 * separators, a BOS and an EOS. Every template differs and none is knowable from
 * a browser, so this is generous.
 */
const TOKENS_PER_MESSAGE_OVERHEAD = 16;

/**
 * The smallest ceiling this will ever impose. A one-word exchange really is a
 * handful of tokens, and a bound that tight would start arguing with honest
 * backends over rounding for no benefit.
 */
const MAX_UNITS_FLOOR = 64n;

export interface CeilingMessage {
  role: string;
  content: string;
}

/**
 * Bytes, not characters.
 *
 * Go's `len()` on a string counts BYTES; JavaScript's `.length` counts UTF-16
 * code units. They agree on ASCII and disagree on everything else - a Korean
 * character is three bytes and one code unit, an emoji four bytes and two - so
 * using `.length` here would compute a ceiling three times too tight for a
 * Korean conversation and refuse every honest bill in it.
 */
const bytes = (s: string): number => new TextEncoder().encode(s).length;

/**
 * The most this exchange can honestly have cost, in units.
 *
 * `reasoning` is a reasoning model's working. It is counted because it is
 * DELIVERED to the buyer alongside the answer and they are charged for it;
 * leaving it out once billed a live sale 241 units for 864 tokens of real work,
 * where the ceiling was not protecting the buyer but underpaying the seller.
 *
 * `tools` and `toolCalls` are counted for exactly the same reason, and this must stay
 * in step with the node's MaxUnitsForResponse. A tool SCHEMA is sent to the model, so
 * it is prompt tokens the seller really spent - several times the size of the
 * question - and a CALL is completion tokens. A ceiling blind to both would refuse an
 * honest tool-calling bill here, in the browser, after the deposit was funded: the
 * reader would be told the seller was overcharging and would forfeit the whole
 * reservation at expiry. The tool contribution is present only when tools are, so
 * every tool-free exchange gets the number it always did.
 */
export function maxUnitsFor(
  messages: CeilingMessage[],
  completion: string,
  reasoning = '',
  tools: CeilingTool[] = [],
  toolCalls: CeilingToolCall[] = [],
): bigint {
  let total = 0;
  for (const m of messages) total += bytes(m.role) + bytes(m.content);

  // The completion is one more message's worth of text and template.
  let count = messages.length + 1;
  total += bytes(completion) + bytes(reasoning);

  for (const t of tools) {
    total += bytes(t.name) + bytes(t.description ?? '') + bytes(t.parameters ?? '');
  }
  if (tools.length > 0) {
    // The definitions arrive as their own block in the prompt template, so they
    // carry a per-block overhead the same way a message does.
    count += 1;
  }
  for (const c of toolCalls) {
    total += bytes(c.id) + bytes(c.name) + bytes(c.arguments);
  }

  const ceiling =
    BigInt(Math.floor(total / BYTES_PER_TOKEN_CEILING)) +
    BigInt(count) * BigInt(TOKENS_PER_MESSAGE_OVERHEAD);
  return ceiling < MAX_UNITS_FLOOR ? MAX_UNITS_FLOOR : ceiling;
}

/** A tool definition, as far as the ceiling is concerned: its bytes. */
export interface CeilingTool {
  name: string;
  description?: string;
  parameters?: string;
}

/** A call the model made, as far as the ceiling is concerned: its bytes. */
export interface CeilingToolCall {
  id: string;
  name: string;
  arguments: string;
}

/**
 * Whether a settlement the node asked for is one this buyer should sign.
 *
 * Refusing is the whole point, so it returns the reason rather than a boolean: a
 * client that refuses without saying why leaves a buyer unable to tell a cheating
 * seller from a bug in this file.
 */
export function checkSettlement(input: {
  /** What the node is asking the buyer to sign, in base units. */
  amount: bigint;
  /** What the reservation holds. A settlement can never exceed it. */
  reserved: bigint;
  /** The provider's advertised price per unit. Zero skips the ceiling check. */
  pricePerUnit: bigint;
  messages: CeilingMessage[];
  completion: string;
  reasoning?: string;
  /** The tools this run offered, and the calls it produced. Both are charged. */
  tools?: CeilingTool[];
  toolCalls?: CeilingToolCall[];
}): { ok: true } | { ok: false; reason: string } {
  if (input.amount === 0n) {
    return { ok: false, reason: 'the settlement is for nothing, which consensus refuses' };
  }
  if (input.amount > input.reserved) {
    return {
      ok: false,
      reason: `the settlement of ${input.amount} exceeds the reservation of ${input.reserved}`,
    };
  }
  if (input.pricePerUnit === 0n) return { ok: true };

  const ceiling = maxUnitsFor(
    input.messages,
    input.completion,
    input.reasoning ?? '',
    input.tools ?? [],
    input.toolCalls ?? [],
  );
  const most = ceiling * input.pricePerUnit;
  if (input.amount > most) {
    return {
      ok: false,
      reason:
        `the settlement of ${input.amount} is more than the ${most} this answer can honestly ` +
        `have cost (${ceiling} units at ${input.pricePerUnit} each)`,
    };
  }
  return { ok: true };
}
