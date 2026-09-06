import { cn } from '@/lib/utils';
import Link from 'next/link';
import { ReactNode } from 'react';

interface ButtonProps {
  children: ReactNode;
  href?: string;
  variant?: 'primary' | 'secondary' | 'outline' | 'ghost';
  size?: 'sm' | 'md' | 'lg';
  className?: string;
  onClick?: () => void;
}

export function Button({ children, href, variant = 'primary', size = 'md', className, onClick }: ButtonProps) {
  const baseStyles =
    'group inline-flex items-center justify-center rounded-full font-semibold tracking-tight transition-all duration-200 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-300/60 focus-visible:ring-offset-2 focus-visible:ring-offset-black';

  const variants = {
    primary:
      'bg-decorative-1 bg-gradient-200 text-white shadow-glow hover:shadow-[0_0_0_1px_rgba(46,107,255,0.5),0_26px_70px_-20px_rgba(46,107,255,0.7)] hover:-translate-y-0.5 animate-gradient-pan',
    secondary:
      'bg-white/[0.06] text-white border border-white/10 backdrop-blur-sm hover:bg-white/[0.12] hover:border-white/20',
    outline:
      'bg-transparent border border-primary-300/50 text-white hover:border-primary-300 hover:bg-primary/10',
    ghost: 'bg-transparent text-grayscale-300 hover:text-white',
  };

  const sizes = {
    sm: 'px-4 py-2 text-sm',
    md: 'px-6 py-3 text-[15px]',
    lg: 'px-8 py-4 text-base',
  };

  const classes = cn(baseStyles, variants[variant], sizes[size], className);

  if (href) {
    return (
      <Link href={href} className={classes}>
        {children}
      </Link>
    );
  }

  return (
    <button type='button' className={classes} onClick={onClick}>
      {children}
    </button>
  );
}
