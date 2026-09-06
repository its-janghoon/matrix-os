import Link from 'next/link'

interface NoticeBannerProps {
  /** CTA message to display */
  cta: string;
  /** Link redirected to when clicked */
  href: string;
}

export function NoticeBanner({ cta, href }: NoticeBannerProps) {
  return (
    <div className="bg-blue-50 border-b border-blue-100">
      <div className="container mx-auto px-4 py-3">
        <Link 
          href={href}
          className="text-blue-600 hover:text-blue-800 transition-colors text-sm flex items-center justify-center"
        >
          {cta} →
        </Link>
      </div>
    </div>
  )
} 