import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { BrandMark } from './BrandMark';

describe('BrandMark', () => {
  it('draws the six-facet mark on the 32x32 grid the assets are generated on', () => {
    const { container } = render(<BrandMark />);
    const svg = container.querySelector('svg');

    expect(svg).not.toBeNull();
    // The viewBox has to stay 0 0 32 32: every generated asset in
    // public/brand shares that grid, so a change here silently desyncs the
    // header mark from the favicons and the og-image.
    expect(svg).toHaveAttribute('viewBox', '0 0 32 32');
    expect(container.querySelectorAll('polygon')).toHaveLength(6);
  });

  it('gives every facet a fill and a matching stroke', () => {
    // The stroke is not decoration: it closes the antialiasing seam that shows
    // between abutting polygons, so a facet without one is a rendering bug.
    const { container } = render(<BrandMark />);

    for (const facet of container.querySelectorAll('polygon')) {
      const fill = facet.getAttribute('fill');
      expect(fill).toMatch(/^#[0-9A-Fa-f]{6}$/);
      expect(facet).toHaveAttribute('stroke', fill!);
    }
  });

  it('renders at the requested size, defaulting to 32', () => {
    const { container: dflt } = render(<BrandMark />);
    expect(dflt.querySelector('svg')).toHaveAttribute('width', '32');

    const { container: sized } = render(<BrandMark size={96} />);
    const svg = sized.querySelector('svg');
    expect(svg).toHaveAttribute('width', '96');
    expect(svg).toHaveAttribute('height', '96');
    // The art scales through the viewBox, which must not change with size.
    expect(svg).toHaveAttribute('viewBox', '0 0 32 32');
  });

  it('is hidden from assistive tech, because the link around it carries the name', () => {
    const { container } = render(<BrandMark />);
    const svg = container.querySelector('svg');
    expect(svg).toHaveAttribute('aria-hidden', 'true');
    expect(svg).toHaveAttribute('focusable', 'false');
  });

  it('passes through a className so callers can position it', () => {
    const { container } = render(<BrandMark className='shrink-0' />);
    expect(container.querySelector('svg')).toHaveClass('shrink-0');
  });
});
