#!/usr/bin/env node
// Phase 5d (task-50bbfd7d) — bundle-size soft-threshold reporter.
//
// Reads dist/assets/*.js after `pnpm run build`, computes raw + gzip + brotli
// sizes per chunk, classifies them (initial vs route vs vendor), and prints a
// markdown table to stdout for the CI workflow to post as a PR comment.
//
// Soft thresholds (warn-only): exit code is always 0. Threshold breaches are
// rendered as ⚠ markers in the table and logged as `::warning::` so they
// surface in the GitHub Actions UI.
//
// Why we read dist/ files instead of parsing the visualizer's stats.html:
// dist/ files are the canonical build output, smaller to read, and produce
// numbers that match what nginx serves. stats.html stays useful for human
// drill-down (treemap UI), but a 1.6MB HTML parse is overkill for a CI gate.

import { readFileSync } from "node:fs";
import { gzipSync, brotliCompressSync, constants as zlibConstants } from "node:zlib";
import { readdirSync } from "node:fs";
import { join, basename } from "node:path";

const DIST = "dist";
const ASSETS = join(DIST, "assets");

// Soft thresholds — warn-only; set ~25-30% above the 2026-05-09 baseline so
// PRs have headroom for normal growth while real regressions still trip the
// warning. Baseline (2026-05-09, dashboard branch):
//   initial   305.4 KB raw / 92.2 KB gzip
//   total    2532.7 KB raw / 759.3 KB gzip
// Initial chunk = page entry the browser must download before any route can
// render. Total = sum of all js chunks (most are route-lazy-loaded).
const THRESHOLDS = {
  initialRawKb: 400,
  initialGzipKb: 120,
  totalRawKb: 3100,
  totalGzipKb: 950,
};

const KB = 1024;

function fmtKb(bytes) {
  return `${(bytes / KB).toFixed(1)} KB`;
}

function classify(name) {
  if (/^index-/.test(name)) return "initial";
  // Vite emits chunk-XXXXXX.js for shared module chunks.
  if (/^chunk-/.test(name)) return "shared";
  return "route";
}

function listJsAssets() {
  let files;
  try {
    files = readdirSync(ASSETS);
  } catch (err) {
    if (err.code === "ENOENT") {
      console.error(`error: ${ASSETS} does not exist. Run \`pnpm run build\` first.`);
      process.exit(2);
    }
    throw err;
  }
  return files
    .filter((f) => f.endsWith(".js") && !f.endsWith(".map"))
    .map((f) => join(ASSETS, f));
}

function measure(path) {
  const raw = readFileSync(path);
  const rawSize = raw.length;
  const gzipSize = gzipSync(raw, { level: 9 }).length;
  const brotliSize = brotliCompressSync(raw, {
    params: { [zlibConstants.BROTLI_PARAM_QUALITY]: 11 },
  }).length;
  return { rawSize, gzipSize, brotliSize };
}

function buildReport() {
  const paths = listJsAssets();
  const rows = paths.map((p) => {
    const name = basename(p);
    const kind = classify(name);
    return { name, kind, ...measure(p) };
  });

  // Sort: initial first, then by raw size desc.
  rows.sort((a, b) => {
    if (a.kind === "initial" && b.kind !== "initial") return -1;
    if (b.kind === "initial" && a.kind !== "initial") return 1;
    return b.rawSize - a.rawSize;
  });

  const initial = rows.find((r) => r.kind === "initial");
  const totalRaw = rows.reduce((acc, r) => acc + r.rawSize, 0);
  const totalGzip = rows.reduce((acc, r) => acc + r.gzipSize, 0);

  const warnings = [];
  if (initial && initial.rawSize > THRESHOLDS.initialRawKb * KB) {
    warnings.push(
      `initial chunk raw ${fmtKb(initial.rawSize)} > ${THRESHOLDS.initialRawKb} KB threshold`,
    );
  }
  if (initial && initial.gzipSize > THRESHOLDS.initialGzipKb * KB) {
    warnings.push(
      `initial chunk gzip ${fmtKb(initial.gzipSize)} > ${THRESHOLDS.initialGzipKb} KB threshold`,
    );
  }
  if (totalRaw > THRESHOLDS.totalRawKb * KB) {
    warnings.push(`total raw ${fmtKb(totalRaw)} > ${THRESHOLDS.totalRawKb} KB threshold`);
  }
  if (totalGzip > THRESHOLDS.totalGzipKb * KB) {
    warnings.push(
      `total gzip ${fmtKb(totalGzip)} > ${THRESHOLDS.totalGzipKb} KB threshold`,
    );
  }

  return { rows, initial, totalRaw, totalGzip, warnings };
}

function renderMarkdown({ rows, initial, totalRaw, totalGzip, warnings }) {
  const lines = [];
  lines.push("<!-- bundle-size-report -->");
  lines.push("## Bundle size report");
  lines.push("");
  lines.push(
    `Soft thresholds (warn-only): initial ≤ ${THRESHOLDS.initialRawKb} KB raw / ${THRESHOLDS.initialGzipKb} KB gzip; total ≤ ${THRESHOLDS.totalRawKb} KB raw / ${THRESHOLDS.totalGzipKb} KB gzip.`,
  );
  lines.push("");
  lines.push("| Kind | Chunk | Raw | Gzip | Brotli |");
  lines.push("| --- | --- | ---: | ---: | ---: |");
  // Cap at 25 chunks so the PR comment doesn't get huge; the rest folds into
  // a "+ N more" line.
  const SHOW = 25;
  rows.slice(0, SHOW).forEach((r) => {
    const flag = r.kind === "initial" ? " ⚙" : "";
    lines.push(
      `| ${r.kind}${flag} | \`${r.name}\` | ${fmtKb(r.rawSize)} | ${fmtKb(r.gzipSize)} | ${fmtKb(r.brotliSize)} |`,
    );
  });
  if (rows.length > SHOW) {
    lines.push(`| _+ ${rows.length - SHOW} more chunks_ | | | | |`);
  }
  lines.push(
    `| **Total** | _${rows.length} chunks_ | **${fmtKb(totalRaw)}** | **${fmtKb(totalGzip)}** | _n/a_ |`,
  );
  if (initial) {
    lines.push(
      `| **Initial** | \`${initial.name}\` | **${fmtKb(initial.rawSize)}** | **${fmtKb(initial.gzipSize)}** | ${fmtKb(initial.brotliSize)} |`,
    );
  }
  lines.push("");
  if (warnings.length) {
    lines.push("### ⚠ Soft-threshold warnings");
    lines.push("");
    warnings.forEach((w) => lines.push(`- ${w}`));
    lines.push("");
    lines.push(
      "_Warn-only: this PR is not blocked. Audit the regression before merging._",
    );
  } else {
    lines.push("All chunks within soft thresholds.");
  }
  return lines.join("\n");
}

const report = buildReport();
process.stdout.write(`${renderMarkdown(report)}\n`);
report.warnings.forEach((w) => {
  // GitHub Actions surface: shows up in the workflow run UI.
  process.stderr.write(`::warning::bundle-size: ${w}\n`);
});
process.exit(0);
