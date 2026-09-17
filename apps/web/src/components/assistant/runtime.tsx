'use client';

import { useEffect, useMemo, useRef, type ReactNode } from 'react';
import {
  AssistantRuntimeProvider,
  useLocalRuntime,
  type ChatModelAdapter,
  type ThreadMessage,
} from '@assistant-ui/react';

import { chatEscrowed, type EscrowPhase } from '@/lib/wallet/escrow';
import { chat, type SellerChoice } from '@/lib/wallet/node';
import { checkReceipt, type ReceiptCheck } from '@/lib/wallet/receipt';
import type { Message } from '@/lib/wallet/signing';
import type { Signer } from '@/lib/wallet/signer';

/**
 * What a settled message cost and what the seller signed for it.
 *
 * It rides on the assistant message's own metadata rather than in a state map
 * beside the thread, because the runtime owns the thread: messages can be
 * edited, branched and regenerated, and a charge that lived in a parallel array
 * would end up attached to the wrong answer the first time someone edits a
 * question. Attached to the message, a branch carries its own bill.
 *
 * Every field is a string or a number. The metadata is handed to a runtime that
 * may clone or persist it, and a bigint is the one value JSON cannot carry - so
 * the base units are decimal text, which is also what the chain speaks.
 */
export interface Purchase {
  provider: string;
  model: string;
  /** Native MATRIX base units actually charged, as a decimal string. */
  units: string;
  promptTokens: number;
  completionTokens: number;
  /** The seller's own endpoint, which is where the work was bought. */
  servedBy: string;
  receipt?: ReceiptCheck;
  /**
   * The run stopped before the model was done, so the text above is what arrived
   * and the charge is for that much.
   *
   * Shown rather than kept quiet: a partial answer that looks finished is the one
   * thing a reader cannot detect for themselves, and they were billed for it.
   */
  cutShort?: boolean;
}

export interface MatrixChatSettings {
  /** The node whose directory is read to find a seller. */
  endpoint: string;
  signer: Signer | null;
  model: string;
  /** Refuse sellers who have staked less than this, in base units. */
  minBond?: bigint;
  /** A seller picked on the marketplace page, by id and never by address. */
  chosen?: SellerChoice;
}

/** Reads a purchase back off a message's metadata. */
export function purchaseOf(metadata: { custom?: Record<string, unknown> } | undefined): Purchase | undefined {
  const value = metadata?.custom?.['purchase'];
  return value ? (value as Purchase) : undefined;
}

/**
 * The transcript, as the chain will see it.
 *
 * Only text survives. A reasoning part is the model's own working, and
 * replaying it as if the buyer had said it would change the prompt that gets
 * signed into something the buyer never wrote.
 */
export function transcriptOf(messages: readonly ThreadMessage[]): Message[] {
  const out: Message[] = [];
  for (const message of messages) {
    if (message.role !== 'user' && message.role !== 'assistant' && message.role !== 'system') continue;
    const text = message.content
      .filter((part): part is { type: 'text'; text: string } => part.type === 'text')
      .map((part) => part.text)
      .join('');
    if (text === '') continue;
    out.push({ role: message.role, content: text });
  }
  return out;
}

/**
 * Wires the chat UI to a seller on the marketplace.
 *
 * IT STREAMS, and what made that possible was not a UI change. The completion
 * used to be withheld until the payment was signed, because the buyer holds
 * their own key and the charge is unknowable until the work is done - so the
 * withholding was the only thing holding them to the bargain, and handing the
 * text over early handed over the leverage.
 *
 * The amount is unknowable; the RESERVATION is not. So the escrowed path pays
 * the most the job can cost BEFORE the model starts, streams because there is
 * nothing left to withhold, and has consensus return the change when the
 * settlement names the actual. The adapter yields rather than returns, and each
 * yield is text the reader has already been charged the cap for.
 *
 * WHEN IT FALLS BACK. A node that has not activated the escrow rules refuses the
 * reserve, and a buyer on such a network should get an answer rather than an
 * error about a protocol version. So the first failure of the escrowed path
 * retries with the one this replaces - which does not stream, and says so.
 *
 * WHAT STOP DOES. It stops the stream and NOT the payment. The reservation is
 * already funded by the time there is anything to stop, so the run still settles
 * for what arrived - a stop that abandoned the job would leave the whole
 * reservation to the provider's claim, making it the most expensive button here.
 * The partial answer is kept and marked as partial.
 *
 * The settings are read through a ref rather than closed over, so that editing
 * the endpoint or picking another model takes effect on the NEXT message instead
 * of rebuilding the runtime and dropping the thread.
 */
export function MatrixRuntimeProvider({
  children,
  settings,
  onSettled,
}: {
  children: ReactNode;
  settings: MatrixChatSettings;
  onSettled?: () => void;
}) {
  // Written after the commit rather than during the render: a ref is not
  // rendering state, and React's own rule against touching one mid-render is
  // what keeps this from being read in a half-applied state. Every send happens
  // after a commit, so the adapter always sees the settings on screen.
  const latest = useRef({ settings, onSettled });
  useEffect(() => {
    latest.current = { settings, onSettled };
  });

  const adapter = useMemo<ChatModelAdapter>(
    () => ({
      async *run({ messages, abortSignal }) {
        const { endpoint, signer, model, minBond, chosen } = latest.current.settings;
        if (!signer) throw new Error('Connect a wallet before sending a message.');
        if (model === '') throw new Error('Pick a model before sending a message.');

        const history = transcriptOf(messages);

        // The text so far, and a promise that resolves when the whole exchange
        // has settled. The deltas arrive in a callback rather than as an async
        // iterator, so they are queued here and drained by the loop below - a
        // generator cannot yield from inside somebody else's callback.
        let text = '';
        let pending = false;
        let finished = false;
        let failed: unknown;
        // Where it got to. The fallback is only safe from the FIRST step: past
        // it the reservation is funded, and retrying down the other path would
        // pay for the same answer twice.
        let phase: EscrowPhase = 'reserving';

        const run = chatEscrowed(endpoint, signer, {
          model,
          messages: history,
          minBond,
          chosen,
          ...(abortSignal ? { signal: abortSignal } : {}),
          onPhase: (p) => {
            phase = p;
          },
          onDelta: (delta) => {
            text += delta;
            pending = true;
          },
        }).catch((err: unknown) => {
          failed = err;
          return undefined;
        }).finally(() => {
          finished = true;
        });

        // Poll rather than await: yielding on every delta would render a frame
        // per token on a fast backend, which is work the browser does not need
        // to do to look like typing.
        //
        // An abort does NOT break this loop. The signal is handed to the escrow
        // client, which stops the stream and then settles for what arrived, and
        // leaving early here would abandon a funded reservation - the reader
        // would pay the cap for a partial answer. The wait after a stop is the
        // settlement, and it is short.
        while (!finished) {
          await new Promise((r) => setTimeout(r, 50));
          if (pending) {
            pending = false;
            yield { content: [{ type: 'text' as const, text }] };
          }
        }
        let settled = await run;
        if (failed !== undefined) {
          // A node that has not activated the escrow rules refuses the reserve,
          // and a reader on such a network should get an answer rather than an
          // error naming a protocol version. Only from the first step: past it
          // the reservation is funded and a retry would pay twice.
          if (phase !== 'reserving') throw failed;
          if (abortSignal?.aborted) throw failed;
          settled = await chat(endpoint, signer, { model, messages: history, minBond, chosen });
          text = settled.completion;
        }
        if (settled === undefined) throw new Error('the seller produced no answer');

        // Checked here, against the prompt this page actually sent and the
        // answer that came back. A receipt the seller's own node vouches for
        // would be evidence of nothing.
        const receipt = await checkReceipt(settled, history, signer.accountId);

        // The balance moved, and the page showing it has no other way to know.
        latest.current.onSettled?.();

        const purchase: Purchase = {
          provider: settled.provider,
          model: settled.model || model,
          units: settled.units.toString(),
          promptTokens: settled.promptTokens,
          completionTokens: settled.completionTokens,
          servedBy: settled.seller.endpoint,
          ...(receipt ? { receipt } : {}),
          ...(settled.cutShort ? { cutShort: true } : {}),
        };

        yield {
          content: [
            // The working comes first because that is the order it was produced
            // in, and it is shown at all because it was BILLED: most of a
            // reasoning model's tokens go here and the receipt's digest covers
            // them, so hiding it would be charging for text the buyer may not
            // read and then leaving them unable to check the bill.
            ...(settled.reasoning !== ''
              ? [{ type: 'reasoning' as const, text: settled.reasoning }]
              : []),
            { type: 'text' as const, text: settled.completion },
          ],
          metadata: { custom: { purchase } },
        };
      },
    }),
    [],
  );

  const runtime = useLocalRuntime(adapter);
  return <AssistantRuntimeProvider runtime={runtime}>{children}</AssistantRuntimeProvider>;
}
