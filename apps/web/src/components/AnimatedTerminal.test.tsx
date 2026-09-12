import { render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AnimatedTerminal, type TerminalLine } from './AnimatedTerminal';

const lines: TerminalLine[] = [
  { command: 'matrix bridge reconcile', output: ['Escrow balance (native): 1'] },
  { command: 'echo done', output: ['done'], tone: 'good' },
];

function setReducedMotion(reduce: boolean) {
  vi.stubGlobal(
    'matchMedia',
    vi.fn().mockImplementation((query: string) => ({
      matches: reduce && query.includes('prefers-reduced-motion'),
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
      onchange: null,
    })),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('AnimatedTerminal', () => {
  // The transcript is the content; the typing is decoration. A reader who has
  // asked for reduced motion, or who has no JavaScript, must still get all of it.
  it('renders the finished transcript when motion is not wanted', () => {
    setReducedMotion(true);
    render(<AnimatedTerminal lines={lines} />);
    expect(screen.getByText('matrix bridge reconcile')).toBeInTheDocument();
    expect(screen.getByText('Escrow balance (native): 1')).toBeInTheDocument();
    expect(screen.getByText('echo done')).toBeInTheDocument();
    expect(screen.getByText('done')).toBeInTheDocument();
  });

  it('exposes the block as one labelled group rather than a live region', () => {
    setReducedMotion(true);
    render(<AnimatedTerminal lines={lines} label='demo' />);
    const group = screen.getByRole('group', { name: 'Terminal transcript: demo' });
    expect(group).toBeInTheDocument();
    expect(group).not.toHaveAttribute('aria-live');
  });

  // `white-space: pre` on a container whose rows are separate elements makes the
  // browser treat the whole transcript as one unbreakable line, so a long
  // command scrolls out of sight instead of wrapping.
  it('lets a long command wrap instead of overflowing', () => {
    setReducedMotion(true);
    const long = 'curl -fsSLO https://example.test/a/very/long/release/archive/name.tar.gz';
    const { container } = render(<AnimatedTerminal lines={[{ command: long }]} />);
    const rows = Array.from(container.querySelectorAll('div')).filter((node) =>
      node.textContent?.includes(long),
    );
    const row = rows[rows.length - 1];
    expect(row?.className).toContain('whitespace-pre-wrap');
    expect(row?.className).toContain('break-all');
    // The container must not opt back into `white-space: pre`, which would make
    // the wrapping classes above unreachable.
    expect(container.querySelector('pre')).toBeNull();
  });

  it('does not blink a cursor when motion is not wanted', () => {
    setReducedMotion(true);
    const { container } = render(<AnimatedTerminal lines={lines} />);
    expect(container.querySelectorAll('.animate-blink')).toHaveLength(0);
  });

  it('renders an action in the chrome without hiding it from assistive tech', () => {
    setReducedMotion(true);
    render(<AnimatedTerminal lines={lines} action={<button type='button'>Copy</button>} />);
    const button = screen.getByRole('button', { name: 'Copy' });
    expect(button).toBeInTheDocument();
    expect(button.closest('[aria-hidden="true"]')).toBeNull();
  });

  it('renders output-only lines that carry no prompt of their own', () => {
    setReducedMotion(true);
    render(<AnimatedTerminal lines={[{ output: ['note'], tone: 'dim' }]} />);
    const note = screen.getByText('note');
    expect(note).toBeInTheDocument();
    // The output line itself has no prompt. The single `$` on the page is the
    // resting prompt drawn after the transcript finishes.
    expect(note.textContent).not.toContain('$');
    expect(screen.getAllByText('$')).toHaveLength(1);
  });
});
