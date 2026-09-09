import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    include: ['src/**/*.test.ts', '../protocol/src/**/*.test.ts'],
    environment: 'node',
  },
});
