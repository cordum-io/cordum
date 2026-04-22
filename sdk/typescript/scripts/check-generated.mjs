import { mkdtempSync, mkdirSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { spawnSync } from "node:child_process";

const specPath = resolve("..", "..", "docs", "api", "openapi", "cordum-api.yaml");
const committedPath = resolve("src", "_generated", "schema.d.ts");
const cliPath = resolve("node_modules", "openapi-typescript", "bin", "cli.js");
const tempDir = mkdtempSync(join(tmpdir(), "cordum-sdk-typescript-"));
const tempPath = join(tempDir, "schema.d.ts");

mkdirSync(dirname(tempPath), { recursive: true });

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
  if (typeof result.status === "number" && result.status !== 0) {
    process.exit(result.status);
  }
  if (result.error) {
    throw result.error;
  }

  const committed = readFileSync(committedPath, "utf8");
  const generated = readFileSync(tempPath, "utf8");

  if (committed !== generated) {
    console.error("Generated schema.d.ts is out of date. Run `npm run generate`.");
    process.exit(1);
  }
} finally {
  rmSync(tempDir, { recursive: true, force: true });
}
