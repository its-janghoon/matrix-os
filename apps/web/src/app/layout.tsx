import '@/styles/globals.css';
import type { Metadata } from 'next';
import { Inter } from 'next/font/google';
import { Providers } from './providers';

const inter = Inter({ subsets: ['latin'] });

export const metadata: Metadata = {
  title: {
    default: 'ECIR Labs - Building the future of decentralized intelligence',
    template: '%s | ECIR Labs',
  },
  description:
    'ECIR Labs is building the future of decentralized intelligence with Matrix OS, enabling secure and scalable distributed computing.',
  keywords: [
    'ECIR Labs',
    'Matrix OS',
    'Soul OS',
    'decentralized intelligence',
    'distributed computing',
    'Matrix Protocol',
    'Soul Protocol',
  ],
  authors: [{ name: 'Janghoon Lee' }],
  creator: 'Janghoon Lee',
  publisher: 'Janghoon Lee',
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
  viewport: {
    width: 'device-width',
    initialScale: 1,
    maximumScale: 1,
  },
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
