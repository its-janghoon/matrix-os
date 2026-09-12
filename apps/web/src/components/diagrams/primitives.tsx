/**
 * Diagram primitives.
 *
 * The site's explanations were carried almost entirely by prose, which is the
 * wrong medium for a flow: "the provider announces capacity, the buyer submits
 * a signed job, the leader batches it into a block, a quorum commits it, and
 * the credits move" is four boxes and four arrows. These primitives keep the
 * diagrams consistent with each other and with the palette, and keep each one
 * to a description of its own shape rather than a pile of raw coordinates.
 *
 * Every diagram is INLINE svg rather than an <img>. An SVG loaded through
 * next/image or <img> renders in an isolated document: it cannot reach the
 * page's font or its colour tokens, so labels come out in a fallback serif at
 * the wrong weight. Inline, the text is the page's Inter and `currentColor`
 * works.
 */
import type { ReactNode } from 'react';

/** Palette lifted from tailwind.config.ts, spelled out because SVG paint
 *  attributes cannot resolve Tailwind class names. */
export const C = {
  primary: '#2E6BFF',
  primaryDim: '#12358F',
  secondary: '#22D3EE',
  secondaryDim: '#0A6E84',
  accent: '#9B6CF9',
  accentDim: '#5B28C3',
  success: '#22C55E',
  text: '#F5F7FB',
  textDim: '#8B96AC',
  line: '#3C465C',
  panel: '#0A0F1C',
  panelEdge: '#232C40',
} as const;

export type Tone = 'primary' | 'secondary' | 'accent' | 'neutral';

const toneColor: Record<Tone, { stroke: string; fill: string }> = {
  primary: { stroke: C.primary, fill: 'rgba(46,107,255,0.10)' },
  secondary: { stroke: C.secondary, fill: 'rgba(34,211,238,0.10)' },
  accent: { stroke: C.accent, fill: 'rgba(155,108,249,0.12)' },
  neutral: { stroke: C.panelEdge, fill: 'rgba(20,27,44,0.70)' },
};

/**
 * Figure wrapper: an accessible, responsive frame around one diagram.
 *
 * `scrollAt` sets a minimum width so a wide diagram scrolls horizontally on a
 * phone instead of shrinking its labels to nothing. Portrait diagrams leave it
 * unset and simply scale.
 */
export function Diagram({
  title,
  description,
  viewBox,
  caption,
  className = '',
  minWidth,
  children,
}: {
  /** Short name, announced to screen readers as the image's label. */
  title: string;
  /** What the diagram says, for readers who cannot see it. */
  description: string;
  viewBox: string;
  caption?: ReactNode;
  className?: string;
  minWidth?: number;
  children: ReactNode;
}) {
  const id = title.toLowerCase().replace(/[^a-z0-9]+/g, '-');
  const svg = (
    <svg
      viewBox={viewBox}
      role='img'
      aria-labelledby={`${id}-title ${id}-desc`}
      className='h-auto w-full'
      style={minWidth ? { minWidth } : undefined}
    >
      <title id={`${id}-title`}>{title}</title>
      <desc id={`${id}-desc`}>{description}</desc>
      <defs>
        {(['primary', 'secondary', 'accent', 'neutral'] as Tone[]).map((tone) => (
          <marker
            key={tone}
            id={`arrow-${tone}`}
            viewBox='0 0 10 10'
            refX='9'
            refY='5'
            markerWidth='6'
            markerHeight='6'
            orient='auto-start-reverse'
          >
            <path d='M 0 1 L 9 5 L 0 9 z' fill={toneColor[tone].stroke} />
          </marker>
        ))}
      </defs>
      {children}
    </svg>
  );

  return (
    <figure className={`my-8 ${className}`}>
      {minWidth ? (
        // A wide diagram scrolls rather than shrinking its labels into
        // illegibility. The fade on the right edge is there so a reader on a
        // phone can tell there is more of it than the screen shows.
        <div className='relative'>
          <div className='overflow-x-auto'>{svg}</div>
          <div
            aria-hidden
            className='pointer-events-none absolute inset-y-0 right-0 w-10 bg-gradient-to-l from-black/80 to-transparent sm:hidden'
          />
        </div>
      ) : (
        svg
      )}
      {caption ? (
        <figcaption className='mt-3 text-center text-caption-regular text-grayscale-400'>{caption}</figcaption>
      ) : null}
    </figure>
  );
}

/** A labelled box. `lines` are rendered one per row, centred. */
export function Box({
  x,
  y,
  w,
  h,
  tone = 'neutral',
  lines,
  sub,
  dashed = false,
}: {
  x: number;
  y: number;
  w: number;
  h: number;
  tone?: Tone;
  lines: string[];
  sub?: string;
  dashed?: boolean;
}) {
  const { stroke, fill } = toneColor[tone];
  const cx = x + w / 2;
  const total = lines.length + (sub ? 1 : 0);
  const lineH = 17;
  const startY = y + h / 2 - ((total - 1) * lineH) / 2 + 5;
  return (
    <g>
      <rect
        x={x}
        y={y}
        width={w}
        height={h}
        rx={12}
        fill={fill}
        stroke={stroke}
        strokeWidth={1.5}
        strokeDasharray={dashed ? '5 4' : undefined}
      />
      {lines.map((line, i) => (
        <text
          key={line}
          x={cx}
          y={startY + i * lineH}
          textAnchor='middle'
          fill={C.text}
          fontSize={13.5}
          fontWeight={600}
        >
          {line}
        </text>
      ))}
      {sub ? (
        <text
          x={cx}
          y={startY + lines.length * lineH}
          textAnchor='middle'
          fill={C.textDim}
          fontSize={11.5}
        >
          {sub}
        </text>
      ) : null}
    </g>
  );
}

/** A straight arrow with an optional label sitting alongside it. */
export function Arrow({
  x1,
  y1,
  x2,
  y2,
  tone = 'neutral',
  label,
  labelAt = 'middle',
  dashed = false,
  labelDy = -8,
}: {
  x1: number;
  y1: number;
  x2: number;
  y2: number;
  tone?: Tone;
  label?: string;
  labelAt?: 'middle' | 'start' | 'end';
  dashed?: boolean;
  labelDy?: number;
}) {
  const { stroke } = toneColor[tone];
  const t = labelAt === 'middle' ? 0.5 : labelAt === 'start' ? 0.2 : 0.8;
  const lx = x1 + (x2 - x1) * t;
  const ly = y1 + (y2 - y1) * t;
  return (
    <g>
      <line
        x1={x1}
        y1={y1}
        x2={x2}
        y2={y2}
        stroke={stroke}
        strokeWidth={1.5}
        strokeDasharray={dashed ? '5 4' : undefined}
        markerEnd={`url(#arrow-${tone})`}
      />
      {label ? <Label x={lx} y={ly + labelDy}>{label}</Label> : null}
    </g>
  );
}

/**
 * Small text with an opaque plate behind it.
 *
 * An arrow label often has to sit where a `Group` boundary or another box edge
 * already runs, and SVG has no z-index beyond document order, so the label was
 * being struck through by dashed borders drawn earlier in the tree. Painting the
 * page background behind the glyphs is the fix that survives a diagram being
 * rearranged later, rather than nudging each colliding label by hand until it
 * happens to miss.
 *
 * The plate is sized from the string length because SVG cannot measure text
 * without a layout pass. 5.9px per character at 11.5px Inter is a slight
 * over-estimate, which is the safe direction: a plate marginally wider than the
 * glyphs hides the line cleanly, while a narrow one leaves the ends showing.
 */
export function Label({
  x,
  y,
  children,
  anchor = 'middle',
  fill = C.textDim,
}: {
  x: number;
  y: number;
  children: string;
  anchor?: 'start' | 'middle' | 'end';
  fill?: string;
}) {
  const width = children.length * 5.9 + 10;
  const plateX = anchor === 'middle' ? x - width / 2 : anchor === 'start' ? x - 5 : x - width + 5;
  return (
    <g>
      <rect x={plateX} y={y - 11} width={width} height={15} rx={3} fill={C.panel} />
      <text x={x} y={y} textAnchor={anchor} fill={fill} fontSize={11.5}>
        {children}
      </text>
    </g>
  );
}

/** A free-standing caption inside the drawing. */
export function Note({
  x,
  y,
  children,
  anchor = 'middle',
  tone,
}: {
  x: number;
  y: number;
  children: string;
  anchor?: 'start' | 'middle' | 'end';
  tone?: Tone;
}) {
  return (
    <text
      x={x}
      y={y}
      textAnchor={anchor}
      fill={tone ? toneColor[tone].stroke : C.textDim}
      fontSize={11.5}
      fontWeight={tone ? 600 : 400}
    >
      {children}
    </text>
  );
}

/** A section heading inside the drawing. */
export function Heading({ x, y, children, anchor = 'start' }: { x: number; y: number; children: string; anchor?: 'start' | 'middle' | 'end' }) {
  return (
    <text
      x={x}
      y={y}
      textAnchor={anchor}
      fill={C.textDim}
      fontSize={10.5}
      fontWeight={700}
      letterSpacing={1.4}
    >
      {children.toUpperCase()}
    </text>
  );
}

/** A dashed grouping frame with a label in its top-left. */
export function Group({
  x,
  y,
  w,
  h,
  label,
  tone = 'neutral',
}: {
  x: number;
  y: number;
  w: number;
  h: number;
  label: string;
  tone?: Tone;
}) {
  const { stroke } = toneColor[tone];
  return (
    <g>
      <rect
        x={x}
        y={y}
        width={w}
        height={h}
        rx={14}
        fill='none'
        stroke={stroke}
        strokeOpacity={0.45}
        strokeWidth={1}
        strokeDasharray='6 5'
      />
      <text x={x + 14} y={y + 18} fill={stroke} fontSize={10.5} fontWeight={700} letterSpacing={1.4}>
        {label.toUpperCase()}
      </text>
    </g>
  );
}
