#!/usr/bin/env node
/*
 * CI gate: regenerate src/api/generated/ and fail if it differs from
 * what is committed. Catches:
 *   - generated files not regenerated after a spec change
 *   - hand-edits to generated files
 *   - orval-version drift between dev and CI
 *
 * Local fix when this fails: `pnpm run generate-api && git add src/api/generated/`.
 */
import { spawnSync } from "node:child_process";

function run(label, cmd, args) {
  const result = spawnSync(cmd, args, {
    stdio: "inherit",
    shell: process.platform === "win32",
  });
  if (result.status !== 0) {
    console.error(`[check-api-codegen] ${label} failed (exit ${result.status})`);
    process.exit(result.status ?? 1);
  }
}

console.log("[check-api-codegen] regenerating src/api/generated/");
run("generate-api", "pnpm", ["run", "generate-api"]);

console.log("[check-api-codegen] checking generated tree against committed state");
const diff = spawnSync(
  "git",
  ["diff", "--exit-code", "--", "src/api/generated/"],
  { stdio: "inherit", shell: process.platform === "win32" },
);

if (diff.status !== 0) {
  console.error(
    "[check-api-codegen] DRIFT: src/api/generated/ differs from regenerated output.\n" +
      "  Run `pnpm run generate-api` locally, commit the updated generated/ tree, and re-push.",
  );
  process.exit(1);
}

console.log("[check-api-codegen] OK — generated tree matches the spec");
