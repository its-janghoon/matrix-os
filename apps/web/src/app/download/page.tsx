import { Button } from '@/components/Button';
import { CopyButton } from '@/components/CopyButton';
import { Footer } from '@/components/Footer';
import Navigation from '@/components/Navigation';
import { Card, Eyebrow, PageHero, Section } from '@/components/marketing';
import { formatBytes, getLatestRelease, REPO, TARGETS } from '@/lib/releases';
import { FiArrowRight, FiDownload, FiPackage, FiTerminal } from 'react-icons/fi';

export const metadata = {
  title: 'Download',
  description:
    'Download Matrix OS, or build the matrix CLI and matrixd node from source. Builds are published as GitHub releases with checksums.',
};

// Re-checked hourly so a newly published release shows up without a redeploy.
export const revalidate = 3600;

const BUILD_FROM_SOURCE = `git clone https://github.com/${REPO}.git
cd matrix-os

# Generate the Protocol Buffers stubs, then build both binaries.
make proto
cd services/core
go build -o matrix  ./cmd/matrix
go build -o matrixd ./cmd/matrixd`;

const FIRST_RUN = `./matrix init          # write a dev config to ~/.matrix
./matrixd &            # start the node
./matrix quickstart    # genesis -> fund -> provider -> first job`;

export default async function DownloadPage() {
  const release = await getLatestRelease();

  return (
    <>
      <Navigation />
      <PageHero
        eyebrow='Download'
        eyebrowTone='primary'
        title={release ? `Matrix OS ${release.tag}` : 'Build Matrix OS from source'}
        lead={
          release
            ? 'Prebuilt binaries for the matrix CLI and the matrixd node. Every archive is published as a GitHub release asset with a SHA256SUMS file generated over the artifacts themselves.'
            : 'There is no tagged release yet, so there are no binaries to download. Everything below builds the same two binaries a release would ship, from source, in about a minute.'
        }
      />

      {release ? (
        <Section>
          <Eyebrow tone='primary'>Prebuilt binaries</Eyebrow>
          <h2 className='mt-3 text-h3-bold text-white'>
            {release.tag}
            {release.publishedAt ? (
              <span className='ml-3 text-label-regular text-grayscale-400'>
                published {new Date(release.publishedAt).toISOString().slice(0, 10)}
              </span>
            ) : null}
          </h2>
          <p className='mt-3 max-w-2xl text-body-regular text-grayscale-300'>
            Each archive contains both <code className='text-white'>matrix</code> and{' '}
            <code className='text-white'>matrixd</code>, plus the licence and README. Sizes are
            reported by GitHub for the exact file you are downloading.
          </p>

          <div className='mt-8 grid gap-4 sm:grid-cols-2 lg:grid-cols-3'>
            {release.assets.map((asset) => (
              <Card key={asset.name}>
                <div className='flex items-baseline justify-between gap-3'>
                  <span className='text-body-semibold text-white'>{asset.os}</span>
                  <span className='text-caption-regular text-grayscale-400'>{asset.arch}</span>
                </div>
                <p className='mt-1 text-caption-regular text-grayscale-400'>
                  {formatBytes(asset.size)}
                </p>
                <Button href={asset.url} variant='primary' size='sm' className='mt-4 w-full'>
                  <FiDownload className='mr-2 h-4 w-4' aria-hidden />
                  Download
                </Button>
              </Card>
            ))}
          </div>

          <div className='mt-8 rounded-xl border border-white/10 bg-white/[0.02] p-6'>
            <h3 className='text-h4-bold text-white'>Verify what you downloaded</h3>
            {release.checksumsUrl ? (
              <>
                <p className='mt-2 max-w-2xl text-body-regular text-grayscale-300'>
                  The release ships a <code className='text-white'>SHA256SUMS</code> file generated
                  over the published archives. Check your download against it:
                </p>
                <div className='mt-4 flex items-start justify-between gap-4 rounded-lg bg-black/60 p-4'>
                  <pre className='overflow-x-auto text-label-regular text-grayscale-200'>
                    <code>{`curl -fsSLO ${release.checksumsUrl}\nsha256sum -c SHA256SUMS --ignore-missing`}</code>
                  </pre>
                  <CopyButton
                    value={`curl -fsSLO ${release.checksumsUrl}\nsha256sum -c SHA256SUMS --ignore-missing`}
                    label='Copy the verification commands'
                  />
                </div>
              </>
            ) : (
              <p className='mt-2 max-w-2xl text-body-regular text-grayscale-300'>
                This release did not publish a checksum file. Compare against the asset digests on
                the{' '}
                <a href={release.htmlUrl} className='text-primary-300 hover:text-secondary-300'>
                  release page
                </a>{' '}
                instead.
              </p>
            )}
          </div>
        </Section>
      ) : (
        <Section>
          <div className='rounded-xl border border-accent-300/25 bg-accent-300/[0.06] p-6'>
            <Eyebrow tone='accent'>No release yet</Eyebrow>
            <h2 className='mt-3 text-h4-bold text-white'>
              Nothing is published to download at the moment
            </h2>
            <p className='mt-2 max-w-2xl text-body-regular text-grayscale-300'>
              Matrix OS has no tagged release, so this page has no binaries to offer. When a{' '}
              <code className='text-white'>v*</code> tag is pushed, the release workflow
              cross-compiles both binaries for Linux, macOS and Windows on x64 and arm64, writes a{' '}
              <code className='text-white'>SHA256SUMS</code> file over the archives, and publishes
              them &mdash; and they appear here automatically.
            </p>
            <Button
              href={`https://github.com/${REPO}/releases`}
              variant='secondary'
              size='sm'
              className='mt-5'
            >
              Watch the releases page
              <FiArrowRight className='ml-2 h-4 w-4' aria-hidden />
            </Button>
          </div>
        </Section>
      )}

      <Section>
        <Eyebrow tone='secondary'>From source</Eyebrow>
        <h2 className='mt-3 text-h3-bold text-white'>Build the binaries yourself</h2>
        <p className='mt-3 max-w-2xl text-body-regular text-grayscale-300'>
          This is the supported path today, and it is what the release workflow runs. You need Go
          (the version in <code className='text-white'>services/core/go.mod</code>) and{' '}
          <a
            href='https://buf.build/docs/installation'
            className='text-primary-300 hover:text-secondary-300'
          >
            buf
          </a>
          , which generates the Protocol Buffers stubs the CLI imports.
        </p>

        <div className='mt-6 space-y-4'>
          <div className='rounded-xl border border-white/10 bg-black/60 p-5'>
            <div className='mb-3 flex items-center justify-between gap-4'>
              <span className='inline-flex items-center gap-2 text-label-medium text-grayscale-300'>
                <FiPackage className='h-4 w-4' aria-hidden />
                Clone and build
              </span>
              <CopyButton value={BUILD_FROM_SOURCE} label='Copy the build commands' />
            </div>
            <pre className='overflow-x-auto text-label-regular text-grayscale-200'>
              <code>{BUILD_FROM_SOURCE}</code>
            </pre>
          </div>

          <div className='rounded-xl border border-white/10 bg-black/60 p-5'>
            <div className='mb-3 flex items-center justify-between gap-4'>
              <span className='inline-flex items-center gap-2 text-label-medium text-grayscale-300'>
                <FiTerminal className='h-4 w-4' aria-hidden />
                First run
              </span>
              <CopyButton value={FIRST_RUN} label='Copy the first-run commands' />
            </div>
            <pre className='overflow-x-auto text-label-regular text-grayscale-200'>
              <code>{FIRST_RUN}</code>
            </pre>
          </div>
        </div>

        <p className='mt-6 text-label-regular text-grayscale-400'>
          Targets the release workflow builds:{' '}
          {TARGETS.map((t) => `${t.goos}/${t.goarch}`).join(', ')}.
        </p>
      </Section>

      <Footer />
    </>
  );
}
