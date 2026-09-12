import { Arrow, Box, C, Diagram, Group, Heading, Label } from './primitives';

/**
 * Why a wrapped token is not new supply.
 *
 * The prose claim "backed 1:1" is the one a reader has least reason to take on
 * faith, and a sentence cannot show it. This draws the conservation instead: the
 * native maximum is one bar, the mint ceiling is a slice of that bar, minted
 * supply is a slice of the ceiling, and every minted unit is drawn as a unit
 * removed from circulation on the left. The empty part of the ceiling is drawn
 * explicitly, because "there is headroom" and "that headroom is already money"
 * are the two readings and only the first is true.
 */

const BAR_X = 60;
const BAR_W = 640;
const NATIVE_MAX = 1_000_000_000;
const MINT_CAP = 60_000_000;
const MINTED = 50_000_000;

const capW = (MINT_CAP / NATIVE_MAX) * BAR_W;
const mintedW = (MINTED / NATIVE_MAX) * BAR_W;

export function EscrowBacking({ className }: { className?: string }) {
  return (
    <Diagram
      title='Wrapped supply is collateral, not new money'
      description='Native MATRIX is capped at one billion whole coins. The WrappedMatrix contract carries an immutable mint ceiling of sixty million, which is six percent of that cap. Fifty million wMATRIX has been minted, and each of those units exists only because an equal amount of native MATRIX sits in bridge escrow and cannot be spent until the wrapped unit is burned. The remaining ten million of ceiling is unminted capacity, not tokens: minting into it would require locking the same amount of native MATRIX first. Burning wMATRIX destroys the wrapped unit and releases its native backing through consensus, so the two sides move together in both directions.'
      viewBox='0 0 760 396'
      minWidth={660}
      className={className}
      caption='The mint ceiling is a limit, not a balance: the unminted remainder is capacity that still has to be collateralized.'
    >
      <Heading x={BAR_X} y={30}>
        Native MATRIX maximum supply
      </Heading>

      {/* The full native cap. */}
      <rect
        x={BAR_X}
        y={44}
        width={BAR_W}
        height={34}
        rx={8}
        fill='rgba(20,27,44,0.70)'
        stroke={C.panelEdge}
        strokeWidth={1.5}
      />
      <text x={BAR_X + BAR_W - 12} y={65} textAnchor='end' fill={C.textDim} fontSize={11.5}>
        1,000,000,000 MATRIX
      </text>

      {/* The immutable ceiling, as a slice of that cap. */}
      <rect
        x={BAR_X}
        y={44}
        width={capW}
        height={34}
        rx={8}
        fill='rgba(155,108,249,0.12)'
        stroke={C.accent}
        strokeWidth={1.5}
      />

      {/* Leader lines from the ceiling slice down to its own detail bar. */}
      <line x1={BAR_X} y1={78} x2={BAR_X} y2={116} stroke={C.accent} strokeWidth={1} strokeDasharray='4 4' strokeOpacity={0.6} />
      <line x1={BAR_X + capW} y1={78} x2={BAR_X + BAR_W} y2={116} stroke={C.accent} strokeWidth={1} strokeDasharray='4 4' strokeOpacity={0.6} />

      <Heading x={BAR_X} y={106}>
        Immutable mint ceiling: 60,000,000 (6%)
      </Heading>

      {/* The ceiling expanded to full width: minted versus unminted. */}
      <rect
        x={BAR_X}
        y={116}
        width={BAR_W}
        height={40}
        rx={8}
        fill='none'
        stroke={C.accent}
        strokeWidth={1.5}
        strokeDasharray='6 5'
        strokeOpacity={0.7}
      />
      <rect
        x={BAR_X}
        y={116}
        width={(mintedW / capW) * BAR_W}
        height={40}
        rx={8}
        fill='rgba(155,108,249,0.20)'
        stroke={C.accent}
        strokeWidth={1.5}
      />
      <text x={BAR_X + 14} y={141} fill={C.text} fontSize={12.5} fontWeight={600}>
        50,000,000 wMATRIX minted
      </text>
      {/*
        Below the bar rather than inside it. The unminted remainder is only
        106px wide at this viewBox and the label needs about 112, so placing it
        in that gap put the minted fill's solid right border through the text.
        A background plate would have hidden the strikethrough by punching a
        hole in the bar's own border, which is a worse drawing. The tick line
        ties the label to the region it names.
      */}
      <line
        x1={BAR_X + mintedW * (BAR_W / capW)}
        y1={156}
        x2={BAR_X + mintedW * (BAR_W / capW)}
        y2={170}
        stroke={C.accent}
        strokeWidth={1}
        strokeOpacity={0.6}
      />
      <text x={BAR_X + BAR_W} y={180} textAnchor='end' fill={C.textDim} fontSize={11.5}>
        10,000,000 unminted: capacity, not tokens
      </text>

      {/* The two sides that must stay equal. */}
      <Group x={16} y={196} w={352} h={150} label='Matrix OS L1' tone='secondary' />
      <Group x={392} y={196} w={352} h={150} label='Base' tone='accent' />

      <Box
        x={44}
        y={236}
        w={296}
        h={62}
        tone='secondary'
        lines={['Bridge escrow: 50,000,000']}
        sub='native, cannot move until burned'
      />
      <Box
        x={420}
        y={236}
        w={296}
        h={62}
        tone='accent'
        lines={['totalSupply(): 50,000,000']}
        sub='wMATRIX, 1 native = 1e9 units'
      />

      {/* Equality drawn as a two-headed relation rather than a flow: neither
          side causes the other, they are the same quantity counted twice. */}
      <line x1={344} y1={267} x2={416} y2={267} stroke={C.success} strokeWidth={2} />
      <rect x={368} y={252} width={24} height={20} rx={4} fill={C.panel} />
      <text x={380} y={267} textAnchor='middle' dominantBaseline='middle' fill={C.success} fontSize={15} fontWeight={700}>
        =
      </text>

      <Arrow x1={192} y1={236} x2={192} y2={212} tone='secondary' label='lock' labelDy={-4} />
      <Arrow x1={568} y1={212} x2={568} y2={234} tone='accent' label='mint, once per lock' labelDy={-6} />
      <Arrow x1={420} y1={318} x2={344} y2={318} tone='accent' label='burn releases the backing' labelDy={16} />

      <Label x={380} y={382} fill={C.primary}>
        no peg, no oracle, no reserve claim: the collateral is the same coin
      </Label>
    </Diagram>
  );
}

export default EscrowBacking;
