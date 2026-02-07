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
  | "timeout";

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
  lastState?: string;
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
  | "cancelled"
  | "queued"
  | "in_progress"
  | "completed"
  | "blocked";

export interface WorkflowStep {
  id: string;
  name: string;
  type: string;
  config: Record<string, unknown>;
  dependsOn?: string[];
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
  timeout: number;
  metadata: Record<string, unknown>;
  orgId?: string;
  teamId?: string;
  description?: string;
  version?: string;
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
  startedAt: string;
  completedAt?: string;
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

export interface AuditEntry {
  id: string;
  timestamp: string;
  eventType: string;
  actor: string;
  resourceType: string;
  resourceId: string;
  action: string;
  message: string;
  payload?: Record<string, unknown>;
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

export interface Approval {
  id: string;
  jobId: string;
  status: string;
  requestedAt: string;
  resolvedAt?: string;
  actor?: string;
  reason?: string;
  comment?: string;
  policyRule?: string;
  jobContext?: Record<string, unknown>;
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
  oauth_enabled?: boolean;
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
