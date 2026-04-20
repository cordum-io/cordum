import { mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { spawnSync } from "node:child_process";

const specPath = resolve("..", "..", "docs", "api", "openapi", "cordum-api.yaml");
const outputPath = resolve("src", "_generated", "schema.d.ts");
const cliPath = resolve("node_modules", "openapi-typescript", "bin", "cli.js");

mkdirSync(dirname(outputPath), { recursive: true });

const args = [
  cliPath,
  specPath,
  "--output",
  outputPath,
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
