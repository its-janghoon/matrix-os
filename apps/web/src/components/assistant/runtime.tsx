'use client';

import { useEffect, useMemo, useRef, type ReactNode } from 'react';
import {
  AssistantRuntimeProvider,
  useLocalRuntime,
  type ChatModelAdapter,
  type ThreadMessage,
} from '@assistant-ui/react';

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
 * WHY THIS DOES NOT STREAM, and why that is not an omission here. The completion
 * is withheld by the serving node until the payment is signed: the buyer holds
 * their own key, so there is no way to take payment first, and handing over the
 * text before being paid would let anyone read for free. So one message is one
 * round trip that returns the whole answer, and the adapter says so by
 * returning rather than yielding. Streaming needs a pre-funded escrow the seller
 * can draw on - the same thing that would let one approval cover many messages -
 * and it is a protocol change, not a UI one.
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
      async run({ messages }) {
        const { endpoint, signer, model, minBond, chosen } = latest.current.settings;
        if (!signer) throw new Error('Connect a wallet before sending a message.');
        if (model === '') throw new Error('Pick a model before sending a message.');

        const history = transcriptOf(messages);
        const settled = await chat(endpoint, signer, { model, messages: history, minBond, chosen });

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
        };

        return {
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
