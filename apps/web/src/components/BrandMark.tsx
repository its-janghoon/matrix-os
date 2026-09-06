/**
 * The Matrix OS / ECIR Labs mark: a hexagonal stone cut in six wedges, lit from
 * the upper right.
 *
 * Inlined rather than loaded from `/brand/shard/mark.svg` for two reasons: the
 * header logo should not cost a request on every page, and an SVG referenced
 * through `<img>` or `next/image` renders in an isolated context, so it cannot
 * inherit the page's font or `currentColor`. Face colours are literal here, off
 * the blue ramp in `tailwind.config.ts`, so there is no gradient id to collide
 * when the mark appears more than once on a page.
 *
 * Geometry is generated - see `public/brand/generate.py`. Edit it there and
 * re-run the script rather than hand-editing these coordinates.
 */
type BrandMarkProps = {
  /** Rendered edge length in px. The art scales from a 32x32 viewBox. */
  size?: number;
  className?: string;
};

const FACES: ReadonlyArray<{ points: string; fill: string }> = [
  { points: '16,16 16,3.4 26.91,9.7', fill: '#22D3EE' },
  { points: '16,16 26.91,9.7 26.91,22.3', fill: '#2E6BFF' },
  { points: '16,16 26.91,22.3 16,28.6', fill: '#1B4FD6' },
  { points: '16,16 16,28.6 5.09,22.3', fill: '#12358F' },
  { points: '16,16 5.09,22.3 5.09,9.7', fill: '#1B4FD6' },
  { points: '16,16 5.09,9.7 16,3.4', fill: '#2E6BFF' },
];

export function BrandMark({ size = 32, className }: BrandMarkProps) {
  return (
    <svg
      viewBox='0 0 32 32'
      width={size}
      height={size}
      className={className}
      fill='none'
      aria-hidden='true'
      focusable='false'
    >
      {FACES.map((face) => (
        // The hairline stroke matching each fill closes the antialiasing seam
        // that otherwise shows between abutting polygons.
        <polygon
          key={face.points}
          points={face.points}
          fill={face.fill}
          stroke={face.fill}
          strokeWidth={0.7}
          strokeLinejoin='round'
        />
      ))}
    </svg>
  );
}

export default BrandMark;
