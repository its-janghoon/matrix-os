'use client';

import { motion } from 'framer-motion';
import { ReactNode } from 'react';

interface AnimatedTextProps {
  children: ReactNode;
  className?: string;
  animation?: 'fade' | 'slide' | 'bounce' | 'typing';
  delay?: number;
}

const animations = {
  fade: {
    initial: { opacity: 0 },
    animate: { opacity: 1 },
    transition: { duration: 0.5 },
  },
  slide: {
    initial: { y: 20, opacity: 0 },
    animate: { y: 0, opacity: 1 },
    transition: { duration: 0.5 },
  },
  bounce: {
    initial: { y: -20, opacity: 0 },
    animate: { y: 0, opacity: 1 },
    transition: {
      duration: 0.5,
      type: 'spring',
      stiffness: 100,
    },
  },
  typing: {
    initial: { width: 0 },
    animate: { width: '100%' },
    transition: { duration: 1, ease: 'easeInOut' },
  },
};

export default function AnimatedText({ children, className = '', animation = 'fade', delay = 0 }: AnimatedTextProps) {
  const selectedAnimation = animations[animation];

  return (
    <motion.div
      className={className}
      initial={selectedAnimation.initial}
      animate={selectedAnimation.animate}
      transition={{ ...selectedAnimation.transition, delay }}
      whileHover={{ scale: 1.05 }}
      whileTap={{ scale: 0.95 }}
    >
      {children}
    </motion.div>
  );
}
