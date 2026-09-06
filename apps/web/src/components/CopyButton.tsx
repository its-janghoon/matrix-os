'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { FiCheck, FiCopy } from 'react-icons/fi';
import { toast } from 'sonner';

interface CopyButtonProps {
  /** The exact command/text copied to the clipboard. */
  value: string;
  /** Accessible label / tooltip. Defaults to "Copy command". */
  label?: string;
  /** Optional extra classes for the button. */
  className?: string;
}

/**
 * CopyButton copies a given command string to the clipboard and shows brief
 * "copied" feedback: the icon swaps to a check for ~1.5s and a sonner toast
 * confirms. It is a self-contained client component so it can be dropped next to
 * any code/terminal snippet.
 *
 * The reported feedback is honest: the toast/check only show on a successful
 * clipboard write, and a failure surfaces an error toast instead of a false
 * "copied" state.
 */
export function CopyButton({ value, label = 'Copy command', className = '' }: CopyButtonProps) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Clear any pending reset timer on unmount so we never setState after unmount.
  useEffect(() => {
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
  }, []);

  const copy = useCallback(async () => {
    try {
      if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(value);
      } else {
        throw new Error('clipboard unavailable');
      }
      setCopied(true);
      toast.success('Copied to clipboard');
      if (timer.current) clearTimeout(timer.current);
      timer.current = setTimeout(() => setCopied(false), 1500);
    } catch {
      toast.error('Could not copy to clipboard');
    }
  }, [value]);

  return (
    <button
      type='button'
      onClick={copy}
      aria-label={label}
      title={label}
      className={`inline-flex items-center gap-1.5 rounded-md border border-white/10 bg-white/[0.04] px-2.5 py-1.5 text-xs font-medium text-grayscale-300 transition-colors hover:border-white/20 hover:text-white ${className}`}
    >
      {copied ? (
        <FiCheck className='h-4 w-4 text-semantic-success' aria-hidden />
      ) : (
        <FiCopy className='h-4 w-4' aria-hidden />
      )}
      <span>{copied ? 'Copied' : 'Copy'}</span>
    </button>
  );
}

export default CopyButton;
