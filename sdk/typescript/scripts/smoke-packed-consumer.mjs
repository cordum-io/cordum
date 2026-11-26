import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readdirSync, realpathSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { isAbsolute, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const sdkRoot = realpathSync.native(fileURLToPath(new URL("../", import.meta.url)));
const repoRoot = realpathSync.native(resolve(sdkRoot, "../.."));
const npmExecPath = process.env.npm_execpath;
assert(npmExecPath, "run via npm run smoke:packed");

function isWithin(root, target) {
  const canonical = (path) => {
    const actual = realpathSync.native(path);
    return process.platform === "win32" ? actual.toLowerCase() : actual;
  };
  const path = relative(canonical(root), canonical(target));
  return path !== ".." && !path.startsWith(`..${sep}`) && !isAbsolute(path);
}

function run(phase, args, cwd) {
  const result = spawnSync(process.execPath, args, {
    cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"],
    timeout: 300000, maxBuffer: 10 * 1024 * 1024,
  });
  if (result.error || result.signal || result.status !== 0) {
    const cause = result.error?.message ?? result.signal ?? `exit ${result.status}`;
    throw new Error(`${phase} failed (${cause})\n${result.stdout ?? ""}${result.stderr ?? ""}`);
  }
  return result.stdout;
}

function writeRuntime(consumer) {
  writeFileSync(join(consumer, "runtime.mjs"), `
import assert from "node:assert/strict";
import { realpathSync } from "node:fs";
import { createRequire } from "node:module";
import { isAbsolute, join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";
import * as esm from "@cordum/sdk";
${isWithin.toString()}
const require = createRequire(import.meta.url);
const cjs = require("@cordum/sdk");
const root = fileURLToPath(new URL(".", import.meta.url));
const dist = join(root, "node_modules", "@cordum", "sdk", "dist");
assert(isWithin(root, dist), "installed dist escaped consumer");
for (const resolved of [fileURLToPath(import.meta.resolve("@cordum/sdk")), require.resolve("@cordum/sdk")]) {
  assert(isWithin(dist, resolved), "runtime resolved outside installed dist: " + resolved);
}
for (const sdk of [esm, cjs]) {
  assert.equal(typeof sdk.CordumClient, "function");
  assert.equal(typeof sdk.createGeneratedClient, "function");
  const client = sdk.createGeneratedClient({ baseUrl: "http://127.0.0.1:9" });
  assert.equal(typeof client.GET, "function");
  assert.equal(typeof client.POST, "function");
}
`);
}

function writeTypes(consumer) {
  writeFileSync(join(consumer, "tsconfig.json"), JSON.stringify({
    compilerOptions: {
      strict: true, noEmit: true, module: "NodeNext", moduleResolution: "NodeNext",
      lib: ["ES2022", "DOM", "DOM.Iterable"], types: [], skipLibCheck: false,
    },
    files: ["consumer.ts"],
  }));
  writeFileSync(join(consumer, "consumer.ts"), `
import { CordumClient, createGeneratedClient, type paths, type operations } from "@cordum/sdk";
export type Client = CordumClient;
export type EdgeEvents = operations["ingestEdgeLLMEvents"];
export type MemoryResolve = operations["resolveMemoryResource"];
export type EdgePath = paths["/api/v1/edge/llm/events"]["post"];
export type MemoryPath = paths["/api/v1/memory/resolve"]["post"];
export async function checkGeneratedRoutes() {
  const client = createGeneratedClient({ baseUrl: "http://127.0.0.1:9" });
  // @ts-expect-error an ungenerated route must be rejected, not silently any-typed
  await client.POST("/api/v1/not-a-generated-route", {});
}
`);
}

function checkTypes(consumer) {
  const compiler = join(sdkRoot, "node_modules", "typescript");
  const args = [join(compiler, "bin", "tsc"), "-p", join(consumer, "tsconfig.json")];
  run("consumer type check", args, consumer);
  const files = run("consumer type file list", [...args, "--listFilesOnly"], consumer)
    .trim().split(/\r?\n/).filter(Boolean);
  assert(files.length > 0, "compiler listed no files");
  for (const file of files) {
    assert(isWithin(consumer, file) || isWithin(join(compiler, "lib"), file),
      `compiler resolved outside consumer and pinned compiler libraries: ${file}`);
  }
  return files.length;
}

function smokePacked(work) {
  assert(!isWithin(repoRoot, work), "temporary consumer must be outside the source tree");
  const pack = join(work, "pack");
  const consumer = join(work, "consumer");
  mkdirSync(pack);
  mkdirSync(consumer);
  run("npm pack", [npmExecPath, "pack", "--pack-destination", pack], sdkRoot);
  const tarballs = readdirSync(pack).filter((name) => name.endsWith(".tgz"));
  assert.equal(tarballs.length, 1, "expected exactly one packed tarball");
  const tarball = join(pack, tarballs[0]);
  writeFileSync(join(consumer, "package.json"), JSON.stringify({
    name: "cordum-sdk-packed-consumer", private: true, type: "module",
  }));
  run("offline consumer install", [npmExecPath, "install", "--offline", "--no-audit",
    "--no-fund", "--ignore-scripts", "--cache", join(work, "npm-cache"), tarball], consumer);
  writeRuntime(consumer);
  run("consumer ESM/CJS runtime", [join(consumer, "runtime.mjs")], consumer);
  writeTypes(consumer);
  const files = checkTypes(consumer);
  console.log(`packed consumer: ${tarballs[0]} (${statSync(tarball).size} bytes), runtime OK, type check OK, ${files} files checked`);
}

const work = mkdtempSync(join(realpathSync.native(tmpdir()), "cordum-sdk-consumer-"));
try {
  smokePacked(work);
} finally {
  rmSync(work, { recursive: true, force: true });
}
