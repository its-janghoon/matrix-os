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
    images: [
      {
        url: '/og-image.svg',
        width: 1200,
        height: 630,
        alt: 'ECIR Labs',
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
  icons: {
    icon: '/favicon.ico',
    shortcut: '/favicon-16x16.png',
    apple: '/apple-touch-icon.png',
  },
  manifest: '/site.webmanifest',
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
