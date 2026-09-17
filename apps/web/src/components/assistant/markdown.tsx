'use client';

import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';

/**
 * The model's answer, rendered.
 *
 * WHY THIS IS NOT A `<p>` WITH `whitespace-pre-wrap`. That is what this page had,
 * and it is a reasonable choice right up until the first answer contains a code
 * block - which for a coding model is the first answer. A reader who cannot tell
 * where the code starts cannot copy it, and a table rendered as pipes is worse
 * than no table.
 *
 * WHAT IS DELIBERATELY NOT HERE. No raw HTML: `react-markdown` refuses it by
 * default and that default is kept, because this text arrives from a SELLER the
 * buyer has not met. A seller who can put HTML in an answer can put a form in it,
 * and a form on this page is a form next to a wallet. Markdown's own syntax is
 * the whole attack surface and it renders to elements this file names.
 *
 * Links open in a new tab and carry `noreferrer`, for the same reason: the
 * destination is the seller's to choose, and it should learn nothing about where
 * it was linked from.
 */

const COMPONENTS: Components = {
  // Paragraph spacing is set here rather than by a prose plugin so the rules are
  // readable in one place and do not change under a dependency bump.
  p: ({ children }) => <p className='mb-3 leading-7 last:mb-0'>{children}</p>,
  ul: ({ children }) => <ul className='mb-3 list-disc space-y-1 pl-5 last:mb-0'>{children}</ul>,
  ol: ({ children }) => <ol className='mb-3 list-decimal space-y-1 pl-5 last:mb-0'>{children}</ol>,
  li: ({ children }) => <li className='leading-7'>{children}</li>,

  h1: ({ children }) => <h1 className='mb-3 mt-5 text-lg font-semibold first:mt-0'>{children}</h1>,
  h2: ({ children }) => <h2 className='mb-2 mt-5 text-base font-semibold first:mt-0'>{children}</h2>,
  h3: ({ children }) => <h3 className='mb-2 mt-4 text-sm font-semibold uppercase tracking-wide text-gray-400 first:mt-0'>{children}</h3>,

  a: ({ href, children }) => (
    <a
      href={href}
      target='_blank'
      rel='noopener noreferrer'
      className='text-blue-300 underline underline-offset-2 hover:text-blue-200'
    >
      {children}
    </a>
  ),

  blockquote: ({ children }) => (
    <blockquote className='mb-3 border-l-2 border-gray-700 pl-4 text-gray-400 last:mb-0'>{children}</blockquote>
  ),

  hr: () => <hr className='my-4 border-gray-800' />,

  // A fenced block and an inline span are the same element in markdown's AST and
  // want opposite treatment, so the distinction is drawn on the newline that only
  // a fence can contain.
  code: ({ className, children }) => {
    const text = String(children ?? '');
    const fenced = text.includes('\n') || /language-/.test(className ?? '');
    if (!fenced) {
      return <code className='rounded bg-black/60 px-1.5 py-0.5 font-mono text-[0.85em] text-gray-200'>{children}</code>;
    }
    return <code className='block font-mono text-[13px] leading-6 text-gray-200'>{children}</code>;
  },

  pre: ({ children }) => (
    <pre className='mb-3 overflow-x-auto rounded-xl border border-gray-800 bg-black/70 p-4 last:mb-0'>{children}</pre>
  ),

  // A table wide enough to overflow scrolls on its own rather than widening the
  // message and every message under it.
  table: ({ children }) => (
    <div className='mb-3 overflow-x-auto last:mb-0'>
      <table className='w-full border-collapse text-sm'>{children}</table>
    </div>
  ),
  th: ({ children }) => (
    <th className='border-b border-gray-700 px-3 py-2 text-left font-semibold text-gray-300'>{children}</th>
  ),
  td: ({ children }) => <td className='border-b border-gray-800 px-3 py-2 align-top'>{children}</td>,
};

export function Markdown({ text }: { text: string }) {
  return (
    <div className='text-[15px] text-gray-100'>
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={COMPONENTS}>
        {text}
      </ReactMarkdown>
    </div>
  );
}
