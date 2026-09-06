'use client';

import { CopyButton } from '@/components/CopyButton';
import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';
import { FiCopy } from 'react-icons/fi';
import { toast } from 'sonner';

const REPO = 'savagemanage/matrix-os';

const BUILD_FROM_SOURCE = `git clone https://github.com/${REPO}.git
cd matrix-os

# Generate the Protocol Buffers stubs the CLI imports.
make proto

cd services/core
go build -o matrix  ./cmd/matrix
go build -o matrixd ./cmd/matrixd`;

// Uses the archive naming the release workflow produces, and verifies against
// the SHA256SUMS file it publishes rather than a hash pasted into a page.
const FETCH_RELEASE = `TAG=$(curl -fsSL https://api.github.com/repos/${REPO}/releases/latest \\
  | grep -m1 '"tag_name"' | cut -d'"' -f4)
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')

curl -fsSLO "https://github.com/${REPO}/releases/download/$TAG/matrix-os-$TAG-$OS-$ARCH.tar.gz"
curl -fsSLO "https://github.com/${REPO}/releases/download/$TAG/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing

tar xzf "matrix-os-$TAG-$OS-$ARCH.tar.gz"`;

const VERIFY_INSTALL = `matrix --version     # matrix version v0.1.0   (or "dev")
matrixd -version     # matrixd v0.1.0          (or "dev")

matrix --help        # the full command tree`;

export default function MatrixOsInstallation() {
  const copyCode = (code: string) => {
    navigator.clipboard.writeText(code);
    toast.success('Code copied to clipboard');
  };

  return (
    <>
      <Navigation />
      <div className='min-h-screen bg-black'>
        <div className='pt-16'>
          <div className='flex'>
            <DocSidebar />

            {/* Main Content */}
            <main className='flex-1 ml-64 p-8'>
              <div className='max-w-4xl mx-auto'>
                <article className='text-gray-100'>
                  {/* Hero Section */}
                  <div className='bg-gradient-to-r from-blue-500/10 via-purple-500/10 to-blue-500/10 rounded-xl p-8 mb-12 border border-blue-500/20'>
                    <h1 className='text-4xl font-bold text-white mb-4'>Installing Matrix OS</h1>
                    <p className='text-xl text-gray-100'>
                      Get Matrix OS up and running on your system in minutes. Follow our step-by-step guide for a smooth
                      installation experience.
                    </p>
                  </div>

                  <h2 className='text-3xl font-bold text-white mt-8 mb-6'>System Requirements</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <div className='grid grid-cols-1 md:grid-cols-2 gap-6'>
                      <div>
                        <h3 className='text-xl font-bold text-white mb-4'>Hardware Requirements</h3>
                        <ul className='text-gray-100 space-y-3 list-disc pl-6'>
                          <li>CPU: 64-bit processor, 2 cores minimum</li>
                          <li>RAM: 4GB minimum (8GB recommended)</li>
                          <li>Storage: 1GB free space</li>
                          <li>Network: Stable internet connection</li>
                        </ul>
                      </div>
                      <div>
                        <h3 className='text-xl font-bold text-white mb-4'>Software Requirements</h3>
                        <ul className='text-gray-100 space-y-3 list-disc pl-6'>
                          <li>OS: Linux, macOS, or Windows 10/11</li>
                          <li>Python: 3.8 or higher</li>
                          <li>Node.js: 16.x or higher</li>
                          <li>Git: 2.x or higher</li>
                        </ul>
                      </div>
                    </div>
                  </div>

                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Installation</h2>

                  {/* This section used to advertise `brew install matrix-os`,
                      `winget install matrix-os`, an apt repository at
                      repo.matrix.os signed by a key at matrix.os/gpg, and
                      `npm install -g @ecirlabs/matrix-core`. None of those
                      exist - `.os` is not a TLD, and the npm scope is empty -
                      and the manual path pointed at github.com/matrix-os/matrix-os
                      with an install.sh that no archive contains. What is left
                      is what actually works. */}
                  <div className='space-y-6'>
                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>Build from source</h3>
                      <p className='text-gray-300 mb-4'>
                        The supported path today, and the one the release workflow itself runs. You
                        need Go (the version pinned in <code className='text-white'>services/core/go.mod</code>) and{' '}
                        <a
                          href='https://buf.build/docs/installation'
                          className='text-primary-300 hover:text-secondary-300'
                        >
                          buf
                        </a>
                        , which generates the Protocol Buffers stubs the CLI imports. There is no
                        package-manager install and no installer script.
                      </p>
                      <div className='bg-black rounded-lg p-4'>
                        <div className='flex justify-between items-center mb-3'>
                          <span className='text-sm text-gray-400'>Clone, generate, build</span>
                          <CopyButton value={BUILD_FROM_SOURCE} label='Copy the build commands' />
                        </div>
                        <pre className='text-sm text-gray-300 overflow-x-auto whitespace-pre-wrap break-words'>
                          <code>{BUILD_FROM_SOURCE}</code>
                        </pre>
                      </div>
                      <p className='text-gray-400 text-sm mt-4'>
                        This produces two binaries: <code className='text-white'>matrix</code>, the
                        CLI, and <code className='text-white'>matrixd</code>, the node. Put them
                        somewhere on your <code className='text-white'>PATH</code> if you want them
                        available everywhere.
                      </p>
                    </div>

                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>Prebuilt binaries</h3>
                      <p className='text-gray-300 mb-4'>
                        Tagged releases publish archives for Linux, macOS and Windows on x64 and
                        arm64, each containing both binaries, alongside a{' '}
                        <code className='text-white'>SHA256SUMS</code> file generated over those
                        archives. The{' '}
                        <a href='/download' className='text-primary-300 hover:text-secondary-300'>
                          download page
                        </a>{' '}
                        reads the release list directly, so it shows only what exists &mdash; if it
                        offers nothing, nothing has been published yet.
                      </p>
                      <div className='bg-black rounded-lg p-4'>
                        <div className='flex justify-between items-center mb-3'>
                          <span className='text-sm text-gray-400'>
                            Fetch and verify a published release
                          </span>
                          <CopyButton value={FETCH_RELEASE} label='Copy the download commands' />
                        </div>
                        <pre className='text-sm text-gray-300 overflow-x-auto whitespace-pre-wrap break-words'>
                          <code>{FETCH_RELEASE}</code>
                        </pre>
                      </div>
                    </div>
                  </div>

                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Post-Installation Setup</h2>

                  <div className='space-y-6'>
                    {/* Verification */}
                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>Verify Installation</h3>
                      {/* This used to say `matrix-os --version`. There is no
                          matrix-os binary - the two are `matrix` and `matrixd` -
                          and the CLI had no --version flag at all, so the
                          command errored with "unknown flag". Both binaries now
                          report a version, stamped from the tag at release
                          time. */}
                      <p className='text-gray-100 leading-relaxed mb-4'>
                        Both binaries report their version. A release prints its tag; a binary you
                        built yourself prints <code className='text-white'>dev</code>, which is the
                        honest answer for a build that did not come from a tag.
                      </p>
                      <div className='bg-black rounded-lg p-4'>
                        <div className='flex justify-between items-center mb-3'>
                          <span className='text-sm text-gray-100'>Check both binaries</span>
                          <CopyButton value={VERIFY_INSTALL} label='Copy the verify commands' />
                        </div>
                        <pre className='text-sm text-gray-100 overflow-x-auto whitespace-pre-wrap break-words'>
                          <code>{VERIFY_INSTALL}</code>
                        </pre>
                      </div>
                    </div>

                    {/* Configuration */}
                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>Initial Configuration</h3>
                      <p className='text-gray-100 leading-relaxed mb-4'>
                        Configure your Matrix OS installation with the interactive setup wizard:
                      </p>
                      <div className='bg-black rounded-lg p-4'>
                        <div className='flex justify-between items-center mb-2'>
                          <span className='text-sm text-gray-100'>Run setup wizard</span>
                          <button
                            onClick={() => copyCode('matrix-os setup')}
                            className='p-2 hover:bg-gray-800 rounded transition-colors'
                          >
                            <FiCopy className='w-4 h-4' />
                          </button>
                        </div>
                        <pre className='text-sm text-gray-100'>
                          <code>matrix-os setup</code>
                        </pre>
                      </div>
                    </div>
                  </div>

                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Next Steps</h2>
                  <div className='bg-blue-500/10 rounded-xl p-6 mt-8 border border-blue-500/20'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      Now that you have Matrix OS installed, you can:
                    </p>
                    <ul className='text-gray-100 space-y-3 list-disc pl-6 mb-0'>
                      <li>
                        Follow our{' '}
                        <a href='/docs/quickstart' className='text-blue-400 hover:text-blue-300 underline'>
                          Quick Start Guide
                        </a>{' '}
                        to create your first agent
                      </li>
                      <li>
                        Learn about{' '}
                        <a href='/docs/configuration' className='text-blue-400 hover:text-blue-300 underline'>
                          configuration options
                        </a>
                      </li>
                    </ul>
                  </div>

                  <div className='bg-gray-900/50 rounded-xl p-6 mt-8 border border-gray-800'>
                    <h3 className='text-xl font-bold text-white mb-3'>Need Help?</h3>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      If you encounter any issues during installation:
                    </p>
                    <ul className='text-gray-100 space-y-3 list-disc pl-6 mb-0'>
                      <li>
                        File an issue on{' '}
                        <a
                          href='https://github.com/ecirlabs/matrix-core/issues'
                          className='text-blue-400 hover:text-blue-300 underline'
                        >
                          GitHub
                        </a>
                      </li>
                    </ul>
                  </div>
                </article>
              </div>
            </main>
          </div>
        </div>
      </div>
    </>
  );
}
