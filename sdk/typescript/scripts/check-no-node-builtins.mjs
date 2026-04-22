import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";

const distDir = new URL("../dist", import.meta.url);
const distPath = fileURLToPath(distDir);
const files = readdirSync(distPath).filter((file) => file.endsWith(".mjs"));
const forbiddenPatterns = [
  /\brequire\(["']node:/,
  /\bBuffer\b/,
  /\bprocess\./,
];

let failed = false;

for (const file of files) {
  const fullPath = join(distPath, file);
  const content = readFileSync(fullPath, "utf8");
  for (const pattern of forbiddenPatterns) {
    if (pattern.test(content)) {
      console.error(`Forbidden browser bundle pattern ${pattern} found in ${file}`);
      failed = true;
    }
  }
}

if (failed) {
  process.exitCode = 1;
} else {
  console.log("No forbidden Node builtins leaked into dist/*.mjs");
}
