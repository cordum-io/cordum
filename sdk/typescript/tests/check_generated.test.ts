import { afterEach, describe, expect, it } from "vitest";
import { spawnSync } from "node:child_process";
import type { SpawnSyncReturns } from "node:child_process";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

// Black-box tests for scripts/check-generated.mjs. The script resolves every path from
// process.cwd() and creates its scratch dir under os.tmpdir(), so a throwaway fixture tree
// plus a redirected temp root exercises every exit path without touching the repository.
const SCRIPT = fileURLToPath(new URL("../scripts/check-generated.mjs", import.meta.url));
const TEMP_PREFIX = "cordum-sdk-typescript-";
const GENERATOR_FLAGS = ["--alphabetize", "--empty-objects-unknown", "--make-paths-enum"];

// Stand-in for openapi-typescript: records its argv, then either exits with the code named
// by a spec of the form "exit <n>" or copies the spec to the --output path.
const FAKE_GENERATOR = [
  '"use strict";',
  'const fs = require("node:fs");',
  'const path = require("node:path");',
  "const argv = process.argv.slice(2);",
  'fs.writeFileSync(path.join(__dirname, "argv.json"), JSON.stringify(argv));',
  'const spec = fs.readFileSync(argv[0], "utf8");',
  "const failure = /^exit (\\d+)$/.exec(spec);",
  "if (failure) process.exit(Number(failure[1]));",
  'fs.writeFileSync(argv[argv.indexOf("--output") + 1], spec);',
].join("\n");

interface CheckRun {
  result: SpawnSyncReturns<string>;
  tempRoot: string;
  committedPath: string;
  argv: string[] | null;
}

const roots: string[] = [];

afterEach(() => {
  for (const root of roots.splice(0)) {
    rmSync(root, { recursive: true, force: true, maxRetries: 3 });
  }
});

function write(path: string, content: string): void {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, content);
}

// os.tmpdir() reads TMPDIR on POSIX and TEMP/TMP on Windows. Drop every spelling first so
// Windows' case-insensitive environment never ends up with two keys for the same variable.
function childEnv(tempRoot: string): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = {};
  for (const [key, value] of Object.entries(process.env)) {
    if (!/^(TMPDIR|TEMP|TMP)$/i.test(key)) {
      env[key] = value;
    }
  }
  return { ...env, TMPDIR: tempRoot, TEMP: tempRoot, TMP: tempRoot };
}

function readArgv(path: string): string[] | null {
  if (!existsSync(path)) {
    return null;
  }
  const parsed: unknown = JSON.parse(readFileSync(path, "utf8"));
  if (!Array.isArray(parsed) || !parsed.every((entry): entry is string => typeof entry === "string")) {
    throw new Error(`fake generator wrote an unexpected argv.json: ${JSON.stringify(parsed)}`);
  }
  return parsed;
}

function runCheck(fixture: { spec: string; committed: string | null }): CheckRun {
  const root = mkdtempSync(join(tmpdir(), "check-generated-test-"));
  roots.push(root);

  const cwd = join(root, "sdk", "typescript");
  const generatorDir = join(cwd, "node_modules", "openapi-typescript");
  const committedPath = join(cwd, "src", "_generated", "schema.d.ts");
  const tempRoot = join(root, "tmp");

  write(join(root, "docs", "api", "openapi", "cordum-api.yaml"), fixture.spec);
  if (fixture.committed !== null) {
    write(committedPath, fixture.committed);
  }
  write(join(generatorDir, "package.json"), '{"type":"commonjs"}\n');
  write(join(generatorDir, "bin", "cli.js"), FAKE_GENERATOR);
  mkdirSync(tempRoot);

  const result = spawnSync(process.execPath, [SCRIPT], {
    cwd,
    env: childEnv(tempRoot),
    encoding: "utf8",
  });

  return {
    result,
    tempRoot,
    committedPath,
    argv: readArgv(join(generatorDir, "bin", "argv.json")),
  };
}

// The script must leave nothing in its temp root, and the committed schema must be as we wrote it.
function expectCleanAndUntouched(run: CheckRun, committed: string | null): void {
  expect(readdirSync(run.tempRoot)).toEqual([]);
  if (committed === null) {
    expect(existsSync(run.committedPath)).toBe(false);
  } else {
    expect(readFileSync(run.committedPath, "utf8")).toBe(committed);
  }
}

// Non-vacuity guard: proves the script really made its scratch dir inside the redirected temp
// root (so the empty-root assertion cannot pass just because the script ignored TMPDIR/TEMP/TMP)
// and that the generator got the same arguments as before.
function expectGeneratorInvocation(run: CheckRun): void {
  if (run.argv === null) {
    throw new Error("the fake generator never ran");
  }
  const [specArg, outputFlag, out, ...flags] = run.argv;
  if (specArg === undefined || out === undefined) {
    throw new Error(`generator argv too short: ${JSON.stringify(run.argv)}`);
  }
  expect(basename(specArg)).toBe("cordum-api.yaml");
  expect(outputFlag).toBe("--output");
  expect(flags).toEqual(GENERATOR_FLAGS);
  expect(basename(out)).toBe("schema.d.ts");
  expect(basename(dirname(out)).startsWith(TEMP_PREFIX)).toBe(true);
  expect(dirname(dirname(out))).toBe(run.tempRoot);
  expect(existsSync(dirname(out))).toBe(false);
}

describe("check-generated.mjs", () => {
  it("exits 0 and removes its temp dir when the committed schema matches", { timeout: 30_000 }, () => {
    const schema = "export type Fixture = 1;\n";
    const run = runCheck({ spec: schema, committed: schema });

    expect(run.result.status).toBe(0);
    expectCleanAndUntouched(run, schema);
    expectGeneratorInvocation(run);
    expect(run.result.stderr).not.toContain("out of date");
    expect(run.result.stderr).not.toContain("openapi-typescript failed");
  });

  it("exits 1 and removes its temp dir on a one-byte drift", { timeout: 30_000 }, () => {
    const spec = "export type Fixture = 1;\n";
    const committed = `${spec}\n`;
    const run = runCheck({ spec, committed });

    expect(run.result.status).toBe(1);
    expectCleanAndUntouched(run, committed);
    expectGeneratorInvocation(run);
    expect(run.result.stderr).toContain("Generated schema.d.ts is out of date");
  });

  it("returns the generator's own exit code and removes its temp dir when it fails", { timeout: 30_000 }, () => {
    const committed = "export type Fixture = 1;\n";
    const run = runCheck({ spec: "exit 7", committed });

    expect(run.result.status).toBe(7);
    expectCleanAndUntouched(run, committed);
    expectGeneratorInvocation(run);
    expect(run.result.stderr).toContain("openapi-typescript failed (exit 7)");
    expect(run.result.stderr).not.toContain("out of date");
  });

  it("exits 1 and removes its temp dir when the committed schema is missing", { timeout: 30_000 }, () => {
    const run = runCheck({ spec: "export type Fixture = 1;\n", committed: null });

    expect(run.result.status).toBe(1);
    expectCleanAndUntouched(run, null);
    expectGeneratorInvocation(run);
    expect(run.result.stderr).toContain("ENOENT");
  });
});
