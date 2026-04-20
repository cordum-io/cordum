import { defineConfig } from 'vitest/config';
import path from 'path';

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
  },
});
