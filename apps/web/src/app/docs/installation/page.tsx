'use client';

import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';
import { FiCopy } from 'react-icons/fi';
import { toast } from 'sonner';

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

                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Installation Methods</h2>

                  <div className='space-y-8'>
                    {/* Package Manager Installation */}
                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-4'>Using Package Managers (Recommended)</h3>

                      {/* macOS */}
                      <div className='mb-8'>
                        <h4 className='text-lg font-semibold text-white mb-3'>macOS (Homebrew)</h4>
                        <div className='bg-black rounded-lg p-4'>
                          <div className='flex justify-between items-center mb-2'>
                            <span className='text-sm text-gray-100'>Install via Homebrew</span>
                            <button
                              onClick={() => copyCode('brew install matrix-os')}
                              className='p-2 hover:bg-gray-800 rounded transition-colors'
                            >
                              <FiCopy className='w-4 h-4' />
                            </button>
                          </div>
                          <pre className='text-sm text-gray-100'>
                            <code>brew install matrix-os</code>
                          </pre>
                        </div>
                      </div>

                      {/* Linux */}
                      <div className='mb-6'>
                        <h4 className='text-lg font-semibold text-white mb-3'>Linux (apt)</h4>
                        <div className='bg-black rounded-lg p-4'>
                          <div className='flex justify-between items-center mb-2'>
                            <span className='text-sm text-gray-400'>Install via apt</span>
                            <button
                              onClick={() =>
                                copyCode(`# Add Matrix OS repository
curl -fsSL https://matrix.os/gpg | sudo gpg --dearmor -o /usr/share/keyrings/matrix-os-archive-keyring.gpg
echo "deb [signed-by=/usr/share/keyrings/matrix-os-archive-keyring.gpg] https://repo.matrix.os stable main" | sudo tee /etc/apt/sources.list.d/matrix-os.list

# Install Matrix OS
sudo apt update
sudo apt install matrix-os`)
                              }
                              className='p-2 hover:bg-gray-800 rounded transition-colors'
                            >
                              <FiCopy className='w-4 h-4' />
                            </button>
                          </div>
                          <pre className='text-sm text-gray-300 overflow-x-auto whitespace-pre-wrap break-words'>
                            <code>{`# Add Matrix OS repository
curl -fsSL https://matrix.os/gpg | sudo gpg --dearmor -o /usr/share/keyrings/matrix-os-archive-keyring.gpg
echo "deb [signed-by=/usr/share/keyrings/matrix-os-archive-keyring.gpg] https://repo.matrix.os stable main" | sudo tee /etc/apt/sources.list.d/matrix-os.list

# Install Matrix OS
sudo apt update
sudo apt install matrix-os`}</code>
                          </pre>
                        </div>
                      </div>

                      {/* Windows */}
                      <div>
                        <h4 className='text-lg font-semibold text-white mb-3'>Windows (winget)</h4>
                        <div className='bg-black rounded-lg p-4'>
                          <div className='flex justify-between items-center mb-2'>
                            <span className='text-sm text-gray-400'>Install via winget</span>
                            <button
                              onClick={() => copyCode('winget install matrix-os')}
                              className='p-2 hover:bg-gray-800 rounded transition-colors'
                            >
                              <FiCopy className='w-4 h-4' />
                            </button>
                          </div>
                          <pre className='text-sm text-gray-300'>
                            <code>winget install matrix-os</code>
                          </pre>
                        </div>
                      </div>
                    </div>

                    {/* Manual Installation */}
                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>Manual Installation</h3>
                      <p className='text-gray-300 mb-4'>
                        If you prefer manual installation or your system doesn&apos;t support package managers, you can
                        download and install Matrix OS directly from our GitHub releases.
                      </p>

                      <div className='mb-6'>
                        <h4 className='text-lg font-semibold text-white mb-3'>Linux/macOS</h4>
                        <div className='bg-black rounded-lg p-4'>
                          <div className='flex justify-between items-center mb-2'>
                            <span className='text-sm text-gray-400'>Manual installation steps</span>
                            <button
                              onClick={() =>
                                copyCode(`# Download the latest release
curl -LO https://github.com/matrix-os/matrix-os/releases/latest/download/matrix-os-$(uname -s)-$(uname -m).tar.gz

# Extract and install
tar xzf matrix-os-*.tar.gz
cd matrix-os-*
sudo ./install.sh`)
                              }
                              className='p-2 hover:bg-gray-800 rounded transition-colors'
                            >
                              <FiCopy className='w-4 h-4' />
                            </button>
                          </div>
                          <pre className='text-sm text-gray-300 overflow-x-auto whitespace-pre-wrap break-words'>
                            <code>{`# Download the latest release
curl -LO https://github.com/matrix-os/matrix-os/releases/latest/download/matrix-os-$(uname -s)-$(uname -m).tar.gz

# Extract and install
tar xzf matrix-os-*.tar.gz
cd matrix-os-*
sudo ./install.sh`}</code>
                          </pre>
                        </div>
                      </div>
                    </div>
                  </div>

                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Post-Installation Setup</h2>

                  <div className='space-y-6'>
                    {/* Verification */}
                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>Verify Installation</h3>
                      <p className='text-gray-100 leading-relaxed mb-4'>
                        After installation, verify that Matrix OS is properly installed by checking the version:
                      </p>
                      <div className='bg-black rounded-lg p-4'>
                        <div className='flex justify-between items-center mb-2'>
                          <span className='text-sm text-gray-100'>Check version</span>
                          <button
                            onClick={() => copyCode('matrix-os --version')}
                            className='p-2 hover:bg-gray-800 rounded transition-colors'
                          >
                            <FiCopy className='w-4 h-4' />
                          </button>
                        </div>
                        <pre className='text-sm text-gray-100'>
                          <code>matrix-os --version</code>
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
