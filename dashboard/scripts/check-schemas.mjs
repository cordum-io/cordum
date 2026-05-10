#!/usr/bin/env node
/*
 * CI gate: regenerate per-RuleType JSON Schemas and fail if the
 * committed tree drifts from regeneration.
 *
 * Catches: SSOT edits in `scripts/policy-studio/rule-payloads.mjs` not
 * accompanied by a regenerated `src/lib/policy-studio/schemas/generated/*.json`,
 * and accidental hand-edits to those generated files.
 *
 * Local fix when this fails:
 *   `pnpm run generate-schemas && git add src/lib/policy-studio/schemas/generated/`
 */
import { readFileSync, mkdtempSync, writeFileSync, mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";
import path from "node:path";

import { rulePayloads, ruleTypeOrder } from "./policy-studio/rule-payloads.mjs";
import { buildSchemaForType, loadOpenApiSchemas } from "./generate-schemas.mjs";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const dashboardRoot = path.resolve(__dirname, "..");
const generatedDir = path.join(
  dashboardRoot,
  "src",
  "lib",
  "policy-studio",
  "schemas",
  "generated",
);

function readCommitted(ruleType) {
  const file = path.join(generatedDir, `${ruleType}.json`);
  return { file, text: readFileSync(file, "utf8") };
}

function regenerate(ruleType, schemas) {
  const built = buildSchemaForType(ruleType, schemas);
  return `${JSON.stringify(built, null, 2)}\n`;
}

function main() {
  // Rule-type catalog itself drifts only when the SSOT changes, so detect
  // mismatch with what the OpenAPI envelope offers and fail fast — that's
  // a meaningful "we forgot to wire a new RuleType" signal.
  const expectedTypes = ruleTypeOrder.slice().sort().join(",");
  const payloadKeys = Object.keys(rulePayloads).sort().join(",");
  if (expectedTypes !== payloadKeys) {
    console.error(
      `[check-schemas] rule-payloads.mjs SSOT mismatch: ruleTypeOrder=[${expectedTypes}] vs payloads=[${payloadKeys}]`,
    );
    process.exit(2);
  }

  const schemas = loadOpenApiSchemas();
  const drifts = [];
  for (const ruleType of ruleTypeOrder) {
    const { file, text: committed } = readCommitted(ruleType);
    const regenerated = regenerate(ruleType, schemas);
    if (committed !== regenerated) {
      drifts.push({ file, ruleType });
    }
  }

  if (drifts.length === 0) {
    console.log(`[check-schemas] OK — ${ruleTypeOrder.length} schemas in sync.`);
    return;
  }

  // Surface a small dump of the first drift so reviewers can spot the
  // diff direction without re-running the generator locally first.
  const tmp = mkdtempSync(path.join(tmpdir(), "policy-studio-schemas-"));
  for (const { file, ruleType } of drifts) {
    const regenerated = regenerate(ruleType, loadOpenApiSchemas());
    const dumpDir = path.join(tmp, ruleType);
    mkdirSync(dumpDir, { recursive: true });
    writeFileSync(path.join(dumpDir, "expected.json"), regenerated);
    writeFileSync(path.join(dumpDir, "committed.json"), readFileSync(file, "utf8"));
  }
  console.error(
    `[check-schemas] drift detected in ${drifts.length} schema file(s):`,
  );
  for (const { file } of drifts) {
    console.error(`  - ${path.relative(dashboardRoot, file)}`);
  }
  console.error(
    `[check-schemas] dumped expected/committed pairs into ${tmp}. ` +
      `Run \`pnpm run generate-schemas\` to update.`,
  );
  process.exit(1);
}

main();
