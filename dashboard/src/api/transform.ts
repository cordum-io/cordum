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
  AgentActionEvent,
  AgentActionEventPage,
  AgentExecution,
  AgentExecutionPage,
  EdgeApproval,
  EdgeApprovalDecision,
  EdgeApprovalPage,
  EdgeApprovalStatus,
  EdgeArtifactPointer,
  EdgeArtifactType,
  EdgeDecision,
  EdgeError,
  EdgeEventStreamEnvelope,
  EdgeJobLink,
  EdgeLabels,
  EdgeLayer,
  EdgeMissingArtifact,
  EdgePage,
  EdgeRedactionLevel,
  EdgeRetentionClass,
  EdgeSession,
  EdgeSessionCreateResponse,
  EdgeSessionExportBundle,
  EdgeSessionPage,
  EdgeStreamPayload,
  JsonObject,
  JsonValue,
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
  /** Design-time policy gate hint exposed by cordum-core task-913b6c6c. */
  policy_gate?: "allow" | "deny" | "require_approval";
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
  /** Audit-chain hash for this run-step's safety decision, exposed by
   *  cordum-core task-913b6c6c (RunStepStatus.audit_hash). */
  audit_hash?: string | null;
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
        logger.warn("transform", "unknown governance verdict, defaulting to deny", { raw });
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
    // Design-time policy gate hint from cordum-core task-913b6c6c. Drives
    // the Shield icon in WorkflowNodeGovernanceOverlay (governance studio).
    policyGate: step.policy_gate,
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
    // Audit-chain hash from cordum-core task-913b6c6c. Drives the audit-hash
    // chip in WorkflowNodeGovernanceOverlay on RunDetailPage.
    auditHash: step.audit_hash ?? undefined,
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

function recordFromUnknown(raw: unknown): Record<string, unknown> | undefined {
  return raw && typeof raw === "object" && !Array.isArray(raw)
    ? (raw as Record<string, unknown>)
    : undefined;
}

function stringArrayFromUnknown(raw: unknown): string[] {
  return Array.isArray(raw)
    ? raw.filter((value): value is string => typeof value === "string")
    : [];
}

function stringFromUnknown(raw: unknown): string {
  return typeof raw === "string" ? raw : "";
}

function boolFromUnknown(raw: unknown): boolean {
  return typeof raw === "boolean" ? raw : false;
}

function numberFromUnknown(raw: unknown, fallback = 0): number {
  if (typeof raw === "number" && Number.isFinite(raw)) return raw;
  if (typeof raw === "string" && raw.trim()) {
    const parsed = Number(raw);
    if (Number.isFinite(parsed)) return parsed;
  }
  return fallback;
}

function mapPolicySnapshotSummaryFromRaw(raw?: unknown): ApprovalPolicySnapshot | undefined {
  const summary = recordFromUnknown(raw);
  if (!summary) return undefined;
  const matchedRule = recordFromUnknown(summary.matched_rule) ?? {};
  return {
    ruleCount: numberFromUnknown(summary.rule_count),
    matchedRule: {
      id: stringFromUnknown(matchedRule.id),
      description: stringFromUnknown(matchedRule.description),
      decision: stringFromUnknown(matchedRule.decision),
      constraintsSummary: stringFromUnknown(matchedRule.constraints_summary),
    },
    policyVersion: stringFromUnknown(summary.policy_version),
  };
}

export function mapApprovalContext(raw: unknown): ApprovalContext {
  const context = recordFromUnknown(raw) ?? {};
  const blastRadius = recordFromUnknown(context.blast_radius);
  const priorApprovals = Array.isArray(context.prior_approvals)
    ? context.prior_approvals
    : [];
  const rawTimeRemainingMs = context.time_remaining_ms;
  const parsedTimeRemainingMs =
    rawTimeRemainingMs == null ? null : numberFromUnknown(rawTimeRemainingMs, Number.NaN);
  const timeRemainingMs =
    parsedTimeRemainingMs == null || Number.isFinite(parsedTimeRemainingMs)
      ? parsedTimeRemainingMs
      : null;
  return {
    approval: recordFromUnknown(context.approval) ?? {},
    blastRadius: blastRadius
      ? {
          systems: stringArrayFromUnknown(blastRadius.systems),
          namespaces: stringArrayFromUnknown(blastRadius.namespaces),
          resources: stringArrayFromUnknown(blastRadius.resources),
          scopeDescription: stringFromUnknown(blastRadius.scope_description),
        }
      : { systems: [], namespaces: [], resources: [], scopeDescription: "" },
    priorApprovals: priorApprovals.flatMap((pa) => {
      const approval = recordFromUnknown(pa);
      if (!approval) return [];
      return [
        {
          jobId: stringFromUnknown(approval.job_id),
          topic: stringFromUnknown(approval.topic),
          tenant: stringFromUnknown(approval.tenant),
          decision: stringFromUnknown(approval.decision),
          resolvedBy: stringFromUnknown(approval.resolved_by),
          resolvedAt: numberFromUnknown(approval.resolved_at),
          wasApproved: boolFromUnknown(approval.was_approved),
        },
      ];
    }),
    rollbackHint: stringFromUnknown(context.rollback_hint),
    policySnapshotSummary: mapPolicySnapshotSummaryFromRaw(context.policy_snapshot_summary) ?? {
      ruleCount: 0,
      matchedRule: { id: "", description: "", decision: "", constraintsSummary: "" },
      policyVersion: "",
    },
    timeRemainingMs,
    constraints: recordFromUnknown(context.constraints) ?? null,
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

// ---------------------------------------------------------------------------
// SIEM audit feed (GET /api/v1/audit/events)
// ---------------------------------------------------------------------------

// SiemAuditEventInput captures the SIEM feed wire shape consumed by
// mapAuditEvent. Mirrors the orval-generated `AuditEvent` interface but
// stays local so transform.ts isn't tied to the regenerated module's
// re-export shuffles. Optional fields use `undefined`-safe defaults so
// a partial backend payload still produces a valid AuditEntry.
export interface SiemAuditEventInput {
  id: string;
  seq?: number;
  timestamp: string;
  event_type: string;
  severity?: string;
  tenant_id?: string;
  agent_id?: string;
  agent_name?: string;
  agent_risk_tier?: string;
  job_id?: string;
  action: string;
  decision?: string;
  matched_rule?: string;
  reason?: string;
  identity?: string;
  extra?: Record<string, string>;
  event_hash?: string;
  prev_hash?: string;
}

// Map an SIEM event type onto an AuditEntry resourceType. Derived from
// the `<resource>.<verb>` event-type convention used by the audit
// exporter; everything before the first separator (`.` or `_`) is the
// resource family. Falls back to the raw event_type when the split
// yields nothing useful.
function siemEventResourceType(eventType: string): string {
  const trimmed = eventType.trim();
  if (!trimmed) return "audit";
  const dotIdx = trimmed.indexOf(".");
  const usIdx = trimmed.indexOf("_");
  const candidates = [dotIdx, usIdx].filter((i) => i > 0);
  if (candidates.length === 0) return trimmed.toLowerCase();
  const cut = Math.min(...candidates);
  return trimmed.slice(0, cut).toLowerCase();
}

// Derive the AuditEntry severity from the SIEM event severity. The
// backend emits CRITICAL/HIGH/MEDIUM/LOW/INFO; the dashboard's
// AuditSeverity is `high|medium|low`. Map CRITICAL→high, INFO→low; the
// middle three pass through.
function siemSeverityToAuditSeverity(raw?: string): AuditSeverity {
  switch ((raw ?? "").toUpperCase()) {
    case "CRITICAL":
    case "HIGH":
      return "high";
    case "MEDIUM":
      return "medium";
    case "LOW":
    case "INFO":
    case "":
    default:
      return "low";
  }
}

// mapAuditEvent translates one SIEM audit event into the dashboard's
// AuditEntry shape. Used by useAuditEvents to feed the Audit Log page
// with the FULL chained event feed (MCP, edge, worker, output policy,
// delegation, ...) — the previous /policy/audit-only path missed every
// non-policy-bundle subsystem.
//
// Resolution order for fields that have multiple sources:
//   actor       = identity → agent_id → "unknown"
//   resourceId  = extra.resource_id → extra.session_id → extra.job_id → job_id
//   payload     = the full extra map (post-server-side redaction)
export function mapAuditEvent(event: SiemAuditEventInput): AuditEntry {
  const extra = event.extra ?? {};
  const actor =
    (event.identity && event.identity.trim()) ||
    (event.agent_id && event.agent_id.trim()) ||
    "unknown";
  const resourceType = siemEventResourceType(event.event_type);
  const resourceId =
    extra.resource_id ||
    extra.session_id ||
    extra.execution_id ||
    extra.job_id ||
    event.job_id ||
    "";

  return {
    id: event.id,
    timestamp: event.timestamp,
    eventType: event.event_type,
    actor,
    resourceType,
    resourceId,
    action: event.action || "",
    message: event.reason || "",
    payload: { ...extra },
    severity: siemSeverityToAuditSeverity(event.severity),
    actorInfo: deriveAuditActor(actor),
    resourceInfo: {
      type: resourceType,
      id: resourceId,
      link: auditResourceLink(resourceType, resourceId),
    },
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

// ---------------------------------------------------------------------------
// Cordum Edge
// ---------------------------------------------------------------------------

export interface BackendEdgeLabels {
  [key: string]: unknown;
}

export interface BackendEdgeEnforcementLayers {
  [key: string]: unknown;
}

export interface BackendEdgeRiskSummary {
  denied_count?: unknown;
  approval_count?: unknown;
  artifact_count?: unknown;
  max_risk?: unknown;
}

export interface BackendEdgeExecutionMetrics {
  events?: unknown;
  allow?: unknown;
  deny?: unknown;
  require_approval?: unknown;
  artifacts?: unknown;
  llm_cost_usd?: unknown;
}

export interface BackendEdgeArtifactPointer {
  artifact_type?: unknown;
  session_id?: unknown;
  execution_id?: unknown;
  event_id?: unknown;
  tenant_id?: unknown;
  retention_class?: unknown;
  redaction_level?: unknown;
  sha256?: unknown;
  uri?: unknown;
  created_at?: unknown;
  size_bytes?: unknown;
  content_type?: unknown;
}

export interface BackendEdgeSession {
  session_id?: unknown;
  tenant_id?: unknown;
  principal_id?: unknown;
  principal_type?: unknown;
  agent_product?: unknown;
  agent_version?: unknown;
  mode?: unknown;
  repo?: unknown;
  git_remote?: unknown;
  git_branch?: unknown;
  git_sha?: unknown;
  cwd?: unknown;
  host_id?: unknown;
  device_id?: unknown;
  trace_id?: unknown;
  workflow_run_id?: unknown;
  job_id?: unknown;
  policy_snapshot?: unknown;
  enforcement_layers?: BackendEdgeEnforcementLayers;
  policy_mode?: unknown;
  status?: unknown;
  risk_summary?: BackendEdgeRiskSummary;
  started_at?: unknown;
  ended_at?: unknown;
  labels?: BackendEdgeLabels;
}

export interface BackendEdgeAgentExecution {
  execution_id?: unknown;
  session_id?: unknown;
  tenant_id?: unknown;
  adapter?: unknown;
  mode?: unknown;
  workflow_run_id?: unknown;
  step_id?: unknown;
  job_id?: unknown;
  attempt?: unknown;
  trace_id?: unknown;
  worker_id?: unknown;
  policy_snapshot?: unknown;
  status?: unknown;
  started_at?: unknown;
  ended_at?: unknown;
  metrics?: BackendEdgeExecutionMetrics;
  labels?: BackendEdgeLabels;
}

export interface BackendEdgeAgentActionEvent {
  event_id?: unknown;
  session_id?: unknown;
  execution_id?: unknown;
  tenant_id?: unknown;
  principal_id?: unknown;
  seq?: unknown;
  ts?: unknown;
  layer?: unknown;
  kind?: unknown;
  agent_product?: unknown;
  tool_name?: unknown;
  tool_use_id?: unknown;
  action_name?: unknown;
  capability?: unknown;
  risk_tags?: unknown;
  input_redacted?: unknown;
  input_hash?: unknown;
  decision?: unknown;
  decision_reason?: unknown;
  rule_id?: unknown;
  policy_snapshot?: unknown;
  approval_ref?: unknown;
  artifact_ptrs?: unknown;
  duration_ms?: unknown;
  status?: unknown;
  error_code?: unknown;
  error_message?: unknown;
  labels?: BackendEdgeLabels;
}

export interface BackendEdgeApproval {
  approval_ref?: unknown;
  tenant_id?: unknown;
  session_id?: unknown;
  execution_id?: unknown;
  event_id?: unknown;
  principal_id?: unknown;
  requester?: unknown;
  resolver_id?: unknown;
  resolved_by?: unknown;
  status?: unknown;
  decision?: unknown;
  reason?: unknown;
  resolution_reason?: unknown;
  rule_id?: unknown;
  policy_snapshot?: unknown;
  action_hash?: unknown;
  input_hash?: unknown;
  created_at?: unknown;
  expires_at?: unknown;
  resolved_at?: unknown;
  consumed_at?: unknown;
  labels?: BackendEdgeLabels;
  metadata?: BackendEdgeLabels;
}

export interface BackendEdgePage<T> {
  items?: T[];
  next_cursor?: unknown;
  nextCursor?: unknown;
}

export interface BackendEdgeSessionCreateResponse {
  session_id?: unknown;
  execution_id?: unknown;
  trace_id?: unknown;
  policy_snapshot?: unknown;
  dashboard_url?: unknown;
  session?: BackendEdgeSession;
  execution?: BackendEdgeAgentExecution;
}

export interface BackendEdgeHeartbeatResponse {
  session_id?: unknown;
  heartbeat_alive?: unknown;
}

export interface BackendEdgeMissingArtifact {
  uri?: unknown;
  sha256?: unknown;
  artifact_type?: unknown;
  session_id?: unknown;
  execution_id?: unknown;
  event_id?: unknown;
  reason?: unknown;
}

export interface BackendEdgeJobLink {
  execution_id?: unknown;
  job_id?: unknown;
  workflow_run_id?: unknown;
  step_id?: unknown;
}

export interface BackendEdgeExportTruncation {
  events_truncated?: unknown;
  event_count?: unknown;
  event_scan_limit_hit?: unknown;
  executions_truncated?: unknown;
}

export interface BackendEdgeSessionExportBundle {
  manifest_version?: unknown;
  generated_at?: unknown;
  tenant_id?: unknown;
  redaction_level?: unknown;
  session?: BackendEdgeSession;
  executions?: unknown;
  events?: unknown;
  approvals?: unknown;
  artifacts?: unknown;
  missing_artifacts?: unknown;
  job_links?: unknown;
  truncation?: BackendEdgeExportTruncation;
}

export interface BackendEdgeError {
  code?: unknown;
  message?: unknown;
  request_id?: unknown;
  details?: unknown;
}

export interface BackendEdgeEventStreamEnvelope {
  type?: unknown;
  tenant_id?: unknown;
  tenantId?: unknown;
  session_id?: unknown;
  sessionId?: unknown;
  execution_id?: unknown;
  executionId?: unknown;
  event?: unknown;
}

const EDGE_DECISIONS = new Set<EdgeDecision>([
  "ALLOW",
  "DENY",
  "REQUIRE_APPROVAL",
  "THROTTLE",
  "CONSTRAIN",
  "RECORDED",
]);

const EDGE_LAYERS = new Set<EdgeLayer>([
  "hook",
  "mcp",
  "llm",
  "runtime",
  "workflow",
  "system",
]);

const EDGE_APPROVAL_DECISIONS = new Set<EdgeApprovalDecision>([
  "",
  "approve",
  "reject",
  "expire",
  "invalidate",
]);

const EDGE_APPROVAL_STATUSES = new Set<EdgeApprovalStatus>([
  "pending",
  "approved",
  "rejected",
  "expired",
  "invalidated",
]);

const EDGE_ARTIFACT_TYPES = new Set<EdgeArtifactType>([
  "edge.transcript",
  "edge.diff",
  "edge.tool_input",
  "edge.tool_result",
  "edge.test_output",
  "edge.mcp_request",
  "edge.mcp_response",
  "edge.llm_prompt_redacted",
  "edge.llm_response_redacted",
  "edge.evidence_bundle",
]);

const EDGE_RETENTION_CLASSES = new Set<EdgeRetentionClass>([
  "short",
  "standard",
  "audit",
]);

const EDGE_REDACTION_LEVELS = new Set<EdgeRedactionLevel>([
  "standard",
  "strict",
]);

const EDGE_JSON_MAX_DEPTH = 6;
const EDGE_JSON_MAX_ARRAY_ITEMS = 50;
const EDGE_JSON_MAX_STRING_LENGTH = 4096;

function isEdgeRecord(raw: unknown): raw is Record<string, unknown> {
  return typeof raw === "object" && raw !== null && !Array.isArray(raw);
}

function edgeString(raw: unknown, fallback = ""): string {
  return typeof raw === "string" ? raw : fallback;
}

function edgeOptionalString(raw: unknown): string | undefined {
  const value = edgeString(raw).trim();
  return value && !looksSensitiveString(value) ? value : undefined;
}

function edgeSafeText(raw: unknown, fallback = ""): string {
  const value = edgeString(raw).trim();
  if (!value) return fallback;
  return looksSensitiveString(value) ? fallback : value;
}

function edgeNumber(raw: unknown, fallback = 0): number {
  if (typeof raw !== "number" || !Number.isFinite(raw)) return fallback;
  return raw;
}

function edgeOptionalNumber(raw: unknown): number | undefined {
  if (typeof raw !== "number" || !Number.isFinite(raw)) return undefined;
  return raw;
}

function edgeBoolean(raw: unknown, fallback = false): boolean {
  return typeof raw === "boolean" ? raw : fallback;
}

function edgeTimestamp(raw: unknown): string {
  return normalizeIsoTimestamp(raw) ?? edgeString(raw);
}

function edgeOptionalTimestamp(raw: unknown): string | null | undefined {
  if (raw === null) return null;
  const normalized = normalizeIsoTimestamp(raw);
  if (normalized) return normalized;
  return edgeOptionalString(raw);
}

function normalizeEdgeUpper<T extends string>(
  raw: unknown,
  allowed: Set<T>,
  fallback: T,
): T | string {
  const value = edgeString(raw).trim();
  if (!value) return fallback;
  const upper = value.toUpperCase();
  return allowed.has(upper as T) ? (upper as T) : value;
}

function normalizeEdgeLower<T extends string>(
  raw: unknown,
  allowed: Set<T>,
  fallback: T,
): T | string {
  const value = edgeString(raw).trim();
  if (!value) return fallback;
  const lower = value.toLowerCase();
  return allowed.has(lower as T) ? (lower as T) : value;
}

function looksSensitiveString(value: string): boolean {
  const normalized = value.toLowerCase();
  return (
    /authorization\s*:/i.test(value) ||
    /bearer\s+[a-z0-9._~+/-]+/i.test(value) ||
    /\bsk-[a-z0-9_-]{8,}/i.test(value) ||
    /\bghp_[a-z0-9_]{8,}/i.test(value) ||
    /\bakia[0-9a-z]{16}\b/i.test(value) ||
    normalized.includes("x-amz-signature=") ||
    normalized.includes("signed_url") ||
    normalized.includes("token=") ||
    normalized.includes("api_key=") ||
    normalized.includes("apikey=") ||
    normalized.includes("secret=")
  );
}

function isUnsafeEdgeKey(key: string): boolean {
  const normalized = key
    .replace(/([a-z0-9])([A-Z])/g, "$1_$2")
    .replace(/[-\s]+/g, "_")
    .toLowerCase();
  if (
    normalized.includes("redacted") ||
    normalized.endsWith("_hash") ||
    normalized.includes("hash")
  ) {
    return false;
  }
  return [
    "raw_payload",
    "raw_prompt",
    "raw_input",
    "raw_transcript",
    "authorization",
    "bearer",
    "token",
    "api_key",
    "apikey",
    "secret",
    "password",
    "signed_url",
    "prompt",
    "tool_input",
    "tool_result",
    "transcript",
    "command_output",
  ].some((needle) => normalized === needle || normalized.includes(`${needle}_`));
}

function sanitizeEdgeString(value: string): string {
  if (looksSensitiveString(value)) return "[redacted]";
  return value.length > EDGE_JSON_MAX_STRING_LENGTH
    ? `${value.slice(0, EDGE_JSON_MAX_STRING_LENGTH)}…`
    : value;
}

function sanitizeEdgeJsonValue(raw: unknown, depth = 0): JsonValue | undefined {
  if (raw === null) return null;
  switch (typeof raw) {
    case "string":
      return sanitizeEdgeString(raw);
    case "number":
      return Number.isFinite(raw) ? raw : undefined;
    case "boolean":
      return raw;
    case "object":
      if (Array.isArray(raw)) {
        if (depth >= EDGE_JSON_MAX_DEPTH) return [];
        const values: JsonValue[] = [];
        for (const item of raw.slice(0, EDGE_JSON_MAX_ARRAY_ITEMS)) {
          const sanitized = sanitizeEdgeJsonValue(item, depth + 1);
          if (sanitized !== undefined) values.push(sanitized);
        }
        return values;
      }
      if (!isEdgeRecord(raw)) return undefined;
      if (depth >= EDGE_JSON_MAX_DEPTH) return {};
      return sanitizeEdgeJsonObject(raw, depth + 1);
    default:
      return undefined;
  }
}

function sanitizeEdgeJsonObject(
  raw: Record<string, unknown>,
  depth = 0,
): JsonObject {
  const out: JsonObject = {};
  for (const [key, value] of Object.entries(raw)) {
    if (isUnsafeEdgeKey(key)) continue;
    const sanitized = sanitizeEdgeJsonValue(value, depth);
    if (sanitized !== undefined) {
      out[key] = sanitized;
    }
  }
  return out;
}

function sanitizeEdgeJsonObjectOrNull(raw: unknown): JsonObject | null | undefined {
  if (raw === null) return null;
  if (!isEdgeRecord(raw)) return undefined;
  return sanitizeEdgeJsonObject(raw);
}

function mapEdgeLabels(raw: unknown): EdgeLabels | undefined {
  if (!isEdgeRecord(raw)) return undefined;
  const labels: EdgeLabels = {};
  for (const [key, value] of Object.entries(raw)) {
    if (isUnsafeEdgeKey(key) || typeof value !== "string" || looksSensitiveString(value)) {
      continue;
    }
    labels[key] = value;
  }
  return Object.keys(labels).length ? labels : undefined;
}

function mapEdgeEnforcementLayers(raw: unknown): Record<string, boolean> | undefined {
  if (!isEdgeRecord(raw)) return undefined;
  const layers: Record<string, boolean> = {};
  for (const [key, value] of Object.entries(raw)) {
    if (!isUnsafeEdgeKey(key) && typeof value === "boolean") {
      layers[key] = value;
    }
  }
  return Object.keys(layers).length ? layers : undefined;
}

function mapEdgeStringMetadata(raw: unknown): Record<string, string> | undefined {
  if (!isEdgeRecord(raw)) return undefined;
  const metadata: Record<string, string> = {};
  for (const [key, value] of Object.entries(raw)) {
    if (isUnsafeEdgeKey(key) || typeof value !== "string" || looksSensitiveString(value)) {
      continue;
    }
    metadata[key] = value;
  }
  return Object.keys(metadata).length ? metadata : undefined;
}

function edgeStringArray(raw: unknown): string[] | undefined {
  if (!Array.isArray(raw)) return undefined;
  const values = raw.filter(
    (value): value is string => typeof value === "string" && !looksSensitiveString(value),
  );
  return values.length ? values : undefined;
}

function edgeArtifactUri(raw: unknown): string {
  const value = edgeString(raw).trim();
  const lower = value.toLowerCase();
  if (
    !value ||
    /^https?:\/\//i.test(value) ||
    value.includes("?") ||
    value.includes("#") ||
    lower.includes("token") ||
    lower.includes("signature") ||
    lower.includes("x-amz-")
  ) {
    return "";
  }
  return value;
}

export function mapEdgeArtifactPointer(raw: unknown): EdgeArtifactPointer | null {
  if (!isEdgeRecord(raw)) return null;
  const artifactType = normalizeEdgeLower(
    raw.artifact_type,
    EDGE_ARTIFACT_TYPES,
    "edge.evidence_bundle",
  );
  const retentionClass = normalizeEdgeLower(
    raw.retention_class,
    EDGE_RETENTION_CLASSES,
    "standard",
  );
  const redactionLevel = normalizeEdgeLower(
    raw.redaction_level,
    EDGE_REDACTION_LEVELS,
    "standard",
  );
  return {
    artifactType,
    sessionId: edgeString(raw.session_id),
    executionId: edgeString(raw.execution_id),
    eventId: edgeString(raw.event_id),
    tenantId: edgeString(raw.tenant_id),
    retentionClass,
    redactionLevel,
    sha256: edgeSafeText(raw.sha256),
    uri: edgeArtifactUri(raw.uri),
    createdAt: edgeTimestamp(raw.created_at),
    sizeBytes: edgeOptionalNumber(raw.size_bytes),
    contentType: edgeOptionalString(raw.content_type),
  };
}

function mapEdgeArtifactPointers(raw: unknown): EdgeArtifactPointer[] | undefined {
  if (!Array.isArray(raw)) return undefined;
  const pointers = raw
    .map(mapEdgeArtifactPointer)
    .filter((value): value is EdgeArtifactPointer => value !== null);
  return pointers.length ? pointers : undefined;
}

function mapEdgeRiskSummary(raw: unknown): EdgeSession["riskSummary"] {
  const record = isEdgeRecord(raw) ? raw : {};
  return {
    deniedCount: edgeNumber(record.denied_count),
    approvalCount: edgeNumber(record.approval_count),
    artifactCount: edgeNumber(record.artifact_count),
    maxRisk: edgeOptionalString(record.max_risk),
  };
}

function mapEdgeExecutionMetrics(raw: unknown): AgentExecution["metrics"] {
  if (!isEdgeRecord(raw)) return undefined;
  return {
    events: edgeOptionalNumber(raw.events),
    allow: edgeOptionalNumber(raw.allow),
    deny: edgeOptionalNumber(raw.deny),
    requireApproval: edgeOptionalNumber(raw.require_approval),
    artifacts: edgeOptionalNumber(raw.artifacts),
    llmCostUsd: edgeOptionalNumber(raw.llm_cost_usd),
  };
}

export function mapEdgeSession(raw: BackendEdgeSession): EdgeSession {
  return {
    sessionId: edgeString(raw.session_id),
    tenantId: edgeString(raw.tenant_id),
    principalId: edgeOptionalString(raw.principal_id),
    principalType: normalizeEdgeLower(raw.principal_type, new Set(["human", "service", "unknown"]), "unknown"),
    agentProduct: edgeOptionalString(raw.agent_product),
    agentVersion: edgeOptionalString(raw.agent_version),
    mode: edgeSafeText(raw.mode, "local-dev"),
    repo: edgeOptionalString(raw.repo),
    gitRemote: edgeOptionalString(raw.git_remote),
    gitBranch: edgeOptionalString(raw.git_branch),
    gitSha: edgeOptionalString(raw.git_sha),
    cwd: edgeOptionalString(raw.cwd),
    hostId: edgeOptionalString(raw.host_id),
    deviceId: edgeOptionalString(raw.device_id),
    traceId: edgeString(raw.trace_id),
    workflowRunId: edgeOptionalString(raw.workflow_run_id),
    jobId: edgeOptionalString(raw.job_id),
    policySnapshot: edgeOptionalString(raw.policy_snapshot),
    enforcementLayers: mapEdgeEnforcementLayers(raw.enforcement_layers),
    policyMode: edgeSafeText(raw.policy_mode, "observe"),
    status: edgeSafeText(raw.status, "running"),
    riskSummary: mapEdgeRiskSummary(raw.risk_summary),
    startedAt: edgeTimestamp(raw.started_at),
    endedAt: edgeOptionalTimestamp(raw.ended_at),
    labels: mapEdgeLabels(raw.labels),
  };
}

export function mapAgentExecution(raw: BackendEdgeAgentExecution): AgentExecution {
  return {
    executionId: edgeString(raw.execution_id),
    sessionId: edgeString(raw.session_id),
    tenantId: edgeString(raw.tenant_id),
    adapter: edgeSafeText(raw.adapter, "claude-code-hook"),
    mode: edgeSafeText(raw.mode, "local-dev"),
    workflowRunId: edgeOptionalString(raw.workflow_run_id),
    stepId: edgeOptionalString(raw.step_id),
    jobId: edgeOptionalString(raw.job_id),
    attempt: edgeOptionalNumber(raw.attempt),
    traceId: edgeOptionalString(raw.trace_id),
    workerId: edgeOptionalString(raw.worker_id),
    policySnapshot: edgeOptionalString(raw.policy_snapshot),
    status: edgeSafeText(raw.status, "running"),
    startedAt: edgeTimestamp(raw.started_at),
    endedAt: edgeOptionalTimestamp(raw.ended_at),
    metrics: mapEdgeExecutionMetrics(raw.metrics),
    labels: mapEdgeLabels(raw.labels),
  };
}

export function mapAgentActionEvent(raw: BackendEdgeAgentActionEvent): AgentActionEvent {
  return {
    eventId: edgeString(raw.event_id),
    sessionId: edgeString(raw.session_id),
    executionId: edgeString(raw.execution_id),
    tenantId: edgeString(raw.tenant_id),
    principalId: edgeOptionalString(raw.principal_id),
    seq: edgeNumber(raw.seq),
    ts: edgeTimestamp(raw.ts),
    layer: normalizeEdgeLower(raw.layer, EDGE_LAYERS, "system"),
    kind: edgeSafeText(raw.kind, "unknown"),
    agentProduct: edgeOptionalString(raw.agent_product),
    toolName: edgeOptionalString(raw.tool_name),
    toolUseId: edgeOptionalString(raw.tool_use_id),
    actionName: edgeOptionalString(raw.action_name),
    capability: edgeOptionalString(raw.capability),
    riskTags: edgeStringArray(raw.risk_tags),
    inputRedacted: sanitizeEdgeJsonObjectOrNull(raw.input_redacted),
    inputHash: edgeOptionalString(raw.input_hash),
    decision: normalizeEdgeUpper(raw.decision, EDGE_DECISIONS, "RECORDED"),
    decisionReason: edgeOptionalString(raw.decision_reason),
    ruleId: edgeOptionalString(raw.rule_id),
    policySnapshot: edgeOptionalString(raw.policy_snapshot),
    approvalRef: edgeOptionalString(raw.approval_ref),
    artifactPtrs: mapEdgeArtifactPointers(raw.artifact_ptrs),
    durationMs: edgeOptionalNumber(raw.duration_ms),
    status: edgeSafeText(raw.status, "ok"),
    errorCode: edgeOptionalString(raw.error_code),
    errorMessage: edgeOptionalString(raw.error_message),
    labels: mapEdgeLabels(raw.labels),
  };
}

export function mapEdgeApproval(raw: BackendEdgeApproval): EdgeApproval {
  return {
    approvalRef: edgeString(raw.approval_ref),
    tenantId: edgeString(raw.tenant_id),
    sessionId: edgeString(raw.session_id),
    executionId: edgeString(raw.execution_id),
    eventId: edgeString(raw.event_id),
    principalId: edgeString(raw.principal_id),
    requester: edgeString(raw.requester),
    resolverId: edgeOptionalString(raw.resolver_id),
    resolvedBy: edgeOptionalString(raw.resolved_by),
    status: normalizeEdgeLower(raw.status, EDGE_APPROVAL_STATUSES, "pending"),
    decision: normalizeEdgeLower(raw.decision, EDGE_APPROVAL_DECISIONS, ""),
    reason: edgeSafeText(raw.reason),
    resolutionReason: edgeOptionalString(raw.resolution_reason),
    ruleId: edgeString(raw.rule_id),
    policySnapshot: edgeString(raw.policy_snapshot),
    actionHash: edgeString(raw.action_hash),
    inputHash: edgeString(raw.input_hash),
    createdAt: edgeTimestamp(raw.created_at),
    expiresAt: edgeOptionalTimestamp(raw.expires_at),
    resolvedAt: edgeOptionalTimestamp(raw.resolved_at),
    consumedAt: edgeOptionalTimestamp(raw.consumed_at),
    labels: mapEdgeLabels(raw.labels),
    metadata: mapEdgeStringMetadata(raw.metadata),
  };
}

function mapEdgeArray<TBackend, TOut>(
  raw: unknown,
  mapper: (item: TBackend) => TOut,
): TOut[] {
  return Array.isArray(raw) ? raw.map((item) => mapper(item as TBackend)) : [];
}

export function mapEdgePage<TBackend, TOut>(
  raw: BackendEdgePage<TBackend>,
  mapper: (item: TBackend) => TOut,
): EdgePage<TOut> {
  return {
    items: mapEdgeArray(raw.items, mapper),
    nextCursor: edgeOptionalString(raw.next_cursor ?? raw.nextCursor) ?? null,
  };
}

export function mapEdgeSessionPage(raw: BackendEdgePage<BackendEdgeSession>): EdgeSessionPage {
  return mapEdgePage(raw, mapEdgeSession);
}

export function mapAgentExecutionPage(raw: BackendEdgePage<BackendEdgeAgentExecution>): AgentExecutionPage {
  return mapEdgePage(raw, mapAgentExecution);
}

export function mapEdgeApprovalPage(raw: BackendEdgePage<BackendEdgeApproval>): EdgeApprovalPage {
  return mapEdgePage(raw, mapEdgeApproval);
}

export function mapAgentActionEventPage(
  raw: BackendEdgePage<BackendEdgeAgentActionEvent>,
): AgentActionEventPage {
  return mapEdgePage(raw, mapAgentActionEvent);
}

export function mapEdgeSessionCreateResponse(
  raw: BackendEdgeSessionCreateResponse,
): EdgeSessionCreateResponse {
  return {
    sessionId: edgeString(raw.session_id),
    executionId: edgeString(raw.execution_id),
    traceId: edgeString(raw.trace_id),
    policySnapshot: edgeString(raw.policy_snapshot),
    dashboardUrl: edgeString(raw.dashboard_url),
    session: mapEdgeSession(raw.session ?? {}),
    execution: mapAgentExecution(raw.execution ?? {}),
  };
}

export function mapEdgeHeartbeatResponse(
  raw: BackendEdgeHeartbeatResponse,
): { sessionId: string; heartbeatAlive: boolean } {
  return {
    sessionId: edgeString(raw.session_id),
    heartbeatAlive: edgeBoolean(raw.heartbeat_alive),
  };
}

function mapEdgeMissingArtifact(raw: unknown): EdgeMissingArtifact | null {
  if (!isEdgeRecord(raw)) return null;
  return {
    uri: edgeArtifactUri(raw.uri),
    sha256: edgeOptionalString(raw.sha256),
    artifactType: normalizeEdgeLower(
      raw.artifact_type,
      EDGE_ARTIFACT_TYPES,
      "edge.evidence_bundle",
    ),
    sessionId: edgeOptionalString(raw.session_id),
    executionId: edgeOptionalString(raw.execution_id),
    eventId: edgeOptionalString(raw.event_id),
    reason: edgeSafeText(raw.reason, "unknown"),
  };
}

function mapEdgeJobLink(raw: unknown): EdgeJobLink | null {
  if (!isEdgeRecord(raw)) return null;
  return {
    executionId: edgeString(raw.execution_id),
    jobId: edgeOptionalString(raw.job_id),
    workflowRunId: edgeOptionalString(raw.workflow_run_id),
    stepId: edgeOptionalString(raw.step_id),
  };
}

export function mapEdgeSessionExportBundle(
  raw: BackendEdgeSessionExportBundle,
): EdgeSessionExportBundle {
  return {
    manifestVersion: edgeSafeText(raw.manifest_version, "edge.export.v1"),
    generatedAt: edgeTimestamp(raw.generated_at),
    tenantId: edgeString(raw.tenant_id),
    redactionLevel: normalizeEdgeLower(
      raw.redaction_level,
      EDGE_REDACTION_LEVELS,
      "standard",
    ),
    session: mapEdgeSession(raw.session ?? {}),
    executions: mapEdgeArray(raw.executions, mapAgentExecution),
    events: mapEdgeArray(raw.events, mapAgentActionEvent),
    approvals: mapEdgeArray(raw.approvals, mapEdgeApproval),
    artifacts: mapEdgeArray(raw.artifacts, mapEdgeArtifactPointer).filter(
      (item): item is EdgeArtifactPointer => item !== null,
    ),
    missingArtifacts: mapEdgeArray(raw.missing_artifacts, mapEdgeMissingArtifact).filter(
      (item): item is EdgeMissingArtifact => item !== null,
    ),
    jobLinks: mapEdgeArray(raw.job_links, mapEdgeJobLink).filter(
      (item): item is EdgeJobLink => item !== null,
    ),
    truncation: raw.truncation
      ? {
          eventsTruncated: edgeBoolean(raw.truncation.events_truncated),
          eventCount: edgeOptionalNumber(raw.truncation.event_count),
          eventScanLimitHit: edgeBoolean(raw.truncation.event_scan_limit_hit),
          executionsTruncated: edgeBoolean(raw.truncation.executions_truncated),
        }
      : undefined,
  };
}

export function mapEdgeErrorEnvelope(raw: unknown): EdgeError {
  const record = isEdgeRecord(raw) ? raw : {};
  const details = sanitizeEdgeJsonObjectOrNull(record.details);
  return {
    code: edgeSafeText(record.code, "edge_error"),
    message: edgeSafeText(record.message, "Edge request failed"),
    requestId: edgeOptionalString(record.request_id),
    details: details ?? undefined,
  };
}

export function mapEdgeEventStreamEnvelope(
  raw: BackendEdgeEventStreamEnvelope,
): EdgeEventStreamEnvelope | null {
  if (edgeString(raw.type) !== "edge.event") return null;
  const event = isEdgeRecord(raw.event)
    ? mapAgentActionEvent(raw.event as BackendEdgeAgentActionEvent)
    : undefined;
  return {
    type: "edge.event",
    tenantId: edgeOptionalString(raw.tenant_id ?? raw.tenantId),
    sessionId: edgeOptionalString(raw.session_id ?? raw.sessionId) ?? event?.sessionId,
    executionId:
      edgeOptionalString(raw.execution_id ?? raw.executionId) ?? event?.executionId,
    event,
  };
}

export function mapEdgeStreamPayload(
  envelope: EdgeEventStreamEnvelope,
): EdgeStreamPayload {
  const event = envelope.event;
  return {
    tenantId: envelope.tenantId ?? event?.tenantId,
    sessionId: envelope.sessionId ?? event?.sessionId,
    executionId: envelope.executionId ?? event?.executionId,
    eventId: event?.eventId,
    kind: event?.kind,
    layer: event?.layer,
    decision: event?.decision,
    approvalRef: event?.approvalRef,
    artifactPtrs: event?.artifactPtrs,
    summary: event
      ? `${event.kind || "edge.event"} ${event.decision || "RECORDED"}`
      : "edge.event",
  };
}
