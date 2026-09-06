import { Arrow, Box, Diagram, Note } from './primitives';

/**
 * Where a prompt goes and how the answer is paid for. The page described two
 * fulfilment modes in prose without making clear that both settle identically.
 */
export function InferencePath({ className }: { className?: string }) {
  const w = 300;
  const h = 54;
  const x = 66;
  return (
    <Diagram
      title='An inference request end to end'
      description='A buyer submits a prompt with a model and a token budget. The provider fulfils it either with a local runner or by proxying a provider API. The response returns to the buyer, and the charge is settled in native MATRIX through the same consensus path a compute job uses.'
      viewBox='0 0 470 430'
      className={className}
      caption='Local runner or proxied API: the settlement path is the same either way.'
    >
      <Box x={x} y={24} w={w} h={h} tone='primary' lines={['Prompt + budget']} sub='model, max tokens, price' />
      <Arrow x1={x + w / 2} y1={78} x2={x + w / 2} y2={110} tone='primary' />

      <Box x={20} y={110} w={190} h={h} tone='secondary' lines={['Local runner']} sub='weights on the machine' />
      <Box x={230} y={110} w={190} h={h} tone='secondary' lines={['Provider API proxy']} sub='upstream model' />

      <Arrow x1={115} y1={164} x2={195} y2={200} tone='secondary' />
      <Arrow x1={325} y1={164} x2={245} y2={200} tone='secondary' />

      <Box x={x} y={200} w={w} h={h} tone='neutral' lines={['Response + token count']} sub='what was actually used' />
      <Arrow x1={x + w / 2} y1={254} x2={x + w / 2} y2={286} tone='neutral' />

      <Box x={x} y={286} w={w} h={h} tone='accent' lines={['Settled through consensus']} sub='committed block, MATRIX moves' />

      <Note x={216} y={378} tone='accent'>
        the buyer is charged for the run, not for a subscription
      </Note>
      <Note x={216} y={400}>
        an unaffordable transfer commits but moves nothing
      </Note>
    </Diagram>
  );
}
