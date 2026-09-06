'use client';

import { motion } from 'framer-motion';
import { useEffect, useState } from 'react';

interface TVTextProps {
  children: string;
  className?: string;
  glitchIntensity?: 'low' | 'medium' | 'high';
  continuous?: boolean;
}

export default function TVText({
  children,
  className = '',
  glitchIntensity = 'medium',
  continuous = false,
}: TVTextProps) {
  const [offset, setOffset] = useState(0);

  // Random glitch effect
  useEffect(() => {
    const intensity = {
      low: 2000,
      medium: 1000,
      high: 500,
    }[glitchIntensity];

    if (continuous) {
      const interval = setInterval(() => {
        setOffset(Math.random() * 10 - 5);
      }, 50);
      return () => clearInterval(interval);
    } else {
      const interval = setInterval(() => {
        setOffset(Math.random() * 10 - 5);
        setTimeout(() => setOffset(0), 50);
      }, intensity);
      return () => clearInterval(interval);
    }
  }, [glitchIntensity, continuous]);

  return (
    <motion.div
      className={`relative font-mono ${className}`}
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      transition={{ duration: 0.3 }}
    >
      {/* Main text */}
      <div className='relative'>
        <div className='relative z-10'>{children}</div>

        {/* RGB shadow effects */}
        <div
          className='absolute inset-0 text-red-500 opacity-50 mix-blend-screen'
          style={{ transform: `translate(${offset}px, ${offset * 0.5}px)` }}
        >
          {children}
        </div>
        <div
          className='absolute inset-0 text-blue-500 opacity-50 mix-blend-screen'
          style={{ transform: `translate(${-offset}px, ${-offset * 0.5}px)` }}
        >
          {children}
        </div>

        {/* Scan lines */}
        <div
          className='absolute inset-0 pointer-events-none mix-blend-overlay'
          style={{
            backgroundImage:
              'repeating-linear-gradient(0deg, transparent, transparent 1px, rgba(0, 0, 0, 0.1) 1px, rgba(0, 0, 0, 0.1) 2px)',
            backgroundSize: '100% 2px',
            opacity: 0.3,
          }}
        />
      </div>

      {/* Static noise overlay */}
      <div
        className='absolute inset-0 pointer-events-none mix-blend-overlay opacity-10'
        style={{
          backgroundImage:
            'url("data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAADIAAAAyBAMAAADsEZWCAAAAGFBMVEUAAAAzMzMyMjIzMzMzMzMyMjIzMzMzMzOW6N0+AAAACHRSTlMABQgKDREVGRt8Ju4AAACWSURBVDjL1ZOxDcMwDEPpn8kbeJMM4Q28SYZwm3sDe4T8YlIHcuMULlIgD0TxCB0pSgAuDUhDL7XQS3NHMG/xPNHMWOxNbHE30czY7E1scTfRzNjsTWxxN9HM2OxNbHE30czY7E1scTfRzNjsTWxxN9HM2OxNbHE30czY7E1scTfRzNjsTWxxN9HM2OxNbHE30czY7E1scTfRzBwWewPBJ1NCzR6pzwAAAABJRU5ErkJggg==")',
          backgroundRepeat: 'repeat',
          animation: 'noise 0.2s infinite',
        }}
      />

      {/* Vignette effect */}
      <div
        className='absolute inset-0 pointer-events-none'
        style={{
          background: 'radial-gradient(circle, transparent 60%, rgba(0, 0, 0, 0.3) 100%)',
        }}
      />
    </motion.div>
  );
}
