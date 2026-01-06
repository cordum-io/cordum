import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
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
