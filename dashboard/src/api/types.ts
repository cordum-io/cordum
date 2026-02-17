// ---------------------------------------------------------------------------
// Response envelope
// ---------------------------------------------------------------------------

export interface ApiResponse<T> {
  items: T extends Array<infer _> ? T : T[];
  next_cursor?: number | null;
}

export interface PaginationParams {
  page?: number;
  perPage?: number;
  sort?: string;
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
  | "output_quarantined";

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
  original_ptr?: string;
}

export interface OutputPolicyRule {
  id: string;
  match: Record<string, unknown>;
  decision: OutputDecision;
  reason?: string;
  enabled?: boolean;
}

export interface SafetyDecision {
  type: "allow" | "deny" | "require_approval" | "throttle";
  reason: string;
  matchedRule?: string;
  evalTimeMs?: number;
  evalPath?: string[];
}

export interface Job {
  id: string;
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
  /** @deprecated Use depends_on */
  dependsOn?: string[];
  /** Legacy config bag — kept for backward compat during migration */
  config?: Record<string, unknown>;
  // Run-time fields (present when viewing runs)
  status?: RunStatus;
  output?: Record<string, unknown>;
  error?: string;
  startedAt?: string;
  completedAt?: string;
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
}

// ---------------------------------------------------------------------------
// Policies
// ---------------------------------------------------------------------------

export interface PolicyRule {
  id: string;
  matchCriteria: Record<string, unknown>;
  decisionType: "allow" | "deny" | "require_approval" | "throttle";
  reason?: string;
  hitCount24h?: number;
  lastTriggered?: string;
  priority?: number;
  logic?: string;
  source?: Record<string, unknown>;
  enabled?: boolean;
}

export interface PolicyBundle {
  id: string;
  name: string;
  rules: PolicyRule[];
  version?: number;
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
  healthStatus?: string;
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

export interface ApprovalWorkflowContext {
  workflowId: string;
  runId: string;
  stepIndex?: number;
  stepName?: string;
  totalSteps?: number;
}

export interface Approval {
  id: string;
  jobId: string;
  status: string;
  requestedAt: string;
  resolvedAt?: string;
  actor?: string;
  actorId?: string;
  reason?: string;
  comment?: string;
  policyRule?: string;
  jobContext?: Record<string, unknown>;
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
  constraints?: Record<string, unknown>;
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
}

export interface ChangePasswordPayload {
  current_password: string;
  new_password: string;
}

export interface ResetUserPasswordPayload {
  password: string;
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
// WebSocket stream events
// ---------------------------------------------------------------------------

export interface StreamEvent {
  id: string;
  type: string;
  timestamp: string;
  payload: Record<string, unknown>;
}
