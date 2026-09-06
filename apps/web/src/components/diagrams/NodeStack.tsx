import { Box, Diagram, Group, Note } from './primitives';

/**
 * What one matrixd process contains, and which parts talk to the network. The
 * architecture page listed these as five bulleted layers, which said nothing
 * about what sits on what.
 */
export function NodeStack({ className }: { className?: string }) {
  return (
    <Diagram
      title='What runs inside a node'
      description='One matrixd process holds a libp2p host and gossip transport, the marketplace and its order book, the consensus engine, the LLM inference runner, and a Pebble key-value store holding the ledger and the committed block chain. It is reachable through the matrix CLI, the gRPC services for market, inference and admin, and an HTTP endpoint that serves the same market and inference services as JSON so a browser can call them.'
      viewBox='0 0 760 380'
      minWidth={640}
      className={className}
      caption='One process, one store. The consensus engine and the marketplace write to the same ledger.'
    >
      <Group x={16} y={30} w={728} h={200} label='matrixd' tone='primary' />

      <Box x={40} y={68} w={216} h={64} tone='secondary' lines={['libp2p host']} sub='peers, gossip topics' />
      <Box x={272} y={68} w={216} h={64} tone='primary' lines={['Marketplace']} sub='providers, jobs, pricing' />
      <Box x={504} y={68} w={216} h={64} tone='accent' lines={['Consensus engine']} sub='blocks, votes, commits' />

      <Box x={40} y={150} w={216} h={64} tone='neutral' lines={['LLM inference']} sub='local runner or API proxy' />
      <Box x={272} y={150} w={448} h={64} tone='neutral' lines={['Pebble store']} sub='ledger, token chain, committed blocks' />

      <Group x={16} y={252} w={728} h={104} label='How you reach it' tone='neutral' />
      <Box x={40} y={278} w={158} h={60} tone='neutral' lines={['matrix CLI']} sub='same host' />
      <Box x={214} y={278} w={158} h={60} tone='neutral' lines={['gRPC']} sub=':9090-:9092' />
      <Box x={388} y={278} w={158} h={60} tone='secondary' lines={['HTTP / Connect']} sub=':9093, browser-ready' />
      <Box x={562} y={278} w={158} h={60} tone='neutral' lines={['Matrix Console']} sub='desktop app' />

      <Note x={380} y={372}>
        every balance-changing path ends in the same committed ledger
      </Note>
    </Diagram>
  );
}
