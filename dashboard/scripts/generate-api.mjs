#!/usr/bin/env node
/*
 * Regenerate src/api/generated/ from cordum/docs/api/openapi/cordum-api.yaml.
 * Configuration lives in dashboard/orval.config.ts.
 *
 * Failure modes:
 *   1 — spec parse error or schema validation failure (orval prints details).
 *   2 — orval binary not found (run `pnpm install` first).
 */
import { spawn } from "node:child_process";

const child = spawn("pnpm", ["exec", "orval", "--config", "./orval.config.ts"], {
  stdio: "inherit",
  shell: process.platform === "win32",
});

child.on("error", (err) => {
  console.error("[generate-api] failed to spawn orval:", err.message);
  process.exit(2);
});

child.on("exit", (code, signal) => {
  if (signal) {
    console.error(`[generate-api] killed by signal ${signal}`);
    process.exit(1);
  }
  process.exit(code ?? 1);
});
