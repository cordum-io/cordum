// ---------------------------------------------------------------------------
// Response envelope
// ---------------------------------------------------------------------------

export interface ApiResponse<T> {
  items?: T extends Array<infer _> ? T : T[];
  next_cursor?: number | null;
}

export interface PaginationParams {
  page?: number;
  perPage?: number;
  sort?: string;
}

// JSON values safe to keep in dashboard state. Edge transforms use these for
// redacted/bounded metadata only; raw prompts, tool payloads, transcripts,
// command output, tokens, Authorization headers, and signed URLs must not be
// cached in these shapes.
export type JsonPrimitive = string | number | boolean | null;
export type JsonValue = JsonPrimitive | JsonObject | JsonValue[];
export interface JsonObject {
  [key: string]: JsonValue;
}

// ---------------------------------------------------------------------------
// Cordum Edge
// ---------------------------------------------------------------------------

export type EdgePrincipalType = "human" | "service" | "unknown";
export type EdgeMode = "local-dev" | "enterprise-managed" | "workflow" | "ci" | "prod-runner";
export type EdgePolicyMode = "observe" | "enforce" | "enterprise-strict";
export type EdgeSessionStatus = "starting" | "running" | "waiting_for_approval" | "degraded" | "ended" | "failed";
export type EdgeExecutionStatus = "running" | "waiting_for_approval" | "succeeded" | "failed" | "cancelled" | "timeout" | "degraded";
export type EdgeLayer = "hook" | "mcp" | "llm" | "runtime" | "workflow" | "system";
export type EdgeDecision = "ALLOW" | "DENY" | "REQUIRE_APPROVAL" | "THROTTLE" | "CONSTRAIN" | "RECORDED";
export type EdgeActionStatus = "ok" | "blocked" | "failed" | "degraded";
export type EdgeRiskLevel = "low" | "medium" | "high" | "critical";
export type EdgeAdapter = "claude-code-hook" | "mcp-gateway" | "llm-proxy" | "runtime-sidecar" | "sdk-runner";
export type EdgeApprovalStatus = "pending" | "approved" | "rejected" | "expired" | "invalidated";
export type EdgeApprovalDecision = "" | "approve" | "reject" | "expire" | "invalidate";
export type EdgeArtifactType =
  | "edge.transcript"
  | "edge.diff"
  | "edge.tool_input"
  | "edge.tool_result"
  | "edge.test_output"
  | "edge.mcp_request"
  | "edge.mcp_response"
  | "edge.llm_prompt_redacted"
  | "edge.llm_response_redacted"
  | "edge.evidence_bundle";
export type EdgeRetentionClass = "short" | "standard" | "audit";
export type EdgeRedactionLevel = "standard" | "strict";

export interface EdgePage<T> {
  items: T[];
  nextCursor: string | null;
}

export interface EdgeLabels {
  [key: string]: string;
}

export interface EdgeEnforcementLayers {
  [key: string]: boolean;
}

export interface EdgeRiskSummary {
  deniedCount: number;
  approvalCount: number;
  artifactCount: number;
  maxRisk?: EdgeRiskLevel | string;
}

export interface EdgeExecutionMetrics {
  events?: number;
  allow?: number;
  deny?: number;
  requireApproval?: number;
  artifacts?: number;
  llmCostUsd?: number;
}

export interface EdgeArtifactPointer {
  artifactType: EdgeArtifactType | string;
  sessionId: string;
  executionId: string;
  eventId: string;
  tenantId: string;
  retentionClass: EdgeRetentionClass | string;
  redactionLevel: EdgeRedactionLevel | string;
  sha256: string;
  uri: string;
  createdAt: string;
  sizeBytes?: number;
  contentType?: string;
}

export interface EdgeSession {
  sessionId: string;
  tenantId: string;
  principalId?: string;
  principalType: EdgePrincipalType | string;
  agentProduct?: string;
  agentVersion?: string;
  mode: EdgeMode | string;
  repo?: string;
  gitRemote?: string;
  gitBranch?: string;
  gitSha?: string;
  cwd?: string;
  hostId?: string;
  deviceId?: string;
  traceId: string;
  workflowRunId?: string;
  jobId?: string;
  policySnapshot?: string;
  enforcementLayers?: EdgeEnforcementLayers;
  policyMode: EdgePolicyMode | string;
  status: EdgeSessionStatus | string;
  riskSummary: EdgeRiskSummary;
  startedAt: string;
  endedAt?: string | null;
  labels?: EdgeLabels;
}

export interface AgentExecution {
  executionId: string;
  sessionId: string;
  tenantId: string;
  adapter: EdgeAdapter | string;
  mode: EdgeMode | string;
  workflowRunId?: string;
  stepId?: string;
  jobId?: string;
  attempt?: number;
  traceId?: string;
  workerId?: string;
  policySnapshot?: string;
  status: EdgeExecutionStatus | string;
  startedAt: string;
  endedAt?: string | null;
  metrics?: EdgeExecutionMetrics;
  labels?: EdgeLabels;
}

export interface AgentActionEvent {
  eventId: string;
  sessionId: string;
  executionId: string;
  tenantId: string;
  principalId?: string;
  seq: number;
  ts: string;
  layer: EdgeLayer | string;
  kind: string;
  agentProduct?: string;
  toolName?: string;
  toolUseId?: string;
  actionName?: string;
  capability?: string;
  riskTags?: string[];
  inputRedacted?: JsonObject | null;
  inputHash?: string;
  decision: EdgeDecision | string;
  decisionReason?: string;
  ruleId?: string;
  policySnapshot?: string;
  approvalRef?: string;
  artifactPtrs?: EdgeArtifactPointer[];
  durationMs?: number;
  status: EdgeActionStatus | string;
  errorCode?: string;
  errorMessage?: string;
  labels?: EdgeLabels;
}

export interface EdgeApproval {
  approvalRef: string;
  tenantId: string;
  sessionId: string;
  executionId: string;
  eventId: string;
  principalId: string;
  requester: string;
  resolverId?: string;
  resolvedBy?: string;
  status: EdgeApprovalStatus | string;
  decision?: EdgeApprovalDecision | string;
  reason: string;
  resolutionReason?: string;
  ruleId: string;
  policySnapshot: string;
  actionHash: string;
  inputHash: string;
  createdAt: string;
  expiresAt?: string | null;
  resolvedAt?: string | null;
  consumedAt?: string | null;
  labels?: EdgeLabels;
  metadata?: Record<string, string>;
}

export interface EdgeSessionCreateResponse {
  sessionId: string;
  executionId: string;
  traceId: string;
  policySnapshot: string;
  dashboardUrl: string;
  session: EdgeSession;
  execution: AgentExecution;
}

export interface EdgeHeartbeatResponse {
  sessionId: string;
  heartbeatAlive: boolean;
}

export interface EdgeSessionListParams {
  cursor?: string;
  limit?: number;
  status?: EdgeSessionStatus | string;
  principalId?: string;
  agentProduct?: string;
}

export interface EdgeExecutionListParams {
  cursor?: string;
  limit?: number;
  sessionId?: string;
  jobId?: string;
  traceId?: string;
  workflowRunId?: string;
}

export interface EdgeEventListParams {
  cursor?: string;
  limit?: number;
  kind?: string;
  decision?: EdgeDecision | string;
  since?: string;
  until?: string;
}

export interface EdgeApprovalListParams {
  cursor?: string;
  limit?: number;
  status?: EdgeApprovalStatus | string;
  sessionId?: string;
  executionId?: string;
  actionHash?: string;
}

export interface EdgeExportRequest {
  maxEvents?: number;
}

export interface EdgeMissingArtifact {
  uri: string;
  sha256?: string;
  artifactType?: EdgeArtifactType | string;
  sessionId?: string;
  executionId?: string;
  eventId?: string;
  reason: "not_found" | "tenant_mismatch" | "store_error" | string;
}

export interface EdgeJobLink {
  executionId: string;
  jobId?: string;
  workflowRunId?: string;
  stepId?: string;
}

export interface EdgeExportTruncation {
  eventsTruncated?: boolean;
  eventCount?: number;
  eventScanLimitHit?: boolean;
  executionsTruncated?: boolean;
}

export interface EdgeSessionExportBundle {
  manifestVersion: "edge.export.v1" | string;
  generatedAt: string;
  tenantId: string;
  redactionLevel?: EdgeRedactionLevel | string;
  session: EdgeSession;
  executions?: AgentExecution[];
  events?: AgentActionEvent[];
  approvals?: EdgeApproval[];
  artifacts?: EdgeArtifactPointer[];
  missingArtifacts?: EdgeMissingArtifact[];
  jobLinks?: EdgeJobLink[];
  truncation?: EdgeExportTruncation;
}

export interface EdgeError {
  code: string;
  message: string;
  requestId?: string;
  details?: JsonObject;
}

export type EdgeSessionPage = EdgePage<EdgeSession>;
export type EdgeApprovalPage = EdgePage<EdgeApproval>;
export type AgentExecutionPage = EdgePage<AgentExecution>;
export type AgentActionEventPage = EdgePage<AgentActionEvent>;

// UI-safe live Edge events expose identifiers, decisions, hashes, artifact
// pointers, and redacted summaries only. Raw prompts, tool payloads,
// transcripts, signed URLs, command output, and tokens must stay out of
// dashboard state and React Query caches.
export interface EdgeStreamPayload {
  tenantId?: string;
  sessionId?: string;
  executionId?: string;
  eventId?: string;
  kind?: string;
  layer?: EdgeLayer | string;
  decision?: EdgeDecision | string;
  approvalRef?: string;
  artifactPtrs?: EdgeArtifactPointer[];
  summary?: string;
}

export interface EdgeEventStreamEnvelope {
  type: "edge.event";
  tenantId?: string;
  sessionId?: string;
  executionId?: string;
  event?: AgentActionEvent;
}

// ---------------------------------------------------------------------------
// Jobs
// ---------------------------------------------------------------------------

export type JobStatus =
  | "pending"
  | "scheduled"
  | "dispatched"
  | "running"
  | "succeeded"
  | "failed"
  | "cancelled"
  | "approval_required"
  | "denied"
  | "timeout"
  | "output_quarantined"
  | "quarantined";

export type ApprovalStatus =
  | "pending"
  | "approved"
  | "rejected"
  | "expired"
  | "invalidated"
  | "repaired";

export type ApprovalActionability =
  | "actionable"
  | "resolved"
  | "expired"
  | "invalidated"
  | "repaired";

export type ApprovalConflictCode =
  | "approval_already_resolved"
  | "approval_retryable_lock"
  | "approval_terminal_run"
  | "approval_stale_snapshot"
  | "approval_stale_request"
  | "approval_not_actionable";

export interface ApprovalConflictPayload {
  code?: ApprovalConflictCode;
  error?: string;
  message?: string;
  retryable?: boolean;
  status?: number;
}

export type OutputDecision = "ALLOW" | "QUARANTINE" | "REDACT";

export interface OutputFinding {
  type: string;
  severity: string;
  detail: string;
  scanner?: string;
  confidence?: number;
  matched_pattern?: string;
  offset?: number;
  length?: number;
}

export interface OutputSafetyRecord {
  decision: OutputDecision;
  reason?: string;
  rule_id?: string;
  findings?: OutputFinding[];
  phase?: string;
  policy_snapshot?: string;
  redacted_ptr?: string;
  redacted?: unknown;
  original_ptr?: string;
}

export interface OutputPolicyRule {
  id: string;
  match: Record<string, unknown>;
  decision: OutputDecision;
  reason?: string;
  enabled?: boolean;
}

export type SafetyDecisionType = "allow" | "deny" | "require_approval" | "allow_with_constraints" | "throttle";

export interface MatchedRule {
  rule_id: string;
  name: string;
  bundle_id?: string;
  priority: number;
  match_reason?: string;
  decision: SafetyDecisionType;
}

export interface PolicyConstraints {
  budgets?: {
    max_runtime_ms?: number;
    max_retries?: number;
    max_artifact_bytes?: number;
    max_concurrent_jobs?: number;
  };
  sandbox?: {
    isolated?: boolean;
    network_allowlist?: string[];
    fs_read_only?: string[];
    fs_read_write?: string[];
  };
  toolchain?: {
    allowed_tools?: string[];
    allowed_commands?: string[];
  };
  diff?: {
    max_files?: number;
    max_lines?: number;
    deny_path_globs?: string[];
  };
  redaction_level?: string;
  maxInvocations?: number;
  allowedDomains?: string[];
  maskedFields?: string[];
  rateLimit?:
    | number
    | string
    | {
        limit?: number;
        requests?: number;
        windowSeconds?: number;
        window_seconds?: number;
        burst?: number;
      };
  requireReviewer?:
    | boolean
    | string
    | {
        role?: string;
        approverRole?: string;
        reason?: string;
      };
}

export type GovernanceVerdict =
  | "allow"
  | "deny"
  | "constrain"
  | "require_approval"
  | "throttle";

export interface GovernanceDecision {
  jobId: string;
  runId?: string;
  stepId?: string;
  topic: string;
  matchedRule: string;
  ruleName?: string;
  verdict: GovernanceVerdict;
  reason: string;
  constraints?: PolicyConstraints;
  approvalStatus?: ApprovalStatus;
  approvalDecision?: "approve" | "reject" | "expire" | "invalidate" | "repair";
  agentId: string;
  policyVersion?: string;
  timestamp: string;
}

export interface GovernanceDecisionsResponse {
  items: GovernanceDecision[];
  nextCursor?: string;
}

export interface McpPolicyResult {
  server?: string;
  tool?: string;
  resource?: string;
  action?: string;
  decision: SafetyDecisionType;
  matched_rules?: string[];
}

export interface SafetyDecision {
  type: SafetyDecisionType;
  reason: string;
  matchedRule?: string;
  evalTimeMs?: number;
  evalPath?: string[];
}

export interface SafetyResult {
  decision: SafetyDecisionType;
  matched_rules: MatchedRule[];
  evaluation_ms: number;
  constraints?: PolicyConstraints;
  mcp_context?: McpPolicyResult;
  approval_required: boolean;
  approval_ref?: string;
}

export interface Job {
  id: string;
  workerId?: string;
  type: string;
  topic: string;
  status: JobStatus;
  safetyDecision?: SafetyDecision;
  pool: string;
  capabilities: string[];
  riskTags: string[];
  metadata: Record<string, unknown>;
  contextPtr?: string;
  resultPtr?: string;
  context?: unknown;
  result?: unknown;
  workflowRunId?: string;
  workflowId?: string;
  createdAt: string;
  updatedAt: string;
  duration?: number;
  traceId?: string;
  tenant?: string;
  team?: string;
  actorId?: string;
  actorType?: string;
  capability?: string;
  requires?: string[];
  attempts?: number;
  errorMessage?: string;
  errorStatus?: string;
  errorCode?: string;
  errorCodeEnum?: number;
  lastState?: string;
  output_safety?: OutputSafetyRecord;
  idempotencyKey?: string;
  labels?: Record<string, string>;
  approvalRequired?: boolean;
  approvalRef?: string;
  approvalBy?: string;
  approvalRole?: string;
  approvalAt?: number;
  approvalReason?: string;
  approvalNote?: string;
  approvalStatus?: ApprovalStatus;
  approvalActionability?: ApprovalActionability;
  approvalRevision?: number;
  approvalDecision?: "approve" | "reject" | "expire" | "invalidate" | "repair";
}

// ---------------------------------------------------------------------------
// ErrorCode enum (matches CAP v2.5.2 protobuf ErrorCode)
// ---------------------------------------------------------------------------

export enum ErrorCode {
  UNSPECIFIED = 0,
  // Protocol (100-104)
  PROTOCOL_VERSION_MISMATCH = 100,
  PROTOCOL_INVALID_PACKET = 101,
  PROTOCOL_SIGNATURE_INVALID = 102,
  PROTOCOL_TIMEOUT = 103,
  PROTOCOL_RATE_LIMITED = 104,
  // Job (200-206)
  JOB_NOT_FOUND = 200,
  JOB_ALREADY_COMPLETED = 201,
  JOB_TIMEOUT = 202,
  JOB_CANCELLED = 203,
  JOB_PERMISSION_DENIED = 204,
  JOB_RESOURCE_EXHAUSTED = 205,
  JOB_INVALID_INPUT = 206,
  // Safety (300-302)
  SAFETY_DENIED = 300,
  SAFETY_POLICY_VIOLATION = 301,
  SAFETY_OUTPUT_QUARANTINED = 302,
  // Transport (400-402)
  TRANSPORT_UNAVAILABLE = 400,
  TRANSPORT_POOL_EXHAUSTED = 401,
  TRANSPORT_DELIVERY_FAILED = 402,
}

/** Human-readable label for an ErrorCode value. */
export function errorCodeLabel(code: number): string {
  switch (code) {
    case ErrorCode.PROTOCOL_VERSION_MISMATCH: return "Protocol: Version Mismatch";
    case ErrorCode.PROTOCOL_INVALID_PACKET: return "Protocol: Invalid Packet";
    case ErrorCode.PROTOCOL_SIGNATURE_INVALID: return "Protocol: Signature Invalid";
    case ErrorCode.PROTOCOL_TIMEOUT: return "Protocol: Timeout";
    case ErrorCode.PROTOCOL_RATE_LIMITED: return "Protocol: Rate Limited";
    case ErrorCode.JOB_NOT_FOUND: return "Job: Not Found";
    case ErrorCode.JOB_ALREADY_COMPLETED: return "Job: Already Completed";
    case ErrorCode.JOB_TIMEOUT: return "Job: Timeout";
    case ErrorCode.JOB_CANCELLED: return "Job: Cancelled";
    case ErrorCode.JOB_PERMISSION_DENIED: return "Job: Permission Denied";
    case ErrorCode.JOB_RESOURCE_EXHAUSTED: return "Job: Resource Exhausted";
    case ErrorCode.JOB_INVALID_INPUT: return "Job: Invalid Input";
    case ErrorCode.SAFETY_DENIED: return "Safety: Denied";
    case ErrorCode.SAFETY_POLICY_VIOLATION: return "Safety: Policy Violation";
    case ErrorCode.SAFETY_OUTPUT_QUARANTINED: return "Safety: Output Quarantined";
    case ErrorCode.TRANSPORT_UNAVAILABLE: return "Transport: Unavailable";
    case ErrorCode.TRANSPORT_POOL_EXHAUSTED: return "Transport: Pool Exhausted";
    case ErrorCode.TRANSPORT_DELIVERY_FAILED: return "Transport: Delivery Failed";
    default: return `Error ${code}`;
  }
}

/** Category for an ErrorCode — used to pick badge color. */
export function errorCodeCategory(code: number): "safety" | "job" | "protocol" | "transport" | "unknown" {
  if (code >= 300 && code < 400) return "safety";
  if (code >= 200 && code < 300) return "job";
  if (code >= 100 && code < 200) return "protocol";
  if (code >= 400 && code < 500) return "transport";
  return "unknown";
}

// ---------------------------------------------------------------------------
// AlertSeverity enum (matches CAP v2.5.2 protobuf AlertSeverity)
// ---------------------------------------------------------------------------

export enum AlertSeverity {
  UNSPECIFIED = 0,
  INFO = 1,
  WARNING = 2,
  ERROR = 3,
  CRITICAL = 4,
}

export type JobPriority = "low" | "normal" | "high" | "critical";

export interface RemediateJobInput {
  topic?: string;
  prompt?: string;
  priority?: JobPriority;
  capability?: string;
  requires?: string[];
  risk_tags?: string[];
  labels?: Record<string, string>;
  reason: string;
}

export interface RemediateJobResponse {
  job_id: string;
  trace_id: string;
}

export interface SubmitJobInput {
  topic: string;
  prompt: string;
  priority?: JobPriority;
  capability?: string;
  requires?: string[];
  risk_tags?: string[];
  labels?: Record<string, string>;
  adapter_id?: string;
  memory_id?: string;
  pack_id?: string;
  idempotency_key?: string;
  max_total_tokens?: number;
  tags?: string[];
  context?: Record<string, unknown>;
}

export interface SubmitJobResponse {
  job_id: string;
  trace_id: string;
}

// ---------------------------------------------------------------------------
// Memory + Artifacts
// ---------------------------------------------------------------------------

export type MemoryEntryRole =
  | "system"
  | "user"
  | "assistant"
  | "agent"
  | "tool"
  | "unknown";

export interface MemoryEntry {
  id: string;
  role: MemoryEntryRole;
  content: string;
  timestamp?: string;
  metadata?: Record<string, unknown>;
}

export interface MemoryPayload {
  pointer: string;
  key: string;
  kind: "context" | "result" | "memory" | string;
  size_bytes: number;
  base64: string;
  text?: string;
  json?: unknown;
  entries?: MemoryEntry[];
}

export interface ArtifactMetadata {
  content_type?: string;
  size_bytes?: number;
  retention?: string;
  labels?: Record<string, string>;
}

export interface ArtifactPayload {
  artifact_ptr: string;
  content_base64: string;
  metadata: ArtifactMetadata;
}

export interface JobArtifactRef {
  ptr: string;
  contentType?: string;
  sizeBytes?: number;
  timestamp?: string;
  source?: string;
}

// ---------------------------------------------------------------------------
// Workflows
// ---------------------------------------------------------------------------

export type RunStatus =
  | "pending"
  | "running"
  | "waiting"
  | "succeeded"
  | "failed"
  | "denied"
  | "timed_out"
  | "cancelled";

export interface WorkflowStep {
  id: string;
  name: string;
  type: string;
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
  meta?: Record<string, unknown>;
  on_error?: string;
  retry?: { max_retries?: number; backoff_sec?: number; backoff_multiplier?: number };
  timeout_sec?: number;
  delay_sec?: number;
  delay_until?: string;
  route_labels?: Record<string, string>;
  /** Free-form configuration bag for step types whose knobs do not
   *  fit under the typed fields above (scheduler-specific delays,
   *  pack-specific tuning). Consumers read from the typed fields
   *  first and fall back to this bag only when the knob is
   *  genuinely step-type-specific. */
  config?: Record<string, unknown>;
  // Run-time fields (present when viewing runs)
  status?: RunStatus;
  output?: Record<string, unknown>;
  error?: string;
  startedAt?: string;
  completedAt?: string;
  /** Design-time policy gate hint for the WorkflowStudio governance overlay.
   *  Sourced from cordum-core `WorkflowStep.policyGate` (task-913b6c6c). */
  policyGate?: "allow" | "deny" | "require_approval";
  /** Audit-chain hash for the runtime safety decision applied to this step.
   *  Sourced from cordum-core `WorkflowRunStep.auditHash` (task-913b6c6c). */
  auditHash?: string;
}

export interface Workflow {
  id: string;
  name: string;
  steps: WorkflowStep[];
  timeout_sec?: number;
  /** @deprecated Use timeout_sec */
  timeout?: number;
  metadata?: Record<string, unknown>;
  orgId?: string;
  teamId?: string;
  description?: string;
  version?: string;
  input_schema?: Record<string, unknown>;
  config?: Record<string, unknown>;
  createdAt?: string;
  updatedAt?: string;
  triggerType?: string;
  lastRunAt?: string;
  successRate?: number;
}

export interface WorkflowRun {
  id: string;
  workflowId: string;
  status: RunStatus;
  steps: WorkflowStep[];
  startedAt: string | null;
  completedAt?: string | null;
  duration?: number;
  createdAt?: string;
  updatedAt?: string;
  orgId?: string;
  teamId?: string;
  input?: Record<string, unknown>;
  output?: Record<string, unknown>;
  error?: Record<string, unknown>;
  rerunOf?: string;
  rerunStep?: string;
  dryRun?: boolean;
  timers?: Array<{
    workflow_id: string;
    run_id: string;
    fires_at: string;
    remaining_ms: number;
  }>;
}

// ---------------------------------------------------------------------------
// Policies
// ---------------------------------------------------------------------------

export interface McpMatchConfig {
  allow_servers?: string[];
  deny_servers?: string[];
  allow_tools?: string[];
  deny_tools?: string[];
  allow_resources?: string[];
  deny_resources?: string[];
  allow_actions?: string[];
  deny_actions?: string[];
}

export interface PolicyRuleMatch {
  tenants?: string[];
  topics?: string[];              // Glob patterns
  capabilities?: string[];
  risk_tags?: string[];
  requires?: string[];
  pack_ids?: string[];
  actor_ids?: string[];
  actor_types?: string[];
  labels?: Record<string, string>;
  label_allowlist?: Record<string, string[]>;
  label_threshold?: Record<string, number>;
  secrets_present?: boolean;
  mcp?: McpMatchConfig;
}

export interface VelocityConfig {
  max_requests: number;
  window_seconds: number;
  key: string;
}

export interface PolicyRule {
  id: string;
  rule_id?: string;
  name: string;
  description?: string;
  bundle_id?: string;
  match: PolicyRuleMatch;
  velocity?: VelocityConfig;
  decision: SafetyDecisionType;
  constraints?: PolicyConstraints;
  priority: number;
  enabled: boolean;
  version?: number;
  created_by?: string;
  created_at?: string;
  updated_at?: string;
  // Legacy-bundle import fields — populated when loading YAML
  // bundles that predate the canonical PolicyRule shape.
  // `dashboard/src/lib/policy-bundle.ts` reads these during import;
  // new bundles never write them.
  matchCriteria?: Record<string, unknown>;
  decisionType?: SafetyDecisionType;
  reason?: string;
  hitCount24h?: number;
  lastTriggered?: string;
  logic?: string;
  source?: Record<string, unknown>;
}

export type BundleStatus = "published" | "draft" | "archived";

export interface BundleSnapshot {
  snapshot_id: string;
  bundle_id: string;
  note: string;
  created_at: string;
  created_by: string;
  version?: number;
  rule_count?: number;
}

export interface PolicyBundleSignature {
  algorithm: string;
  key_id: string;
  value: string;
  hash: string;
  signed_bytes: number;
}

export interface PolicyBundle {
  id: string;
  bundle_id?: string;
  name: string;
  rules: PolicyRule[];
  version?: number;
  status?: BundleStatus;
  enabled?: boolean;
  content?: string;
  source?: string;
  author?: string;
  message?: string;
  createdAt?: string;
  updatedAt?: string;
  installedAt?: string;
  sha256?: string;
  publishedAt?: string;
  published_by?: string;
  healthStatus?: string;
  snapshots?: BundleSnapshot[];
  rule_count?: number;
  eval_count_24h?: number;
  last_triggered?: string;
  // shadow summary surfaces when the gateway detail response carries a
  // /shadow sidecar. Absent = no shadow active for this bundle.
  shadow?: ShadowPolicySummary | null;
  // Signature fields sourced from the bundle's _signature map attached
  // server-side by handlers_policy_bundles_signing.go. Missing when the
  // bundle predates the signing pipeline or when the detail endpoint
  // has not yet been extended to surface the _signature entry. UI
  // surfaces default to 'unknown' badge when absent.
  signed?: boolean;
  signature?: PolicyBundleSignature;
}

export interface PolicyPublishRequest {
  note?: string;
  dry_run?: boolean;
}

export interface PolicyPublishResult {
  version: number;
  published_at: string;
  published_by: string;
  rule_count: number;
  bundle_count: number;
  diff?: {
    added: number;
    removed: number;
    modified: number;
  };
}

export interface PolicyRollbackRequest {
  target_version: number;
  note?: string;
}

// ---------------------------------------------------------------------------
// Policy Replay
// ---------------------------------------------------------------------------

export interface PolicyReplayFilter {
  tenant?: string;
  topic_pattern?: string;
  original_decision?: string;
}

export interface PolicyReplayRequest {
  from: string;
  to: string;
  filters?: PolicyReplayFilter;
  candidate_bundle_id?: string;
  candidate_content?: string;
  use_current_policy?: boolean;
  max_jobs?: number;
}

export interface PolicyReplaySummary {
  total_jobs: number;
  evaluated: number;
  escalated: number;
  relaxed: number;
  unchanged: number;
  errored: number;
}

export interface PolicyReplayRuleHit {
  rule_id: string;
  decision: string;
  count: number;
}

export interface PolicyReplayChange {
  job_id: string;
  topic: string;
  tenant: string;
  original_decision: string;
  new_decision: string;
  new_rule_id?: string;
  new_reason?: string;
  direction: "escalated" | "relaxed" | "unchanged";
}

export interface PolicyReplayTimeRange {
  from: string;
  to: string;
}

export interface PolicyReplayResponse {
  replay_id: string;
  policy_snapshot: string;
  time_range: PolicyReplayTimeRange;
  summary: PolicyReplaySummary;
  rule_hits: PolicyReplayRuleHit[];
  changes: PolicyReplayChange[];
  warnings: string[];
  errors: string[];
}

// ---------------------------------------------------------------------------
// Policy Analytics
// ---------------------------------------------------------------------------

export interface PolicyAnalyticsRequest {
  from: string;
  to: string;
  rule_filter?: string;
}

export interface RuleAnalytics {
  rule_id: string;
  hit_count: number;
  approval_count: number;
  override_count: number;
  override_rate: number;
  avg_approval_latency_ms: number;
  daily_hits: number[];
}

export interface PolicyAnalyticsSummary {
  total_rules: number;
  total_hits: number;
  total_overrides: number;
  highest_override_rule_id: string;
}

export interface PolicyAnalyticsResponse {
  time_range: { from: string; to: string };
  rules: RuleAnalytics[];
  summary: PolicyAnalyticsSummary;
}

// ---------------------------------------------------------------------------
// Agent Identities
// ---------------------------------------------------------------------------

export interface AgentIdentity {
  id: string;
  name: string;
  description?: string;
  owner: string;
  team?: string;
  risk_tier: "low" | "medium" | "high" | "critical";
  allowed_topics?: string[];
  allowed_pools?: string[];
  allowed_tools?: string[];
  preapproved_mutating_tools?: string[];
  data_classifications?: string[];
  status: "active" | "suspended" | "revoked";
  created_at: string;
  updated_at: string;
  last_active?: number;
}

export interface AgentStats {
  agent_id: string;
  total_jobs_7d: number;
  denied_7d: number;
  last_active: number;
}

// ---------------------------------------------------------------------------
// Workers
// ---------------------------------------------------------------------------

export interface Worker {
  id: string;
  name: string;
  pool: string;
  capabilities: string[];
  status: string;
  activeJobs: number;
  capacity: number;
  lastHeartbeat?: string;
  uptime?: number;
  version?: string;
  address?: string;
  region?: string;
  type?: string;
  cpuLoad?: number;
  gpuUtilization?: number;
  memoryLoad?: number;
  // Heartbeat demotion (phase-2 boundary hardening). `online` is the
  // authoritative dispatch-eligibility signal backed by the worker's
  // session token; lastHeartbeat + heartbeatAgeSeconds are telemetry
  // only — never gate UX behaviour on them.
  online?: boolean;
  sessionValid?: boolean;
  sessionExpMs?: number;
  sessionRevoked?: boolean;
  sessionState?: WorkerSessionState;
  lastHeartbeatAt?: string;
  heartbeatAgeSeconds?: number;
}

/**
 * WorkerSessionState mirrors the scheduler TrustReason* constants.
 * Surfaced on /api/v1/workers for dashboards + external consumers.
 */
export type WorkerSessionState =
  | "valid"
  | "no_session"
  | "session_expired"
  | "session_revoked"
  | "trust_store_unready";

export interface Pool {
  name: string;
  workerCount: number;
  activeJobs: number;
  capacity: number;
  utilization: number;
  topics: string[];
  workers: Worker[];
}

// ---------------------------------------------------------------------------
// Topics
// ---------------------------------------------------------------------------

export interface TopicRegistration {
  name: string;
  pool: string;
  inputSchemaId?: string;
  outputSchemaId?: string;
  packId?: string;
  requires: string[];
  riskTags: string[];
  status: string;
  activeWorkers: number;
}

export interface TopicsResponse extends ApiResponse<TopicRegistration[]> {
  registryEmpty?: boolean;
}

// ---------------------------------------------------------------------------
// Packs
// ---------------------------------------------------------------------------

export interface Pack {
  id: string;
  name: string;
  version: string;
  status: string;
  capabilities: string[];
  config?: Record<string, unknown>;
  poolAssignment?: string;
  manifest?: Record<string, unknown>;
  resources?: Record<string, unknown>;
  installedAt?: string;
  installedBy?: string;
  description?: string;
  author?: string;
  homepage?: string;
  source?: string;
  image?: string;
  license?: string;
  url?: string;
  sha256?: string;
}

// ---------------------------------------------------------------------------
// Audit
// ---------------------------------------------------------------------------

export type AuditCategory = "safety_decision" | "human_action" | "system_event" | "access_event";
export type AuditSeverity = "high" | "medium" | "low";

export interface AuditActor {
  id: string;
  name?: string;
  type: "user" | "system" | "agent" | "api_key";
  role?: string;
}

export interface AuditResource {
  type: string;
  id: string;
  name?: string;
  link: string;
}

export interface AuditEntry {
  id: string;
  timestamp: string;
  eventType: string;
  actor: string;
  resourceType: string;
  resourceId: string;
  resourceName?: string;
  action: string;
  message: string;
  payload?: Record<string, unknown>;
  // Enriched fields
  category?: AuditCategory;
  severity?: AuditSeverity;
  actorInfo?: AuditActor;
  resourceInfo?: AuditResource;
  snapshotBefore?: Record<string, unknown>;
  snapshotAfter?: Record<string, unknown>;
  bundleIds?: string[];
}

// ---------------------------------------------------------------------------
// DLQ
// ---------------------------------------------------------------------------

export interface RetryAttempt {
  attemptedAt: string;
  error: string;
}

export interface DLQEntry {
  id: string;
  jobId: string;
  error?: string;
  retryCount?: number;
  maxRetries?: number;
  originalTopic?: string;
  failedAt?: string;
  retryAttempts?: RetryAttempt[];
  status?: string;
  reasonCode?: string;
  lastState?: string;
  reason?: string;
  attempts?: number;
  createdAt?: string;
}

// ---------------------------------------------------------------------------
// Approvals
// ---------------------------------------------------------------------------

export type UrgencyLevel = "fresh" | "aging" | "critical" | "breach";
export type ApprovalDecisionSummarySource =
  | "workflow_payload"
  | "workflow_labels"
  | "policy_only";
export type ApprovalDecisionSummaryCompleteness = "rich" | "partial" | "minimal";
export type ApprovalContextStatus =
  | "available"
  | "missing"
  | "malformed"
  | "unavailable"
  | "absent";

export interface ApprovalDecisionSummary {
  source: ApprovalDecisionSummarySource;
  completeness: ApprovalDecisionSummaryCompleteness;
  contextStatus: ApprovalContextStatus;
  title: string;
  subject?: string;
  why?: string;
  nextEffect?: string;
  amount?: number;
  currency?: string;
  vendor?: string;
  itemCount?: number;
  itemsPreview?: string[];
  escalationReason?: string;
  missingFields?: string[];
}

export interface ApprovalWorkflowContext {
  workflowId: string;
  workflowName?: string;
  runId: string;
  stepId?: string;
  stepIndex?: number;
  stepName?: string;
  totalSteps?: number;
}

export interface Approval {
  id: string;
  jobId: string;
  status: ApprovalStatus;
  requestedAt: string;
  resolvedAt?: string;
  actor?: string;
  actorId?: string;
  reason?: string;
  comment?: string;
  policyRule?: string;
  jobContext?: Record<string, unknown>;
  decisionSummary?: ApprovalDecisionSummary;
  // Enriched fields
  topic?: string;
  safetyDecision?: SafetyDecision;
  riskTags?: string[];
  capabilities?: string[];
  workflowContext?: ApprovalWorkflowContext;
  humanSummary?: string;
  urgencyLevel?: UrgencyLevel;
  waitMs?: number;
  policySnapshot?: string;
  jobHash?: string;
  approvalRef?: string;
  tenant?: string;
  contextPtr?: string;
  jobInput?: Record<string, unknown>;
  constraints?: Record<string, unknown>;
  actionability?: ApprovalActionability;
  revision?: number;
  approvalDecision?: "approve" | "reject" | "expire" | "invalidate" | "repair";
  // Backend-compatible fields
  job?: {
    id: string;
    type?: string;
    topic?: string;
    status?: string;
    metadata?: Record<string, unknown>;
    risk_tags?: string[];
    requires?: string[];
    capabilities?: string[];
    capability?: string;
    actor_id?: string;
    actor_type?: string;
    pack_id?: string;
    tenant?: string;
  };
  decision?: string;
  policy_rule_id?: string;
  policy_snapshot?: string;
  policy_reason?: string;
  approval_required?: boolean;
  approval_status?: ApprovalStatus;
  approval_actionability?: ApprovalActionability;
  approval_revision?: number;
  approval_decision?: "approve" | "reject" | "expire" | "invalidate" | "repair";
  // Enriched context fields (mapped from backend).
  blastRadius?: BlastRadius;
  priorApprovals?: PriorApproval[];
  rollbackHint?: string;
  policySnapshotSummary?: ApprovalPolicySnapshot;
}

// Enriched approval context types for decision-grade UX.
export interface BlastRadius {
  systems: string[];
  namespaces: string[];
  resources: string[];
  scopeDescription: string;
}

export interface PriorApproval {
  jobId: string;
  topic: string;
  tenant: string;
  decision: string;
  resolvedBy: string;
  resolvedAt: number;
  wasApproved: boolean;
}

export interface ApprovalPolicySnapshot {
  ruleCount: number;
  matchedRule: {
    id: string;
    description: string;
    decision: string;
    constraintsSummary: string;
  };
  policyVersion: string;
}

export interface ApprovalContext {
  approval: Record<string, unknown>;
  blastRadius: BlastRadius;
  priorApprovals: PriorApproval[];
  rollbackHint: string;
  policySnapshotSummary: ApprovalPolicySnapshot;
  timeRemainingMs: number | null;
  constraints: Record<string, unknown> | null;
}

export interface ApprovalHistoryEntry {
  id: string;
  action: "approve" | "reject";
  jobId: string;
  actor: string;
  timestamp: string;
  reason?: string;
  policyRule?: string;
  bundleIds?: string[];
  topic?: string;
  workflowId?: string;
  waitDurationMs?: number;
}

// ---------------------------------------------------------------------------
// Schemas
// ---------------------------------------------------------------------------

export interface SchemaField {
  name: string;
  type: string;
  required?: boolean;
  description?: string;
}

export interface Schema {
  id: string;
  name?: string;
  version?: number;
  fields?: SchemaField[];
  schema?: Record<string, unknown>;
  createdAt?: string;
  updatedAt?: string;
}

// ---------------------------------------------------------------------------
// Marketplace
// ---------------------------------------------------------------------------

export interface MarketplaceCatalog {
  id: string;
  title?: string;
  url?: string;
  enabled?: boolean;
  updatedAt?: string;
  error?: string;
}

export interface MarketplacePack {
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
  catalogId?: string;
  catalogTitle?: string;
  capabilities?: string[];
  requires?: string[];
  riskTags?: string[];
  installedVersion?: string;
  installedStatus?: string;
  installedAt?: string;
}

export interface MarketplaceResponse {
  catalogs: MarketplaceCatalog[];
  items: MarketplacePack[];
  fetched_at?: string;
  cached?: boolean;
}

// ---------------------------------------------------------------------------
// Policy snapshots
// ---------------------------------------------------------------------------

export interface PolicySnapshotSummary {
  id: string;
  createdAt: string;
  note?: string;
  version?: number;
  createdBy?: string;
}

export interface PolicySnapshot extends PolicySnapshotSummary {
  bundles?: Record<string, unknown>;
  rules?: PolicyRule[];
}

// ---------------------------------------------------------------------------
// Users / Auth
// ---------------------------------------------------------------------------

export interface User {
  id: string;
  username: string;
  email: string;
  display_name: string;
  roles: string[];
  tenant: string;
  createdAt?: string;
  lastLogin?: string;
}

export interface ApiKey {
  id: string;
  name: string;
  prefix: string;
  scopes: string[];
  createdAt: string;
  lastUsed?: string;
  usageCount: number;
  expiresAt?: string;
}

export interface AuthConfig {
  password_enabled: boolean;
  user_auth_enabled?: boolean;
  saml_enabled: boolean;
  saml_enterprise?: boolean;
  saml_login_url?: string;
  saml_metadata_url?: string;
  session_ttl?: string;
  require_rbac?: boolean;
  require_principal?: boolean;
  default_tenant: string;
  oidc_enabled?: boolean;
  oidc_issuer?: string;
  oidc_login_url?: string;
  oidc_client_id?: string;
  oidc_redirect_uri?: string;
  oidc_scopes?: string[];
  oidc_groups_claim?: string;
  oidc_group_role_mapping?: Record<string, string>;
  oidc_client_secret_masked?: string;
}

export interface ChangePasswordPayload {
  current_password: string;
  new_password: string;
}

export interface ResetUserPasswordPayload {
  password: string;
}

// ---------------------------------------------------------------------------
// Licensing
// ---------------------------------------------------------------------------

export type LicensePlan = "community" | "team" | "enterprise";
export type LicenseApprovalMode = "single" | "multi" | "custom";
export type TelemetryMode = "off" | "local_only" | "anonymous";

export interface LicenseRights {
  hostedService: boolean;
  embedding: boolean;
  resale: boolean;
  whiteLabel: boolean;
  supportSla: boolean;
}

export interface LicenseEntitlements {
  approvalMode?: LicenseApprovalMode | string;
  telemetryMode?: TelemetryMode | string;
  maxWorkers?: number;
  requestsPerSecond?: number;
  maxConcurrentJobs?: number;
  maxWorkflowSteps?: number;
  maxActiveWorkflows?: number;
  maxTenants?: number;
  maxSchemaCount?: number;
  maxPromptChars?: number;
  maxBodyBytes?: number;
  maxArtifactBytes?: number;
  maxPolicyBundles?: number;
  auditRetentionDays?: number;
  sso?: boolean;
  saml?: boolean;
  scim?: boolean;
  rbac?: boolean;
  audit?: boolean;
  auditExport?: boolean;
  siemExport?: boolean;
  legalHold?: boolean;
  velocityRules?: boolean;
  breakGlassAdmin?: boolean;
  agentIdentity?: boolean;
  features?: Record<string, boolean>;
  limits?: Record<string, number>;
}

export interface LicenseInfo {
  mode?: string;
  status?: string;
  plan?: LicensePlan | string;
  orgId?: string;
  licenseId?: string;
  deploymentType?: string;
  issuedAt?: string;
  notBefore?: string;
  expiresAt?: string;
  features?: string[];
  limits?: Record<string, number>;
}

export interface LicenseSummary {
  plan: LicensePlan | string;
  entitlements: LicenseEntitlements;
  rights: LicenseRights | null;
  license?: LicenseInfo | null;
  expiryStatus?: string;
}

export interface TierUsageMetric<TAllowed = number | string> {
  current?: number;
  allowed?: TAllowed;
  registered?: number;
  connected?: number;
}

export interface LicenseUsage {
  workers: TierUsageMetric<number>;
  concurrentJobs: TierUsageMetric<number>;
  activeWorkflows: TierUsageMetric<number>;
  workflowSteps: TierUsageMetric<number>;
  schemas: TierUsageMetric<number>;
  policyBundles: TierUsageMetric<number>;
  requestsPerSecond: TierUsageMetric<number>;
  promptChars: TierUsageMetric<number>;
  bodyBytes: TierUsageMetric<number>;
  approvalMode: TierUsageMetric<string>;
}

export interface LicenseUsageSummary {
  tenantId: string;
  plan: LicensePlan | string;
  license?: LicenseInfo | null;
  usage: LicenseUsage;
}

// ---------------------------------------------------------------------------
// Notifications
// ---------------------------------------------------------------------------

export type NotificationChannelType = "email" | "slack" | "webhook" | "pagerduty";

export interface NotificationChannel {
  id: string;
  name: string;
  type: NotificationChannelType;
  config: Record<string, unknown>;
  enabled: boolean;
  lastSentAt?: string;
  error?: string;
}

export interface NotificationRule {
  id: string;
  eventPattern: string;
  channelIds: string[];
  throttleMs?: number;
  muteUntil?: string;
  enabled: boolean;
}

// ---------------------------------------------------------------------------
// Environments
// ---------------------------------------------------------------------------

export interface Environment {
  id: string;
  name: string;
  status: "active" | "maintenance" | "degraded";
  endpoint?: string;
  config: Record<string, unknown>;
  lastPromotedAt?: string;
  lastDeployedAt?: string;
}

// ---------------------------------------------------------------------------
// MCP
// ---------------------------------------------------------------------------

export type McpTransport = "http" | "stdio" | "both";

export interface McpConfig {
  enabled: boolean;
  transport: McpTransport;
  port: number;
  requireAuth: boolean;
  allowedOrigins: string[];
  apiKeyMasked?: string;
  tools: Record<string, { enabled: boolean }>;
  resources: Record<string, { enabled: boolean }>;
}

export interface McpTool {
  name: string;
  description: string;
  enabled: boolean;
  inputSchema: Record<string, unknown>;
}

export interface McpResource {
  uri: string;
  name: string;
  description: string;
  enabled: boolean;
  mimeType: string;
}

export interface McpPromptArgument {
  name: string;
  description: string;
  required: boolean;
}

export interface McpPrompt {
  name: string;
  description: string;
  arguments: McpPromptArgument[];
  // modelClass hints at the model tier the prompt is tuned for —
  // e.g. 'small' for straightforward rewriting, 'reasoning' for the
  // policy-migration helper that has to navigate grammar diffs.
  // Rendered on the catalogue card.
  modelClass: "small" | "reasoning";
  // safetyDisclaimer is true for prompts whose rendered output
  // operators should simulate before applying (draft_safety_rule +
  // policy_migration_helper). Gets a distinct amber chip on the card.
  safetyDisclaimer: boolean;
  // docsHref links to the long-form catalogue entry in
  // docs/mcp/prompts.md so operators can jump straight to the
  // argument-schema + example-output deep-dive.
  docsHref: string;
}

export interface McpStatus {
  running: boolean;
  connectedClients: number;
  uptime: number;
  transport?: string;
  enabledTools?: number;
  enabledResources?: number;
}

// ---------------------------------------------------------------------------
// General Config
// ---------------------------------------------------------------------------

export interface MaintenanceWindow {
  startedAt: string;
  endedAt: string;
  durationMs: number;
  message?: string;
}

export interface MaintenanceSchedule {
  id: string;
  startAt: string;
  endAt: string;
  message?: string;
  recurring?: {
    daysOfWeek: number[]; // 0=Sun..6=Sat
    startHour: number;
    endHour: number;
  };
}

export interface GeneralConfig {
  safetyStance: "permissive" | "balanced" | "strict";
  approvalTimeoutMs: number;
  autoDenyOnTimeout: boolean;
  logRetentionDays: number;
  auditRetentionDays: number;
  dlqRetentionDays: number;
  rateLimitPerKey: number;
  concurrentJobsLimit: number;
  wsConnectionsLimit: number;
  maintenanceMode: boolean;
  maintenanceMessage?: string;
  maintenanceStartedAt?: string;
  maintenanceHistory?: MaintenanceWindow[];
  maintenanceSchedule?: MaintenanceSchedule[];
}

// ---------------------------------------------------------------------------
// Admin: Distributed Locks
// ---------------------------------------------------------------------------

export interface AdminLock {
  key: string;
  holder: string;
  ttl_remaining_ms: number;
  type: string;
}

// ---------------------------------------------------------------------------
// WebSocket stream events
// ---------------------------------------------------------------------------

export interface StreamEvent {
  id: string;
  type: string;
  timestamp: string;
  payload: Record<string, unknown>;
  severity?: string;
  eventType?: string;
  jobId?: string;
  runId?: string;
  workflowId?: string;
  source?: string;
  chatData?: unknown;
}

// ---------------------------------------------------------------------------
// Traces
// ---------------------------------------------------------------------------

export interface TraceSpan {
  span_id: string;
  parent_span_id?: string;
  operation: string;
  service: string;
  start_time: string;
  end_time?: string;
  duration_ms?: number;
  status: "ok" | "error" | "timeout";
  attributes?: Record<string, unknown>;
  safety_decision?: SafetyDecisionType;
  error_message?: string;
}

export interface Trace {
  trace_id: string;
  job_id?: string;
  agent_id?: string;
  spans: TraceSpan[];
  start_time: string;
  end_time?: string;
  total_duration_ms?: number;
  service_count?: number;
}

// ---------------------------------------------------------------------------
// Agent Registry
// ---------------------------------------------------------------------------

export interface AgentRegistryEntry {
  agent_id: string;
  name?: string;
  total_jobs: number;
  safety_breakdown: {
    allow: number;
    deny: number;
    require_approval: number;
    allow_with_constraints: number;
    throttle: number;
  };
  active_policy_bindings?: string[];
  last_activity?: string;
  metadata?: Record<string, unknown>;
}

// ---------------------------------------------------------------------------
// Setup Wizard
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Governance health — Command Center composite score.
// Matches core/governance.HealthScore / HealthFactor on the wire.
// ---------------------------------------------------------------------------

export type GovernanceGrade = "A" | "B" | "C" | "D" | "F";

export interface GovernanceHealthFactor {
  score: number;
  weight: number;
  raw?: unknown;
  notes?: string;
}

export interface GovernanceHealth {
  score: number;
  grade: GovernanceGrade;
  generated_at: string;
  factors: Record<string, GovernanceHealthFactor>;
  truncated_at_max?: boolean;
}

// ---------------------------------------------------------------------------
// Policy shadow mode (mirrors core/policyshadow wire types)
// ---------------------------------------------------------------------------

export interface ShadowPolicy {
  shadow_bundle_id: string;
  bundle_id: string;
  tenant_id: string;
  content: string;
  created_at: string;
  activated_at: string;
  created_by?: string;
  metadata?: Record<string, string>;
}

export interface ShadowPolicySummary {
  shadow_bundle_id: string;
  bundle_id: string;
  tenant_id: string;
  created_by?: string;
  created_at: string;
  activated_at: string;
}

export interface ShadowResultsSummary {
  total_evaluated: number;
  escalated_count: number;
  relaxed_count: number;
  approval_differ_count: number;
  unchanged_count: number;
  first_evaluated_at?: string;
  last_evaluated_at?: string;
}

export type ShadowDiff =
  | "escalated"
  | "relaxed"
  | "approval_differ"
  | "unchanged";

export interface ShadowComparisonEntry {
  ts_ms: number;
  job_id: string;
  agent_id: string;
  active_verdict: string;
  shadow_verdict: string;
  diff: ShadowDiff;
  active_rule_id?: string;
  shadow_rule_id?: string;
  latency_ms?: number;
  seq?: number;
}

export interface ShadowComparisonsResponse {
  entries: ShadowComparisonEntry[];
  next_cursor?: string;
  truncated_at_max?: boolean;
}

export interface ShadowTimeseriesBucket {
  ts_ms: number;
  escalated: number;
  relaxed: number;
  approval_differ: number;
  unchanged: number;
  total: number;
}

export interface ShadowTimeseriesResponse {
  buckets: ShadowTimeseriesBucket[];
  window_ms: number;
}

// ---------------------------------------------------------------------------
// MCP governance dashboard
// ---------------------------------------------------------------------------

export type SignatureStatus = "verified" | "unverified" | "invalid";

export interface MCPUsageCell {
  agent_id: string;
  tool_name: string;
  count: number;
  allow_count: number;
  deny_count: number;
  approval_required_count: number;
  p50_latency_ms: number;
  p99_latency_ms: number;
  last_invoked_at_ms: number;
}

export interface MCPUsageResponse {
  cells: MCPUsageCell[];
  total_calls: number;
  window_ms: number;
  truncated_at_max: boolean;
}

export interface MCPOutboundEntry {
  ts_ms: number;
  stream_id: string;
  agent_id: string;
  tool_name: string;
  target_server: string;
  signature_status: SignatureStatus;
  signature_key_id?: string;
  latency_ms?: number;
  result_type?: string;
  event_hash?: string;
}

export interface MCPOutboundResponse {
  entries: MCPOutboundEntry[];
  next_cursor?: string;
  truncated_at_max: boolean;
}

export interface SetupStatus {
  setup_complete: boolean;
  steps: {
    admin_created: boolean;
    api_key_configured: boolean;
    safety_kernel_connected: boolean;
    first_agent_registered: boolean;
    first_job_submitted: boolean;
  };
}

// ---------------------------------------------------------------------------
// Delegations
// ---------------------------------------------------------------------------

export type DelegationStatus = "active" | "revoked" | "expired" | "all";

export interface DelegationChainLink {
  agentId: string;
  issuedAt: string;
  expiresAt: string;
  jti: string;
  parentJti?: string;
  issuedBy: string;
}

export interface DelegationView {
  jti: string;
  issuer: string;
  subject: string;
  audience: string;
  allowedActions: string[];
  allowedTopics: string[];
  chain: DelegationChainLink[];
  chainDepth: number;
  issuedAt: string;
  expiresAt: string;
  revoked: boolean;
  revokedAt?: string;
  revokedReason?: string;
}

export interface DelegationListResponse {
  items: DelegationView[];
  nextCursor?: string;
}

export interface RevokeDelegationResult {
  jti: string;
  cascadedCount: number;
}

// ---------------------------------------------------------------------------
// Evals
// ---------------------------------------------------------------------------

export interface EvalDataset {
  id: string;
  name: string;
  version: number;
  tenant: string;
  description?: string;
  entryCount: number;
  contentHash: string;
  createdAt: string;
  updatedAt: string;
  createdBy?: string;
}

export interface EvalEntry {
  id: string;
  input: Record<string, unknown>;
  expectedDecision: SafetyDecisionType;
  ruleId?: string;
  metadata?: Record<string, unknown>;
  source: string;
  sourceRef?: string;
  notes?: string;
}

export type EvalRunStatus = "pass" | "fail" | "regression" | "error";

export type EvalDriftDirection = "escalated" | "relaxed" | "unchanged";

export interface EvalEntryResult {
  entryId: string;
  input: Record<string, unknown>;
  expectedDecision: SafetyDecisionType;
  actualDecision: SafetyDecisionType | string;
  ruleId?: string;
  reason?: string;
  status: EvalRunStatus;
  driftDirection: EvalDriftDirection;
  constraints?: PolicyConstraints;
}

export interface EvalRunSummary {
  total: number;
  passed: number;
  failed: number;
  regressions: number;
  errored: number;
  scorePercent: number | null;
}

export interface EvalRun {
  runId: string;
  datasetId: string;
  datasetName: string;
  datasetVersion: number;
  policySnapshot: string;
  startedAt: string;
  completedAt?: string;
  summary: EvalRunSummary;
  entries?: EvalEntryResult[];
}

export interface ExtractIncidentsRequest {
  since?: string;
  until?: string;
  topicPattern?: string;
  ruleId?: string;
  verdicts?: SafetyDecisionType[];
  agentId?: string;
  maxEntries?: number;
  datasetName: string;
  datasetDescription?: string;
  dryRun?: boolean;
}

export interface ExtractIncidentsPreview {
  scannedDecisions: number;
  entryCount: number;
  dedupedCount: number;
  warnings: string[];
  datasetId?: string;
}

// ---------------------------------------------------------------------------
// Copilot Sessions
// ---------------------------------------------------------------------------

export type CopilotMessageRole = "user" | "assistant" | "system";

export interface CopilotMessage {
  id: string;
  role: CopilotMessageRole;
  content: string;
  timestamp: string;
  jobIds?: string[]; // IDs of jobs spawned by this message/turn
}

export interface CopilotSession {
  id: string;
  title?: string;
  userId: string;
  createdAt: string;
  updatedAt: string;
  messages: CopilotMessage[];
  metadata?: Record<string, string>;
}

// The `/api/v1/copilot/sessions/:sessionId` endpoint returns a sanitized,
// session-scoped projection of jobs and decisions. Several fields the full
// `Job` and `GovernanceDecision` types treat as required (e.g. `topic`,
// `matchedRule`, `agentId`) are intentionally omitted server-side when meta
// has expired, when the caller lacks `governance.read`, or when the source
// records were partial. Modelling that wire shape with the full types above
// would force consumers into unsafe non-null assertions; use these
// Copilot-specific views with optional fields instead.
export interface CopilotSessionJob {
  id: string;
  status: JobStatus | string;
  type?: string;
  topic?: string;
  pool?: string;
  capabilities?: string[];
  riskTags?: string[];
  metadata?: Record<string, string>;
  createdAt?: string;
  updatedAt?: string;
}

export interface CopilotSessionDecision {
  jobId: string;
  verdict: GovernanceVerdict | string;
  timestamp: string;
  topic?: string;
  matchedRule?: string;
  ruleName?: string;
  reason?: string;
  constraints?: PolicyConstraints;
  approvalStatus?: ApprovalStatus;
  approvalDecision?: "approve" | "reject" | "expire" | "invalidate" | "repair";
  agentId?: string;
  policyVersion?: string;
}

export interface CopilotSessionDetailResponse {
  session: CopilotSession;
  jobs: CopilotSessionJob[];
  decisions: CopilotSessionDecision[];
  truncated: boolean;
}
