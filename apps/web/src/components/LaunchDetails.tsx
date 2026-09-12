import { Card, CheckItem, Eyebrow, IconBadge, Section } from '@/components/marketing';
import { AnimatedTerminal, type TerminalLine } from '@/components/AnimatedTerminal';
import { Button } from '@/components/Button';
import { EscrowBacking } from '@/components/diagrams';
import {
  ATTESTOR_COUNT,
  ATTESTOR_THRESHOLD,
  FOUNDER_ALLOCATION_WHOLE,
  MINT_CAP_WHOLE,
  NATIVE_MAX_SUPPLY_WHOLE,
  addressUrl,
  launchFacts,
  shortHex,
} from '@/lib/launch';
import { MinBridgeLockAmount, NativeUnit } from '@matrix-os/protocol';
import { FiArrowRight, FiExternalLink, FiLock, FiShield } from 'react-icons/fi';

const MIN_LOCK_WHOLE = MinBridgeLockAmount / NativeUnit;
const CAP_PERCENT = Math.round((MINT_CAP_WHOLE / NATIVE_MAX_SUPPLY_WHOLE) * 100);
const HEADROOM_WHOLE = MINT_CAP_WHOLE - FOUNDER_ALLOCATION_WHOLE;

/**
 * The reconciliation transcript.
 *
 * These are the CLI's own field labels and the figures the production network
 * actually reports, so a reader who runs the command sees this shape. Inventing
 * a prettier output here would make the block a mock-up of a feature rather than
 * a record of one.
 */
const RECONCILE_TRANSCRIPT: TerminalLine[] = [
  {
    command: 'matrix bridge reconcile',
    output: [
      'Locked (native, cumulative):    50000000000000000',
      'Unlocked (native, cumulative):  0',
      'Outstanding (native):           50000000000000000',
      'Escrow balance (native):        50000000000000000',
      'Outstanding (erc20, 18dp):      50000000000000000000000000',
    ],
  },
  {
    output: [
      "The contract's totalSupply() must equal the erc20 figure above.",
      'Escrow may lead it by a lock whose mint has not been broadcast; it',
      'must never lag it.',
    ],
    tone: 'dim',
  },
  {
    command: 'cast call $WMATRIX "totalSupply()(uint256)"',
    output: ['50000000000000000000000000'],
    tone: 'good',
  },
];

/**
 * The launch detail section for the landing page.
 *
 * This exists because the banner has room for a claim but not for proof, and an
 * unproven claim about backed supply is the kind a reader should not accept. Each
 * figure here is either an immutable constructor value or something a visitor can
 * read back from the chain themselves, and the section says which is which.
 */
export function LaunchDetails() {
  const facts = launchFacts;
  if (!facts) return null;

  return (
    <Section className='bg-section-glow'>
      <div className='mx-auto max-w-3xl text-center'>
        <Eyebrow tone='secondary'>Base production</Eyebrow>
        <h2 className='mt-4 text-3xl font-bold tracking-tight sm:text-4xl'>
          Every wrapped token has native collateral behind it
        </h2>
        <p className='mt-4 text-lg text-grayscale-300'>
          wMATRIX is a mirror, not a second currency. It is minted only against native MATRIX moved
          into consensus-held escrow, and the numbers below are readable on Base and on the Matrix L1
          without trusting this page.
        </p>
      </div>

      <div className='mt-14 grid grid-cols-2 gap-4 lg:grid-cols-4'>
        {[
          {
            k: 'Native escrow',
            v: `${FOUNDER_ALLOCATION_WHOLE.toLocaleString('en-US')} MATRIX`,
            note: 'Locked on the L1',
          },
          {
            k: 'Wrapped supply',
            v: `${FOUNDER_ALLOCATION_WHOLE.toLocaleString('en-US')} wMATRIX`,
            note: 'Matches escrow exactly',
          },
          {
            k: 'Immutable mint cap',
            v: `${MINT_CAP_WHOLE.toLocaleString('en-US')}`,
            note: `${CAP_PERCENT}% of native maximum`,
          },
          {
            k: 'Remaining headroom',
            v: `${HEADROOM_WHOLE.toLocaleString('en-US')}`,
            note: 'Still needs collateral',
          },
        ].map((s) => (
          <div
            key={s.k}
            className='rounded-2xl border border-white/10 bg-white/[0.03] p-6 backdrop-blur-sm'
          >
            <div className='text-[11px] uppercase tracking-[0.12em] text-grayscale-500'>{s.k}</div>
            <div className='mt-2 text-lg font-semibold text-white'>{s.v}</div>
            <div className='mt-1 text-xs text-grayscale-500'>{s.note}</div>
          </div>
        ))}
      </div>

      <div className='mt-6 grid grid-cols-1 gap-6 md:grid-cols-2'>
        <Card>
          <div className='flex items-center gap-3'>
            <IconBadge icon={FiShield} tone='secondary' />
            <h3 className='text-xl font-semibold'>What secures a mint</h3>
          </div>
          <p className='mt-4 text-grayscale-300'>
            The attestor committee and its threshold are constructor values on the deployed contract.
            They cannot be edited, and they are a separate security domain from the native validator
            set, so validators joining or leaving the chain never changes who can authorize a mint.
          </p>
          <ul className='mt-6 space-y-3'>
            <CheckItem>
              {ATTESTOR_THRESHOLD} of {ATTESTOR_COUNT} independent attestor signatures per mint,
              fixed at deployment
            </CheckItem>
            <CheckItem>
              Every signature is recovered against the contract digest before the browser simulates
            </CheckItem>
            <CheckItem>
              Burns release native MATRIX only after validators observe the confirmed Base event and
              order it through consensus
            </CheckItem>
            <CheckItem>
              Changing the committee would require a new deployment and a published migration, not a
              settings change
            </CheckItem>
          </ul>
        </Card>

        {/* Neither card uses `flex-1` on its paragraph. The two bullet lists wrap
            to different heights, and a growing paragraph would push the shorter
            list down to bottom-align with the taller one, opening an obvious gap
            under the prose. */}
        <Card>
          <div className='flex items-center gap-3'>
            <IconBadge icon={FiLock} tone='accent' />
            <h3 className='text-xl font-semibold'>What the founder allocation cannot do</h3>
          </div>
          <p className='mt-4 text-grayscale-300'>
            The founder allocation is fully collateralized and sits in an immutable vesting vault. It
            is linear over five years from the recorded start with a one-year cliff, so nothing is
            releasable before that cliff and no single unlock can arrive early.
          </p>
          <ul className='mt-6 space-y-3'>
            <CheckItem>Held by the vault contract, not an operator wallet</CheckItem>
            <CheckItem>One-year cliff, then linear release across a five-year total</CheckItem>
            <CheckItem>Twenty percent vested at the cliff, by the schedule itself</CheckItem>
            <CheckItem>Backed 1:1 by native escrow, at an exact 1e9 decimal conversion</CheckItem>
          </ul>
        </Card>
      </div>

      <Card className='mt-6'>
        <div className='grid gap-8 lg:grid-cols-2 lg:items-center'>
          <div className='min-w-0'>
            <h3 className='text-xl font-semibold'>Anyone can check the backing</h3>
            <p className='mt-3 text-grayscale-300'>
              The invariant is not a dashboard reading. A node refuses to return a snapshot at all
              when its own accounting and its real escrow balance disagree, so the command below
              either prints matching figures or fails. Compare the erc20 figure with{' '}
              <code className='rounded bg-white/[0.06] px-1.5 py-0.5 font-mono text-[12px] text-grayscale-200'>
                totalSupply()
              </code>{' '}
              on the contract and the two must be equal.
            </p>
            <ul className='mt-6 space-y-3'>
              <CheckItem>Escrow may lead supply while a committed lock awaits its mint</CheckItem>
              <CheckItem>Escrow must never lag supply; that would be uncollateralized wMATRIX</CheckItem>
              <CheckItem>A lock pays no protocol fee, so escrow receives the full amount</CheckItem>
            </ul>
          </div>
          <AnimatedTerminal label='matrix bridge reconcile' lines={RECONCILE_TRANSCRIPT} />
        </div>
      </Card>

      <Card className='mt-6'>
        <h3 className='text-xl font-semibold'>Where the supply actually sits</h3>
        <p className='mt-3 max-w-3xl text-grayscale-300'>
          The mint ceiling is a limit rather than a balance. The unminted remainder below is capacity
          that would still have to be collateralized by locking native MATRIX first, so it is not
          tokens waiting to be released.
        </p>
        <EscrowBacking />
      </Card>

      <Card className='mt-6'>
        <div className='flex flex-col gap-6 lg:flex-row lg:items-center lg:justify-between'>
          <div className='min-w-0'>
            <h3 className='text-xl font-semibold'>Check it yourself</h3>
            <p className='mt-3 max-w-2xl text-grayscale-300'>
              The launch record lists the source revision, verified contract addresses, raw
              constructor arguments, the founder ceremony, and the reconciliation snapshot at the
              stated heights. Reading it is the only reason to believe any of the above.
            </p>
            <div className='mt-4 flex flex-col gap-1 text-xs text-grayscale-400 sm:flex-row sm:gap-6'>
              <span>
                wMATRIX{' '}
                <a
                  className='font-mono underline decoration-white/30 hover:text-white'
                  href={`${addressUrl(facts.explorerUrl, facts.wrappedMatrixAddress)}#code`}
                  target='_blank'
                  rel='noopener noreferrer'
                >
                  {shortHex(facts.wrappedMatrixAddress)}
                </a>
              </span>
              <span>
                Vault{' '}
                <a
                  className='font-mono underline decoration-white/30 hover:text-white'
                  href={`${addressUrl(facts.explorerUrl, facts.vaultAddress)}#code`}
                  target='_blank'
                  rel='noopener noreferrer'
                >
                  {shortHex(facts.vaultAddress)}
                </a>
              </span>
              <span>Source verified on {facts.config.chain.name}</span>
            </div>
          </div>
          <div className='flex shrink-0 flex-col gap-3 sm:flex-row'>
            <Button href='/bridge' variant='primary' size='md'>
              Bridge {MIN_LOCK_WHOLE.toLocaleString('en-US')}+ MATRIX
              <FiArrowRight className='ml-2 h-4 w-4' />
            </Button>
            <a
              href={facts.evidenceUrl}
              target='_blank'
              rel='noopener noreferrer'
              className='group inline-flex items-center justify-center rounded-full border border-white/10 bg-white/[0.06] px-6 py-3 text-[15px] font-semibold tracking-tight text-white backdrop-blur-sm transition-colors hover:border-white/20 hover:bg-white/[0.12]'
            >
              Launch evidence
              <FiExternalLink className='ml-2 h-4 w-4' />
            </a>
          </div>
        </div>
      </Card>

      <p className='mt-6 text-center text-xs text-grayscale-500'>
        wMATRIX has no USD, fiat, or stablecoin peg. Any exchange or DEX price is market-discovered
        and can move freely. You pay your own Base gas for every EVM transaction; there is no relayer
        or gas sponsorship.
      </p>
    </Section>
  );
}

export default LaunchDetails;
