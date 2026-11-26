import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { spawnSync } from "node:child_process";

const specPath = resolve("..", "..", "docs", "api", "openapi", "cordum-api.yaml");
const committedPath = resolve("src", "_generated", "schema.d.ts");
const cliPath = resolve("node_modules", "openapi-typescript", "bin", "cli.js");
const tempDir = mkdtempSync(join(tmpdir(), "cordum-sdk-typescript-"));
const tempPath = join(tempDir, "schema.d.ts");

// Report failures through process.exitCode and let control reach the finally below. Calling
// the process exit function directly ends the process at once and skips the finally, which
// used to leak tempDir.
try {
  const args = [
    cliPath,
    specPath,
    "--output",
    tempPath,
    "--alphabetize",
    "--empty-objects-unknown",
    "--make-paths-enum",
  ];

  const result = spawnSync(process.execPath, args, { stdio: "inherit" });
  if (result.error) {
    throw result.error;
  }

  if (result.status !== 0) {
    // status is null when a signal killed the generator: fail closed instead of reading a
    // missing or partial temp file.
    console.error(`openapi-typescript failed (${result.signal ?? `exit ${result.status}`}).`);
    process.exitCode = result.status ?? 1;
  } else {
    const committed = readFileSync(committedPath, "utf8");
    const generated = readFileSync(tempPath, "utf8");

    if (committed !== generated) {
      console.error("Generated schema.d.ts is out of date. Run `npm run generate`.");
      process.exitCode = 1;
    }
  }
} finally {
  // maxRetries covers the transient EBUSY/EPERM Windows raises on a file it just saw written.
  rmSync(tempDir, { recursive: true, force: true, maxRetries: 3 });
}
