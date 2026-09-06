/**
 * Matrix OS marketing site design tokens.
 *
 * The palette is an ORIGINAL, blue-forward crypto-infrastructure system
 * (inspired by the visual language of top-tier infra landing pages: a deep
 * navy canvas, a vivid electric-blue primary, a cyan secondary accent, and a
 * warm coral highlight). Token class NAMES are kept stable
 * (primary / secondary / accent / grayscale / semantic, decorative-1..4) so
 * existing components keep resolving, while the underlying values, type scale,
 * gradients, shadows and motion are modernised.
 */

const colors = {
  // Deep navy canvas used as the base background across the site.
  background: '#060A16',

  // Electric blue primary.
  primary: {
    DEFAULT: '#2E6BFF',
    100: '#E8EFFF',
    200: '#B4CBFF',
    300: '#6E9BFF',
    400: '#2E6BFF',
    500: '#1B4FD6',
    600: '#12358F',
  },

  // Cyan / aqua secondary accent for contrast against the blue primary.
  secondary: {
    DEFAULT: '#22D3EE',
    100: '#E4FBFF',
    200: '#A9EEFA',
    300: '#5FDDF0',
    400: '#22D3EE',
    500: '#0EA5C4',
    600: '#0A6E84',
  },

  // Warm coral highlight (retained from the previous system, refreshed).
  accent: {
    DEFAULT: '#FF7A66',
    50: '#FFF3F1',
    100: '#FFE1DB',
    200: '#FF9E8E',
    300: '#FF7A66',
    400: '#F0533B',
    500: '#C63A26',
  },

  // Cool neutral ramp tuned for a dark navy UI.
  grayscale: {
    100: '#F5F7FB',
    200: '#E4E9F2',
    300: '#C3CCDD',
    400: '#8B96AC',
    500: '#5E6A82',
    600: '#3C465C',
    700: '#232C40',
    800: '#141B2C',
    900: '#0A0F1C',
  },

  /** Semantic */
  semantic: {
    success: '#22C55E',
    error: '#EF4444',
    info: '#2E6BFF',
    processing: '#F59E0B',
  },

  white: '#FFFFFF',
  black: '#050813',
};

const backgroundImage = {
  // Signature primary -> secondary sweep used for headline text and CTAs.
  'decorative-1': 'linear-gradient(101deg, #2E6BFF 0%, #22D3EE 100.01%)',
  // Primary -> coral for warmer accents.
  'decorative-2': 'linear-gradient(96deg, #2E6BFF 0%, #FF7A66 100%)',
  // Vertical cyan -> violet.
  'decorative-3': 'linear-gradient(0deg, #22D3EE 0%, #6E9BFF 100%)',
  // Warm sunset accent.
  'decorative-4': 'linear-gradient(180deg, #6E9BFF 0%, #FF7A66 100%)',
  // Radial mesh accents for hero / section backdrops (original composition).
  'mesh-hero':
    'radial-gradient(60% 60% at 15% 15%, rgba(46,107,255,0.28) 0%, rgba(46,107,255,0) 60%), radial-gradient(50% 50% at 85% 10%, rgba(34,211,238,0.22) 0%, rgba(34,211,238,0) 55%), radial-gradient(55% 55% at 75% 85%, rgba(110,155,255,0.18) 0%, rgba(110,155,255,0) 60%)',
  'grid-fade':
    'linear-gradient(to bottom, rgba(255,255,255,0.04) 1px, transparent 1px), linear-gradient(to right, rgba(255,255,255,0.04) 1px, transparent 1px)',
  'section-glow':
    'radial-gradient(80% 120% at 50% 0%, rgba(46,107,255,0.12) 0%, rgba(6,10,22,0) 70%)',
};

const opacities = {
  8: '0.08',
  16: '0.16',
  24: '0.24',
  40: '0.4',
  56: '0.56',
  80: '0.8',
};

const fontWeight = {
  bold130: {
    fontWeight: 700,
    lineHeight: '130%',
  },
  bold140: {
    fontWeight: 700,
    lineHeight: '140%',
  },
  semibold130: {
    fontWeight: 600,
    lineHeight: '130%',
  },
  semibold140: {
    fontWeight: 600,
    lineHeight: '140%',
  },
  medium140: {
    fontWeight: 500,
    lineHeight: '140%',
  },
  regular140: {
    fontWeight: 400,
    lineHeight: '140%',
  },
};

const font = {
  // Display scale for a modern landing page (larger, tighter headings).
  'display-bold': ['72px', { fontWeight: 700, lineHeight: '104%', letterSpacing: '-0.03em' }],
  'display-bold-mobile': ['44px', { fontWeight: 700, lineHeight: '108%', letterSpacing: '-0.02em' }],

  'h1-bold': ['52px', { fontWeight: 700, lineHeight: '112%', letterSpacing: '-0.02em' }],
  'h2-bold': ['38px', { fontWeight: 700, lineHeight: '118%', letterSpacing: '-0.01em' }],
  'h3-bold': ['26px', fontWeight.bold130],
  'h4-bold': ['20px', fontWeight.bold130],
  'body-large': ['20px', fontWeight.regular140],
  'body-regular': ['16px', fontWeight.regular140],
  'body-semibold': ['16px', fontWeight.semibold140],
  'body-bold': ['16px', fontWeight.bold140],
  'label-regular': ['14px', fontWeight.regular140],
  'label-medium': ['14px', fontWeight.medium140],
  'label-semibold': ['14px', fontWeight.semibold140],
  'caption-semibold': ['12px', fontWeight.semibold140],
  'caption-medium': ['12px', fontWeight.medium140],
  'caption-regular': ['12px', fontWeight.regular140],
  'buttonL-semibold': ['16px', fontWeight.semibold140],
  'buttonM-semibold': ['14px', fontWeight.semibold140],
  'buttonS-semibold': ['12px', fontWeight.semibold140],

  'h1-bold-mobile': ['34px', fontWeight.bold130],
  'h2-bold-mobile': ['26px', fontWeight.bold130],
  'h3-bold-mobile': ['20px', fontWeight.bold130],
  'h4-bold-mobile': ['17px', fontWeight.bold130],
  'body-regular-mobile': ['14px', fontWeight.regular140],
  'body-medium-mobile': ['14px', fontWeight.medium140],
  'body-bold-mobile': ['14px', fontWeight.bold140],
};

module.exports = {
  content: ['./src/app/**/*.{js,ts,jsx,tsx,mdx}', './src/components/**/*.{js,ts,jsx,tsx,mdx}'],

  theme: {
    extend: {
      typography: {
        DEFAULT: {
          css: {
            color: colors.white,
            a: {
              color: colors.primary[300],
              '&:hover': {
                color: colors.secondary[300],
              },
            },
            h1: {
              color: colors.white,
            },
            h2: {
              color: colors.white,
            },
            h3: {
              color: colors.white,
            },
            h4: {
              color: colors.white,
            },
            p: {
              color: colors.grayscale[300],
            },
            li: {
              color: colors.grayscale[300],
            },
            strong: {
              color: colors.white,
            },
            code: {
              color: colors.white,
            },
          },
        },
      },
      animation: {
        wiggle: 'wiggle 0.5s ease-in-out infinite',
        blink: 'blink 0.75s step-end infinite',
        appear: 'appear 1s ease-in-out forwards',
        progress: 'progress 2.3s ease-in-out',
        shimmer: 'shimmer 2s infinite',
        'float-slow': 'floatSlow 9s ease-in-out infinite',
        'gradient-pan': 'gradientPan 8s ease infinite',
        'pulse-ring': 'pulseRing 3s ease-in-out infinite',
        'fade-up': 'fadeUp 0.7s ease-out forwards',
      },
      fontFamily: {
        Inter: ['var(--font-inter)'],
      },
      backgroundColor: colors,
      opacity: opacities,
      backgroundImage: backgroundImage,
      backgroundSize: {
        'grid-sm': '44px 44px',
        'gradient-200': '200% 200%',
      },

      dropShadow: {
        lg: '15px 15px 15px rgba(0, 0, 0, 0.1)',
        glow: '0 0 24px rgba(46,107,255,0.45)',
      },
      boxShadow: {
        sm: '0px 1px 2px 0px rgba(3, 7, 18, 0.4)', // level-1
        md: '0px 8px 24px -8px rgba(3, 7, 18, 0.55)', // level-2
        lg: '0px 24px 60px -20px rgba(3, 7, 18, 0.7)', // level-3
        glow: '0 0 0 1px rgba(46,107,255,0.35), 0 20px 60px -20px rgba(46,107,255,0.55)',
        'glow-cyan': '0 0 0 1px rgba(34,211,238,0.35), 0 20px 60px -20px rgba(34,211,238,0.45)',
        card: '0 1px 0 0 rgba(255,255,255,0.05) inset, 0 20px 50px -30px rgba(0,0,0,0.9)',
      },
      colors: colors,
      fontSize: font,
      borderRadius: {
        '4xl': '2rem',
      },
      keyframes: {
        appear: {
          '0%': { opacity: 0 },
          '100%': { opacity: 1 },
        },
        blink: {
          '0%, 100%': { opacity: '1' },
          '50%': { opacity: '0' },
        },
        progress: {
          '0%': { width: '0%' },
          '100%': { width: '100%' },
        },
        shimmer: {
          '0%': { transform: 'translateX(-100%)' },
          '100%': { transform: 'translateX(100%)' },
        },
        wiggle: {
          '0%, 100%': { transform: 'rotate(-24deg)' },
          '50%': { transform: 'rotate(24deg)' },
        },
        floatSlow: {
          '0%, 100%': { transform: 'translateY(0px)' },
          '50%': { transform: 'translateY(-16px)' },
        },
        gradientPan: {
          '0%, 100%': { backgroundPosition: '0% 50%' },
          '50%': { backgroundPosition: '100% 50%' },
        },
        pulseRing: {
          '0%, 100%': { opacity: '0.4', transform: 'scale(1)' },
          '50%': { opacity: '0.15', transform: 'scale(1.08)' },
        },
        fadeUp: {
          '0%': { opacity: '0', transform: 'translateY(24px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
      },
      scrollbarHide: {
        '-ms-overflow-style': 'none',
        'scrollbar-width': 'none',
        '&::-webkit-scrollbar': {
          display: 'none',
        },
      },
    },
  },
  plugins: [require('@tailwindcss/typography')],
};
