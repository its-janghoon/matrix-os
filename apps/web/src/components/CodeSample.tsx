import { CopyButton } from '@/components/CopyButton';

/**
 * A labelled code block with a copy button.
 *
 * It exists to hold the snippet ONCE. The docs pages used to pass the same
 * string twice - into a copy handler and into the rendered `<pre>` - which is
 * a standing invitation for the button to copy something other than what the
 * reader sees, and it doubles the size of every edit.
 */
export function CodeSample({
  label,
  code,
  className = '',
}: {
  /** What this snippet is, shown above it (a filename, a shell, a language). */
  label: string;
  code: string;
  className?: string;
}) {
  return (
    <div className={`rounded-lg border border-gray-800 bg-black p-4 ${className}`}>
      <div className='mb-2 flex items-center justify-between gap-3'>
        <span className='text-sm text-gray-400'>{label}</span>
        <CopyButton value={code} label={`Copy ${label}`} />
      </div>
      <pre className='overflow-x-auto text-sm text-gray-100'>
        <code>{code}</code>
      </pre>
    </div>
  );
}
