/**
 * The tool loop, and the bound on what it can spend.
 *
 * A tool call is not one job. The model returns a call, THIS CODE executes it, and
 * the result goes back as a new message - so every round trip is a separate
 * reservation, a separate settlement and a separate charge. `chatEscrowed` is one
 * job and knows nothing about iterations; the loop belongs here, on the buyer's
 * side, because the buyer is the party that pays for another one.
 *
 * WHY THE BOUND IS NOT OPTIONAL. A budget's per-job cap bounds ONE job. Nothing in
 * the protocol bounds the NUMBER of jobs, so a model that keeps calling a tool that
 * keeps failing drains a budget through many individually legal jobs, each of which
 * consensus is right to accept. The only thing standing between a confused model and
 * an empty budget is this counter. So it has a default, it cannot be disabled, and
 * the ceiling is applied even when a caller passes something larger.
 *
 * WHY THE NODE DOES NOT RUN THE TOOLS. A seller that ran them would need the buyer's
 * credentials for whatever the tool reaches. This network already declined that
 * custody story once, when it declined to let a validator buy on someone's behalf;
 * this is the same answer for the same reason. The seller says what to call. The
 * buyer calls it.
 */

import { chatEscrowed, type EscrowChatOptions, type EscrowPhase } from './escrow';
import type { Settled } from './node';
import type { Message, Signer, ToolCall, ToolDefinition } from './signer';

/**
 * The most round trips one ask may take, when the caller names none.
 *
 * Four is enough for search-then-answer with two retries and small enough that a
 * loop going wrong costs four jobs rather than a budget. A reader who needs more
 * asks again, which is a decision they make rather than one a model makes for them.
 */
export const DEFAULT_MAX_ITERATIONS = 4;

/**
 * The hard ceiling, applied to whatever the caller asks for.
 *
 * A caller is not trusted with "unlimited" here because the caller is a page, and a
 * page can be wrong. Twelve jobs is already an expensive mistake; it is not an
 * unbounded one.
 */
export const MAX_ITERATIONS_CEILING = 12;

/** A tool the caller can actually run. */
export interface ExecutableTool {
  definition: ToolDefinition;
  /**
   * Runs the tool and returns what the model should see.
   *
   * It is handed the RAW argument string, not a parsed object, because the model
   * emits JSON that may not parse and this is the layer that owns the schema. A tool
   * that throws is reported to the model as a failed call rather than ending the
   * ask: telling the model its call failed is what lets it try something else, and a
   * thrown error that ended the turn would bill the reader for a dead end.
   */
  run(rawArguments: string): Promise<string>;
}

export interface ToolLoopOptions extends Omit<EscrowChatOptions, 'tools' | 'messages'> {
  messages: Message[];
  tools: ExecutableTool[];
  /** Round-trip bound. Defaults to DEFAULT_MAX_ITERATIONS, capped at the ceiling. */
  maxIterations?: number;
  /** Called when a tool is about to run, so a page can say what it is doing. */
  onToolStart?: (call: ToolCall) => void;
  /** Called with the tool's own output, or the failure handed back to the model. */
  onToolResult?: (call: ToolCall, result: string, failed: boolean) => void;
  /** Called at the start of each round trip, 1-based. */
  onIteration?: (n: number, of: number) => void;
}

export interface ToolLoopResult {
  /** The last run's answer. Empty only if the loop ran out of iterations. */
  completion: string;
  /** Every job the ask took, in order. One per round trip. */
  runs: Settled[];
  /** The transcript as it ended, including the tool turns. */
  messages: Message[];
  /** Total units paid across every job. The number a reader cares about. */
  units: bigint;
  /**
   * True when the bound stopped the loop while the model was still calling tools.
   *
   * Reported rather than thrown: the reader has paid for the jobs that ran and is
   * owed both the partial work and the fact that it was cut off. A thrown error
   * would hide the spend.
   */
  exhausted: boolean;
}

/**
 * Runs one ask to completion, executing tools as the model requests them.
 *
 * Each iteration is a full escrowed job: reserve, fund, stream, settle. The
 * transcript grows by the assistant's call and the tool's result, and both are
 * inside the next job's signature - so a node cannot rewrite what the model asked or
 * what the tool answered between rounds.
 */
export async function runWithTools(
  endpoint: string,
  signer: Signer,
  options: ToolLoopOptions,
): Promise<ToolLoopResult> {
  const limit = Math.max(1, Math.min(options.maxIterations ?? DEFAULT_MAX_ITERATIONS, MAX_ITERATIONS_CEILING));
  const byName = new Map(options.tools.map((t) => [t.definition.name, t]));
  if (byName.size !== options.tools.length) {
    // Two tools under one name leave this loop choosing which to run for a call that
    // names it, and the buyer signed for both - so whichever it picked, the
    // signature would not settle the question. The node refuses this too.
    throw new Error('runWithTools: two tools share a name, so a call naming it would be ambiguous');
  }

  const definitions = options.tools.map((t) => t.definition);
  const messages: Message[] = [...options.messages];
  const runs: Settled[] = [];
  let units = 0n;

  for (let i = 1; i <= limit; i++) {
    options.onIteration?.(i, limit);
    const run = await chatEscrowed(endpoint, signer, {
      ...options,
      messages,
      // Offered on EVERY iteration, not just the first. A model that stops being
      // shown its tools mid-conversation sees a transcript where it called something
      // that no longer exists, and answers about the tool rather than the question.
      tools: definitions,
    });
    runs.push(run);
    units += run.units;

    const calls = run.toolCalls ?? [];
    if (calls.length === 0) {
      return { completion: run.completion, runs, messages, units, exhausted: false };
    }

    // The model's own turn goes in FIRST, with the calls on it. Without it the next
    // request has tool results answering nothing, and a model shown a result for a
    // call it cannot see will either re-call the tool or answer from nowhere.
    messages.push({
      role: 'assistant',
      content: run.completion,
      toolCalls: calls,
    });

    for (const call of calls) {
      options.onToolStart?.(call);
      const tool = byName.get(call.name);
      let result: string;
      let failed = false;
      if (tool === undefined) {
        // The model invented a tool. Reported TO THE MODEL rather than thrown: it is
        // the only party that can pick a different one, and ending the ask here
        // would bill the reader for a turn with nothing to show.
        failed = true;
        result = `no tool named ${JSON.stringify(call.name)} is available. Available: ${
          definitions.map((d) => d.name).join(', ') || 'none'
        }.`;
      } else {
        try {
          result = await tool.run(call.arguments);
        } catch (err) {
          failed = true;
          result = `the tool failed: ${err instanceof Error ? err.message : String(err)}`;
        }
      }
      options.onToolResult?.(call, result, failed);
      messages.push({ role: 'tool', content: result, toolCallId: call.id });
    }
  }

  // Out of iterations with the model still calling. The last completion is whatever
  // text came with the final call, which may be empty.
  const lastRun = runs[runs.length - 1];
  return {
    completion: lastRun?.completion ?? '',
    runs,
    messages,
    units,
    exhausted: true,
  };
}

/** Re-exported so a caller does not need to import from two places to show progress. */
export type { EscrowPhase };
