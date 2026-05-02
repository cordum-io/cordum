/**
 * Centralized query key factory for React Query.
 *
 * All query keys in the app should be defined here. This eliminates
 * string duplication, prevents typos, and makes cache invalidation
 * patterns auditable in one place.
 *
 * Convention:
 * - `.all` — prefix key for `invalidateQueries` (fuzzy match)
 * - `.list(filters)` — list queries with filter objects
 * - `.detail(id)` — single-item queries
 */

import type { JobFilters } from "../hooks/useJobs";
import type { DLQFilters } from "../hooks/useDLQ";
import type { ApprovalHistoryFilters } from "../hooks/useApprovals";
import type { AuditFilters, ExportFormat } from "../hooks/useAudit";
import type { WorkflowListParams, WorkflowRunsParams, AllRunsParams } from "../hooks/useWorkflows";
import type { QuarantinedJobsFilters } from "../hooks/useOutputPolicy";
import type {
  EdgeApprovalListParams,
  EdgeEventListParams,
  EdgeExecutionListParams,
  EdgeSessionListParams,
} from "../api/types";

const scalarKey = (value: string | number | null | undefined, fallback = "all") =>
  value === null || value === undefined || value === "" ? fallback : value;

const edgeSessionListFilterKey = (params?: EdgeSessionListParams) => [
  scalarKey(params?.status),
  scalarKey(params?.principalId),
  scalarKey(params?.agentProduct),
  scalarKey(params?.cursor, "first"),
  scalarKey(params?.limit, "default"),
] as const;

const edgeExecutionListFilterKey = (params?: EdgeExecutionListParams) => [
  scalarKey(params?.sessionId),
  scalarKey(params?.jobId),
  scalarKey(params?.traceId),
  scalarKey(params?.workflowRunId),
  scalarKey(params?.cursor, "first"),
  scalarKey(params?.limit, "default"),
] as const;

const edgeEventFilterKey = (params?: EdgeEventListParams) => [
  scalarKey(params?.kind),
  scalarKey(params?.decision),
  scalarKey(params?.since, "since:all"),
  scalarKey(params?.until, "until:all"),
  scalarKey(params?.cursor, "first"),
  scalarKey(params?.limit, "default"),
] as const;

const edgeApprovalListFilterKey = (params?: EdgeApprovalListParams) => [
  scalarKey(params?.status),
  scalarKey(params?.sessionId),
  scalarKey(params?.executionId),
  scalarKey(params?.actionHash),
  scalarKey(params?.cursor, "first"),
  scalarKey(params?.limit, "default"),
] as const;

export const queryKeys = {
  // ── Jobs ──────────────────────────────────────────────────────────
  jobs: {
    all: ["jobs"] as const,
    list: (filters: JobFilters) => ["jobs", filters] as const,
    quarantined: (filters: QuarantinedJobsFilters) => ["jobs", "quarantined", filters] as const,
    recent: (limit: number) => ["jobs", "recent", limit] as const,
    safetyDecisions: (limit: number) => ["jobs", "safety-decisions", limit] as const,
    detail: (id: string) => ["job", id] as const,
    decisions: (id: string) => ["job", id, "decisions"] as const,
    outputFindings: (jobId: string) => ["job", jobId, "output-findings"] as const,
    artifacts: (jobId: string) => ["job-artifacts", jobId] as const,
  },

  // ── Approvals ─────────────────────────────────────────────────────
  approvals: {
    all: ["approvals"] as const,
    list: (status?: string) => ["approvals", status ?? "all"] as const,
    detail: (id: string) => ["approval", id] as const,
    history: (filters: ApprovalHistoryFilters) => ["approvals", "history", filters] as const,
    nav: () => ["approvals", "nav"] as const,
    context: (jobId: string) => ["approvals", "context", jobId] as const,
  },

  // ── DLQ ───────────────────────────────────────────────────────────
  dlq: {
    all: ["dlq"] as const,
    list: (filters: DLQFilters) => ["dlq", filters] as const,
    nav: () => ["dlq", "nav"] as const,
  },

  // ── Audit ─────────────────────────────────────────────────────────
  audit: {
    all: ["audit"] as const,
    event: (eventId: string | null) => ["audit", "event", eventId] as const,
    correlation: (resourceId: string | null) => ["audit", "correlation", resourceId] as const,
    export: (filters: AuditFilters, format: ExportFormat) => ["audit-export", filters, format] as const,
  },

  // ── Workflows ─────────────────────────────────────────────────────
  workflows: {
    all: ["workflows"] as const,
    list: (params?: WorkflowListParams) => ["workflows", params ?? {}] as const,
    detail: (id: string | null | undefined) => ["workflow", id] as const,
  },

  // ── Workflow Runs ─────────────────────────────────────────────────
  workflowRuns: {
    all: ["workflow-runs"] as const,
    byWorkflow: (workflowId: string | null | undefined, params?: WorkflowRunsParams) =>
      ["workflow-runs", workflowId, params ?? {}] as const,
    allRuns: (filters?: AllRunsParams) => ["workflow-runs", "all", filters ?? {}] as const,
    active: () => ["workflow-runs", "active"] as const,
    recent: (limit: number) => ["runs", "recent", limit] as const,
    detail: (runId: string | null | undefined) => ["workflow-run", runId] as const,
    timeline: (runId: string | null | undefined, limit?: number) =>
      ["workflow-run", runId, "timeline", limit ?? "default"] as const,
  },

  // ── Policies ──────────────────────────────────────────────────────
  policies: {
    bundles: () => ["policy-bundles"] as const,
    bundle: (id: string) => ["policy-bundle", id] as const,
    rules: () => ["policy-rules"] as const,
    velocityRules: () => ["policy-velocity-rules"] as const,
    velocityRuleStats: () => ["policy-velocity-rules", "stats"] as const,
    audit: () => ["policy-audit"] as const,
    snapshots: () => ["policy-snapshots"] as const,
    snapshot: (id: string | null) => ["policy-snapshot", id] as const,
    config: () => ["policy-config"] as const,
    stats: (range: string) => ["policy-stats", range] as const,
  },

  // ── Output Policy ────────────────────────────────────────────────
  outputPolicy: {
    config: () => ["output-policy-config"] as const,
    stats: () => ["output-policy", "stats"] as const,
    rules: () => ["output-rules"] as const,
    ruleAudit: (ruleId: string, limit: number) => ["output-rule-audit", ruleId, limit] as const,
  },

  // ── Workers ───────────────────────────────────────────────────────
  workers: {
    all: ["workers"] as const,
    detail: (id: string) => ["worker", id] as const,
    jobs: (workerId: string) => ["worker-jobs", workerId] as const,
  },

  // ── Pools ──────────────────────────────────────────────────────────
  pools: {
    all: ["pools"] as const,
    detail: (name: string) => ["pool", name] as const,
  },

  // ── Config ────────────────────────────────────────────────────────
  config: {
    system: () => ["config"] as const,
    effective: (params?: Record<string, unknown>) => ["effective-config", params ?? {}] as const,
  },

  // ── Status ────────────────────────────────────────────────────────
  status: {
    overview: () => ["status"] as const,
    pipelineFallback: (limit: number) => ["status", "pipeline-fallback", limit] as const,
  },

  // ── Auth ──────────────────────────────────────────────────────────
  auth: {
    config: () => ["auth-config"] as const,
    configAdmin: () => ["auth-config-admin"] as const,
    session: () => ["auth-session"] as const,
    sessionValidate: () => ["auth-session-validate"] as const,
    apiKeys: () => ["api-keys"] as const,
  },

  // ── Cordum Edge ───────────────────────────────────────────────────
  edge: {
    all: ["edge"] as const,
    sessions: {
      all: ["edge", "sessions"] as const,
      lists: () => ["edge", "sessions", "list"] as const,
      list: (params?: EdgeSessionListParams) =>
        ["edge", "sessions", "list", edgeSessionListFilterKey(params)] as const,
      detail: (sessionId: string | null | undefined) =>
        ["edge", "sessions", "detail", scalarKey(sessionId, "missing")] as const,
      events: (sessionId: string | null | undefined, params?: EdgeEventListParams) =>
        ["edge", "sessions", "events", scalarKey(sessionId, "missing"), edgeEventFilterKey(params)] as const,
      eventLists: (sessionId: string | null | undefined) =>
        ["edge", "sessions", "events", scalarKey(sessionId, "missing")] as const,
      export: (sessionId: string | null | undefined) =>
        ["edge", "sessions", "export", scalarKey(sessionId, "missing")] as const,
    },
    executions: {
      all: ["edge", "executions"] as const,
      lists: () => ["edge", "executions", "list"] as const,
      list: (params?: EdgeExecutionListParams) =>
        ["edge", "executions", "list", edgeExecutionListFilterKey(params)] as const,
      detail: (executionId: string | null | undefined) =>
        ["edge", "executions", "detail", scalarKey(executionId, "missing")] as const,
      events: (executionId: string | null | undefined, params?: EdgeEventListParams) =>
        ["edge", "executions", "events", scalarKey(executionId, "missing"), edgeEventFilterKey(params)] as const,
      eventLists: (executionId: string | null | undefined) =>
        ["edge", "executions", "events", scalarKey(executionId, "missing")] as const,
    },
    approvals: {
      all: ["edge", "approvals"] as const,
      lists: () => ["edge", "approvals", "list"] as const,
      list: (params?: EdgeApprovalListParams) =>
        ["edge", "approvals", "list", edgeApprovalListFilterKey(params)] as const,
      detail: (approvalRef: string | null | undefined) =>
        ["edge", "approvals", "detail", scalarKey(approvalRef, "missing")] as const,
    },
  },

  // ── Packs ─────────────────────────────────────────────────────────
  packs: {
    all: ["packs"] as const,
    detail: (id: string) => ["pack", id] as const,
    marketplace: () => ["marketplace-packs"] as const,
  },

  // ── Schemas ───────────────────────────────────────────────────────
  schemas: {
    all: ["schemas"] as const,
    detail: (id: string) => ["schema", id] as const,
  },

  // ── Memory ────────────────────────────────────────────────────────
  memory: {
    get: (ptr: string) => ["memory", ptr] as const,
    artifact: (ptr: string) => ["artifact", ptr] as const,
  },

  // ── System Health ─────────────────────────────────────────────────
  systemHealth: {
    status: () => ["system-health"] as const,
  },

  // ── MCP ───────────────────────────────────────────────────────────
  mcp: {
    status: (enabled: boolean, transport: string) => ["mcp-status", enabled, transport] as const,
  },

  // ── Users ─────────────────────────────────────────────────────────
  users: {
    all: ["users"] as const,
  },
} as const;
