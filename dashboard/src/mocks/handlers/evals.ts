import type {
  EvalDataset,
  EvalEntryResult,
  EvalRun,
  SafetyDecisionType,
} from "@/api/types";

/**
 * Dev-only msw handler fixtures for the Evals UI so the dashboard is demoable
 * before the three sibling backend tasks (task-f34c528f dataset store,
 * task-08a86cc0 extraction pipeline, task-42b98ec6 runner) are merged.
 * Gated behind `FEATURE_FLAGS.evalsPageMocks` — never true in prod or test runs.
 */

const ISO_NOW = new Date().toISOString();

export const MOCK_EVAL_DATASETS: EvalDataset[] = [
  {
    id: "ds-denies-2026-04",
    name: "denies-2026-04",
    version: 2,
    tenant: "acme",
    description: "April deny + approval-required incidents",
    entryCount: 142,
    contentHash: "sha256:mock-abc",
    createdAt: ISO_NOW,
    updatedAt: ISO_NOW,
    createdBy: "ops@acme",
  },
  {
    id: "ds-mcp-tool-calls",
    name: "mcp-tool-calls",
    version: 1,
    tenant: "acme",
    description: "Denied MCP tool invocations",
    entryCount: 38,
    contentHash: "sha256:mock-xyz",
    createdAt: ISO_NOW,
    updatedAt: ISO_NOW,
  },
];

function mkEntry(
  id: string,
  status: EvalEntryResult["status"],
  drift: EvalEntryResult["driftDirection"] = "unchanged",
  expected: SafetyDecisionType = "deny",
  actual: SafetyDecisionType | string = "deny",
  ruleId = "rule-mock",
): EvalEntryResult {
  return {
    entryId: id,
    input: { topic: "mcp.tool.call", tool: id, payload: { target: "/secret" } },
    expectedDecision: expected,
    actualDecision: actual,
    ruleId,
    status,
    driftDirection: drift,
    reason: "policy simulated locally (mock)",
  };
}

export function mockEvalRunHistory(datasetId: string): EvalRun[] {
  const base: EvalRun = {
    runId: `run-${datasetId}-1`,
    datasetId,
    datasetName: datasetId,
    datasetVersion: 1,
    policySnapshot: "snap-mock-111",
    startedAt: new Date(Date.now() - 1000 * 60 * 60 * 24 * 3).toISOString(),
    completedAt: new Date(Date.now() - 1000 * 60 * 60 * 24 * 3 + 5000).toISOString(),
    summary: { total: 100, passed: 92, failed: 6, regressions: 2, errored: 0, scorePercent: 92 },
  };
  const second: EvalRun = {
    ...base,
    runId: `run-${datasetId}-2`,
    startedAt: new Date(Date.now() - 1000 * 60 * 60 * 24).toISOString(),
    completedAt: new Date(Date.now() - 1000 * 60 * 60 * 24 + 5000).toISOString(),
    policySnapshot: "snap-mock-222",
    summary: { total: 100, passed: 98, failed: 2, regressions: 0, errored: 0, scorePercent: 98 },
  };
  return [second, base];
}

export function mockEvalRunDetail(runId: string): EvalRun {
  return {
    runId,
    datasetId: "ds-denies-2026-04",
    datasetName: "denies-2026-04",
    datasetVersion: 2,
    policySnapshot: "snap-mock-222",
    startedAt: new Date(Date.now() - 1000 * 60 * 5).toISOString(),
    completedAt: new Date().toISOString(),
    summary: { total: 6, passed: 3, failed: 1, regressions: 2, errored: 0, scorePercent: 50 },
    entries: [
      mkEntry("e-1", "pass", "unchanged", "deny", "deny", "rule-fs-delete"),
      mkEntry("e-2", "pass", "unchanged"),
      mkEntry("e-3", "regression", "relaxed", "deny", "allow", "rule-http-post"),
      mkEntry("e-4", "regression", "relaxed", "deny", "allow", "rule-http-post"),
      mkEntry("e-5", "fail", "unchanged", "require_approval", "deny", "rule-prompt-leak"),
      mkEntry("e-6", "pass", "unchanged"),
    ],
  };
}

export interface ExtractIncidentsMockBody {
  datasetName?: string;
  dryRun?: boolean;
}

export function mockExtractIncidentsResponse(body: ExtractIncidentsMockBody): {
  dataset?: EvalDataset;
  preview: { scanned_decisions: number; entry_count: number; deduped_count: number; warnings: string[] };
} {
  const preview = {
    scanned_decisions: 1420,
    entry_count: 142,
    deduped_count: 16,
    warnings: [],
  };
  if (body.dryRun) return { preview };
  return {
    dataset: {
      id: `ds-${body.datasetName ?? "mock"}`,
      name: body.datasetName ?? "mock",
      version: 1,
      tenant: "acme",
      entryCount: 142,
      contentHash: "sha256:mock-new",
      createdAt: ISO_NOW,
      updatedAt: ISO_NOW,
    },
    preview,
  };
}
