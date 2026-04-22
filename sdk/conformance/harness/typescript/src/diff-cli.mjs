#!/usr/bin/env node
// diff-cli: reads parity scenarios from stdin, emits per-case verdict
// JSON on stdout. Used by parity/run.sh to cross-check this
// harness's diff against Go + Python.
//
//   node src/diff-cli.mjs < parity/scenarios.json > verdicts-typescript.json

import { diff, DiffError } from './diff.mjs';

async function readStdin() {
  const chunks = [];
  for await (const chunk of process.stdin) chunks.push(chunk);
  return Buffer.concat(chunks).toString('utf8');
}

async function main() {
  const raw = await readStdin();
  const scenarios = JSON.parse(raw);
  const verdicts = scenarios.map((s) => {
    const want = Boolean(s.want_pass);
    let passed = true;
    let err = '';
    try {
      diff(s.actual, s.expected, '$');
    } catch (e) {
      if (!(e instanceof DiffError)) throw e;
      passed = false;
      err = e.message;
    }
    return {
      name: s.name,
      passed,
      error: err,
      want_pass: want,
      agreed_with_want: passed === want,
    };
  });
  process.stdout.write(JSON.stringify(verdicts, null, 2) + '\n');
}

main().catch((err) => {
  process.stderr.write(`diff-cli: ${err.stack || err.message}\n`);
  process.exit(2);
});
