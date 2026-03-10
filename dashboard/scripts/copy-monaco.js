import fs from "node:fs";
import path from "node:path";

function copyDir(src, dest) {
  fs.rmSync(dest, { recursive: true, force: true });
  fs.mkdirSync(dest, { recursive: true });
  fs.cpSync(src, dest, { recursive: true });
}

const root = process.cwd();
const src = path.join(root, "node_modules", "monaco-editor", "min", "vs");
const dest = path.join(root, "public", "monaco", "vs");

if (!fs.existsSync(src)) {
  console.error(`[copy-monaco] missing ${src}`);
  process.exit(1);
}

copyDir(src, dest);
console.log(`[copy-monaco] copied to ${dest}`);
