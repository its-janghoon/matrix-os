import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { Amount } from './ui';

/**
 * The rule that is easy to get backwards.
 *
 * Setting the fraction quieter than the whole number makes a long settled total
 * readable. Applied to a price below one it does the opposite: the single digit
 * it emphasises is the zero, and the three it fades are the entire figure.
 */
describe('Amount', () => {
  const fadedClass = (el: Element | null) => el?.className.includes('text-grayscale-500') ?? false;

  it('quiets the fraction when the whole number carries the magnitude', () => {
    const { container } = render(<Amount value='712,800.000000005' />);
    const parts = container.querySelectorAll('span > span');
    expect(parts[0]?.textContent).toBe('712,800');
    expect(parts[1]?.textContent).toBe('.000000005');
    expect(fadedClass(parts[1])).toBe(true);
  });

  it('does NOT quiet the fraction when it is the whole figure', () => {
    // A price of 0.005 MATRIX per million units. Fading this would render the
    // number backwards: zero loud, the value silent.
    const { container } = render(<Amount value='0.005' />);
    const parts = container.querySelectorAll('span > span');
    expect(parts[0]?.textContent).toBe('0');
    expect(parts[1]?.textContent).toBe('.005');
    expect(fadedClass(parts[1])).toBe(false);
  });

  it('renders a whole amount with no fraction at all', () => {
    render(<Amount value='300,000' />);
    expect(screen.getByText('300,000')).toBeTruthy();
  });
});
