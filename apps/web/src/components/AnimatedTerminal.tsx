'use client';

import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';

export interface TerminalLine {
  /** A shell prompt line. Typed out character by character. */
  command?: string;
  /** Output lines, revealed together once the command finishes typing. */
  output?: string[];
  /** Highlight tone applied to the output block. */
  tone?: 'default' | 'good' | 'dim';
}

interface Props {
  lines: TerminalLine[];
  /** Filename shown in the window chrome. */
  label?: string;
  className?: string;
  /** Rendered at the right of the window chrome, e.g. a copy button. */
  action?: ReactNode;
  /** Milliseconds per typed character. */
  charMs?: number;
  /** Pause after a command finishes typing, before its output appears. */
  outputDelayMs?: number;
  /** Pause after output, before the next command starts. */
  lineDelayMs?: number;
}

const TONE: Record<NonNullable<TerminalLine['tone']>, string> = {
  default: 'text-grayscale-200',
  good: 'text-secondary-300',
  dim: 'text-grayscale-400',
};

/**
 * A terminal that types itself out.
 *
 * Two things make this honest rather than decorative. The commands and their
 * output are the real ones - taken from the CLI's own help text and printed
 * field names - so a reader who copies a line gets that result rather than a
 * plausible-looking fiction. And it is driven by one timer that advances a
 * character index, not by a CSS animation per line, so the transcript can be
 * rendered complete and static when the reader has asked for reduced motion.
 *
 * Rendering starts from the finished transcript and animates only after mount.
 * Server-rendered HTML therefore contains the whole transcript, which is what a
 * crawler and a reader with JavaScript disabled will see.
 */
export function AnimatedTerminal({
  lines,
  label = 'matrix',
  className = '',
  action,
  charMs = 26,
  outputDelayMs = 320,
  lineDelayMs = 620,
}: Props) {
  // The total number of animation steps: one per character of every command,
  // plus one settling step per line for its output.
  const steps = useMemo(() => {
    let total = 0;
    for (const line of lines) total += (line.command?.length ?? 0) + 1;
    return total;
  }, [lines]);

  const [animate, setAnimate] = useState(false);
  const [step, setStep] = useState(steps);
  const containerRef = useRef<HTMLDivElement | null>(null);

  // Animate only after mount, only when motion is welcome, and only once the
  // block is actually on screen - a transcript that finished typing above the
  // fold before the reader arrived may as well have been static.
  useEffect(() => {
    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)');
    if (reduced.matches) return;
    const node = containerRef.current;
    if (!node) return;

    if (typeof IntersectionObserver === 'undefined') {
      // Deferred rather than called inline: state set directly in an effect
      // body can cascade into a second render of this same pass, which is what
      // the observer branch below avoids by only ever setting state from its
      // own callback. A same-tick timeout gets the same "from a callback, not
      // from the render that scheduled the effect" shape without waiting on
      // an intersection that will never fire in an environment without one.
      const timer = window.setTimeout(() => {
        setStep(0);
        setAnimate(true);
      }, 0);
      return () => window.clearTimeout(timer);
    }
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            setStep(0);
            setAnimate(true);
            observer.disconnect();
          }
        }
      },
      { threshold: 0.35 },
    );
    observer.observe(node);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    if (!animate || step >= steps) return;
    // Where the cursor is now, so the delay can match what just happened:
    // a character is fast, finishing a command pauses, finishing output pauses
    // longer.
    let remaining = step;
    let delay = charMs;
    for (const line of lines) {
      const len = line.command?.length ?? 0;
      if (remaining < len) {
        delay = charMs;
        break;
      }
      if (remaining === len) {
        delay = line.output?.length ? outputDelayMs : lineDelayMs;
        break;
      }
      remaining -= len + 1;
    }
    const timer = window.setTimeout(() => setStep((value) => value + 1), delay);
    return () => window.clearTimeout(timer);
  }, [animate, step, steps, lines, charMs, outputDelayMs, lineDelayMs]);

  const done = step >= steps;

  // Resolve the step counter into per-line typing progress.
  //
  // Built with reduce rather than a mutable running counter closed over by the
  // map callback: a variable reassigned from inside a callback survives only
  // by convention, not by the language, and a lint rule that assumes render
  // bodies are pure cannot tell that convention from a bug. The accumulator
  // carries the remaining cursor forward explicitly instead.
  const rendered = lines.reduce<{
    list: Array<{ line: TerminalLine; typed: number; showOutput: boolean; active: boolean }>;
    remaining: number;
  }>(
    (acc, line) => {
      const len = line.command?.length ?? 0;
      if (acc.remaining <= 0) {
        return { list: [...acc.list, { line, typed: 0, showOutput: false, active: false }], remaining: 0 };
      }
      if (acc.remaining <= len) {
        return {
          list: [...acc.list, { line, typed: acc.remaining, showOutput: false, active: true }],
          remaining: 0,
        };
      }
      const showOutput = acc.remaining > len;
      return {
        list: [...acc.list, { line, typed: len, showOutput, active: false }],
        remaining: acc.remaining - len - 1,
      };
    },
    { list: [], remaining: step },
  ).list;

  const firstPending = rendered.findIndex((entry) => entry.typed === 0 && !entry.showOutput);
  const visible = firstPending === -1 ? rendered : rendered.slice(0, firstPending + 1);

  return (
    <div
      ref={containerRef}
      className={`overflow-hidden rounded-2xl border border-white/10 bg-black/70 shadow-card backdrop-blur-sm ${className}`}
    >
      {/* Window chrome. The dots are decorative; the action slot is not, so only
          the dots and the label are hidden from assistive tech. */}
      <div className='flex items-center gap-2 border-b border-white/10 bg-white/[0.03] px-4 py-2.5'>
        <span aria-hidden className='h-2.5 w-2.5 rounded-full bg-white/20' />
        <span aria-hidden className='h-2.5 w-2.5 rounded-full bg-white/15' />
        <span aria-hidden className='h-2.5 w-2.5 rounded-full bg-white/10' />
        <span aria-hidden className='ml-2 font-mono text-[11px] tracking-tight text-grayscale-500'>
          {label}
        </span>
        {action ? <div className='ml-auto'>{action}</div> : null}
      </div>

      {/*
        A div, not a <pre>. The lines are already separate elements, so `pre`
        added nothing except `white-space: pre`, which made the browser treat the
        whole transcript as one unbreakable line: a long command such as a
        release URL then forced a horizontal scrollbar and was cut off at the
        right edge rather than wrapping. Each row below opts into wrapping
        explicitly, so a long command breaks instead of hiding.

        One live region would re-announce the whole transcript on every typed
        character. The animation is presentational, so the block is exposed as a
        static labelled group instead.
      */}
      <div
        role='group'
        aria-label={`Terminal transcript: ${label}`}
        className='px-4 py-4 font-mono text-[12.5px] leading-relaxed sm:text-[13px]'
      >
        {visible.map((entry, index) => {
          const { line, typed, showOutput, active } = entry;
          return (
            <div key={index}>
              {line.command !== undefined ? (
                <div className='whitespace-pre-wrap break-all text-white'>
                  <span className='select-none text-secondary-400'>$ </span>
                  {line.command.slice(0, typed)}
                  {active && !done ? <Cursor /> : null}
                </div>
              ) : null}
              {showOutput && line.output
                ? line.output.map((out, outIndex) => (
                    <div
                      key={outIndex}
                      className={`whitespace-pre-wrap break-all ${TONE[line.tone ?? 'default']}`}
                    >
                      {out}
                    </div>
                  ))
                : null}
            </div>
          );
        })}
        {/* The resting prompt. It blinks only while the transcript is animating:
            once finished, or when motion is not wanted, a permanently flashing
            block is noise rather than a signal that anything is happening. */}
        <div className='text-white'>
          <span className='select-none text-secondary-400'>$ </span>
          {animate ? <Cursor /> : null}
        </div>
      </div>
    </div>
  );
}

function Cursor() {
  return (
    <span
      aria-hidden
      className='ml-0.5 inline-block h-[1.05em] w-[0.5em] translate-y-[0.15em] animate-blink bg-secondary-300 align-baseline'
    />
  );
}

export default AnimatedTerminal;
