import { Arrow, Box, Diagram, Group, Note } from './primitives';

/**
 * The whole market on one picture: who supplies, who buys, and what makes the
 * payment final. It replaces the paragraph that used to describe the same four
 * steps in words on the home page and on /how-it-works.
 */
export function MarketFlow({ className }: { className?: string }) {
  return (
    <Diagram
      title='How the market works'
      description='Providers announce idle capacity over libp2p. Buyers discover it and submit a signed job. The job is settled by the consensus layer, which orders it into a committed block, and the provider is paid in native MATRIX from the one agreed ledger.'
      viewBox='0 0 760 348'
      minWidth={620}
      className={className}
      caption='One transaction path: announce, discover, sign, commit, pay.'
    >
      <Group x={16} y={40} w={220} h={250} label='Supply' tone='secondary' />
      <Group x={524} y={40} w={220} h={250} label='Demand' tone='primary' />

      <Box x={40} y={78} w={172} h={62} tone='secondary' lines={['Idle machine']} sub='your laptop, your rack' />
      <Box x={40} y={186} w={172} h={62} tone='secondary' lines={['matrixd node']} sub='announces capacity + price' />
      <Arrow x1={126} y1={140} x2={126} y2={182} tone='secondary' />

      <Box x={548} y={78} w={172} h={62} tone='primary' lines={['Buyer']} sub='app, agent or CLI' />
      <Box x={548} y={186} w={172} h={62} tone='primary' lines={['Signed job']} sub='units + price, ed25519' />
      <Arrow x1={634} y1={140} x2={634} y2={182} tone='primary' />

      <Box x={286} y={78} w={188} h={62} tone='neutral' lines={['libp2p gossip']} sub='discovery, no broker' />
      <Box x={286} y={186} w={188} h={62} tone='accent' lines={['Consensus L1']} sub='leader-based BFT' />

      <Arrow x1={212} y1={109} x2={282} y2={109} tone='secondary' />
      <Arrow x1={548} y1={109} x2={478} y2={109} tone='primary' />

      <Arrow x1={548} y1={217} x2={478} y2={217} tone='primary' />
      {/* The payment arrow carries no label of its own: the gap it crosses is 70
          units wide and "MATRIX paid" is wider than that, so the text ran into
          the provider box and the group frame beside it. It sits under the
          middle column instead, where there is room. */}
      <Arrow x1={282} y1={217} x2={212} y2={217} tone='accent' />
      <Note x={380} y={268}>
        MATRIX paid to the provider
      </Note>

      <Note x={380} y={322} tone='accent'>
        one committed block = one agreed balance
      </Note>
    </Diagram>
  );
}
