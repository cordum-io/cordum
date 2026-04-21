#!/usr/bin/env node
// Fixture validator — ships with the conformance suite so CI catches
// a broken fixture BEFORE the simulator sees it.
//
// Checks per fixture:
//   1. `schemaVersion === 1` (future v2 migrations stage safely).
//   2. Every declared `operationId` exists in the OpenAPI spec when
//      `--operation-spec` is set. Missing ops = typo or spec drift.
//   3. Every step's `kind` is one of the 5 known kinds.
//
// Exits non-zero on the first failing fixture so CI surfaces the
// problem file path cleanly.

import { promises as fs } from 'node:fs';
import path from 'node:path';

const KNOWN_STEP_KINDS = new Set([
  'request', 'assert_error', 'sleep', 'stream', 'paginate',
]);

const argv = parseArgs(process.argv.slice(2));
if (!argv.fixtures) {
  console.error('usage: validate_fixtures.mjs --fixtures DIR [--operation-spec FILE]');
  process.exit(2);
}

const operationIds = argv['operation-spec']
  ? await loadOperationIds(argv['operation-spec'])
  : null;

const failures = [];
let total = 0;
for await (const fxPath of walkJson(argv.fixtures)) {
  total += 1;
  const raw = await fs.readFile(fxPath, 'utf8');
  let fx;
  try {
    fx = JSON.parse(raw);
  } catch (err) {
    failures.push({ path: fxPath, reason: `not valid JSON: ${err.message}` });
    continue;
  }
  if (fx.schemaVersion !== 1) {
    failures.push({ path: fxPath, reason: `schemaVersion=${fx.schemaVersion} (want 1)` });
    continue;
  }
  for (const [i, step] of (fx.steps || []).entries()) {
    if (!KNOWN_STEP_KINDS.has(step.kind)) {
      failures.push({ path: fxPath, reason: `step ${i}: unknown kind ${JSON.stringify(step.kind)}` });
    }
    if (step.operationId && operationIds && !operationIds.has(step.operationId)) {
      failures.push({ path: fxPath, reason: `step ${i}: operationId ${JSON.stringify(step.operationId)} not in OpenAPI spec` });
    }
  }
}

if (failures.length) {
  console.error(`validate-fixtures: ${failures.length} failure(s) across ${total} fixtures:`);
  for (const f of failures) {
    console.error(`  ${f.path}: ${f.reason}`);
  }
  process.exit(1);
}
console.log(`validate-fixtures: ${total} fixtures ok`);

// --- helpers -------------------------------------------------------

function parseArgs(args) {
  const out = {};
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a.startsWith('--')) {
      const key = a.slice(2);
      const val = args[i + 1];
      if (val && !val.startsWith('--')) {
        out[key] = val;
        i += 1;
      } else {
        out[key] = 'true';
      }
    }
  }
  return out;
}

async function* walkJson(root) {
  const entries = await fs.readdir(root, { withFileTypes: true });
  entries.sort((a, b) => a.name.localeCompare(b.name));
  for (const e of entries) {
    const p = path.join(root, e.name);
    if (e.isDirectory()) {
      yield* walkJson(p);
    } else if (e.isFile() && p.endsWith('.json')) {
      yield p;
    }
  }
}

async function loadOperationIds(specPath) {
  // Minimal YAML operationId extractor — the spec is large and a
  // full YAML parse pulls in a dep. We regex-scan for `operationId:`
  // which is unambiguous in the spec's shape.
  try {
    const raw = await fs.readFile(specPath, 'utf8');
    const ids = new Set();
    const re = /operationId:\s*([A-Za-z][A-Za-z0-9_]*)/g;
    let m;
    while ((m = re.exec(raw))) ids.add(m[1]);
    return ids;
  } catch (err) {
    console.error(`validate-fixtures: failed to read OpenAPI spec at ${specPath}: ${err.message}`);
    return null;
  }
}
