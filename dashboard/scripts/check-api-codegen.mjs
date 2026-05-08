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

// Pre-regen check: orval runs with `clean: true` and would silently wipe any
// uncommitted hand-edits in src/api/generated/. Refuse to run if the working
// tree is dirty so a developer doesn't lose work without seeing the warning.
const preStatus = spawnSync(
  "git",
  ["status", "--porcelain", "--", "src/api/generated/"],
  { encoding: "utf8", shell: process.platform === "win32" },
);
const preDrift = (preStatus.stdout || "").trim();
if (preDrift.length > 0) {
  console.error(
    "[check-api-codegen] uncommitted changes in src/api/generated/:\n" +
      preDrift +
      "\n  Commit or revert first — `pnpm run generate-api` would overwrite them.",
  );
  process.exit(1);
}

console.log("[check-api-codegen] regenerating src/api/generated/");
run("generate-api", "pnpm", ["run", "generate-api"]);

console.log("[check-api-codegen] checking generated tree against committed state");
// `git status --porcelain` covers modifications, deletions, AND untracked
// files — important when the spec adds a new tag and orval emits a
// previously-uncommitted file.
const status = spawnSync(
  "git",
  ["status", "--porcelain", "--", "src/api/generated/"],
  { encoding: "utf8", shell: process.platform === "win32" },
);
const drift = (status.stdout || "").trim();

if (drift.length > 0) {
  console.error("[check-api-codegen] DRIFT detected in src/api/generated/:");
  console.error(drift);
  console.error(
    "\n  Run `pnpm run generate-api` locally, commit the updated generated/ tree, and re-push.",
  );
  process.exit(1);
}

console.log("[check-api-codegen] OK — generated tree matches the spec");
