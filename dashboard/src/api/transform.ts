import YAML from "yaml";
import { logger } from "../lib/logger";
import type {
  Job,
  JobStatus,
  OutputDecision,
  OutputFinding,
  OutputSafetyRecord,
  SafetyDecision,
  Approval,
  UrgencyLevel,
  AuditEntry,
  AuditCategory,
  AuditSeverity,
  AuditActor,
  AuditResource,
  Workflow,
  WorkflowRun,
  WorkflowStep,
  PolicyBundle,
  Worker,
  DLQEntry,
  Pack,
  MarketplacePack,
  MarketplaceCatalog,
  PolicyRule,
} from "./types";

// ---------------------------------------------------------------------------
// Backend response shapes (minimal)
// ---------------------------------------------------------------------------

export interface BackendJobRecord {
  id: string;
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
  constraints?: Record<string, unknown>;
  workflow_id?: string;
  workflow_run_id?: string;
  step_index?: number;
  step_name?: string;
  total_steps?: number;
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
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function logInvalidDateInput(fn: string, raw: unknown): void {
  if (import.meta.env.DEV && raw != null) {
    logger.warn("transform", `${fn} received invalid value`, { raw });
  }
}

export function microsToISO(raw: unknown): string {
  if (typeof raw !== "number" || !Number.isFinite(raw) || raw <= 0) {
    logInvalidDateInput("microsToISO", raw);
    return "";
  }
  const ms = Math.floor(raw / 1000);
  const d = new Date(ms);
  return isNaN(d.getTime()) ? "" : d.toISOString();
}

export function normalizeJobStatus(raw?: string): JobStatus {
  switch ((raw || "").toUpperCase()) {
    case "PENDING":
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
      return "pending";
  }
}

export function normalizeDecisionType(raw?: string): SafetyDecision["type"] {
  switch ((raw || "").toUpperCase()) {
    case "ALLOW":
    case "ALLOW_WITH_CONSTRAINTS":
    case "DECISION_TYPE_ALLOW":
    case "DECISION_TYPE_ALLOW_WITH_CONSTRAINTS":
      return "allow";
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

function normalizeOutputDecision(raw?: string): OutputDecision {
  switch ((raw || "").toUpperCase()) {
    case "ALLOW":
      return "ALLOW";
    case "QUARANTINE":
      return "QUARANTINE";
    case "REDACT":
      return "REDACT";
    default:
      return "ALLOW";
  }
}

function mapOutputFinding(raw: BackendOutputFinding): OutputFinding {
  return {
    type: raw.type || "",
    severity: raw.severity || "",
    detail: raw.detail || "",
    scanner: raw.scanner,
    confidence: raw.confidence,
    matched_pattern: raw.matched_pattern,
    offset: raw.offset,
    length: raw.length,
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
    id = crypto.randomUUID();
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
    type: "",
    topic: record.topic || "",
    status: normalizeJobStatus(record.state),
    safetyDecision: mapSafetyDecision(
      record.safety_decision,
      record.safety_reason,
      record.safety_rule_id,
    ),
    pool: "",
    capabilities,
    riskTags: record.risk_tags ?? [],
    metadata: {},
    contextPtr: undefined,
    resultPtr: undefined,
    workflowRunId: undefined,
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
    contextPtr: detail.context_ptr,
    resultPtr: detail.result_ptr,
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
  };
}

const WORKFLOW_NODE_TYPES = new Set([
  "job",
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
): { uiType: string; backendType?: string } {
  const trimmed = (raw || "").trim().toLowerCase();
  if (!trimmed) {
    return { uiType: "agent-task" };
  }
  if (trimmed === "subworkflow") {
    return { uiType: "sub-workflow" };
  }
  if (WORKFLOW_NODE_TYPES.has(trimmed) && trimmed !== "job") {
    return { uiType: trimmed };
  }
  // Backend "job" → differentiate into agent-task / pack-action / tool-call
  if (trimmed === "job" && meta) {
    if (typeof meta.pack_id === "string" && meta.pack_id) {
      return { uiType: "pack-action" };
    }
    if (typeof meta.capability === "string" && meta.capability && !meta.prompt) {
      return { uiType: "tool-call" };
    }
  }
  if (trimmed === "job") {
    return { uiType: "agent-task" };
  }
  return { uiType: "agent-task", backendType: trimmed };
}

function normalizeSwitchCases(value: unknown): Array<{ matchValue: string; stepId: string }> {
  const fromRecord = (record: Record<string, unknown>) => {
    const matchRaw = record.match ?? record.when ?? record.value ?? record.matchValue;
    const stepRaw = record.next ?? record.step ?? record.target ?? record.step_id ?? record.goto ?? record.stepId;
    const stepId = typeof stepRaw === "string" ? stepRaw.trim() : "";
    if (!stepId) return null;
    return {
      matchValue: matchRaw == null ? "" : String(matchRaw),
      stepId,
    };
  };

  if (Array.isArray(value)) {
    return value
      .map((entry) => (entry && typeof entry === "object" ? fromRecord(entry as Record<string, unknown>) : null))
      .filter((entry): entry is { matchValue: string; stepId: string } => entry !== null);
  }
  if (value && typeof value === "object") {
    return Object.entries(value as Record<string, unknown>)
      .map(([matchValue, stepRaw]) => {
        const stepId = typeof stepRaw === "string" ? stepRaw.trim() : "";
        if (!stepId) return null;
        return { matchValue, stepId };
      })
      .filter((entry): entry is { matchValue: string; stepId: string } => entry !== null);
  }
  return [];
}

function buildWorkflowStepConfig(step: BackendWorkflowStep): Record<string, unknown> {
  const config: Record<string, unknown> = {};

  if (step.topic) config.topic = step.topic;
  if (step.worker_id) config.workerId = step.worker_id;
  if (typeof step.timeout_sec === "number" && step.timeout_sec > 0) {
    config.timeout = `${step.timeout_sec}s`;
  }
  if (step.retry && typeof step.retry.max_retries === "number") {
    config.retryMax = step.retry.max_retries;
  }
  if (step.condition) config.expression = step.condition;
  if (step.for_each) config.forEach = step.for_each;
  if (typeof step.max_parallel === "number") {
    config.parallelism = step.max_parallel;
  }
  if (typeof step.delay_sec === "number" && step.delay_sec > 0) {
    config.duration = `${step.delay_sec}s`;
  } else if (step.delay_until) {
    config.duration = step.delay_until;
  }
  if (step.input && typeof step.input === "object") {
    config.input = step.input;
    const input = step.input as Record<string, unknown>;
    if (step.type === "parallel") {
      if (Array.isArray(input.steps)) {
        config.parallelSteps = input.steps.map((entry) => String(entry).trim()).filter(Boolean);
      }
      if (typeof input.strategy === "string" && input.strategy.trim()) {
        config.completionStrategy = input.strategy.trim();
      }
      if (typeof input.required === "number" && Number.isFinite(input.required)) {
        config.requiredCount = Math.floor(input.required);
      }
    }
    if (step.type === "loop") {
      if (typeof input.body_step === "string" && input.body_step.trim()) {
        config.bodyStep = input.body_step.trim();
      } else if (typeof input.body === "string" && input.body.trim()) {
        config.bodyStep = input.body.trim();
      }
      if (typeof input.max_iterations === "number" && Number.isFinite(input.max_iterations)) {
        config.maxIterations = Math.floor(input.max_iterations);
      } else if (typeof input.maxIterations === "number" && Number.isFinite(input.maxIterations)) {
        config.maxIterations = Math.floor(input.maxIterations);
      }
      if (typeof input.condition === "string" && input.condition.trim()) {
        config.condition = input.condition.trim();
      } else if (typeof input.while === "string" && input.while.trim()) {
        config.condition = input.while.trim();
      }
      if (typeof input.until === "string" && input.until.trim()) {
        config.until = input.until.trim();
      }
    }
    if (step.type === "subworkflow" || step.type === "sub-workflow") {
      if (typeof input.workflow_id === "string" && input.workflow_id.trim()) {
        config.workflowId = input.workflow_id.trim();
      }
      if (input.input_mapping !== undefined) {
        config.inputMapping = input.input_mapping;
      }
      if (input.output_mapping !== undefined) {
        config.outputMapping = input.output_mapping;
      }
    }
    if (step.type === "switch") {
      const switchCases = normalizeSwitchCases(input.cases);
      if (switchCases.length > 0) {
        config.switchCases = switchCases;
        config.cases = switchCases;
      }
      if (typeof input.default === "string" && input.default.trim()) {
        config.defaultBranch = input.default.trim();
      } else if (typeof input.default_step === "string" && input.default_step.trim()) {
        config.defaultBranch = input.default_step.trim();
      }
    }
    if (typeof input.message === "string" && input.message.trim()) {
      config.messageTemplate = input.message;
    }
    if (typeof input.component === "string" && input.component.trim()) {
      config.channel = input.component;
    }
    if (typeof input.prompt === "string" && input.prompt.trim()) {
      config.prompt = input.prompt;
    }
    // Budget from input.budget or input itself
    const budget = (input.budget ?? input) as Record<string, unknown>;
    if (typeof budget.input_tokens === "number") config.maxInputTokens = budget.input_tokens;
    if (typeof budget.output_tokens === "number") config.maxOutputTokens = budget.output_tokens;
    if (typeof budget.total_tokens === "number") config.maxTotalTokens = budget.total_tokens;
  }
  if (step.meta && typeof step.meta === "object") {
    config.meta = step.meta;
    const caps: string[] = [];
    if (typeof step.meta.capability === "string" && step.meta.capability.trim()) {
      caps.push(step.meta.capability);
    }
    if (Array.isArray(step.meta.requires)) {
      for (const req of step.meta.requires) {
        const trimmed = String(req).trim();
        if (trimmed) caps.push(trimmed);
      }
    }
    if (caps.length > 0) {
      config.capabilities = caps;
    }
    if (Array.isArray(step.meta.risk_tags) && step.meta.risk_tags.length > 0) {
      config.riskTags = step.meta.risk_tags;
    }
    if (step.meta.labels) config.labels = step.meta.labels;
    if (step.meta.pack_id) config.packId = step.meta.pack_id;
    if (step.meta.actor_id) config.actorId = step.meta.actor_id;
    if (step.meta.actor_type) config.actorType = step.meta.actor_type;
    if (typeof step.meta.adapter_id === "string" && step.meta.adapter_id) {
      config.adapterId = step.meta.adapter_id;
    }
    if (typeof step.meta.memory_id === "string" && step.meta.memory_id) {
      config.memoryId = step.meta.memory_id;
    }
    if (typeof step.meta.context_mode === "string" && step.meta.context_mode) {
      config.contextMode = step.meta.context_mode;
    }
    if (typeof step.meta.allow_summarization === "boolean") {
      config.allowSummarization = step.meta.allow_summarization;
    }
    if (typeof step.meta.allow_retrieval === "boolean") {
      config.allowRetrieval = step.meta.allow_retrieval;
    }
    if (typeof step.meta.deadline_ms === "number") {
      config.deadlineMs = step.meta.deadline_ms;
    }
    if (typeof step.meta.priority === "string" && step.meta.priority) {
      config.priority = step.meta.priority;
    }
    // Budget fallback from meta.budget
    const metaBudget = step.meta.budget as Record<string, unknown> | undefined;
    if (metaBudget && typeof metaBudget === "object") {
      if (typeof metaBudget.input_tokens === "number" && !config.maxInputTokens) config.maxInputTokens = metaBudget.input_tokens;
      if (typeof metaBudget.output_tokens === "number" && !config.maxOutputTokens) config.maxOutputTokens = metaBudget.output_tokens;
      if (typeof metaBudget.total_tokens === "number" && !config.maxTotalTokens) config.maxTotalTokens = metaBudget.total_tokens;
    }
  }
  if (step.route_labels) config.routeLabels = step.route_labels;
  if (step.input_schema) config.inputSchema = step.input_schema;
  if (step.input_schema_id) config.inputSchemaId = step.input_schema_id;
  if (step.output_schema) config.outputSchema = step.output_schema;
  if (step.output_schema_id) config.outputSchemaId = step.output_schema_id;
  if (step.output_path) config.outputPath = step.output_path;

  return config;
}

export function mapWorkflowStep(step: BackendWorkflowStep, fallbackId: string): WorkflowStep {
  let { uiType, backendType } = normalizeWorkflowNodeType(
    step.type,
    step.meta as Record<string, unknown> | undefined,
  );
  if (uiType === "agent-task" && step.for_each) {
    uiType = "fan-out";
  }
  // Legacy config bag for backward compat during migration
  const config = buildWorkflowStepConfig(step);
  if (backendType) {
    config.backendType = backendType;
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
    // Legacy compat
    config,
    dependsOn: step.depends_on,
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

export function mapWorkflowRunStep(step: BackendStepRun, fallbackId: string): WorkflowStep {
  return {
    id: step.step_id || fallbackId,
    name: step.step_id || fallbackId,
    type: "step",
    status: step.status as WorkflowStep["status"],
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
    status: (run.status as WorkflowRun["status"]) || "pending",
    steps,
    startedAt: run.started_at ?? null,
    completedAt: run.completed_at ?? null,
    duration: undefined,
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
  if (waitMs < 2 * 60_000) return "fresh";
  if (waitMs < 15 * 60_000) return "aging";
  if (waitMs < 60 * 60_000) return "critical";
  return "breach";
}

export function deriveApprovalStatus(
  jobState: string | undefined,
  decision: string | undefined,
): string {
  if (decision === "approve" || decision === "approved") return "approved";
  if (decision === "reject" || decision === "rejected" || decision === "deny")
    return "rejected";
  if (jobState === "denied") return "rejected";
  if (jobState === "output_quarantined") return "quarantined";
  if (jobState === "approval_required") return "pending";
  if (
    jobState === "succeeded" ||
    jobState === "failed" ||
    jobState === "cancelled"
  )
    return "resolved";
  return "pending";
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

  const workflowContext =
    item.workflow_id || item.workflow_run_id
      ? {
          workflowId: item.workflow_id || "",
          runId: item.workflow_run_id || "",
          stepIndex: item.step_index,
          stepName: item.step_name,
          totalSteps: item.total_steps,
        }
      : undefined;

  return {
    id: item.approval_ref || job.id,
    jobId: job.id,
    status: deriveApprovalStatus(item.job.state, item.decision),
    requestedAt,
    resolvedAt: item.resolved_at ? microsToISO(item.resolved_at) : undefined,
    actor: item.resolved_by,
    actorId: job.actorId,
    reason: item.policy_reason,
    comment: item.resolved_comment,
    policyRule: item.policy_rule_id,
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
    humanSummary: deriveHumanSummary(
      job.topic,
      job.capabilities,
      item.policy_reason,
    ),
    urgencyLevel: computeUrgencyLevel(Math.max(0, waitMs)),
    waitMs: Math.max(0, waitMs),
    policySnapshot: item.policy_snapshot,
    jobHash: item.job_hash,
    approvalRef: item.approval_ref,
    tenant: job.tenant,
    contextPtr: item.context_ptr,
    constraints: item.constraints,
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

function normalizeMatchCriteria(raw: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(raw)) {
    switch (key) {
      case "risk_tags":
        out.riskTags = value;
        break;
      case "pack_ids":
        out.packIds = value;
        break;
      case "actor_ids":
        out.actorIds = value;
        break;
      case "actor_types":
        out.actorTypes = value;
        break;
      case "secrets_present":
        out.secretsPresent = value;
        break;
      default:
        out[key] = value;
    }
  }
  return out;
}

export function mapPolicyRule(raw: Record<string, unknown>): PolicyRule {
  const id = typeof raw.id === "string" ? raw.id : "";
  const decision = typeof raw.decision === "string" ? raw.decision : "";
  const reason = typeof raw.reason === "string" ? raw.reason : "";
  const match = (raw.match as Record<string, unknown>) ?? {};
  const priority = typeof raw.priority === "number" ? raw.priority : undefined;
  const logic = typeof raw.logic === "string" ? raw.logic : undefined;
  return {
    id,
    matchCriteria: normalizeMatchCriteria(match),
    decisionType: normalizeDecisionType(decision),
    reason,
    priority,
    logic,
    source: typeof raw.source === "object" && raw.source ? (raw.source as Record<string, unknown>) : undefined,
    enabled: typeof raw.enabled === "boolean" ? raw.enabled : undefined,
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
      logger.debug("transform", "YAML parse error in policy bundle summary, falling back to empty rules");
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
    healthStatus: undefined,
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
      logger.debug("transform", "YAML parse error in policy bundle detail, falling back to empty rules");
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
    case "workflow": return `/workflows/${resourceId}`;
    case "run": return `/workflows`;
    case "policy": return `/policies`;
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
  if (snapshot.bundles) {
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
  const status = activeJobs > 0 ? "active" : "online";
  return {
    id: hb.worker_id,
    name,
    pool: hb.pool ?? "default",
    capabilities: hb.capabilities ?? [],
    status,
    activeJobs,
    // capacity fallback: if backend reports 0 max_parallel_jobs, use at least 1
    capacity: capacity > 0 ? capacity : Math.max(1, activeJobs),
    region: hb.region,
    type: hb.type,
    cpuLoad: hb.cpu_load,
    gpuUtilization: hb.gpu_utilization,
    memoryLoad: hb.memory_load,
  };
}
