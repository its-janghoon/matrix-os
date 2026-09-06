import '@/styles/globals.css';
import type { Metadata, Viewport } from 'next';
import { Inter } from 'next/font/google';
import { Providers } from './providers';

const inter = Inter({ subsets: ['latin'] });

export const viewport: Viewport = {
  width: 'device-width',
  initialScale: 1,
  maximumScale: 1,
};

export const metadata: Metadata = {
  // Without this, Next resolves relative og:image URLs against localhost, so
  // link previews in production point at a host nobody can reach.
  metadataBase: new URL('https://ecirlabs.com'),
  title: {
    default: 'ECIR Labs - Building the future of decentralized intelligence',
    template: '%s | ECIR Labs',
  },
  description:
    'ECIR Labs is building the future of decentralized intelligence with Matrix OS: a peer-to-peer compute marketplace where idle machines announce capacity over libp2p, buyers pay for LLM compute and API responses through cryptographically signed token transfers on a hash-chained transaction log, and an external gRPC API lets buyers and providers interact from outside the node.',
  keywords: [
    'ECIR Labs',
    'Matrix OS',
    'Soul OS',
    'decentralized intelligence',
    'distributed computing',
    'compute marketplace',
    'signed token settlement',
    'hash-chained transaction log',
    'P2P compute',
    'gRPC market API',
    'Matrix Protocol',
    'Soul Protocol',
  ],
  authors: [{ name: 'ECIR Labs' }],
  creator: 'ECIR Labs',
  publisher: 'ECIR Labs',
  openGraph: {
    type: 'website',
    locale: 'en_US',
    url: 'https://ecirlabs.com',
    siteName: 'ECIR Labs',
    title: 'ECIR Labs - Decentralized Intelligence Platform',
    description:
      'ECIR Labs is building the future of decentralized intelligence with Matrix OS, enabling secure and scalable distributed computing.',
    // A raster, not the SVG this used to point at: Twitter, Facebook and Slack
    // do not render an SVG og:image.
    images: [
      {
        url: '/og-image.png',
        width: 1200,
        height: 630,
        type: 'image/png',
        alt: 'Matrix OS - a peer-to-peer marketplace for compute',
      },
    ],
  },
  robots: {
    index: true,
    follow: true,
    googleBot: {
      index: true,
      follow: true,
      'max-video-preview': -1,
      'max-image-preview': 'large',
      'max-snippet': -1,
    },
  },
  // These all pointed at files that did not exist (/favicon.ico,
  // /apple-touch-icon.png, /site.webmanifest), so the site served no icon at
  // all. The files below are generated from the brand mark; see
  // public/brand/README.md.
  icons: {
    icon: [
      { url: '/favicon.svg', type: 'image/svg+xml' },
      { url: '/favicon-32x32.png', sizes: '32x32', type: 'image/png' },
      { url: '/favicon-16x16.png', sizes: '16x16', type: 'image/png' },
    ],
    shortcut: '/favicon.ico',
    apple: [{ url: '/apple-touch-icon.png', sizes: '180x180', type: 'image/png' }],
  },
  manifest: '/manifest.json',
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang='en'>
      <body className={inter.className}>
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
