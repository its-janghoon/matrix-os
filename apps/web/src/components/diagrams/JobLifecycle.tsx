import { Arrow, Box, Diagram, Note } from './primitives';

/**
 * A job's states and what moves at each one. The distinction that matters -
 * capacity is reserved when the job is submitted, but no credits move until it
 * completes - was buried in a paragraph before.
 */
export function JobLifecycle({ className }: { className?: string }) {
  const x = 34;
  const w = 300;
  const h = 56;
  const step = 84;
  const returnX = 424;
  const rows = [
    { lines: ['Provider registers'], sub: 'capacity + price per unit', tone: 'secondary' as const },
    { lines: ['Job submitted'], sub: 'capacity reserved, nothing paid', tone: 'primary' as const },
    { lines: ['Job runs'], sub: 'provider does the work', tone: 'primary' as const },
    { lines: ['Job completed'], sub: 'MATRIX moves, buyer to provider', tone: 'accent' as const },
  ];
  const cancelY = 30 + step + h / 2;

  return (
    <Diagram
      title='Job lifecycle'
      description='A provider registers capacity and a price. A submitted job reserves capacity but moves no credits. The job runs, and only on completion do credits move from buyer to provider. A cancelled job returns the reserved capacity and moves nothing.'
      viewBox='0 0 470 420'
      className={className}
      caption='Capacity is reserved on submit; money moves only on completion.'
    >
      {rows.map((row, i) => (
        <g key={row.lines[0]}>
          <Box x={x} y={30 + i * step} w={w} h={h} tone={row.tone} lines={row.lines} sub={row.sub} />
          {i < rows.length - 1 ? (
            <Arrow x1={x + w / 2} y1={30 + i * step + h} x2={x + w / 2} y2={30 + (i + 1) * step} tone={row.tone} />
          ) : null}
        </g>
      ))}

      {/* The cancel branch, drawn in its own channel on the right: capacity goes
          back to the provider and nothing is transferred. */}
      <Arrow x1={x + w} y1={cancelY} x2={returnX} y2={cancelY} tone='neutral' />
      <Arrow x1={returnX} y1={cancelY - 6} x2={returnX} y2={30 + h / 2} tone='neutral' />
      <Arrow x1={returnX} y1={30 + h / 2} x2={x + w + 4} y2={30 + h / 2} tone='neutral' />
      <Note x={379} y={cancelY - 12}>
        cancel
      </Note>
      <Note x={x + w / 2} y={392}>
        a cancelled job returns the capacity and transfers nothing
      </Note>
    </Diagram>
  );
}
