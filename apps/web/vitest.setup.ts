// jest-dom's matchers (toBeInTheDocument, toHaveAttribute, ...) on Vitest's
// expect, plus automatic unmount between tests so one test's DOM cannot leak
// into the next.
import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach } from 'vitest';

afterEach(() => {
  cleanup();
});
