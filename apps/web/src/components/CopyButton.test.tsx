import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { CopyButton } from './CopyButton';

// The toast is the user-visible half of the contract, so it is asserted on
// rather than allowed to fire for real.
const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock('sonner', () => ({ toast }));

/** Replace navigator.clipboard, which jsdom does not provide and which is
 *  read-only on the real navigator. */
function setClipboard(writeText: ((v: string) => Promise<void>) | undefined) {
  Object.defineProperty(globalThis.navigator, 'clipboard', {
    value: writeText ? { writeText } : undefined,
    configurable: true,
    writable: true,
  });
}

describe('CopyButton', () => {
  beforeEach(() => {
    toast.success.mockClear();
    toast.error.mockClear();
  });

  afterEach(() => {
    setClipboard(undefined);
  });

  it('writes the exact value it was given, unmodified', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    setClipboard(writeText);
    // A multi-line command with flags: the thing actually being copied on the
    // quickstart and inference pages.
    const command = 'matrix inference submit \\\n  --provider demo --api-key $KEY';
    render(<CopyButton value={command} />);

    await userEvent.click(screen.getByRole('button'));

    expect(writeText).toHaveBeenCalledTimes(1);
    expect(writeText).toHaveBeenCalledWith(command);
  });

  it('confirms only after the write resolves', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    setClipboard(writeText);
    render(<CopyButton value='matrix status' />);

    expect(screen.getByRole('button')).toHaveTextContent('Copy');

    await userEvent.click(screen.getByRole('button'));

    await waitFor(() => expect(screen.getByRole('button')).toHaveTextContent('Copied'));
    expect(toast.success).toHaveBeenCalledTimes(1);
    expect(toast.error).not.toHaveBeenCalled();
  });

  it('reports failure instead of claiming success when the write rejects', async () => {
    // This is the point of the component: a browser that denies clipboard
    // permission must not leave the user believing the command was copied.
    const writeText = vi.fn().mockRejectedValue(new Error('denied'));
    setClipboard(writeText);
    render(<CopyButton value='matrix status' />);

    await userEvent.click(screen.getByRole('button'));

    await waitFor(() => expect(toast.error).toHaveBeenCalledTimes(1));
    expect(toast.success).not.toHaveBeenCalled();
    expect(screen.getByRole('button')).toHaveTextContent('Copy');
    expect(screen.getByRole('button')).not.toHaveTextContent('Copied');
  });

  it('reports failure when there is no clipboard API at all', async () => {
    // Non-secure origins and older browsers expose no navigator.clipboard.
    setClipboard(undefined);
    render(<CopyButton value='matrix status' />);

    await userEvent.click(screen.getByRole('button'));

    await waitFor(() => expect(toast.error).toHaveBeenCalledTimes(1));
    expect(toast.success).not.toHaveBeenCalled();
    expect(screen.getByRole('button')).toHaveTextContent('Copy');
  });

  it('returns to its resting label after the confirmation window', async () => {
    // fireEvent rather than userEvent here: userEvent schedules its own
    // inter-event delays, which fake timers have to drive, and the two fight.
    // A plain synchronous dispatch plus an explicit timer advance is what this
    // test is actually about.
    vi.useFakeTimers();
    try {
      const writeText = vi.fn().mockResolvedValue(undefined);
      setClipboard(writeText);
      render(<CopyButton value='matrix status' />);
      const button = screen.getByRole('button');

      // The handler awaits the clipboard write, so the resulting state update
      // lands in a later microtask. `act` with an async callback flushes both
      // that promise and React's render work; advancing timers alone does not.
      await act(async () => {
        fireEvent.click(button);
      });
      expect(button).toHaveTextContent('Copied');

      // The component resets after 1.5s; anything short of that must still read
      // as copied, so the window itself is asserted rather than just the end.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1400);
      });
      expect(button).toHaveTextContent('Copied');

      await act(async () => {
        await vi.advanceTimersByTimeAsync(200);
      });
      expect(button).toHaveTextContent('Copy');
      expect(button).not.toHaveTextContent('Copied');
    } finally {
      vi.useRealTimers();
    }
  });

  it('is reachable by its accessible label', () => {
    render(<CopyButton value='matrix status' label='Copy the install command' />);
    expect(screen.getByRole('button', { name: 'Copy the install command' })).toBeInTheDocument();
  });
});
