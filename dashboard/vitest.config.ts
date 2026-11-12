import { defineConfig } from 'vitest/config';
import path from 'path';
import { isLostConsoleLogAtTeardown } from './src/test-utils/vitestTeardown';

export default defineConfig({
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
      jspdf: path.resolve(__dirname, './src/test-stubs/jspdf.ts'),
      html2canvas: path.resolve(__dirname, './src/test-stubs/html2canvas.ts'),
      '@monaco-editor/react': path.resolve(__dirname, './src/test-stubs/monaco-react.tsx'),
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test-utils/setup.ts'],
    // Vitest flushes console writes through an onUserConsoleLog RPC nobody awaits; teardown
    // rejects one still pending (EnvironmentTeardownError), losing a late log line, not a test.
    // Ignore exactly that; every other unhandled error still fails the run. The match is exact,
    // so a reworded vitest message fails visibly (task-04eef2b3).
    onUnhandledError: (error) => !isLostConsoleLogAtTeardown(error),
  },
});
