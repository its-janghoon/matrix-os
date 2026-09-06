/// <reference types="vitest" />
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

// Vitest rather than Jest: it reads tsconfig paths and JSX through Vite with no
// Babel config of its own, so it does not need to be taught the Next 16 /
// React 19 / Tailwind v4 toolchain. It also runs entirely beside `next build`
// and `eslint` - nothing here is wired into either, which is what kept a test
// runner off this project until now.
export default defineConfig({
  plugins: [react()],
  // Vite 8 resolves tsconfig `paths` natively, so the `@/*` alias needs no
  // plugin and no second copy of the mapping here.
  resolve: { tsconfigPaths: true },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./vitest.setup.ts'],
    // Only our own tests. Without this, Vitest would also try to collect from
    // node_modules and from Next's build output.
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
    exclude: ['node_modules/**', '.next/**'],
    css: false,
    restoreMocks: true,
  },
});
