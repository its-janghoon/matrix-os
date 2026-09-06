'use client';

/**
 * Original gradient + mesh hero backdrop.
 *
 * A lightweight, CSS-only composition (radial mesh glows, a faint grid, and a
 * slow floating orb) that gives the hero a modern crypto-infrastructure feel
 * without shipping a heavy WebGL canvas. All shapes are original.
 */
export default function HeroBackdrop() {
  return (
    <div aria-hidden className='pointer-events-none absolute inset-0 overflow-hidden'>
      {/* Base navy wash */}
      <div className='absolute inset-0 bg-black' />

      {/* Radial mesh accents */}
      <div className='absolute inset-0 bg-mesh-hero' />

      {/* Faint technical grid, fading out toward the bottom */}
      <div className='absolute inset-0 bg-grid-fade bg-grid-sm [mask-image:radial-gradient(70%_60%_at_50%_30%,black,transparent)]' />

      {/* Floating accent orbs */}
      <div className='absolute -top-24 left-[10%] h-72 w-72 rounded-full bg-primary/25 blur-3xl animate-float-slow' />
      <div className='absolute top-1/3 right-[8%] h-64 w-64 rounded-full bg-secondary/20 blur-3xl animate-float-slow [animation-delay:1.5s]' />
      <div className='absolute bottom-0 left-1/2 h-56 w-[36rem] -translate-x-1/2 rounded-full bg-primary/10 blur-3xl' />

      {/* Bottom fade into the page background */}
      <div className='absolute inset-x-0 bottom-0 h-40 bg-gradient-to-b from-transparent to-black' />
    </div>
  );
}
