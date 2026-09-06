import { cn } from '@/lib/utils';
import Link from 'next/link';
import { ReactNode } from 'react';

interface ButtonProps {
  children: ReactNode;
  href?: string;
  variant?: 'primary' | 'secondary' | 'outline';
  size?: 'sm' | 'md' | 'lg';
  className?: string;
  onClick?: () => void;
}

export function Button({ children, href, variant = 'primary', size = 'md', className, onClick }: ButtonProps) {
  const baseStyles = 'inline-flex items-center justify-center font-medium transition-colors rounded-lg';

  const variants = {
    primary: 'bg-primary text-white hover:bg-primary-500 shadow-lg shadow-primary/20',
    secondary: 'bg-white/10 text-white hover:bg-white/20 border border-white/10',
    outline: 'bg-transparent border-2 border-primary-300 text-white hover:bg-primary/10',
  };

  const sizes = {
    sm: 'px-4 py-2 text-sm',
    md: 'px-6 py-3 text-base',
    lg: 'px-8 py-4 text-lg',
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
