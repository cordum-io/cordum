#!/usr/bin/env node
// compare_verdicts.mjs — reads the three per-harness verdict files,
// reports divergences. Exit 0 when every harness agreed with every
// scenario's want_pass; exit 1 on any divergence.

import { promises as fs } from 'node:fs';

const argv = parseArgs(process.argv.slice(2));
for (const k of ['go', 'python', 'typescript']) {
  if (!argv[k]) {
    console.error(`usage: compare_verdicts.mjs --go FILE --python FILE --typescript FILE`);
    process.exit(2);
  }
}

const sets = {
  go: await readVerdicts(argv.go),
  python: await readVerdicts(argv.python),
  typescript: await readVerdicts(argv.typescript),
};

const names = new Set();
for (const arr of Object.values(sets)) for (const v of arr) names.add(v.name);

const divergences = [];
for (const name of names) {
  const g = findByName(sets.go, name);
  const p = findByName(sets.python, name);
  const t = findByName(sets.typescript, name);
  if (!g || !p || !t) {
    divergences.push({ name, reason: `missing from one or more harnesses (go=${!!g} python=${!!p} typescript=${!!t})` });
    continue;
  }
  const passes = [g.passed, p.passed, t.passed];
  const allAgree = passes.every((x) => x === passes[0]);
  if (!allAgree) {
    divergences.push({
      name,
      reason: `harnesses disagreed: go=${g.passed} python=${p.passed} typescript=${t.passed}`,
      go_error: g.error,
      python_error: p.error,
      typescript_error: t.error,
    });
    continue;
  }
  // All three agreed with each other — now verify agreement with want.
  const want = g.want_pass;
  if (g.passed !== want) {
    divergences.push({
      name,
      reason: `harnesses agreed ${g.passed} but want=${want}`,
    });
  }
}

if (divergences.length) {
  console.error(`parity: ${divergences.length} divergence(s) across ${names.size} scenarios:`);
  for (const d of divergences) {
    console.error(`  ${d.name}: ${d.reason}`);
    if (d.go_error) console.error(`    go:         ${d.go_error}`);
    if (d.python_error) console.error(`    python:     ${d.python_error}`);
    if (d.typescript_error) console.error(`    typescript: ${d.typescript_error}`);
  }
  process.exit(1);
}
console.log(`parity: all ${names.size} scenarios agreed across go/python/typescript + matched want_pass`);

// --- helpers -------------------------------------------------------

async function readVerdicts(path) {
  const raw = await fs.readFile(path, 'utf8');
  return JSON.parse(raw);
}

function findByName(arr, name) {
  return arr.find((v) => v.name === name) || null;
}

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
