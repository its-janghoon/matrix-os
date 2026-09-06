import { Arrow, Box, Diagram, Group, Note } from './primitives';

/**
 * Where MATRIX lives and how it crosses to Ethereum. Two questions the prose
 * never answered plainly: native MATRIX is the settlement coin and wMATRIX is
 * only a mirror, and the mirror's supply is created solely by locking native.
 */
export function TokenBridge({ className }: { className?: string }) {
  return (
    <Diagram
      title='Native MATRIX and the wrapped mirror'
      description='Native MATRIX is the coin of the Matrix OS Layer 1 with nine decimals, capped at one billion, and is the only thing marketplace jobs settle in. To move value to Ethereum, native MATRIX is locked into an on-chain escrow account and the validator set signs an attestation; the WrappedMatrix contract mints wMATRIX only against a threshold of those signatures, once per lock. Burning wMATRIX names a native recipient and releases the escrowed native MATRIX. Wrapped supply therefore always equals locked native.'
      viewBox='0 0 760 340'
      minWidth={640}
      className={className}
      caption='wMATRIX is a mirror, not a second money supply: every wrapped token is a native token locked in escrow.'
    >
      <Group x={16} y={44} w={330} h={256} label='Matrix OS L1' tone='secondary' />
      <Group x={414} y={44} w={330} h={256} label='Ethereum' tone='accent' />

      <Box x={44} y={84} w={274} h={62} tone='secondary' lines={['Native MATRIX']} sub='9 decimals, cap 1,000,000,000' />
      <Box x={44} y={210} w={274} h={62} tone='neutral' lines={['Bridge escrow']} sub='locked native backs the mirror' />
      <Arrow x1={181} y1={146} x2={181} y2={206} tone='secondary' label='lock' labelDy={-6} />

      <Box x={442} y={84} w={274} h={62} tone='accent' lines={['wMATRIX (ERC-20)']} sub='18 decimals, 1 native = 1e9' />
      <Box x={442} y={210} w={274} h={62} tone='neutral' lines={['Validator attestations']} sub='threshold of secp256k1 sigs' />

      <Arrow x1={318} y1={241} x2={438} y2={241} tone='neutral' label='attest lock' labelDy={-8} />
      <Arrow x1={579} y1={206} x2={579} y2={150} tone='accent' label='mint, once per lock' labelDy={-6} />

      <Arrow x1={442} y1={115} x2={322} y2={115} tone='accent' label='burn -> unlock native' labelDy={-8} />

      <Note x={380} y={320}>
        settlement happens only in native MATRIX; the mirror exists to be listed
      </Note>
    </Diagram>
  );
}
