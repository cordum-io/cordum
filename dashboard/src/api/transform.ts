import YAML from "yaml";
import { generateUUID } from "../lib/uuid";
import { logger } from "../lib/logger";
import { normalizeRunStatusValue } from "../lib/runVisibility";
import type {
  Job,
  JobStatus,
  ApprovalActionability,
  OutputDecision,
  OutputFinding,
  OutputSafetyRecord,
  SafetyDecision,
  Approval,
  ApprovalStatus,
  ApprovalContextStatus,
  ApprovalDecisionSummary,
  ApprovalDecisionSummaryCompleteness,
  ApprovalDecisionSummarySource,
  UrgencyLevel,
  AuditEntry,
  AuditCategory,
  AuditSeverity,
  AuditActor,
  Workflow,
  WorkflowRun,
  WorkflowStep,
  RunStatus,
  PolicyBundle,
  Worker,
  Pool,
  DLQEntry,
  Pack,
  MarketplacePack,
  MarketplaceCatalog,
  PolicyRule,
  PolicyRuleMatch,
  ApprovalContext,
  ApprovalPolicySnapshot,
  GovernanceDecision,
  GovernanceVerdict,
  EvalDataset,
  EvalEntry,
  EvalRun,
  EvalRunStatus,
  EvalRunSummary,
  EvalEntryResult,
  EvalDriftDirection,
  SafetyDecisionType,
  PolicyConstraints,
  PolicyBundleSignature,
  DelegationChainLink,
  DelegationListResponse,
  DelegationView,
} from "./types";

// ---------------------------------------------------------------------------
// Backend response shapes (minimal)
// ---------------------------------------------------------------------------

export interface BackendJobRecord {
  id: string;
  worker_id?: string;
  trace_id?: string;
  updated_at?: number;
  state?: string;
  topic?: string;
  tenant?: string;
  team?: string;
  actor_id?: string;
  actor_type?: string;
  capability?: string;
  risk_tags?: string[];
  requires?: string[];
  pack_id?: string;
  attempts?: number;
  safety_decision?: string;
  safety_reason?: string;
  safety_rule_id?: string;
  output_decision?: string;
  output_safety?: BackendOutputSafetyRecord;
  workflow_run_id?: string;
  labels?: Record<string, string>;
  metadata?: { [key: string]: string | undefined };
}

export interface BackendOutputFinding {
  type?: string;
  severity?: string;
  detail?: string;
  scanner?: string;
  confidence?: number;
  matched_pattern?: string;
  offset?: number;
  length?: number;
}

export interface BackendOutputSafetyRecord {
  decision?: string;
  reason?: string;
  rule_id?: string;
  findings?: BackendOutputFinding[];
  phase?: string;
  policy_snapshot?: string;
  redacted_ptr?: string;
  original_ptr?: string;
}

export interface BackendJobDetail extends BackendJobRecord {
  context_ptr?: string;
  result_ptr?: string;
  context?: unknown;
  result?: unknown;
  error_message?: string;
  error_status?: string;
  error_code?: string;
  error_code_enum?: number;
  last_state?: string;
  workflow_id?: string;
  run_id?: string;
  step_id?: string;
  idempotency_key?: string;
  labels?: Record<string, string>;
  approval_required?: boolean;
  approval_ref?: string;
  approval_by?: string;
  approval_role?: string;
  approval_at?: number;
  approval_reason?: string;
  approval_note?: string;
  approval_status?: ApprovalStatus;
  approval_actionability?: ApprovalActionability;
  approval_revision?: number;
  approval_decision?: "approve" | "reject" | "expire" | "invalidate" | "repair";
}

export interface BackendWorkflowStep {
  id?: string;
  name?: string;
  type?: string;
  worker_id?: string;
  topic?: string;
  depends_on?: string[];
  condition?: string;
  for_each?: string;
  max_parallel?: number;
  input?: Record<string, unknown>;
  input_schema?: Record<string, unknown>;
  input_schema_id?: string;
  output_path?: string;
  output_schema?: Record<string, unknown>;
  output_schema_id?: string;
  meta?: {
    actor_id?: string;
    actor_type?: string;
    idempotency_key?: string;
    pack_id?: string;
    capability?: string;
    risk_tags?: string[];
    requires?: string[];
    labels?: Record<string, string>;
    adapter_id?: string;
    memory_id?: string;
    context_mode?: string;
    allow_summarization?: boolean;
    allow_retrieval?: boolean;
    deadline_ms?: number;
    priority?: string;
    budget?: { input_tokens?: number; output_tokens?: number; total_tokens?: number };
    prompt?: string;
  };
  retry?: {
    max_retries?: number;
    initial_backoff_sec?: number;
    max_backoff_sec?: number;
    multiplier?: number;
  };
  timeout_sec?: number;
  delay_sec?: number;
  delay_until?: string;
  route_labels?: Record<string, string>;
  on_error?: string;
  config?: Record<string, unknown>;
  status?: string;
  output?: Record<string, unknown>;
  error?: string;
  started_at?: string;
  completed_at?: string;
}

export interface BackendWorkflow {
  id: string;
  org_id?: string;
  team_id?: string;
  name?: string;
  description?: string;
  version?: string;
  timeout_sec?: number;
  steps?: Record<string, BackendWorkflowStep>;
  config?: Record<string, unknown>;
  input_schema?: Record<string, unknown>;
  parameters?: Array<Record<string, unknown>>;
  created_at?: string;
  updated_at?: string;
}

export interface BackendStepRun {
  step_id?: string;
  status?: string;
  started_at?: string;
  completed_at?: string;
  output?: Record<string, unknown>;
  error?: Record<string, unknown>;
  job_id?: string;
}

export interface BackendWorkflowRun {
  id: string;
  workflow_id?: string;
  org_id?: string;
  team_id?: string;
  status?: string;
  steps?: Record<string, BackendStepRun>;
  started_at?: string | null;
  completed_at?: string | null;
  created_at?: string;
  updated_at?: string;
  input?: Record<string, unknown>;
  output?: Record<string, unknown>;
  error?: Record<string, unknown>;
  rerun_of?: string;
  rerun_step?: string;
  dry_run?: boolean;
  timers?: Array<{
    workflow_id: string;
    run_id: string;
    fires_at: string;
    remaining_ms: number;
  }>;
}

export interface BackendApprovalDecisionSummary {
  source?: ApprovalDecisionSummarySource;
  completeness?: ApprovalDecisionSummaryCompleteness;
  context_status?: ApprovalContextStatus;
  title?: string;
  subject?: string;
  why?: string;
  next_effect?: string;
  amount?: number;
  currency?: string;
  vendor?: string;
  item_count?: number;
  items_preview?: string[];
  escalation_reason?: string;
  missing_fields?: string[];
}

export interface BackendGovernanceDecision {
  job_id?: string;
  run_id?: string;
  step_id?: string;
  topic?: string;
  matched_rule?: string;
  rule_name?: string;
  verdict?: string;
  reason?: string;
  constraints?: Record<string, unknown>;
  approval_status?: ApprovalStatus;
  approval_decision?: "approve" | "reject" | "expire" | "invalidate" | "repair";
  agent_id?: string;
  policy_version?: string;
  timestamp?: string;
}

export interface BackendApprovalItem {
  job?: BackendJobRecord;
  decision?: string;
  policy_rule_id?: string;
  policy_reason?: string;
  approval_required?: boolean;
  approval_ref?: string;
  job_hash?: string;
  policy_snapshot?: string;
  context_ptr?: string;
  resolved_at?: number;
  resolved_by?: string;
  resolved_comment?: string;
  approval_status?: ApprovalStatus;
  approval_actionability?: ApprovalActionability;
  approval_revision?: number;
  approval_decision?: "approve" | "reject" | "expire" | "invalidate" | "repair";
  constraints?: Record<string, unknown>;
  job_input?: Record<string, unknown>;
  decision_summary?: BackendApprovalDecisionSummary;
  workflow_id?: string;
  workflow_name?: string;
  workflow_run_id?: string;
  workflow_step_id?: string;
  step_index?: number;
  step_name?: string;
  total_steps?: number;
  // Enriched context fields for decision-grade UX.
  blast_radius?: {
    systems?: string[];
    namespaces?: string[];
    resources?: string[];
    scope_description?: string;
  };
  prior_approvals?: Array<{
    job_id?: string;
    topic?: string;
    tenant?: string;
    decision?: string;
    resolved_by?: string;
    resolved_at?: number;
    was_approved?: boolean;
  }>;
  rollback_hint?: string;
  policy_snapshot_summary?: {
    rule_count?: number;
    matched_rule?: {
      id?: string;
      description?: string;
      decision?: string;
      constraints_summary?: string;
    };
    policy_version?: string;
  };
}

export interface BackendDLQEntry {
  job_id: string;
  topic?: string;
  status?: string;
  reason?: string;
  reason_code?: string;
  last_state?: string;
  attempts?: number;
  created_at?: string;
}

export interface BackendPolicyBundleSummary {
  id: string;
  enabled?: boolean;
  source?: string;
  author?: string;
  message?: string;
  created_at?: string;
  updated_at?: string;
  version?: string;
  installed_at?: string;
  sha256?: string;
  rule_count?: number;
}

export interface BackendPolicyBundleDetail {
  id: string;
  content?: string;
  enabled?: boolean;
  author?: string;
  message?: string;
  created_at?: string;
  updated_at?: string;
}

export interface BackendPolicyAuditEntry {
  id: string;
  action?: string;
  resource_type?: string;
  resource_id?: string;
  resource_name?: string;
  actor_id?: string;
  role?: string;
  bundle_ids?: string[];
  message?: string;
  snapshot_before?: string;
  snapshot_after?: string;
  created_at?: string;
}

export interface BackendPolicySnapshotSummary {
  id: string;
  created_at?: string;
  note?: string;
}

export interface BackendPolicySnapshot extends BackendPolicySnapshotSummary {
  bundles?: Record<string, unknown>;
}

export interface BackendPackRecord {
  id: string;
  version?: string;
  status?: string;
  installed_at?: string;
  installed_by?: string;
  manifest?: {
    metadata?: {
      id?: string;
      version?: string;
      title?: string;
      description?: string;
    };
    topics?: Array<{
      name?: string;
      requires?: string[];
      riskTags?: string[];
      capability?: string;
    }>;
    compatibility?: Record<string, unknown>;
  };
  resources?: Record<string, unknown>;
  overlays?: Record<string, unknown>;
  tests?: Record<string, unknown>;
}

export interface BackendMarketplaceCatalog {
  id: string;
  title?: string;
  url?: string;
  enabled?: boolean;
  updated_at?: string;
  error?: string;
}

export interface BackendMarketplaceItem {
  id: string;
  version: string;
  title?: string;
  description?: string;
  author?: string;
  homepage?: string;
  source?: string;
  image?: string;
  license?: string;
  url?: string;
  sha256?: string;
  catalog_id?: string;
  catalog_title?: string;
  capabilities?: string[];
  requires?: string[];
  risk_tags?: string[];
  installed_version?: string;
  installed_status?: string;
  installed_at?: string;
}

export interface BackendMarketplaceResponse {
  catalogs?: BackendMarketplaceCatalog[];
  items?: BackendMarketplaceItem[];
  fetched_at?: string;
  cached?: boolean;
}

export interface BackendHeartbeat {
  worker_id?: string;
  region?: string;
  type?: string;
  cpu_load?: number;
  gpu_utilization?: number;
  active_jobs?: number;
  capabilities?: string[];
  pool?: string;
  max_parallel_jobs?: number;
  labels?: Record<string, string>;
  memory_load?: number;
  progress_pct?: number;
  last_memo?: string;
  last_heartbeat?: string;
  status?: string;
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function logInvalidDateInput(fn: string, raw: unknown): void {
  if (import.meta.env.DEV && raw != null) {
    logger.warn("transform", `${fn} received invalid value`, { raw });
  }
}

export function microsToISO(raw: unknown): string | null {
  if (typeof raw !== "number" || !Number.isFinite(raw) || raw <= 0) {
    logInvalidDateInput("microsToISO", raw);
    return null;
  }
  const ms = Math.floor(raw / 1000);
  const d = new Date(ms);
  return isNaN(d.getTime()) ? null : d.toISOString();
}

export function normalizeJobStatus(raw?: string): JobStatus {
  switch ((raw || "").toUpperCase()) {
    case "PENDING":
    case "":
      return "pending";
    case "SCHEDULED":
      return "scheduled";
    case "DISPATCHED":
      return "dispatched";
    case "RUNNING":
      return "running";
    case "SUCCEEDED":
      return "succeeded";
    case "FAILED":
    case "FAILED_RETRYABLE":
    case "FAILED_FATAL":
      return "failed";
    case "CANCELLED":
      return "cancelled";
    case "APPROVAL_REQUIRED":
      return "approval_required";
    case "DENIED":
      return "denied";
    case "TIMEOUT":
      return "timeout";
    case "OUTPUT_QUARANTINED":
      return "output_quarantined";
    default:
      // Unknown backend states should not silently become "pending".
      // Log and return "pending" but with visibility.
      logger.warn("transform", "Unknown job status from backend, defaulting to pending", { raw });
      return "pending";
  }
}

export function normalizeDecisionType(raw?: string): SafetyDecision["type"] {
  switch ((raw || "").toUpperCase()) {
    case "ALLOW":
    case "DECISION_TYPE_ALLOW":
      return "allow";
    case "ALLOW_WITH_CONSTRAINTS":
    case "DECISION_TYPE_ALLOW_WITH_CONSTRAINTS":
      return "allow_with_constraints";
    case "DENY":
    case "DECISION_TYPE_DENY":
      return "deny";
    case "REQUIRE_APPROVAL":
    case "REQUIRE_HUMAN":
    case "DECISION_TYPE_REQUIRE_HUMAN":
    case "DECISION_TYPE_REQUIRE_APPROVAL":
      return "require_approval";
    case "THROTTLE":
    case "DECISION_TYPE_THROTTLE":
      return "throttle";
    default:
      return "deny";
  }
}

export function normalizeGovernanceVerdict(raw?: string): GovernanceVerdict {
  switch ((raw || "").trim().toUpperCase()) {
    case "ALLOW":
      return "allow";
    case "CONSTRAIN":
    case "CONSTRAINED":
    case "ALLOW_WITH_CONSTRAINTS":
      return "constrain";
    case "DENY":
      return "deny";
    case "REQUIRE_APPROVAL":
    case "REQUIRE_HUMAN":
      return "require_approval";
    case "THROTTLE":
      return "throttle";
    default:
      if (raw) {
        console.warn(
          `[transform] Unknown governance verdict "${raw}", defaulting to deny`,
        );
      }
      return "deny";
  }
}

function normalizeIsoTimestamp(raw: unknown): string | null {
  if (typeof raw !== "string" || !raw.trim()) {
    return null;
  }
  const parsed = new Date(raw);
  return Number.isNaN(parsed.getTime()) ? null : parsed.toISOString();
}

export function mapGovernanceDecision(
  decision: BackendGovernanceDecision,
): GovernanceDecision | null {
  const timestamp = normalizeIsoTimestamp(decision.timestamp);
  if (!timestamp) {
    return null;
  }
  const constraints =
    decision.constraints && typeof decision.constraints === "object"
      ? (decision.constraints as GovernanceDecision["constraints"])
      : undefined;
  return {
    jobId: decision.job_id ?? "",
    topic: decision.topic ?? "",
    matchedRule: decision.matched_rule ?? "",
    verdict: normalizeGovernanceVerdict(decision.verdict),
    reason: decision.reason ?? "",
    agentId: decision.agent_id ?? "",
    timestamp,
    ...(decision.run_id ? { runId: decision.run_id } : {}),
    ...(decision.step_id ? { stepId: decision.step_id } : {}),
    ...(decision.rule_name ? { ruleName: decision.rule_name } : {}),
    ...(constraints ? { constraints } : {}),
    ...(decision.approval_status ? { approvalStatus: decision.approval_status } : {}),
    ...(decision.approval_decision ? { approvalDecision: decision.approval_decision } : {}),
    ...(decision.policy_version ? { policyVersion: decision.policy_version } : {}),
  };
}

export function mapSafetyDecision(
  decision?: string,
  reason?: string,
  ruleId?: string,
): SafetyDecision | undefined {
  if (!decision && !reason && !ruleId) return undefined;
  return {
    type: normalizeDecisionType(decision),
    reason: reason || "",
    matchedRule: ruleId,
  };
}

export function normalizeOutputDecision(raw?: string): OutputDecision {
  switch ((raw || "").toUpperCase()) {
    case "ALLOW":
      return "ALLOW";
    case "QUARANTINE":
      return "QUARANTINE";
    case "REDACT":
      return "REDACT";
    case "DENY":
      return "QUARANTINE";
    default:
      // Fail-closed: unknown output decisions must NOT default to ALLOW.
      // An unrecognized decision from the backend should quarantine for safety.
      if (raw) {
        logger.warn("transform", "Unknown output decision, defaulting to QUARANTINE", { raw });
      }
      return raw ? "QUARANTINE" : "ALLOW";
  }
}

function mapOutputFinding(raw: BackendOutputFinding): OutputFinding {
  return {
    type: raw.type ?? "",
    severity: raw.severity ?? "",
    detail: raw.detail ?? "",
    scanner: raw.scanner ?? undefined,
    confidence: raw.confidence ?? undefined,
    matched_pattern: raw.matched_pattern ?? undefined,
    offset: raw.offset ?? undefined,
    length: raw.length ?? undefined,
  };
}

export function mapOutputSafetyRecord(
  raw?: BackendOutputSafetyRecord,
): OutputSafetyRecord | undefined {
  if (!raw || typeof raw !== "object") return undefined;
  return {
    decision: normalizeOutputDecision(raw.decision),
    reason: raw.reason ?? undefined,
    rule_id: raw.rule_id ?? undefined,
    findings: Array.isArray(raw.findings) ? raw.findings.map(mapOutputFinding) : [],
    phase: raw.phase ?? undefined,
    policy_snapshot: raw.policy_snapshot ?? undefined,
    redacted_ptr: raw.redacted_ptr ?? undefined,
    original_ptr: raw.original_ptr ?? undefined,
  };
}

// ---------------------------------------------------------------------------
// Mappers
// ---------------------------------------------------------------------------

export function mapJobRecord(record: BackendJobRecord): Job {
  let id = record.id;
  if (!id) {
    id = generateUUID();
    logger.warn("transform", "Job record with empty ID, assigned placeholder", { placeholderId: id });
  }
  const updatedAt = microsToISO(record.updated_at);
  const outputSafetyFromDecision =
    record.output_decision
      ? { decision: normalizeOutputDecision(record.output_decision) as OutputDecision }
      : undefined;
  const capabilities = Array.from(
    new Set(
      [
        record.capability ? String(record.capability).trim() : "",
        ...(record.requires ?? []).map((r) => String(r).trim()),
      ].filter(Boolean),
    ),
  );
  return {
    id,
    workerId: record.worker_id,
    type: record.topic || "",
    topic: record.topic || "",
    status: normalizeJobStatus(record.state),
    safetyDecision: mapSafetyDecision(
      record.safety_decision,
      record.safety_reason,
      record.safety_rule_id,
    ),
    pool: record.topic || "",
    capabilities,
    riskTags: record.risk_tags ?? [],
    metadata: (() => {
      // Session/run id fallback chain: prefer metadata, fall back to labels.
      // Backends that surface session_id/run_id only via labels would otherwise
      // leave job.metadata.session_id undefined, breaking downstream consumers
      // (OriginPill, ParentContextBanner, JobDetail MetadataBar) that read
      // job.metadata.* via the shared `getJobParentRefs` helper.
      const sessionId =
        record.metadata?.session_id ?? record.labels?.session_id;
      const runId = record.metadata?.run_id ?? record.labels?.run_id;
      return {
        ...(record.actor_id ? { actor_id: record.actor_id } : {}),
        ...(record.actor_type ? { actor_type: record.actor_type } : {}),
        ...(record.pack_id ? { pack_id: record.pack_id } : {}),
        ...(record.tenant ? { tenant: record.tenant } : {}),
        ...(sessionId ? { session_id: sessionId } : {}),
        ...(runId ? { run_id: runId } : {}),
      };
    })(),
    labels: record.labels,
    contextPtr: undefined,
    resultPtr: undefined,
    workflowRunId: record.workflow_run_id,
    createdAt: updatedAt || new Date().toISOString(),
    updatedAt: updatedAt || new Date().toISOString(),
    traceId: record.trace_id,
    tenant: record.tenant,
    team: record.team,
    actorId: record.actor_id,
    actorType: record.actor_type,
    capability: record.capability,
    requires: record.requires,
    attempts: record.attempts,
    output_safety: mapOutputSafetyRecord(record.output_safety) ?? outputSafetyFromDecision,
  };
}

export function mapJobDetail(detail: BackendJobDetail): Job {
  const base = mapJobRecord(detail);
  return {
    ...base,
    metadata: {
      ...base.metadata,
      ...(detail.labels ? detail.labels : {}),
      ...(detail.workflow_id ? { workflow_id: detail.workflow_id } : {}),
      ...(detail.run_id ? { run_id: detail.run_id } : {}),
    },
    contextPtr: detail.context_ptr,
    resultPtr: detail.result_ptr,
    context: detail.context,
    result: detail.result,
    errorMessage: detail.error_message,
    errorStatus: detail.error_status,
    errorCode: detail.error_code,
    errorCodeEnum: detail.error_code_enum && detail.error_code_enum !== 0
      ? detail.error_code_enum
      : undefined,
    lastState: detail.last_state,
    workflowRunId: detail.run_id || base.workflowRunId,
    workflowId: detail.workflow_id,
    idempotencyKey: detail.idempotency_key,
    labels: detail.labels,
    approvalRequired: detail.approval_required,
    approvalRef: detail.approval_ref,
    approvalBy: detail.approval_by,
    approvalRole: detail.approval_role,
    approvalAt: detail.approval_at,
    approvalReason: detail.approval_reason,
    approvalNote: detail.approval_note,
    approvalStatus: detail.approval_status,
    approvalActionability: detail.approval_actionability,
    approvalRevision: detail.approval_revision,
    approvalDecision: detail.approval_decision,
  };
}

const WORKFLOW_NODE_TYPES = new Set([
  "job",
  "worker",
  "agent-task",
  "pack-action",
  "tool-call",
  "approval",
  "delay",
  "condition",
  "notify",
  "fan-out",
  "parallel",
  "http",
  "transform",
  "switch",
  "loop",
  "sub-workflow",
  "subworkflow",
  "error-trigger",
]);

function normalizeWorkflowNodeType(
  raw?: string,
  meta?: Record<string, unknown>,
): string {
  const trimmed = (raw || "").trim().toLowerCase();
  if (!trimmed) return "agent-task";
  if (trimmed === "subworkflow") return "sub-workflow";
  if (WORKFLOW_NODE_TYPES.has(trimmed) && trimmed !== "job" && trimmed !== "worker") return trimmed;
  // Backend "job" or "worker" → differentiate into agent-task / pack-action / tool-call
  if ((trimmed === "job" || trimmed === "worker") && meta) {
    if (typeof meta.pack_id === "string" && meta.pack_id) return "pack-action";
    if (typeof meta.capability === "string" && meta.capability && !meta.prompt) return "tool-call";
  }
  return "agent-task";
}

export function mapWorkflowStep(step: BackendWorkflowStep, fallbackId: string): WorkflowStep {
  let uiType = normalizeWorkflowNodeType(
    step.type,
    step.meta as Record<string, unknown> | undefined,
  );
  if (uiType === "agent-task" && step.for_each) {
    uiType = "fan-out";
  }
  return {
    id: step.id || fallbackId,
    name: step.name || fallbackId,
    type: uiType,
    // Direct backend fields
    worker_id: step.worker_id,
    topic: step.topic,
    depends_on: step.depends_on,
    condition: step.condition,
    for_each: step.for_each,
    max_parallel: step.max_parallel,
    input: step.input,
    input_schema: step.input_schema,
    input_schema_id: step.input_schema_id,
    output_path: step.output_path,
    output_schema: step.output_schema,
    output_schema_id: step.output_schema_id,
    meta: step.meta as Record<string, unknown> | undefined,
    retry: step.retry ? {
      max_retries: step.retry.max_retries,
      backoff_sec: step.retry.initial_backoff_sec,
      backoff_multiplier: step.retry.multiplier,
    } : undefined,
    timeout_sec: step.timeout_sec,
    delay_sec: step.delay_sec,
    delay_until: step.delay_until,
    route_labels: step.route_labels,
    on_error: step.on_error,
    // Raw backend config (branches, parallel steps, etc.)
    config: step.config,
    // Run-time fields
    status: step.status as WorkflowStep["status"],
    output: step.output,
    error: step.error,
    startedAt: step.started_at,
    completedAt: step.completed_at,
  };
}

export function mapWorkflow(def: BackendWorkflow): Workflow {
  const steps = def.steps
    ? Object.entries(def.steps).map(([id, step]) => mapWorkflowStep(step ?? {}, id))
    : [];
  return {
    id: def.id,
    name: def.name || def.id,
    steps,
    timeout_sec: def.timeout_sec,
    timeout: def.timeout_sec ?? 0,
    metadata: {
      orgId: def.org_id,
      teamId: def.team_id,
      description: def.description,
      version: def.version,
      config: def.config,
      inputSchema: def.input_schema,
      parameters: def.parameters,
    },
    input_schema: def.input_schema,
    config: def.config,
    orgId: def.org_id,
    teamId: def.team_id,
    description: def.description,
    version: def.version,
    createdAt: def.created_at,
    updatedAt: def.updated_at,
  };
}

function normalizeRunStatus(raw?: string): WorkflowStep["status"] {
  return normalizeRunStatusValue(raw) ?? "pending";
}

export function mapWorkflowRunStep(step: BackendStepRun, fallbackId: string): WorkflowStep {
  // Detect quarantined steps: status is "failed" but error.code is "output_quarantined"
  let status = normalizeRunStatus(step.status);
  if (status === "failed" && step.error?.code === "output_quarantined") {
    status = "quarantined" as RunStatus;
  }
  return {
    id: step.step_id || fallbackId,
    name: step.step_id || fallbackId,
    type: "step",
    status,
    output: (step.output as Record<string, unknown>) ?? undefined,
    error: step.error ? JSON.stringify(step.error) : undefined,
    startedAt: step.started_at || undefined,
    completedAt: step.completed_at || undefined,
  };
}

export function mapWorkflowRun(run: BackendWorkflowRun): WorkflowRun {
  const steps = run.steps
    ? Object.entries(run.steps).map(([id, step]) => mapWorkflowRunStep(step ?? {}, id))
    : [];
  return {
    id: run.id,
    workflowId: run.workflow_id || "",
    status: normalizeRunStatus(run.status) as WorkflowRun["status"] || "pending",
    steps,
    startedAt: run.started_at ?? null,
    completedAt: run.completed_at ?? null,
    duration: run.completed_at && run.started_at
      ? new Date(run.completed_at).getTime() - new Date(run.started_at).getTime()
      : undefined,
    createdAt: run.created_at,
    updatedAt: run.updated_at,
    orgId: run.org_id,
    teamId: run.team_id,
    input: run.input,
    output: run.output,
    error: run.error,
    rerunOf: run.rerun_of,
    rerunStep: run.rerun_step,
    dryRun: run.dry_run,
    timers: run.timers,
  };
}

export function computeUrgencyLevel(waitMs: number): UrgencyLevel {
  if (!Number.isFinite(waitMs) || waitMs < 0) return "fresh";
  if (waitMs < 2 * 60_000) return "fresh";
  if (waitMs < 15 * 60_000) return "aging";
  if (waitMs < 60 * 60_000) return "critical";
  return "breach";
}

export function deriveApprovalStatus(
  jobState: string | undefined,
  decision: string | undefined,
  resolvedBy?: string,
  explicitStatus?: string,
  explicitDecision?: string,
): ApprovalStatus {
  const normalizedExplicit = (explicitStatus || "").trim().toLowerCase();
  if (
    normalizedExplicit === "pending" ||
    normalizedExplicit === "approved" ||
    normalizedExplicit === "rejected" ||
    normalizedExplicit === "expired" ||
    normalizedExplicit === "invalidated" ||
    normalizedExplicit === "repaired"
  ) {
    return normalizedExplicit;
  }
  const d = (explicitDecision || decision || "").toLowerCase();
  const s = (jobState || "").toLowerCase();
  if (d === "approve" || d === "approved") return "approved";
  if (d === "reject" || d === "rejected" || d === "deny")
    return "rejected";
  if (d === "expire" || d === "expired") return "expired";
  if (d === "invalidate" || d === "invalidated") return "invalidated";
  if (d === "repair" || d === "repaired") return "repaired";
  if (s === "denied") return "rejected";
  if (s === "output_quarantined") return "approved";
  if (s === "timeout") return "expired";
  if (s === "approval_required") return "pending";
  // Job resolved through approval flow — derive from post-approval state.
  if (resolvedBy) {
    if (s === "denied") return "rejected";
    return "approved";
  }
  if (s === "succeeded" || s === "failed" || s === "cancelled" || s === "pending")
    return "approved";
  return "pending";
}

export function deriveApprovalActionability(
  explicitActionability: string | undefined,
  status: ApprovalStatus,
): ApprovalActionability {
  const normalizedExplicit = (explicitActionability || "").trim().toLowerCase();
  if (
    normalizedExplicit === "actionable" ||
    normalizedExplicit === "resolved" ||
    normalizedExplicit === "expired" ||
    normalizedExplicit === "invalidated" ||
    normalizedExplicit === "repaired"
  ) {
    return normalizedExplicit;
  }
  switch (status) {
    case "pending":
      return "actionable";
    case "expired":
      return "expired";
    case "invalidated":
      return "invalidated";
    case "repaired":
      return "repaired";
    case "approved":
    case "rejected":
    default:
      return "resolved";
  }
}

function deriveHumanSummary(
  topic: string,
  capabilities: string[],
  policyReason?: string,
): string {
  const parts: string[] = [];
  if (topic) parts.push(`Job on "${topic}"`);
  if (capabilities.length)
    parts.push(`requires ${capabilities.join(", ")}`);
  if (policyReason) parts.push(`— ${policyReason}`);
  return parts.join(" ") || "Approval requested";
}

export function mapApprovalItem(item: BackendApprovalItem): Approval | null {
  if (!item.job) return null;
  const job = mapJobRecord(item.job);
  const now = Date.now();
  const requestedAt = job.updatedAt || new Date().toISOString();
  const waitMs = now - new Date(requestedAt).getTime();
  const decisionSummary = mapApprovalDecisionSummary(item.decision_summary);
  const status = deriveApprovalStatus(
    item.job.state,
    item.decision,
    item.resolved_by,
    item.approval_status,
    item.approval_decision,
  );
  const actionability = deriveApprovalActionability(
    item.approval_actionability,
    status,
  );

  const workflowContext =
    item.workflow_id || item.workflow_run_id
      ? {
          workflowId: item.workflow_id || "",
          workflowName: item.workflow_name,
          stepId: item.workflow_step_id,
          runId: item.workflow_run_id || "",
          stepIndex: item.step_index,
          stepName: item.step_name,
          totalSteps: item.total_steps,
        }
      : undefined;

  return {
    id: item.approval_ref || job.id,
    jobId: job.id,
    status,
    requestedAt,
    resolvedAt: (item.resolved_at ? microsToISO(item.resolved_at) : undefined) ?? undefined,
    actor: item.resolved_by,
    actorId: job.actorId,
    reason: decisionSummary?.why || item.policy_reason,
    comment: item.resolved_comment,
    policyRule: item.policy_rule_id,
    decisionSummary,
    jobContext: {
      topic: job.topic,
      tenant: job.tenant,
      capabilities: job.capabilities,
      riskTags: job.riskTags,
    },
    topic: job.topic,
    safetyDecision: job.safetyDecision,
    riskTags: job.riskTags,
    capabilities: job.capabilities,
    workflowContext,
    humanSummary:
      decisionSummary?.title ||
      deriveHumanSummary(job.topic, job.capabilities, item.policy_reason),
    urgencyLevel: computeUrgencyLevel(Math.max(0, waitMs)),
    waitMs: Math.max(0, waitMs),
    policySnapshot: item.policy_snapshot,
    jobHash: item.job_hash,
    approvalRef: item.approval_ref,
    tenant: job.tenant,
    contextPtr: item.context_ptr,
    jobInput: item.job_input as Record<string, unknown> | undefined,
    constraints: item.constraints,
    actionability,
    revision: item.approval_revision,
    approvalDecision: item.approval_decision,
    approval_status: item.approval_status ?? status,
    approval_actionability: item.approval_actionability ?? actionability,
    approval_revision: item.approval_revision,
    approval_decision: item.approval_decision,
    blastRadius: item.blast_radius
      ? {
          systems: item.blast_radius.systems ?? [],
          namespaces: item.blast_radius.namespaces ?? [],
          resources: item.blast_radius.resources ?? [],
          scopeDescription: item.blast_radius.scope_description ?? "",
        }
      : undefined,
    priorApprovals: (item.prior_approvals ?? []).map((pa) => ({
      jobId: pa.job_id ?? "",
      topic: pa.topic ?? "",
      tenant: pa.tenant ?? "",
      decision: pa.decision ?? "",
      resolvedBy: pa.resolved_by ?? "",
      resolvedAt: pa.resolved_at ?? 0,
      wasApproved: pa.was_approved ?? false,
    })),
    rollbackHint: item.rollback_hint ?? "",
    policySnapshotSummary: item.policy_snapshot_summary
      ? {
          ruleCount: item.policy_snapshot_summary.rule_count ?? 0,
          matchedRule: {
            id: item.policy_snapshot_summary.matched_rule?.id ?? "",
            description:
              item.policy_snapshot_summary.matched_rule?.description ?? "",
            decision:
              item.policy_snapshot_summary.matched_rule?.decision ?? "",
            constraintsSummary:
              item.policy_snapshot_summary.matched_rule?.constraints_summary ??
              "",
          },
          policyVersion: item.policy_snapshot_summary.policy_version ?? "",
        }
      : undefined,
  };
}

function mapApprovalDecisionSummary(
  summary?: BackendApprovalDecisionSummary,
): ApprovalDecisionSummary | undefined {
  if (!summary || typeof summary !== "object") return undefined;
  const title = typeof summary.title === "string" ? summary.title.trim() : "";
  if (!title) return undefined;
  return {
    source: summary.source ?? "policy_only",
    completeness: summary.completeness ?? "minimal",
    contextStatus: summary.context_status ?? "absent",
    title,
    subject: typeof summary.subject === "string" ? summary.subject : undefined,
    why: typeof summary.why === "string" ? summary.why : undefined,
    nextEffect:
      typeof summary.next_effect === "string"
        ? summary.next_effect
        : undefined,
    amount:
      typeof summary.amount === "number" && Number.isFinite(summary.amount)
        ? summary.amount
        : undefined,
    currency:
      typeof summary.currency === "string" ? summary.currency : undefined,
    vendor: typeof summary.vendor === "string" ? summary.vendor : undefined,
    itemCount:
      typeof summary.item_count === "number" && Number.isFinite(summary.item_count)
        ? summary.item_count
        : undefined,
    itemsPreview: Array.isArray(summary.items_preview)
      ? summary.items_preview.filter(
          (value): value is string => typeof value === "string" && value.length > 0,
        )
      : undefined,
    escalationReason:
      typeof summary.escalation_reason === "string"
        ? summary.escalation_reason
        : undefined,
    missingFields: Array.isArray(summary.missing_fields)
      ? summary.missing_fields.filter(
          (value): value is string => typeof value === "string" && value.length > 0,
        )
      : undefined,
  };
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function mapPolicySnapshotSummaryFromRaw(raw?: any): ApprovalPolicySnapshot | undefined {
  if (!raw || typeof raw !== "object") return undefined;
  return {
    ruleCount: raw.rule_count ?? 0,
    matchedRule: {
      id: raw.matched_rule?.id ?? "",
      description: raw.matched_rule?.description ?? "",
      decision: raw.matched_rule?.decision ?? "",
      constraintsSummary: raw.matched_rule?.constraints_summary ?? "",
    },
    policyVersion: raw.policy_version ?? "",
  };
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function mapApprovalContext(raw: any): ApprovalContext {
  return {
    approval: raw.approval ?? {},
    blastRadius: raw.blast_radius
      ? {
          systems: raw.blast_radius.systems ?? [],
          namespaces: raw.blast_radius.namespaces ?? [],
          resources: raw.blast_radius.resources ?? [],
          scopeDescription: raw.blast_radius.scope_description ?? "",
        }
      : { systems: [], namespaces: [], resources: [], scopeDescription: "" },
    priorApprovals: (raw.prior_approvals ?? []).map((pa: any) => ({
      jobId: pa.job_id ?? "",
      topic: pa.topic ?? "",
      tenant: pa.tenant ?? "",
      decision: pa.decision ?? "",
      resolvedBy: pa.resolved_by ?? "",
      resolvedAt: pa.resolved_at ?? 0,
      wasApproved: pa.was_approved ?? false,
    })),
    rollbackHint: raw.rollback_hint ?? "",
    policySnapshotSummary: mapPolicySnapshotSummaryFromRaw(raw.policy_snapshot_summary) ?? {
      ruleCount: 0,
      matchedRule: { id: "", description: "", decision: "", constraintsSummary: "" },
      policyVersion: "",
    },
    timeRemainingMs: raw.time_remaining_ms ?? null,
    constraints: raw.constraints ?? null,
  };
}

export function mapDLQEntry(entry: BackendDLQEntry): DLQEntry {
  return {
    id: entry.job_id,
    jobId: entry.job_id,
    error: entry.reason || "",
    retryCount: entry.attempts ?? 0,
    maxRetries: 0,
    originalTopic: entry.topic || "",
    failedAt: entry.created_at || "",
    status: entry.status,
    reasonCode: entry.reason_code,
    lastState: entry.last_state,
    reason: entry.reason,
    attempts: entry.attempts,
    createdAt: entry.created_at,
  };
}

function normalizeMatchCriteria(raw: Record<string, unknown>): PolicyRuleMatch {
  const out: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(raw)) {
    out[key] = value;
  }
  return out as PolicyRuleMatch;
}

export function mapPolicyRule(raw: Record<string, unknown>): PolicyRule {
  const id = typeof raw.id === "string" ? raw.id : "";
  const decision = typeof raw.decision === "string" ? raw.decision : "";
  const reason = typeof raw.reason === "string" ? raw.reason : "";
  const match = (raw.match as Record<string, unknown>) ?? {};
  const priority = typeof raw.priority === "number" ? raw.priority : undefined;
  const logic = typeof raw.logic === "string" ? raw.logic : undefined;
  const name = typeof raw.name === "string" ? raw.name : id;
  const normalizedDecision = normalizeDecisionType(decision);
  const normalizedMatch = normalizeMatchCriteria(match);
  return {
    id,
    name,
    description: typeof raw.description === "string" ? raw.description : undefined,
    match: normalizedMatch,
    decision: normalizedDecision,
    matchCriteria: normalizedMatch as Record<string, unknown>,
    decisionType: normalizedDecision,
    reason,
    priority: priority ?? 0,
    logic,
    source: typeof raw.source === "object" && raw.source ? (raw.source as Record<string, unknown>) : undefined,
    enabled: typeof raw.enabled === "boolean" ? raw.enabled : true,
    constraints: raw.constraints && typeof raw.constraints === "object" ? (raw.constraints as Record<string, unknown>) : undefined,
    velocity: raw.velocity && typeof raw.velocity === "object" ? (raw.velocity as { max_requests: number; window_seconds: number; key: string }) : undefined,
  };
}

export function mapPolicyBundleSummary(summary: BackendPolicyBundleSummary, content?: string): PolicyBundle {
  const versionNum = Number.parseInt(summary.version ?? "", 10);
  let rules: PolicyRule[] = [];
  if (content) {
    try {
      const parsed = YAML.parse(content) as Record<string, unknown> | null;
      const rawRules = Array.isArray(parsed?.rules) ? parsed.rules : [];
      rules = rawRules.map((r: unknown) => mapPolicyRule(r as Record<string, unknown>));
    } catch {
      logger.warn("transform", "YAML parse error in policy bundle summary, falling back to empty rules");
    }
  }
  return {
    id: summary.id,
    name: summary.id,
    rules,
    version: Number.isFinite(versionNum) ? versionNum : undefined,
    enabled: summary.enabled ?? true,
    publishedAt: summary.updated_at || summary.created_at,
    source: summary.source,
    author: summary.author,
    message: summary.message,
    createdAt: summary.created_at,
    updatedAt: summary.updated_at,
    installedAt: summary.installed_at,
    sha256: summary.sha256,
    rule_count: summary.rule_count,
    healthStatus: undefined,
  };
}

export function readPolicyBundleSignature(
  raw: unknown,
): PolicyBundleSignature | undefined {
  if (!raw || typeof raw !== "object") return undefined;
  const bundle = raw as Record<string, unknown>;
  const rawSig = bundle._signature ?? bundle.signature;
  if (!rawSig || typeof rawSig !== "object") return undefined;

  const sig = rawSig as Record<string, unknown>;
  const algorithm =
    typeof sig.algorithm === "string" ? sig.algorithm.trim() : "";
  const keyID = typeof sig.key_id === "string" ? sig.key_id.trim() : "";
  const value = typeof sig.value === "string" ? sig.value.trim() : "";
  const hash = typeof sig.hash === "string" ? sig.hash.trim() : "";
  const signedBytes =
    typeof sig.signed_bytes === "number"
      ? sig.signed_bytes
      : typeof sig.signed_bytes === "string"
        ? Number.parseInt(sig.signed_bytes, 10)
        : Number.NaN;

  if (!algorithm || !keyID || !value || !hash || !Number.isFinite(signedBytes)) {
    return undefined;
  }

  return {
    algorithm,
    key_id: keyID,
    value,
    hash,
    signed_bytes: signedBytes,
  };
}

export function mapPolicyBundleDetail(detail: BackendPolicyBundleDetail): PolicyBundle {
  let rules: PolicyRule[] = [];
  if (!detail || typeof detail !== "object") {
    return {
      id: "",
      name: "",
      rules,
      enabled: true,
      content: "",
    };
  }
  const content = typeof detail.content === "string" ? detail.content : "";
  if (content) {
    try {
      const parsed = YAML.parse(content) as Record<string, unknown> | null;
      const rawRules = Array.isArray(parsed?.rules) ? parsed.rules : [];
      rules = rawRules.map((r: unknown) => mapPolicyRule(r as Record<string, unknown>));
    } catch {
      logger.warn("transform", "YAML parse error in policy bundle detail, falling back to empty rules");
    }
  }
  return {
    id: detail.id,
    name: detail.id,
    rules,
    enabled: detail.enabled ?? true,
    content,
    author: detail.author,
    message: detail.message,
    createdAt: detail.created_at,
    updatedAt: detail.updated_at,
  };
}

// ---------------------------------------------------------------------------
// Audit classification helpers
// ---------------------------------------------------------------------------

const SAFETY_ACTIONS = new Set(["evaluate", "allow", "deny", "throttle"]);
const HUMAN_ACTIONS = new Set([
  "edit", "create", "delete", "approve", "reject", "cancel",
  "remediate", "change_password", "set", "snapshot", "submit",
]);
const SYSTEM_ACTIONS = new Set([
  "dispatch", "complete", "fail", "timeout", "retry", "escalate",
]);
const ACCESS_ACTIONS = new Set(["login", "logout", "register"]);

function classifyAuditCategory(
  action: string,
  resourceType: string,
  actorId: string,
): AuditCategory {
  const a = action.toLowerCase();
  if (SAFETY_ACTIONS.has(a) || resourceType.toLowerCase() === "safety") return "safety_decision";
  if (ACCESS_ACTIONS.has(a)) return "access_event";
  if (HUMAN_ACTIONS.has(a) && actorId !== "system") return "human_action";
  if (SYSTEM_ACTIONS.has(a)) return "system_event";
  // Default: system_event
  return "system_event";
}

const HIGH_SEVERITY_ACTIONS = new Set(["edit", "delete", "create", "set", "change_password"]);
const HIGH_SEVERITY_RESOURCES = new Set(["policy", "user", "config", "approval"]);
const MEDIUM_SEVERITY_ACTIONS = new Set(["approve", "reject", "submit", "cancel", "remediate", "snapshot"]);

function classifyAuditSeverity(action: string, resourceType: string): AuditSeverity {
  const a = action.toLowerCase();
  const r = resourceType.toLowerCase();
  if (HIGH_SEVERITY_ACTIONS.has(a) && HIGH_SEVERITY_RESOURCES.has(r)) return "high";
  if (MEDIUM_SEVERITY_ACTIONS.has(a)) return "medium";
  return "low";
}

function deriveAuditActor(actorId: string, role?: string): AuditActor {
  let type: AuditActor["type"] = "api_key";
  if (actorId === "system") type = "system";
  else if (role === "admin" || role === "user") type = "user";
  return { id: actorId, type, role };
}

export function auditResourceLink(
  resourceType: string,
  resourceId: string,
): string {
  switch (resourceType.toLowerCase()) {
    case "job": return `/jobs/${resourceId}`;
    case "workflow": return `/workflows/${resourceId}/studio`;
    case "run": return `/workflows`;
    case "policy": return `/govern/overview?tab=bundles`;
    case "user": return `/settings`;
    case "pack": return `/packs`;
    case "approval": return `/approvals`;
    default: return "";
  }
}

function tryParseJson(raw?: string): Record<string, unknown> | undefined {
  if (!raw) return undefined;
  try {
    const parsed = JSON.parse(raw);
    return typeof parsed === "object" && parsed !== null ? parsed : undefined;
  } catch {
    logger.debug("transform", "JSON parse failed in tryParseJson");
    return undefined;
  }
}

export function mapPolicyAuditEntry(entry: BackendPolicyAuditEntry): AuditEntry {
  const actorId = entry.actor_id || "unknown";
  const resourceType = entry.resource_type || "policy";
  const resourceId = entry.resource_id || "";
  const resourceName = entry.resource_name || undefined;
  const action = entry.action || "";

  return {
    id: entry.id,
    timestamp: entry.created_at || new Date().toISOString(),
    eventType: action || "policy",
    actor: actorId || entry.role || "unknown",
    resourceType,
    resourceId,
    resourceName,
    action,
    message: entry.message || "",
    payload: {
      bundle_ids: entry.bundle_ids,
      snapshot_before: entry.snapshot_before,
      snapshot_after: entry.snapshot_after,
    },
    category: classifyAuditCategory(action, resourceType, actorId),
    severity: classifyAuditSeverity(action, resourceType),
    actorInfo: deriveAuditActor(actorId, entry.role),
    resourceInfo: {
      type: resourceType,
      id: resourceId,
      name: resourceName,
      link: auditResourceLink(resourceType, resourceId),
    },
    snapshotBefore: tryParseJson(entry.snapshot_before),
    snapshotAfter: tryParseJson(entry.snapshot_after),
    bundleIds: entry.bundle_ids,
  };
}

export function mapPolicySnapshotSummary(snapshot: BackendPolicySnapshotSummary) {
  return {
    id: snapshot.id,
    createdAt: snapshot.created_at || "",
    note: snapshot.note,
  };
}

export function mapPolicySnapshot(snapshot: BackendPolicySnapshot) {
  // Extract rules from all bundles in the snapshot
  const rules: ReturnType<typeof mapPolicyRule>[] = [];
  if (snapshot.bundles && typeof snapshot.bundles === "object") {
    for (const bundle of Object.values(snapshot.bundles)) {
      const b = bundle as Record<string, unknown>;
      const bundleRules = Array.isArray(b.rules) ? b.rules : [];
      for (const r of bundleRules) {
        rules.push(mapPolicyRule(r as Record<string, unknown>));
      }
    }
  }

  return {
    id: snapshot.id,
    createdAt: snapshot.created_at || "",
    note: snapshot.note,
    bundles: snapshot.bundles,
    rules,
  };
}

export function mapPackRecord(record: BackendPackRecord): Pack {
  const metadata = record.manifest?.metadata;
  const manifest = record.manifest as Record<string, unknown> | undefined;
  const topics = record.manifest?.topics ?? [];

  // Extract capabilities from topics first
  const capSet = new Set<string>();
  for (const t of topics) {
    const cap = (t?.capability || "").trim();
    if (cap) capSet.add(cap);
  }

  // Fallback: manifest.capabilities, manifest.actions, manifest.tools
  if (capSet.size === 0 && manifest) {
    const fallbackArrays = [manifest.capabilities, manifest.actions, manifest.tools];
    for (const arr of fallbackArrays) {
      if (Array.isArray(arr)) {
        for (const item of arr) {
          const name = typeof item === "string" ? item.trim()
            : typeof item === "object" && item !== null
              ? (String((item as Record<string, unknown>).name ?? (item as Record<string, unknown>).id ?? "")).trim()
              : "";
          if (name) capSet.add(name);
        }
      }
      if (capSet.size > 0) break;
    }
  }

  // Derive poolAssignment from manifest
  const pool = manifest
    ? String(manifest.pool ?? manifest.poolAssignment ?? manifest.pool_assignment ?? "").trim()
    : "";

  const title = metadata?.title?.trim();
  return {
    id: record.id,
    name: title || metadata?.id || record.id,
    version: record.version || metadata?.version || "",
    status: record.status || "unknown",
    capabilities: Array.from(capSet),
    poolAssignment: pool || undefined,
    config: {},
    manifest: manifest,
    resources: record.resources,
    installedAt: record.installed_at,
    installedBy: record.installed_by,
    description: metadata?.description,
  };
}

export function mapMarketplaceCatalog(cat: BackendMarketplaceCatalog): MarketplaceCatalog {
  return {
    id: cat.id,
    title: cat.title,
    url: cat.url,
    enabled: cat.enabled,
    updatedAt: cat.updated_at,
    error: cat.error,
  };
}

export function mapMarketplaceItem(item: BackendMarketplaceItem): MarketplacePack {
  return {
    id: item.id,
    version: item.version,
    title: item.title,
    description: item.description,
    author: item.author,
    homepage: item.homepage,
    source: item.source,
    image: item.image,
    license: item.license,
    url: item.url,
    sha256: item.sha256,
    catalogId: item.catalog_id,
    catalogTitle: item.catalog_title,
    capabilities: item.capabilities,
    requires: item.requires,
    riskTags: item.risk_tags,
    installedVersion: item.installed_version,
    installedStatus: item.installed_status,
    installedAt: item.installed_at,
  };
}

export function mapHeartbeatToWorker(hb: BackendHeartbeat): Worker | null {
  if (!hb || !hb.worker_id) return null;
  const activeJobs = Number(hb.active_jobs) || 0;
  const capacity = Math.max(0, Number(hb.max_parallel_jobs) || 0);
  const name =
    (hb.labels && (hb.labels.name || hb.labels.worker_name || hb.labels.worker)) ||
    hb.worker_id;
  const status = hb.status ?? (activeJobs > 0 ? "busy" : "idle");
  return {
    id: hb.worker_id,
    name,
    pool: hb.pool ?? "default",
    capabilities: hb.capabilities ?? [],
    status,
    activeJobs,
    // capacity fallback: if backend reports 0 max_parallel_jobs, use at least 1
    capacity: capacity > 0 ? capacity : Math.max(1, activeJobs),
    lastHeartbeat: hb.last_heartbeat,
    region: hb.region,
    type: hb.type,
    cpuLoad: hb.cpu_load,
    gpuUtilization: hb.gpu_utilization,
    memoryLoad: hb.memory_load,
  };
}

// ---------------------------------------------------------------------------
// Pool mapper
// ---------------------------------------------------------------------------

export interface BackendPoolSummary {
  name: string;
  workers: number;
  active_jobs: number;
  capacity: number;
  utilization: number;
  topics?: string[];
  worker_list?: BackendHeartbeat[];
  captured_at?: string;
}

export function mapPoolResponse(bp: BackendPoolSummary, mapWorker = mapHeartbeatToWorker): Pool {
  return {
    name: bp.name,
    workerCount: bp.workers ?? 0,
    activeJobs: bp.active_jobs ?? 0,
    capacity: bp.capacity ?? 0,
    utilization: Math.round((bp.utilization ?? 0) * 100),
    topics: bp.topics ?? [],
    workers: (bp.worker_list ?? []).map(mapWorker).filter((w): w is Worker => !!w),
  };
}

// ---------------------------------------------------------------------------
// Evals mappers
// ---------------------------------------------------------------------------

const EVAL_RUN_STATUSES: ReadonlySet<EvalRunStatus> = new Set([
  "pass",
  "fail",
  "regression",
  "error",
]);

const EVAL_DRIFT_DIRECTIONS: ReadonlySet<EvalDriftDirection> = new Set([
  "escalated",
  "relaxed",
  "unchanged",
]);

const SAFETY_DECISION_TYPES: ReadonlySet<SafetyDecisionType> = new Set([
  "allow",
  "deny",
  "require_approval",
  "allow_with_constraints",
  "throttle",
]);

export interface BackendEvalDataset {
  id?: string;
  name?: string;
  version?: number;
  tenant?: string;
  description?: string;
  entry_count?: number;
  content_hash?: string;
  created_at?: string;
  updated_at?: string;
  created_by?: string;
}

export interface BackendEvalEntry {
  id?: string;
  input?: Record<string, unknown>;
  expected_decision?: string;
  rule_id?: string;
  metadata?: Record<string, unknown>;
  source?: string;
  source_ref?: string;
  notes?: string;
}

export interface BackendEvalRunSummary {
  total?: number;
  passed?: number;
  failed?: number;
  regressions?: number;
  errored?: number;
  score_percent?: number | null;
}

export interface BackendEvalEntryResult {
  entry_id?: string;
  input?: Record<string, unknown>;
  expected_decision?: string;
  actual_decision?: string;
  rule_id?: string;
  reason?: string;
  status?: string;
  drift_direction?: string;
  constraints?: Record<string, unknown>;
}

export interface BackendEvalRun {
  run_id?: string;
  dataset_id?: string;
  dataset_name?: string;
  dataset_version?: number;
  policy_snapshot?: string;
  started_at?: string;
  completed_at?: string;
  summary?: BackendEvalRunSummary;
  entries?: BackendEvalEntryResult[];
}

function normalizeEvalRunStatus(raw: unknown): EvalRunStatus {
  if (typeof raw === "string") {
    const lower = raw.toLowerCase();
    if (EVAL_RUN_STATUSES.has(lower as EvalRunStatus)) {
      return lower as EvalRunStatus;
    }
  }
  logger.warn("evals", "unknown run status, falling back to error", { raw });
  return "error";
}

function normalizeDriftDirection(raw: unknown): EvalDriftDirection {
  if (typeof raw === "string") {
    const lower = raw.toLowerCase();
    if (EVAL_DRIFT_DIRECTIONS.has(lower as EvalDriftDirection)) {
      return lower as EvalDriftDirection;
    }
  }
  return "unchanged";
}

function normalizeSafetyDecisionType(raw: unknown): SafetyDecisionType {
  if (typeof raw === "string") {
    const lower = raw.toLowerCase();
    if (SAFETY_DECISION_TYPES.has(lower as SafetyDecisionType)) {
      return lower as SafetyDecisionType;
    }
  }
  return "deny";
}

function coerceScorePercent(raw: unknown): number | null {
  if (raw === null || raw === undefined) return null;
  const num = typeof raw === "number" ? raw : Number(raw);
  if (!Number.isFinite(num)) return null;
  return num;
}

export function mapEvalDataset(raw: BackendEvalDataset): EvalDataset {
  return {
    id: raw.id ?? "",
    name: raw.name ?? "",
    version: typeof raw.version === "number" ? raw.version : Number(raw.version ?? 1),
    tenant: raw.tenant ?? "",
    description: raw.description,
    entryCount: typeof raw.entry_count === "number" ? raw.entry_count : 0,
    contentHash: raw.content_hash ?? "",
    createdAt: raw.created_at ?? "",
    updatedAt: raw.updated_at ?? raw.created_at ?? "",
    createdBy: raw.created_by,
  };
}

export function mapEvalEntry(raw: BackendEvalEntry): EvalEntry {
  return {
    id: raw.id ?? "",
    input: raw.input ?? {},
    expectedDecision: normalizeSafetyDecisionType(raw.expected_decision),
    ruleId: raw.rule_id,
    metadata: raw.metadata,
    source: raw.source ?? "unknown",
    sourceRef: raw.source_ref,
    notes: raw.notes,
  };
}

export function mapEvalEntryResult(raw: BackendEvalEntryResult): EvalEntryResult {
  const expected = normalizeSafetyDecisionType(raw.expected_decision);
  const actualRaw = typeof raw.actual_decision === "string" ? raw.actual_decision.toLowerCase() : "";
  const actual: SafetyDecisionType | string = SAFETY_DECISION_TYPES.has(
    actualRaw as SafetyDecisionType,
  )
    ? (actualRaw as SafetyDecisionType)
    : actualRaw || "unknown";
  return {
    entryId: raw.entry_id ?? "",
    input: raw.input ?? {},
    expectedDecision: expected,
    actualDecision: actual,
    ruleId: raw.rule_id,
    reason: raw.reason,
    status: normalizeEvalRunStatus(raw.status),
    driftDirection: normalizeDriftDirection(raw.drift_direction),
    constraints: raw.constraints as PolicyConstraints | undefined,
  };
}

function mapEvalRunSummary(raw: BackendEvalRunSummary | undefined): EvalRunSummary {
  const summary = raw ?? {};
  return {
    total: typeof summary.total === "number" ? summary.total : 0,
    passed: typeof summary.passed === "number" ? summary.passed : 0,
    failed: typeof summary.failed === "number" ? summary.failed : 0,
    regressions: typeof summary.regressions === "number" ? summary.regressions : 0,
    errored: typeof summary.errored === "number" ? summary.errored : 0,
    scorePercent: coerceScorePercent(summary.score_percent),
  };
}

export function mapEvalRun(raw: BackendEvalRun): EvalRun {
  return {
    runId: raw.run_id ?? "",
    datasetId: raw.dataset_id ?? "",
    datasetName: raw.dataset_name ?? "",
    datasetVersion:
      typeof raw.dataset_version === "number" ? raw.dataset_version : Number(raw.dataset_version ?? 0),
    policySnapshot: raw.policy_snapshot ?? "",
    startedAt: raw.started_at ?? "",
    completedAt: raw.completed_at,
    summary: mapEvalRunSummary(raw.summary),
    entries: Array.isArray(raw.entries) ? raw.entries.map(mapEvalEntryResult) : undefined,
  };
}

export function isRegressionRun(run: Pick<EvalRun, "summary">): boolean {
  return (run.summary?.regressions ?? 0) > 0;
}

export interface BackendDelegationChainLink {
  agent_id?: string;
  issued_at?: string;
  expires_at?: string;
  jti?: string;
  parent_jti?: string;
  issued_by?: string;
}

export interface BackendDelegationView {
  jti?: string;
  issuer?: string;
  subject?: string;
  audience?: string;
  allowed_actions?: string[];
  allowed_topics?: string[];
  chain?: BackendDelegationChainLink[];
  chain_depth?: number;
  issued_at?: string;
  expires_at?: string;
  revoked?: boolean;
  revoked_at?: string;
  revoked_reason?: string;
}

export interface BackendDelegationListResponse {
  items?: BackendDelegationView[];
  next_cursor?: string | null;
  nextCursor?: string | null;
}

export function mapDelegationChainLink(
  raw: BackendDelegationChainLink,
): DelegationChainLink {
  return {
    agentId: raw.agent_id ?? "",
    issuedAt: raw.issued_at ?? "",
    expiresAt: raw.expires_at ?? "",
    jti: raw.jti ?? "",
    parentJti: raw.parent_jti || undefined,
    issuedBy: raw.issued_by ?? "",
  };
}

export function mapDelegationView(raw: BackendDelegationView): DelegationView {
  return {
    jti: raw.jti ?? "",
    issuer: raw.issuer ?? "",
    subject: raw.subject ?? "",
    audience: raw.audience ?? "",
    allowedActions: Array.isArray(raw.allowed_actions)
      ? raw.allowed_actions.filter((value): value is string => typeof value === "string")
      : [],
    allowedTopics: Array.isArray(raw.allowed_topics)
      ? raw.allowed_topics.filter((value): value is string => typeof value === "string")
      : [],
    chain: Array.isArray(raw.chain) ? raw.chain.map(mapDelegationChainLink) : [],
    chainDepth: typeof raw.chain_depth === "number" ? raw.chain_depth : 0,
    issuedAt: raw.issued_at ?? "",
    expiresAt: raw.expires_at ?? "",
    revoked: raw.revoked ?? false,
    revokedAt: raw.revoked_at || undefined,
    revokedReason: raw.revoked_reason || undefined,
  };
}

export function mapDelegationListResponse(
  raw: BackendDelegationListResponse,
): DelegationListResponse {
  return {
    items: Array.isArray(raw.items) ? raw.items.map(mapDelegationView) : [],
    nextCursor: raw.next_cursor ?? raw.nextCursor ?? undefined,
  };
}

