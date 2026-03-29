import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "path";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
      // Force CJS entries so Rollup's commonjs plugin can statically
      // resolve require("@dagrejs/graphlib"). The ESM build of dagre
      // wraps the require in a dynamic helper that Rollup cannot analyse.
      "@dagrejs/dagre": path.resolve(
        __dirname,
        "node_modules/@dagrejs/dagre/dist/dagre.cjs.js",
      ),
      "@dagrejs/graphlib": path.resolve(
        __dirname,
        "node_modules/@dagrejs/graphlib/dist/graphlib.cjs.js",
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
        target: process.env.VITE_API_TARGET || "http://localhost:8080",
        changeOrigin: true,
      },
    },
  },
});
