const colors = {
  background: '#F7F9FA',

  primary: {
    DEFAULT: '#0066FF',
    100: '#EBF0FF',
    200: '#B3D1FF',
    300: '#3385FF',
    400: '#0066FF',
    500: '#0052CC',
    600: '#002966',
  },

  secondary: {
    DEFAULT: '#7C24FF',
    100: '#F2E9FF',
    200: '#D8BDFF',
    300: '#9650FF',
    400: '#7C24FF',
    500: '#631DCC',
    600: '#320E66',
  },

  accent: {
    DEFAULT: '#F97F6F',
    50: '#FBF8F7',
    100: '#F6E9E7',
    200: '#F97F6F',
    300: '#F05743',
    400: '#DD3A25',
    500: '#BA2D1B',
  },

  grayscale: {
    100: '#F7F9FB',
    200: '#EDF2F7',
    300: '#E2E8F0',
    400: '#CBD5E0',
    500: '#A0AEC0',
    600: '#718096',
    700: '#4A5568',
    800: '#2D3748',
    900: '#010820',
  },

  /** Semantic */
  semantic: {
    success: '#00AB44',
    error: '#E4000A',
    info: '#2D81FF',
    processing: '#FF8409',
  },

  white: '#FFFFFF',
  black: '#010820',
};

const backgroundImage = {
  'decorative-1': 'linear-gradient(101deg, #0066FF 0%, #7C24FF 100.01%)',
  'decorative-2': 'linear-gradient(96deg, #0066FF 0%, #FF43E0 100%)',
  'decorative-3': 'linear-gradient(0deg, #2D81FF 0%, #C684F5 100%)',
  'decorative-4': 'linear-gradient(180deg, #C684F5 0%, #FF8409 100%)',
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
  'h1-bold': ['48px', fontWeight.bold130],
  'h2-bold': ['32px', fontWeight.bold130],
  'h3-bold': ['24px', fontWeight.bold130],
  'h4-bold': ['18px', fontWeight.bold130],
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

  'h1-bold-mobile': ['32px', fontWeight.bold130],
  'h2-bold-mobile': ['24px', fontWeight.bold130],
  'h3-bold-mobile': ['18px', fontWeight.bold130],
  'h4-bold-mobile': ['16px', fontWeight.bold130],
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
              color: colors.primary.DEFAULT,
              '&:hover': {
                color: colors.primary[300],
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
      },
      fontFamily: {
        Inter: ['var(--font-inter)'],
      },
      backgroundColor: colors,
      opacity: opacities,
      backgroundImage: backgroundImage,

      dropShadow: {
        lg: '15px 15px 15px rgba(0, 0, 0, 0.1)',
      },
      boxShadow: {
        sm: '0px 0px 8px 0px rgba(45, 55, 72, 0.08)', // level-1
        md: '0px 0px 16px 0px rgba(45, 55, 72, 0.08)', // level-2
        lg: '0px 0px 16px 0px rgba(45, 55, 72, 0.16)', // level-3
      },
      colors: colors,
      fontSize: font,
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
