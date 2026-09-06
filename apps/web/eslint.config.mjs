// Flat config, consumed by the ESLint CLI directly: Next 16 removed the
// `next lint` command, and `next build` no longer lints.
//
// `eslint-config-next` v16 ships its presets AS flat config arrays, so they are
// spread in as-is. Wrapping them in `FlatCompat` from `@eslint/eslintrc` - which
// is what this file used to do - now throws inside the eslintrc config
// validator, because it tries to validate an already-flat config against the
// legacy schema.
import nextCoreWebVitals from 'eslint-config-next/core-web-vitals';
import nextTypeScript from 'eslint-config-next/typescript';

const eslintConfig = [
  // ESLint ignores node_modules and .git by default; build output is not.
  { ignores: ['.next/**', 'out/**', 'next-env.d.ts'] },
  ...nextCoreWebVitals,
  ...nextTypeScript,
  {
    rules: {
      // Prose in this marketing copy uses apostrophes and quotes freely.
      'react/no-unescaped-entities': 'off',
      '@typescript-eslint/no-unused-vars': [
        'warn',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_' },
      ],
    },
  },
  {
    // tailwind.config.ts is a CommonJS module (`module.exports`), where
    // `require` is the correct idiom rather than a lint violation. Converting
    // 300 lines of design tokens to ESM to satisfy the rule is not worth the
    // risk of breaking how Tailwind loads it.
    files: ['tailwind.config.ts'],
    rules: { '@typescript-eslint/no-require-imports': 'off' },
  },
];

export default eslintConfig;
