import { Arrow, Box, Diagram, Group, Note } from './primitives';

/**
 * How the validator set changes while the network is running.
 *
 * This replaced a paragraph that had to carry four separate ideas at once - a
 * local allow-list, a quorum, a transaction in a block, and a delayed
 * activation - which is one idea too many for prose. The two lanes are the
 * point of the picture: approval is per-operator and local, activation is
 * shared and on-chain.
 */
export function ValidatorSetChange({ className }: { className?: string }) {
  return (
    <Diagram
      title='Changing the validator set'
      description='Each operator lists a change under consensus.approved_changes in their own config. A node offers the changes its operator approved, re-offering them on a backoff until they commit. A node prevotes nil on a block carrying a change its operator has not approved, so the change needs a quorum of operators to have approved it. Once committed, the change waits for the next epoch boundary - a height that is a multiple of consensus.epoch_length - and every node applies it at that same height. Proven equivocation is approved automatically, because the evidence proves itself.'
      viewBox='0 0 700 382'
      minWidth={620}
      className={className}
      caption='Approval is local and per-operator. Activation is shared: every node switches sets at the same height, which is what keeps the leader schedule identical everywhere.'
    >
      <Group x={16} y={12} w={318} h={186} label='local to each operator' tone='primary' />
      <Group x={366} y={12} w={318} h={186} label='shared, on-chain' tone='accent' />

      <Box
        x={40}
        y={44}
        w={270}
        h={58}
        tone='primary'
        lines={['approved_changes']}
        sub='add:<pubkey> / remove:<account id>'
      />
      <Box
        x={40}
        y={130}
        w={270}
        h={54}
        tone='primary'
        lines={['the node offers it']}
        sub='and re-offers until it commits'
      />
      <Arrow x1={175} y1={102} x2={175} y2={130} tone='primary' />

      <Box
        x={390}
        y={44}
        w={270}
        h={58}
        tone='secondary'
        lines={['a quorum votes for the block']}
        sub='a node not approving it prevotes nil'
      />
      <Box x={390} y={130} w={270} h={54} tone='accent' lines={['committed']} sub='waiting for an epoch boundary' />
      <Arrow x1={525} y1={102} x2={525} y2={130} tone='secondary' />
      {/* No label on this arrow: it has to cross both dashed lane borders, and
          any text at its midpoint lands on top of them. The lanes and the
          caption already say what crosses here. */}
      <Arrow x1={310} y1={157} x2={390} y2={157} tone='primary' />

      <Arrow x1={525} y1={184} x2={525} y2={228} tone='accent' />
      <Box
        x={220}
        y={228}
        w={440}
        h={58}
        tone='accent'
        lines={['height % epoch_length == 0']}
        sub='every node applies the change at this height'
      />

      <Box
        x={40}
        y={310}
        w={270}
        h={54}
        tone='secondary'
        lines={['proven equivocation']}
        sub='approved with no config entry'
      />
      <Arrow x1={175} y1={310} x2={175} y2={190} tone='secondary' dashed />
      <Note x={470} y={318}>
        the evidence carries the offender&apos;s own two signatures,
      </Note>
      <Note x={470} y={336}>
        so every node checks it instead of trusting a peer
      </Note>
    </Diagram>
  );
}
