'use client';

import {
  ActionBarPrimitive,
  BranchPickerPrimitive,
  ComposerPrimitive,
  ErrorPrimitive,
  MessagePrimitive,
  ThreadPrimitive,
  useAuiState,
} from '@assistant-ui/react';

import { formatMatrix, SYMBOL } from '@/lib/wallet/format';
import { receiptVerdict } from '@/lib/wallet/receipt';
import { Markdown } from './markdown';
import { purchaseOf, type Purchase } from './runtime';

/**
 * The chat surface, composed from assistant-ui's headless primitives.
 *
 * The primitives own what is genuinely hard and generic - the message store,
 * branching, editing, regeneration, viewport anchoring, composer state - and
 * nothing else. Every pixel here is ours, because the parts of this page that
 * matter are the parts no chat library has an opinion about: what was charged,
 * which seller served it, and whether their signed receipt checks out.
 *
 * VERIFY THIS FILE AGAINST A PRODUCTION BUILD, NOT `next dev`. Under the dev
 * server the composer store never sees what is typed - Send stays disabled with
 * text in the box, the empty state does not render, and nothing sends. It is not
 * this file: the component as it stood before a line of this was written fails
 * the same way, and every one of those works in `next build && next start`.
 * Hours can go into "fixing" a page that was never broken.
 *
 * THE BILL GOT QUIETER, NOT ABSENT. An earlier version gave the charge the same
 * visual weight as the answer, which is the right instinct pointed at the wrong
 * moment: a reader wants the answer, and wants the price to be there when they
 * look for it. It is one line under the message now, and the receipt verdict is
 * coloured only when it FAILED - an unchecked receipt and a bad one rendered in
 * the same grey is the page telling the reader they are the same thing.
 */

const SUGGESTIONS = [
  'Write a haiku about paying for compute',
  'Explain what a spend budget is, in two sentences',
  'Give me a bash one-liner that counts files by extension',
];

export function Thread({ settlesWithoutPrompting = false }: { settlesWithoutPrompting?: boolean }) {
  return (
    <ThreadPrimitive.Root className='flex min-h-0 flex-1 flex-col'>
      <div className='relative flex min-h-0 flex-1 flex-col'>
        <ThreadPrimitive.Viewport className='flex-1 space-y-6 overflow-y-auto pb-4'>
          <ThreadPrimitive.Empty>
            <Welcome />
          </ThreadPrimitive.Empty>

          {/*
            The waiting state belongs INSIDE the assistant message, not beside
            it. A thread that is running already has an assistant message - empty
            until the answer lands - so a sibling indicator draws a second avatar
            under the first and the reader sees two speakers where there is one.
          */}
          <ThreadPrimitive.Messages>
            {({ message }) =>
              message.role === 'user' ? (
                <UserMessage />
              ) : (
                <AssistantMessage settlesWithoutPrompting={settlesWithoutPrompting} />
              )
            }
          </ThreadPrimitive.Messages>
        </ThreadPrimitive.Viewport>

        <ThreadPrimitive.ScrollToBottom
          className='absolute bottom-2 left-1/2 -translate-x-1/2 rounded-full border border-gray-700
                     bg-gray-900 p-2 text-gray-300 shadow-lg transition hover:bg-gray-800
                     disabled:pointer-events-none disabled:opacity-0'
          aria-label='Scroll to the newest message'
        >
          <ArrowDown />
        </ThreadPrimitive.ScrollToBottom>
      </div>

      <Composer />
    </ThreadPrimitive.Root>
  );
}

function Welcome() {
  return (
    <div className='flex flex-col items-center gap-6 py-10 text-center'>
      <div>
        <p className='text-lg font-medium text-gray-200'>Ask a seller something.</p>
        <p className='mt-1 text-sm text-gray-500'>
          Every message is signed by a key in this browser and paid for in {SYMBOL}.
        </p>
      </div>
      <div className='flex flex-wrap justify-center gap-2'>
        {SUGGESTIONS.map((prompt) => (
          <ThreadPrimitive.Suggestion
            key={prompt}
            prompt={prompt}
            method='replace'
            autoSend={false}
            className='rounded-full border border-gray-800 bg-gray-900/60 px-4 py-2 text-sm text-gray-300
                       transition hover:border-gray-600 hover:text-gray-100'
          >
            {prompt}
          </ThreadPrimitive.Suggestion>
        ))}
      </div>
    </div>
  );
}

function UserMessage() {
  return (
    <MessagePrimitive.Root className='flex justify-end'>
      <div className='max-w-[85%] rounded-2xl rounded-br-md bg-white/10 px-4 py-3 text-[15px] text-gray-100'>
        <MessagePrimitive.Parts components={{ Text: ({ text }) => <p className='whitespace-pre-wrap'>{text}</p> }} />
      </div>
    </MessagePrimitive.Root>
  );
}

function AssistantMessage({ settlesWithoutPrompting }: { settlesWithoutPrompting: boolean }) {
  return (
    <MessagePrimitive.Root className='group flex gap-3'>
      <Avatar />
      <div className='min-w-0 flex-1'>
        <WhileRunning settlesWithoutPrompting={settlesWithoutPrompting} />

        <MessagePrimitive.Parts components={{ Text: ({ text }) => <Markdown text={text} />, Reasoning: Reasoning }} />

        <MessagePrimitive.Error>
          <ErrorPrimitive.Root className='mt-3 rounded-xl border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-200'>
            <ErrorPrimitive.Message />
          </ErrorPrimitive.Root>
        </MessagePrimitive.Error>

        <div className='mt-2 flex items-center gap-1'>
          <ActionBarPrimitive.Root
            hideWhenRunning
            autohide='not-last'
            className='flex items-center gap-1 opacity-0 transition group-hover:opacity-100 focus-within:opacity-100'
          >
            <ActionBarPrimitive.Copy className={ICON_BUTTON} aria-label='Copy this answer'>
              <Clipboard />
            </ActionBarPrimitive.Copy>
            <ActionBarPrimitive.Reload className={ICON_BUTTON} aria-label='Ask again - this buys a new answer'>
              <Refresh />
            </ActionBarPrimitive.Reload>
          </ActionBarPrimitive.Root>
          <Branches />
        </div>

        <Bill />
      </div>
    </MessagePrimitive.Root>
  );
}

/**
 * The waiting state, scoped to THIS message.
 *
 * `MessagePrimitive.If` has no `running`, and the thread-level one is the wrong
 * scope anyway: it is true while any message runs, which during a regeneration
 * would put an indicator under an answer that is already on screen.
 */
function WhileRunning({ settlesWithoutPrompting }: { settlesWithoutPrompting: boolean }) {
  const running = useAuiState((s) => s.message.status?.type === 'running');
  if (!running) return null;
  return <Working settlesWithoutPrompting={settlesWithoutPrompting} />;
}

const ICON_BUTTON =
  'rounded-lg p-1.5 text-gray-500 transition hover:bg-gray-800 hover:text-gray-200 disabled:opacity-40';

/**
 * Which regeneration you are looking at.
 *
 * Present because Reload costs money: a reader who buys a second answer and
 * cannot get back to the first has paid for something they can no longer read.
 */
function Branches() {
  return (
    <BranchPickerPrimitive.Root
      hideWhenSingleBranch
      className='flex items-center gap-1 text-xs text-gray-500 opacity-0 transition group-hover:opacity-100'
    >
      <BranchPickerPrimitive.Previous className={ICON_BUTTON} aria-label='Previous answer'>
        <ChevronLeft />
      </BranchPickerPrimitive.Previous>
      <span className='font-mono'>
        <BranchPickerPrimitive.Number /> / <BranchPickerPrimitive.Count />
      </span>
      <BranchPickerPrimitive.Next className={ICON_BUTTON} aria-label='Next answer'>
        <ChevronRight />
      </BranchPickerPrimitive.Next>
    </BranchPickerPrimitive.Root>
  );
}

function Avatar() {
  return (
    <div
      aria-hidden
      className='mt-1 h-7 w-7 shrink-0 rounded-full border border-gray-700 bg-gray-900
                 text-center font-mono text-[11px] leading-[26px] text-gray-500'
    >
      M
    </div>
  );
}

/**
 * What the reader is waiting for, said accurately.
 *
 * The sentence this replaced told every reader that the answer would arrive
 * "once you have signed the payment". Under a spend budget nobody signs - the
 * delegate settles without a prompt - so that text sent people looking for a
 * wallet dialog that was never going to open. Which path they are on is not
 * something this component can infer, so the page tells it.
 *
 * The withholding is real on both paths and stays described: this chain hands
 * over the answer when the job settles rather than streaming it, and that is a
 * property of the protocol rather than of this page. Streaming needs the seller
 * to be paid BEFORE the run - the escrow half of the proposal - and until that
 * exists, promising a stream here would be the same kind of lie in a nicer font.
 */
function Working({ settlesWithoutPrompting }: { settlesWithoutPrompting: boolean }) {
  return (
    <div className='pt-1'>
      <Dots />
      <p className='mt-2 text-sm text-gray-500'>
          The seller is generating. The answer arrives in one piece when the job settles, because the text is
          withheld until it is paid for.{' '}
          {settlesWithoutPrompting
            ? 'Your budget settles it automatically - there is nothing for you to approve.'
          : 'Your wallet will ask you to sign the payment when the price is known.'}
      </p>
    </div>
  );
}

function Dots() {
  return (
    <span className='flex items-center gap-1' aria-label='Working'>
      {[0, 150, 300].map((delay) => (
        <span
          key={delay}
          className='h-2 w-2 animate-bounce rounded-full bg-gray-600'
          style={{ animationDelay: `${delay}ms` }}
        />
      ))}
    </span>
  );
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
function Reasoning({ text }: { text: string }) {
  if (text === '') return null;
  return (
    <details className='mb-3 rounded-xl border border-gray-800 bg-black/40 p-3'>
      <summary className='cursor-pointer text-xs uppercase tracking-wide text-gray-500'>
        reasoning - you paid for these tokens
      </summary>
      <p className='mt-2 whitespace-pre-wrap text-sm text-gray-400'>{text}</p>
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
    <details className='mt-1 font-mono text-xs'>
      <summary className='cursor-pointer list-none text-gray-600 hover:text-gray-400'>
        {formatMatrix(BigInt(purchase.units))} {SYMBOL} · {purchase.promptTokens + purchase.completionTokens} tokens
        {failed ? <span className='ml-2 text-red-300'>receipt failed</span> : null}
      </summary>
      <div className='mt-2 space-y-1 border-l border-gray-800 pl-3 text-gray-500'>
        <p>
          paid {formatMatrix(BigInt(purchase.units))} {SYMBOL} to {purchase.provider}
        </p>
        <p>
          {purchase.promptTokens} prompt + {purchase.completionTokens} completion tokens, {purchase.model}
        </p>
        <p>served by {purchase.servedBy}</p>
        <p className={failed ? 'text-red-300' : undefined}>{verdict}</p>
      </div>
    </details>
  );
}

/**
 * The composer.
 *
 * One rounded field with the send control inside it, which is what a reader
 * expects a chat box to be, and Enter to send with Shift+Enter for a newline.
 * While a job is running Send becomes Cancel: the previous version disabled the
 * button, which leaves someone who asked the wrong question watching their money
 * being spent with nothing to press.
 */
function Composer() {
  return (
    <ComposerPrimitive.Root
      className='mt-4 flex items-end gap-2 rounded-2xl border border-gray-700 bg-black/60 p-2
                 focus-within:border-gray-500'
    >
      <ComposerPrimitive.Input
        rows={1}
        autoFocus
        submitMode='enter'
        placeholder='Message a seller'
        className='max-h-48 flex-1 resize-none bg-transparent px-3 py-2 text-[15px] text-gray-100
                   outline-none placeholder:text-gray-600'
      />

      <ThreadPrimitive.If running={false}>
        <ComposerPrimitive.Send
          className='mb-0.5 rounded-xl bg-white p-2.5 text-black transition hover:bg-gray-200
                     disabled:cursor-not-allowed disabled:opacity-30'
          aria-label='Send'
        >
          <ArrowUp />
        </ComposerPrimitive.Send>
      </ThreadPrimitive.If>

      <ThreadPrimitive.If running>
        <ComposerPrimitive.Cancel
          className='mb-0.5 rounded-xl border border-gray-700 p-2.5 text-gray-300 transition hover:bg-gray-800'
          aria-label='Stop'
        >
          <Square />
        </ComposerPrimitive.Cancel>
      </ThreadPrimitive.If>
    </ComposerPrimitive.Root>
  );
}

// Inline rather than an icon package: five glyphs do not justify a dependency,
// and these are the only five this page draws.
const STROKE = {
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 2,
  strokeLinecap: 'round',
  strokeLinejoin: 'round',
} as const;

function ArrowUp() {
  return (
    <svg viewBox='0 0 24 24' className='h-4 w-4' {...STROKE} aria-hidden>
      <path d='M12 19V5M5 12l7-7 7 7' />
    </svg>
  );
}

function ArrowDown() {
  return (
    <svg viewBox='0 0 24 24' className='h-4 w-4' {...STROKE} aria-hidden>
      <path d='M12 5v14M19 12l-7 7-7-7' />
    </svg>
  );
}

function Square() {
  return (
    <svg viewBox='0 0 24 24' className='h-4 w-4' {...STROKE} aria-hidden>
      <rect x='6' y='6' width='12' height='12' rx='2' />
    </svg>
  );
}

function Clipboard() {
  return (
    <svg viewBox='0 0 24 24' className='h-4 w-4' {...STROKE} aria-hidden>
      <rect x='9' y='9' width='11' height='11' rx='2' />
      <path d='M5 15V5a2 2 0 0 1 2-2h10' />
    </svg>
  );
}

function Refresh() {
  return (
    <svg viewBox='0 0 24 24' className='h-4 w-4' {...STROKE} aria-hidden>
      <path d='M21 12a9 9 0 1 1-3-6.7M21 4v5h-5' />
    </svg>
  );
}

function ChevronLeft() {
  return (
    <svg viewBox='0 0 24 24' className='h-3.5 w-3.5' {...STROKE} aria-hidden>
      <path d='M15 18l-6-6 6-6' />
    </svg>
  );
}

function ChevronRight() {
  return (
    <svg viewBox='0 0 24 24' className='h-3.5 w-3.5' {...STROKE} aria-hidden>
      <path d='M9 18l6-6-6-6' />
    </svg>
  );
}
