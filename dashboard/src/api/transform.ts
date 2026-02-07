import type {
  Job,
  JobStatus,
  SafetyDecision,
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
}

export interface BackendJobDetail extends BackendJobRecord {
  context_ptr?: string;
  result_ptr?: string;
  error_message?: string;
  error_status?: string;
  error_code?: string;
  last_state?: string;
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
}

export interface BackendApprovalItem {
  job?: BackendJobRecord;
  decision?: string;
  policy_rule_id?: string;
  policy_reason?: string;
  approval_required?: boolean;
  approval_ref?: string;
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

export function microsToISO(raw?: number): string {
  if (!raw || raw <= 0) return "";
  const ms = Math.floor(raw / 1000);
  const d = new Date(ms);
  return isNaN(d.getTime()) ? "" : d.toISOString();
}

export function secondsToISO(raw?: number): string {
  if (!raw || raw <= 0) return "";
  const d = new Date(raw * 1000);
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

// ---------------------------------------------------------------------------
// Mappers
// ---------------------------------------------------------------------------

export function mapJobRecord(record: BackendJobRecord): Job {
  const updatedAt = microsToISO(record.updated_at);
  const capabilities = Array.from(
    new Set(
      [
        record.capability ? String(record.capability).trim() : "",
        ...(record.requires ?? []).map((r) => String(r).trim()),
      ].filter(Boolean),
    ),
  );
  return {
    id: record.id,
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
    lastState: detail.last_state,
  };
}

const WORKFLOW_NODE_TYPES = new Set([
  "job",
  "approval",
  "delay",
  "condition",
  "notify",
  "fan-out",
]);

function normalizeWorkflowNodeType(raw?: string): { uiType: string; backendType?: string } {
  const trimmed = (raw || "").trim();
  if (!trimmed) {
    return { uiType: "job" };
  }
  const lower = trimmed.toLowerCase();
  if (WORKFLOW_NODE_TYPES.has(lower)) {
    return { uiType: lower };
  }
  return { uiType: "job", backendType: trimmed };
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
    if (typeof input.message === "string" && input.message.trim()) {
      config.messageTemplate = input.message;
    }
    if (typeof input.component === "string" && input.component.trim()) {
      config.channel = input.component;
    }
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
  let { uiType, backendType } = normalizeWorkflowNodeType(step.type);
  if (uiType === "job" && step.for_each) {
    uiType = "fan-out";
  }
  const config = buildWorkflowStepConfig(step);
  if (backendType) {
    config.backendType = backendType;
  }
  return {
    id: step.id || fallbackId,
    name: step.name || fallbackId,
    type: uiType,
    config,
    dependsOn: step.depends_on,
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
    config: {},
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
    startedAt: run.started_at || "",
    completedAt: run.completed_at || undefined,
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
  };
}

export function mapApprovalItem(item: BackendApprovalItem): {
  id: string;
  jobId: string;
  status: string;
  requestedAt: string;
  reason?: string;
  policyRule?: string;
  jobContext?: Record<string, unknown>;
} | null {
  if (!item.job) return null;
  const job = mapJobRecord(item.job);
  return {
    id: job.id,
    jobId: job.id,
    status: "pending",
    requestedAt: job.updatedAt,
    reason: item.policy_reason,
    policyRule: item.policy_rule_id,
    jobContext: {
      topic: job.topic,
      tenant: job.tenant,
      capabilities: job.capabilities,
      riskTags: job.riskTags,
    },
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
  };
}

export function mapPolicyBundleSummary(summary: BackendPolicyBundleSummary): PolicyBundle {
  const versionNum = Number.parseInt(summary.version ?? "", 10);
  return {
    id: summary.id,
    name: summary.id,
    rules: [],
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
  return {
    id: detail.id,
    name: detail.id,
    rules: [],
    enabled: detail.enabled ?? true,
    content: detail.content ?? "",
    author: detail.author,
    message: detail.message,
    createdAt: detail.created_at,
    updatedAt: detail.updated_at,
  };
}

export function mapPolicyAuditEntry(entry: BackendPolicyAuditEntry): {
  id: string;
  timestamp: string;
  eventType: string;
  actor: string;
  resourceType: string;
  resourceId: string;
  action: string;
  message: string;
  payload?: Record<string, unknown>;
} {
  return {
    id: entry.id,
    timestamp: entry.created_at || new Date().toISOString(),
    eventType: entry.action || "policy",
    actor: entry.actor_id || entry.role || "unknown",
    resourceType: entry.resource_type || "policy",
    resourceId: entry.resource_id || "",
    action: entry.action || "",
    message: entry.message || "",
    payload: {
      bundle_ids: entry.bundle_ids,
      snapshot_before: entry.snapshot_before,
      snapshot_after: entry.snapshot_after,
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
  const topics = record.manifest?.topics ?? [];
  const capabilities = Array.from(
    new Set(
      topics
        .map((t) => (t?.capability || "").trim())
        .filter((c) => c.length > 0),
    ),
  );
  const title = metadata?.title?.trim();
  return {
    id: record.id,
    name: title || metadata?.id || record.id,
    version: record.version || metadata?.version || "",
    status: record.status || "unknown",
    capabilities,
    config: {},
    manifest: record.manifest as Record<string, unknown> | undefined,
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
  const activeJobs = hb.active_jobs ?? 0;
  const capacity = hb.max_parallel_jobs ?? 0;
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
    capacity: capacity > 0 ? capacity : Math.max(1, activeJobs),
    region: hb.region,
    type: hb.type,
    cpuLoad: hb.cpu_load,
    gpuUtilization: hb.gpu_utilization,
    memoryLoad: hb.memory_load,
  };
}
