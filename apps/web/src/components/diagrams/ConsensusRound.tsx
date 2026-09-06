import { Arrow, Box, Diagram, Note } from './primitives';

/**
 * One height of the consensus protocol, as it actually behaves: two voting
 * phases, and what each quorum means. This is the page's central claim - that a
 * balance is one agreed fact - so it is worth a picture rather than three
 * paragraphs.
 */
export function ConsensusRound({ className }: { className?: string }) {
  const x = 108;
  const w = 300;
  const h = 54;
  const step = 82;
  const steps = [
    { lines: ['Leader proposes'], sub: 'a block of signed transfers', tone: 'primary' as const },
    { lines: ['Prevote'], sub: 'each validator: this is what I see', tone: 'primary' as const },
    { lines: ['Polka'], sub: 'a quorum prevoted one block', tone: 'secondary' as const },
    { lines: ['Precommit'], sub: 'and the validator locks on it', tone: 'secondary' as const },
    { lines: ['Commit'], sub: 'a quorum precommitted: final', tone: 'accent' as const },
  ];
  return (
    <Diagram
      title='One consensus height'
      description='The round leader proposes a block. Each validator prevotes what it sees. A quorum of prevotes for one block is a polka; on seeing one a validator precommits that block and locks on it. A quorum of precommits commits the block. If the round times out, the leader rotates and the next leader must re-propose the block that last reached a polka, carrying the polka as proof, so validators that locked can safely vote for it.'
      viewBox='0 0 470 476'
      className={className}
      caption='A quorum is more than two thirds of the validator set. Locking on a precommit, not on a single vote, is what makes rotation safe.'
    >
      {steps.map((s, i) => (
        <g key={s.lines[0]}>
          <Box x={x} y={26 + i * step} w={w} h={h} tone={s.tone} lines={s.lines} sub={s.sub} />
          {i < steps.length - 1 ? (
            <Arrow x1={x + w / 2} y1={26 + i * step + h} x2={x + w / 2} y2={26 + (i + 1) * step} tone={s.tone} />
          ) : null}
        </g>
      ))}

      {/* Rotation: the timeout path back to a new leader, carrying the polka.
          The label runs vertically along that path - there is no horizontal room
          for it beside the boxes, and set horizontally it landed on top of one. */}
      <Arrow x1={x} y1={26 + 3 * step + h / 2} x2={54} y2={26 + 3 * step + h / 2} tone='neutral' />
      <Arrow x1={54} y1={26 + 3 * step + h / 2} x2={54} y2={26 + h / 2} tone='neutral' />
      <Arrow x1={54} y1={26 + h / 2} x2={x - 4} y2={26 + h / 2} tone='neutral' />
      <text
        x={40}
        y={26 + 2 * step}
        transform={`rotate(-90 40 ${26 + 2 * step})`}
        textAnchor='middle'
        fill='#8B96AC'
        fontSize={11.5}
      >
        on timeout: rotate leader, re-propose with the polka
      </text>

      <Note x={x + w / 2} y={26 + 5 * step - 2} tone='accent'>
        committed = every node has the same balance
      </Note>
    </Diagram>
  );
}
