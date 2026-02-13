import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "path";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      // Force CJS entry so Rollup's commonjs plugin can transform the require() calls.
      // The "ESM" entry in @dagrejs/dagre v2 uses dynamic require() which Rollup can't handle.
      "@dagrejs/dagre": path.resolve(__dirname, "node_modules/@dagrejs/dagre/dist/dagre.cjs.js"),
    },
  },
  optimizeDeps: {
    include: ["@dagrejs/dagre"],
  },
  build: {
    commonjsOptions: {
      include: [/@dagrejs/, /node_modules/],
    },
  },
  server: {
    port: 5173,
    host: true,
    proxy: {
      "/api/v1": {
        target: "http://localhost:8081",
        changeOrigin: true,
      },
      "/api/v1/stream": {
        target: "ws://localhost:8081",
        ws: true,
      },
    },
  },
});
