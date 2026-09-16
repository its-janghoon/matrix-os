'use client';

import { ComposerPrimitive, ErrorPrimitive, MessagePrimitive, ThreadPrimitive, useAuiState } from '@assistant-ui/react';

import { formatMatrix, SYMBOL } from '@/lib/wallet/format';
import { receiptVerdict } from '@/lib/wallet/receipt';
import { purchaseOf, type Purchase } from './runtime';

const FIELD =
  'w-full rounded-lg border border-gray-700 bg-black/60 px-3 py-2 font-mono text-sm text-gray-100 ' +
  'outline-none focus:border-gray-500 disabled:opacity-50';

/**
 * The chat surface, composed from assistant-ui's headless primitives.
 *
 * The primitives own what is genuinely hard and generic - the message store,
 * branching, editing, regeneration, viewport anchoring, composer state - and
 * nothing else. Every pixel here is ours, because the parts of this page that
 * matter are the parts no chat library has an opinion about: what was charged,
 * which seller served it, and whether their signed receipt checks out.
 */
export function Thread() {
  return (
    <ThreadPrimitive.Root className='flex min-h-0 flex-1 flex-col'>
      <ThreadPrimitive.Viewport className='flex-1 space-y-3 overflow-y-auto'>
        <ThreadPrimitive.Empty>
          <p className='rounded-xl border border-gray-800 bg-gray-900/50 p-6 text-sm text-gray-400'>
            Nothing yet. Every message is signed by your own key and paid for in {SYMBOL}.
          </p>
        </ThreadPrimitive.Empty>

        <ThreadPrimitive.Messages>
          {({ message }) => (message.role === 'user' ? <UserMessage /> : <AssistantMessage />)}
        </ThreadPrimitive.Messages>

        <ThreadPrimitive.If running>
          <p className='rounded-xl border border-gray-800 bg-gray-900/40 p-4 text-sm text-gray-400'>
            The seller is working. The answer arrives once you have signed the payment - there is nothing to
            stream, because the text is withheld until then.
          </p>
        </ThreadPrimitive.If>
      </ThreadPrimitive.Viewport>

      <Composer />
    </ThreadPrimitive.Root>
  );
}

function UserMessage() {
  return (
    <MessagePrimitive.Root className='rounded-xl border border-gray-800 bg-gray-900/40 p-4'>
      <p className='mb-1 text-xs uppercase tracking-wide text-gray-500'>you</p>
      <MessagePrimitive.Parts components={{ Text: PlainText }} />
    </MessagePrimitive.Root>
  );
}

function AssistantMessage() {
  return (
    <MessagePrimitive.Root className='rounded-xl border border-gray-700 bg-gray-900/70 p-4'>
      <p className='mb-1 text-xs uppercase tracking-wide text-gray-500'>seller</p>
      <MessagePrimitive.Parts components={{ Text: PlainText, Reasoning: Working }} />
      <MessagePrimitive.Error>
        <ErrorPrimitive.Root className='mt-3 rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-200'>
          <ErrorPrimitive.Message />
        </ErrorPrimitive.Root>
      </MessagePrimitive.Error>
      <Bill />
    </MessagePrimitive.Root>
  );
}

function PlainText({ text }: { text: string }) {
  return <p className='max-w-3xl whitespace-pre-wrap text-gray-100'>{text}</p>;
}

/**
 * A reasoning model's working.
 *
 * Collapsed because it is long and it is not the answer; present because it was
 * PAID FOR. Most of a reasoning model's tokens go here, they settle, and the
 * receipt's digest covers them - so withholding it would be charging for text
 * the buyer is not allowed to read, and would leave them unable to check the
 * receipt at all.
 */
function Working({ text }: { text: string }) {
  if (text === '') return null;
  return (
    <details className='mt-3 rounded-lg border border-gray-800 bg-black/40 p-3'>
      <summary className='cursor-pointer text-xs uppercase tracking-wide text-gray-500'>
        reasoning - you paid for these tokens
      </summary>
      <p className='mt-2 max-w-3xl whitespace-pre-wrap text-sm text-gray-400'>{text}</p>
    </details>
  );
}

/** What this answer cost, who served it, and what they signed for it. */
function Bill() {
  const custom = useAuiState((s) => s.message.metadata?.custom);
  const purchase = purchaseOf(custom ? { custom } : undefined);
  if (!purchase) return null;
  return <BillLines purchase={purchase} />;
}

function BillLines({ purchase }: { purchase: Purchase }) {
  const verdict = receiptVerdict(purchase.receipt);
  // An unchecked receipt and a failed one are not the same thing, and a page
  // that renders both in grey is telling the reader they are.
  const failed = purchase.receipt !== undefined && !purchase.receipt.signatureValid;
  return (
    <div className='mt-3 space-y-1 font-mono text-xs'>
      <p className='text-gray-500'>
        paid {formatMatrix(BigInt(purchase.units))} {SYMBOL} to {purchase.provider} - {purchase.promptTokens} prompt
        + {purchase.completionTokens} completion tokens, {purchase.model}
      </p>
      <p className='text-gray-600'>served by {purchase.servedBy}</p>
      <p className={failed ? 'text-red-300' : 'text-gray-500'}>{verdict}</p>
    </div>
  );
}

function Composer() {
  const running = useAuiState((s) => s.thread.isRunning);
  return (
    <ComposerPrimitive.Root className='mt-4 flex gap-3'>
      <ComposerPrimitive.Input
        className={FIELD}
        placeholder={running ? 'waiting for the model...' : 'Say something'}
        rows={1}
        autoFocus
        submitMode='enter'
      />
      <ComposerPrimitive.Send
        className='rounded-lg bg-white px-5 py-2 text-sm font-semibold text-black disabled:opacity-40'
        disabled={running}
      >
        Send
      </ComposerPrimitive.Send>
    </ComposerPrimitive.Root>
  );
}
