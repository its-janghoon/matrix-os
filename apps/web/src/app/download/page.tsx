'use client';

import { Footer } from '@/components/Footer';
import Navigation from '@/components/Navigation';
import { useState } from 'react';
import { FaApple, FaDocker, FaLinux, FaWindows } from 'react-icons/fa';
import { FiCopy, FiDownload, FiTerminal } from 'react-icons/fi';
import { toast } from 'sonner';

interface Release {
  version: string;
  date: string;
  platforms: {
    [key: string]: {
      name: string;
      arch: string;
      url: string;
      checksum: string;
      size: string;
    }[];
  };
}

const currentRelease: Release = {
  version: '1.0.0',
  date: '2024-03-20',
  platforms: {
    windows: [
      {
        name: 'Windows',
        arch: 'x64',
        url: '/downloads/matrix-core-1.0.0-windows-amd64.exe',
        checksum: 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
        size: '64.2 MB',
      },
      {
        name: 'Windows',
        arch: 'arm64',
        url: '/downloads/matrix-core-1.0.0-windows-arm64.exe',
        checksum: 'd8022f2419a8fdc80563c1fc0ce5d480daa32717aff0e6069d2d32a5d5cf7c53',
        size: '61.8 MB',
      },
    ],
    mac: [
      {
        name: 'macOS',
        arch: 'x64',
        url: '/downloads/matrix-core-1.0.0-darwin-amd64',
        checksum: '6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b',
        size: '58.4 MB',
      },
      {
        name: 'macOS',
        arch: 'arm64',
        url: '/downloads/matrix-core-1.0.0-darwin-arm64',
        checksum: 'ef537f25c895bfa782526529a9b63d97aa631564d5d789c2b765448c8635fb6c',
        size: '56.9 MB',
      },
    ],
    linux: [
      {
        name: 'Linux',
        arch: 'x64',
        url: '/downloads/matrix-core-1.0.0-linux-amd64',
        checksum: '19581e27de7ced00ff1ce50b2047e7a567c76b1cbaebabe5ef03f7c3017bb5b7',
        size: '52.3 MB',
      },
      {
        name: 'Linux',
        arch: 'arm64',
        url: '/downloads/matrix-core-1.0.0-linux-arm64',
        checksum: '4b227777d4dd1fc61c6f884f48641d02b4d121d3fd328cb08b5531fcacdabf8a',
        size: '49.8 MB',
      },
    ],
  },
};

export default function Download() {
  const [, setCopiedHash] = useState<string | null>(null);

  const copyHash = (hash: string) => {
    navigator.clipboard.writeText(hash);
    setCopiedHash(hash);
    setTimeout(() => setCopiedHash(null), 2000);
    toast.success('Checksum copied to clipboard');
  };

  const copyCommand = (cmd: string) => {
    navigator.clipboard.writeText(cmd);
    toast.success('Command copied to clipboard');
  };

  return (
    <>
      <Navigation />
      <main className='min-h-screen bg-black text-white pt-16'>
        {/* Hero Section */}
        <section className='py-24 bg-gradient-to-b from-black to-grayscale-900'>
          <div className='container mx-auto px-4'>
            <div className='max-w-4xl mx-auto text-center'>
              <h1 className='text-5xl font-bold mb-6 bg-decorative-1 text-transparent bg-clip-text'>
                Download Matrix Core
              </h1>
              <p className='text-xl text-grayscale-300 mb-12'>
                Current Version: {currentRelease.version} ({currentRelease.date})
              </p>
            </div>
          </div>
        </section>

        {/* Download Options */}
        <section className='py-16 bg-black'>
          <div className='container mx-auto px-4'>
            <div className='max-w-5xl mx-auto'>
              {/* Platform Downloads */}
              <div className='grid md:grid-cols-2 gap-6 mb-16'>
                {/* Windows Downloads */}
                <div className='bg-grayscale-900 rounded-xl p-6 border border-grayscale-800 hover:border-primary-300/40 transition-colors'>
                  <div className='flex items-center gap-3 mb-6'>
                    <FaWindows className='w-8 h-8 text-primary-300' />
                    <h2 className='text-2xl font-bold'>Windows</h2>
                  </div>
                  <div className='space-y-4'>
                    {currentRelease.platforms.windows.map((platform, idx) => (
                      <div key={idx} className='p-4 bg-grayscale-800 rounded-lg'>
                        <div className='flex items-center justify-between mb-3'>
                          <span className='text-sm text-grayscale-300'>{platform.arch}</span>
                          <span className='text-sm text-grayscale-300'>{platform.size}</span>
                        </div>
                        <div className='flex items-center justify-between'>
                          <button
                            onClick={() => copyHash(platform.checksum)}
                            className='text-xs font-mono bg-grayscale-700 px-3 py-1 rounded flex items-center gap-2 hover:bg-grayscale-600 transition-colors'
                          >
                            {platform.checksum.slice(0, 8)}...
                            <FiCopy className='w-4 h-4' />
                          </button>
                          <a
                            href={platform.url}
                            className='flex items-center gap-2 px-4 py-2 bg-primary rounded-lg hover:bg-primary-500 transition-colors text-sm'
                          >
                            <FiDownload className='w-4 h-4' />
                            Download
                          </a>
                        </div>
                      </div>
                    ))}
                  </div>
                </div>

                {/* macOS Downloads */}
                <div className='bg-grayscale-900 rounded-xl p-6 border border-grayscale-800 hover:border-primary-300/40 transition-colors'>
                  <div className='flex items-center gap-3 mb-6'>
                    <FaApple className='w-8 h-8 text-primary-300' />
                    <h2 className='text-2xl font-bold'>macOS</h2>
                  </div>
                  <div className='space-y-4'>
                    {currentRelease.platforms.mac.map((platform, idx) => (
                      <div key={idx} className='p-4 bg-grayscale-800 rounded-lg'>
                        <div className='flex items-center justify-between mb-3'>
                          <span className='text-sm text-grayscale-300'>{platform.arch}</span>
                          <span className='text-sm text-grayscale-300'>{platform.size}</span>
                        </div>
                        <div className='flex items-center justify-between'>
                          <button
                            onClick={() => copyHash(platform.checksum)}
                            className='text-xs font-mono bg-grayscale-700 px-3 py-1 rounded flex items-center gap-2 hover:bg-grayscale-600 transition-colors'
                          >
                            {platform.checksum.slice(0, 8)}...
                            <FiCopy className='w-4 h-4' />
                          </button>
                          <a
                            href={platform.url}
                            className='flex items-center gap-2 px-4 py-2 bg-primary rounded-lg hover:bg-primary-500 transition-colors text-sm'
                          >
                            <FiDownload className='w-4 h-4' />
                            Download
                          </a>
                        </div>
                      </div>
                    ))}
                  </div>
                </div>

                {/* Linux Downloads */}
                <div className='bg-grayscale-900 rounded-xl p-6 border border-grayscale-800 hover:border-primary-300/40 transition-colors'>
                  <div className='flex items-center gap-3 mb-6'>
                    <FaLinux className='w-8 h-8 text-primary-300' />
                    <h2 className='text-2xl font-bold'>Linux</h2>
                  </div>
                  <div className='space-y-4'>
                    {currentRelease.platforms.linux.map((platform, idx) => (
                      <div key={idx} className='p-4 bg-grayscale-800 rounded-lg'>
                        <div className='flex items-center justify-between mb-3'>
                          <span className='text-sm text-grayscale-300'>{platform.arch}</span>
                          <span className='text-sm text-grayscale-300'>{platform.size}</span>
                        </div>
                        <div className='flex items-center justify-between'>
                          <button
                            onClick={() => copyHash(platform.checksum)}
                            className='text-xs font-mono bg-grayscale-700 px-3 py-1 rounded flex items-center gap-2 hover:bg-grayscale-600 transition-colors'
                          >
                            {platform.checksum.slice(0, 8)}...
                            <FiCopy className='w-4 h-4' />
                          </button>
                          <a
                            href={platform.url}
                            className='flex items-center gap-2 px-4 py-2 bg-primary rounded-lg hover:bg-primary-500 transition-colors text-sm'
                          >
                            <FiDownload className='w-4 h-4' />
                            Download
                          </a>
                        </div>
                      </div>
                    ))}
                  </div>
                </div>

                {/* Docker */}
                <div className='bg-grayscale-900 rounded-xl p-6 border border-grayscale-800 hover:border-primary-300/40 transition-colors'>
                  <div className='flex items-center gap-3 mb-6'>
                    <FaDocker className='w-8 h-8 text-primary-300' />
                    <h2 className='text-2xl font-bold'>Docker</h2>
                  </div>
                  <div className='space-y-4'>
                    <div className='p-4 bg-grayscale-800 rounded-lg'>
                      <p className='text-sm text-grayscale-300 mb-3'>Pull and run the official Docker image</p>
                      <div className='flex items-center justify-between gap-4'>
                        <pre className='bg-grayscale-700 px-3 py-2 rounded text-xs font-mono flex-1'>
                          docker pull ecirlabs/matrix-core:latest
                        </pre>
                        <button
                          onClick={() => copyCommand('docker pull ecirlabs/matrix-core:latest')}
                          className='p-2 hover:bg-grayscale-600 rounded transition-colors'
                        >
                          <FiCopy className='w-4 h-4' />
                        </button>
                      </div>
                    </div>
                  </div>
                </div>
              </div>

              {/* Package Managers */}
              <div className='bg-grayscale-900 rounded-xl p-8 border border-grayscale-800'>
                <div className='flex items-center gap-3 mb-6'>
                  <FiTerminal className='w-8 h-8 text-primary-300' />
                  <h2 className='text-2xl font-bold'>Package Managers</h2>
                </div>
                <div className='space-y-6'>
                  <div>
                    <h3 className='text-lg font-semibold mb-3'>NPM</h3>
                    <div className='flex items-center justify-between gap-4 bg-grayscale-800 p-4 rounded-lg'>
                      <pre className='bg-grayscale-700 px-3 py-2 rounded text-xs font-mono flex-1'>
                        npm install -g @ecirlabs/matrix-core
                      </pre>
                      <button
                        onClick={() => copyCommand('npm install -g @ecirlabs/matrix-core')}
                        className='p-2 hover:bg-grayscale-600 rounded transition-colors'
                      >
                        <FiCopy className='w-4 h-4' />
                      </button>
                    </div>
                  </div>
                </div>
              </div>

              {/* Verification Instructions */}
              <div className='mt-16'>
                <h2 className='text-2xl font-bold mb-6'>Verify Download</h2>
                <div className='bg-grayscale-900 rounded-xl p-6 border border-grayscale-800'>
                  <p className='text-grayscale-300 mb-4'>
                    To verify your download, compare the SHA256 checksum of the downloaded file with the provided hash.
                  </p>
                  <div className='space-y-4'>
                    <div>
                      <h3 className='text-lg font-semibold mb-2'>Windows (PowerShell)</h3>
                      <pre className='bg-grayscale-800 p-4 rounded-lg text-sm font-mono'>
                        Get-FileHash matrix-core-1.0.0-windows-amd64.exe -Algorithm SHA256
                      </pre>
                    </div>
                    <div>
                      <h3 className='text-lg font-semibold mb-2'>macOS/Linux</h3>
                      <pre className='bg-grayscale-800 p-4 rounded-lg text-sm font-mono'>
                        shasum -a 256 matrix-core-1.0.0-darwin-amd64
                      </pre>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </section>
      </main>
      <Footer />
    </>
  );
}
