import { Eyebrow } from '@/components/marketing';
import {
  ATTESTOR_THRESHOLD,
  FOUNDER_ALLOCATION_WHOLE,
  addressUrl,
  launchFacts,
  shortHex,
  txUrl,
} from '@/lib/launch';
import { MinBridgeLockAmount, NativeUnit } from '@matrix-os/protocol';
import Link from 'next/link';
import { FiArrowRight, FiExternalLink } from 'react-icons/fi';

const MIN_LOCK_WHOLE = MinBridgeLockAmount / NativeUnit;

/**
 * The launch announcement band.
 *
 * It renders nothing unless this build is the published production deployment,
 * so a Sepolia or misconfigured preview cannot announce a mainnet launch. The
 * copy deliberately carries the two constraints a visitor hits first - the
 * minimum lock and paying their own Base gas - rather than saving them for an
 * error message after a wallet is already open. It makes no price, peg, or
 * listing claim, because none of those are facts.
 */
export function LaunchBanner({ className = '' }: { className?: string }) {
  const facts = launchFacts;
  if (!facts) return null;

  return (
    <div
      className={`relative overflow-hidden rounded-3xl border border-white/10 p-[1px] ${className}`}
    >
      <div className='absolute inset-0 bg-decorative-1 bg-gradient-200 animate-gradient-pan opacity-70' />
      <div className='relative rounded-3xl bg-black/85 px-6 py-6 backdrop-blur-sm sm:px-8'>
        <div className='flex flex-col gap-5 lg:flex-row lg:items-center lg:justify-between lg:gap-10'>
          <div className='min-w-0 lg:flex-1'>
            <div className='flex flex-wrap items-center gap-2'>
              <Eyebrow tone='secondary'>
                <span className='relative flex h-1.5 w-1.5'>
                  <span className='absolute inline-flex h-full w-full animate-ping rounded-full bg-secondary-300 opacity-75' />
                  <span className='relative inline-flex h-1.5 w-1.5 rounded-full bg-secondary-300' />
                </span>
                Live on Base
              </Eyebrow>
              <span className='text-xs text-grayscale-500'>
                {facts.config.chain.name} · chain {facts.config.chain.id}
              </span>
            </div>

            <h2 className='mt-3 text-2xl font-bold tracking-tight sm:text-3xl'>
              The wMATRIX bridge is live and reconciled
            </h2>
            <p className='mt-3 max-w-2xl text-grayscale-300'>
              {FOUNDER_ALLOCATION_WHOLE.toLocaleString('en-US')} wMATRIX is held in the immutable
              founder vault against exactly the same amount of native MATRIX in escrow. Locks start
              at {MIN_LOCK_WHOLE.toLocaleString('en-US')} MATRIX, need{' '}
              {ATTESTOR_THRESHOLD} of {ATTESTOR_THRESHOLD} attestor signatures, and you submit every
              Base transaction and pay its gas yourself. There is no relayer and no fiat peg.
            </p>

            {/* Four columns at desktop rather than two: as a 2-column grid the
                rows only filled about half the card and left the lower right
                quadrant empty next to the button column. */}
            <dl className='mt-5 grid grid-cols-1 gap-x-6 gap-y-2 text-xs sm:grid-cols-2 xl:grid-cols-4'>
              <div className='flex items-center gap-2'>
                <dt className='text-grayscale-500'>wMATRIX</dt>
                <dd>
                  <a
                    className='font-mono text-grayscale-300 underline decoration-white/30 hover:text-white'
                    href={`${addressUrl(facts.explorerUrl, facts.wrappedMatrixAddress)}#code`}
                    target='_blank'
                    rel='noopener noreferrer'
                  >
                    {shortHex(facts.wrappedMatrixAddress)}
                  </a>
                </dd>
              </div>
              <div className='flex items-center gap-2'>
                <dt className='text-grayscale-500'>Founder vault</dt>
                <dd>
                  <a
                    className='font-mono text-grayscale-300 underline decoration-white/30 hover:text-white'
                    href={`${addressUrl(facts.explorerUrl, facts.vaultAddress)}#code`}
                    target='_blank'
                    rel='noopener noreferrer'
                  >
                    {shortHex(facts.vaultAddress)}
                  </a>
                </dd>
              </div>
              <div className='flex items-center gap-2'>
                <dt className='text-grayscale-500'>Backing mint</dt>
                <dd>
                  <a
                    className='font-mono text-grayscale-300 underline decoration-white/30 hover:text-white'
                    href={txUrl(facts.explorerUrl, facts.mintTx)}
                    target='_blank'
                    rel='noopener noreferrer'
                  >
                    {shortHex(facts.mintTx)}
                  </a>
                </dd>
              </div>
              <div className='flex items-center gap-2'>
                <dt className='text-grayscale-500'>Attesting validators</dt>
                <dd className='font-mono text-grayscale-300'>{facts.validatorCount}</dd>
              </div>
            </dl>
          </div>

          <div className='flex shrink-0 flex-col gap-3 sm:flex-row lg:flex-col'>
            <Link
              href='/bridge'
              className='inline-flex items-center justify-center rounded-full bg-white px-6 py-3 text-[15px] font-semibold tracking-tight text-black transition-transform hover:-translate-y-0.5'
            >
              Open the bridge
              <FiArrowRight className='ml-2 h-4 w-4' />
            </Link>
            <a
              href={facts.evidenceUrl}
              target='_blank'
              rel='noopener noreferrer'
              className='inline-flex items-center justify-center rounded-full border border-white/15 bg-white/[0.06] px-6 py-3 text-[15px] font-semibold tracking-tight text-white backdrop-blur-sm transition-colors hover:border-white/30'
            >
              Verify the launch
              <FiExternalLink className='ml-2 h-4 w-4' />
            </a>
            {/* Only appears when the deployment configured a trusted HTTPS DEX
                URL (see resolveLaunchFacts). This is a market price, not a peg,
                so it is offered as a secondary link rather than promoted above
                the bridge itself. */}
            {facts.dexUrl ? (
              <a
                href={facts.dexUrl}
                target='_blank'
                rel='noopener noreferrer'
                className='inline-flex items-center justify-center rounded-full border border-white/15 bg-white/[0.06] px-6 py-3 text-[15px] font-semibold tracking-tight text-white backdrop-blur-sm transition-colors hover:border-white/30'
              >
                Trade wMATRIX
                <FiExternalLink className='ml-2 h-4 w-4' />
              </a>
            ) : null}
          </div>
        </div>
      </div>
    </div>
  );
}

export default LaunchBanner;
