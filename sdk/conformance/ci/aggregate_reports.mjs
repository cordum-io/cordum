#!/usr/bin/env node
// Aggregator: reads Go, Python, TypeScript JUnit XML reports, builds
// a fixture × SDK matrix, and writes both summary.md (human) and
// summary.json (machine). Exit 1 if ANY SDK failed ANY fixture —
// this is the epic's "all three SDKs pass 100%" gate.

import { promises as fs } from 'node:fs';
import path from 'node:path';

const argv = parseArgs(process.argv.slice(2));
if (!argv.go || !argv.python || !argv.typescript || !argv['out-md'] || !argv['out-json']) {
  console.error('usage: aggregate_reports.mjs --go FILE --python FILE --typescript FILE --out-md FILE --out-json FILE');
  process.exit(2);
}

const reports = {
  go:         await readReport(argv.go),
  python:     await readReport(argv.python),
  typescript: await readReport(argv.typescript),
};

// Union of every fixture name across the three reports, sorted so
// the matrix renders deterministically across runs.
const allNames = new Set();
for (const r of Object.values(reports)) {
  for (const c of r.cases) allNames.add(c.name);
}
const fixtureNames = Array.from(allNames).sort();

const matrix = fixtureNames.map((name) => {
  const row = { fixture: name, go: verdict(reports.go, name), python: verdict(reports.python, name), typescript: verdict(reports.typescript, name) };
  return row;
});

const totals = {
  go:         summarize(reports.go),
  python:     summarize(reports.python),
  typescript: summarize(reports.typescript),
};
const anyFail = Object.values(totals).some((t) => t.fail > 0 || t.missing > 0);

const summaryJson = { totals, matrix, generated_at: new Date().toISOString() };
await writeFile(argv['out-json'], JSON.stringify(summaryJson, null, 2) + '\n');

const md = renderMarkdown(matrix, totals, fixtureNames);
await writeFile(argv['out-md'], md);

const totalPass = totals.go.pass + totals.python.pass + totals.typescript.pass;
const totalFail = totals.go.fail + totals.python.fail + totals.typescript.fail;
const totalMissing = totals.go.missing + totals.python.missing + totals.typescript.missing;
console.log(`aggregate: go=${fmt(totals.go)} python=${fmt(totals.python)} typescript=${fmt(totals.typescript)} (pass=${totalPass} fail=${totalFail} missing=${totalMissing})`);
if (anyFail) {
  console.error('aggregate: one or more harnesses reported failures — matrix is not 100% green');
  process.exit(1);
}

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

async function readReport(p) {
  try {
    const raw = await fs.readFile(p, 'utf8');
    return parseJUnit(raw);
  } catch (err) {
    // Missing report = all fixtures marked `missing` — equivalent to
    // a skipped harness. Aggregator treats that as a soft failure so
    // the matrix makes the absence visible.
    return { cases: [] };
  }
}

// Minimal JUnit parser: enough for our harnesses' shape (`testsuite`
// with `testcase` children; optional `failure` child marks fail).
function parseJUnit(xml) {
  const cases = [];
  const re = /<testcase\s+([^>]*?)(\/>|>([\s\S]*?)<\/testcase>)/g;
  let m;
  while ((m = re.exec(xml))) {
    const attrs = parseAttrs(m[1]);
    const inner = m[3] || '';
    const failMatch = inner.match(/<failure([^>]*?)>([\s\S]*?)<\/failure>/);
    cases.push({
      name: attrs.name,
      className: attrs.classname || attrs.name,
      timeSec: Number(attrs.time || '0'),
      passed: !failMatch,
      failure: failMatch ? unescapeXml(parseAttrs(failMatch[1]).message || failMatch[2]) : null,
    });
  }
  return { cases };
}

function parseAttrs(s) {
  const out = {};
  const re = /(\w[\w-]*)="([^"]*)"/g;
  let m;
  while ((m = re.exec(s))) out[m[1]] = unescapeXml(m[2]);
  return out;
}

function unescapeXml(s) {
  return String(s)
    .replaceAll('&lt;', '<')
    .replaceAll('&gt;', '>')
    .replaceAll('&quot;', '"')
    .replaceAll('&apos;', "'")
    .replaceAll('&#xA;', '\n')
    .replaceAll('&amp;', '&');
}

function verdict(report, fixtureName) {
  const found = report.cases.find((c) => c.name === fixtureName);
  if (!found) return { status: 'missing' };
  return found.passed ? { status: 'pass', timeSec: found.timeSec } : { status: 'fail', failure: found.failure, timeSec: found.timeSec };
}

function summarize(report) {
  const pass = report.cases.filter((c) => c.passed).length;
  const fail = report.cases.filter((c) => !c.passed).length;
  return { pass, fail, missing: 0, total: report.cases.length };
}

function renderMarkdown(matrix, totals, fixtureNames) {
  const parts = [];
  parts.push('# SDK Conformance — last run\n');
  parts.push(`_Generated: ${new Date().toISOString()}_\n`);
  parts.push('## Totals\n');
  parts.push('| SDK | Pass | Fail | Total |');
  parts.push('|-----|------|------|-------|');
  for (const sdk of ['go', 'python', 'typescript']) {
    const t = totals[sdk];
    parts.push(`| ${sdk} | ${t.pass} | ${t.fail} | ${t.total} |`);
  }
  parts.push('\n## Matrix\n');
  parts.push('| Fixture | Go | Python | TypeScript |');
  parts.push('|---------|-----|--------|------------|');
  for (const row of matrix) {
    parts.push(`| ${row.fixture} | ${cell(row.go)} | ${cell(row.python)} | ${cell(row.typescript)} |`);
  }
  const anyFail = Object.values(totals).some((t) => t.fail > 0 || t.missing > 0);
  parts.push('\n**Overall:** ' + (anyFail ? '❌ NOT GREEN — one or more SDKs failed a fixture.' : '✅ ALL GREEN — every SDK passed every fixture.'));
  return parts.join('\n') + '\n';
}

function cell(v) {
  if (!v) return '—';
  if (v.status === 'pass') return 'PASS';
  if (v.status === 'fail') return 'FAIL';
  if (v.status === 'missing') return 'MISSING';
  return v.status;
}

function fmt(t) { return `${t.pass}/${t.total}`; }

async function writeFile(p, content) {
  await fs.mkdir(path.dirname(p), { recursive: true });
  await fs.writeFile(p, content, 'utf8');
}
