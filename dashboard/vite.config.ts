import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { visualizer } from "rollup-plugin-visualizer";
import fs from "fs";
import path from "path";

// Dev proxy target: the compose gateway serves HTTPS only, signed by the dev
// CA from `cordumctl generate-certs` (repo-root certs/). For a localhost
// target, trust that CA; any other target uses Node's default trust store
// (add a private CA with NODE_EXTRA_CA_CERTS).
const apiTarget = process.env.VITE_API_TARGET || "https://localhost:8081";
const devCAPath = path.resolve(__dirname, "../certs/ca/ca.crt");
const apiTargetCA =
  /^https:\/\/(localhost|127\.0\.0\.1|\[::1\])(:\d+)?(\/|$)/.test(apiTarget) &&
  fs.existsSync(devCAPath)
    ? fs.readFileSync(devCAPath, "utf8")
    : undefined;

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    // Phase 5d (task-50bbfd7d): emit dist/stats.html on every build so CI can
    // parse per-chunk size and post a soft-threshold report on PRs.
    // emitFile keeps stats.html inside dist/; open=false suppresses
    // auto-open in dev. Cast to any because rollup-plugin-visualizer's
    // PluginOption is not the same shape as Vite's plugins[] entry.
    visualizer({
      // emitFile=true makes filename relative to Vite's output dir (dist/),
      // so the path is plain "stats.html" — adding a "dist/" prefix would
      // produce dist/dist/stats.html.
      filename: "stats.html",
      template: "treemap",
      gzipSize: true,
      brotliSize: true,
      emitFile: true,
      open: false,
    }) as never,
  ],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
      // Force CJS entries so Rollup's commonjs plugin can statically
      // resolve require("@dagrejs/graphlib"). The ESM build of dagre
      // wraps the require in a dynamic helper that Rollup cannot analyse.
      "@dagrejs/dagre": path.resolve(
        __dirname,
        "node_modules/@dagrejs/dagre/dist/dagre.cjs",
      ),
      "@dagrejs/graphlib": path.resolve(
        __dirname,
        "node_modules/@dagrejs/graphlib/dist/graphlib.cjs",
      ),
    },
  },
  build: {
    commonjsOptions: {
      include: [/node_modules/],
      dynamicRequireTargets: [
        "node_modules/@dagrejs/graphlib/**/*.js",
      ],
    },
  },
  optimizeDeps: {
    include: ["@dagrejs/dagre", "@dagrejs/graphlib"],
  },
  server: {
    allowedHosts: true,
    proxy: {
      "/api": {
        target: apiTarget,
        changeOrigin: true,
        // /api/v1/stream is a WebSocket.
        ws: true,
        ca: apiTargetCA,
      },
    },
  },
  test: {
    environment: "jsdom",
  },
});
