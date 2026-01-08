export type Workflow = {
  id: string;
  org_id: string;
  team_id: string;
  name: string;
  description: string;
  version: string;
  timeout_sec: number;
  steps: Record<string, Step>;
  config?: Record<string, unknown>;
  input_schema?: Record<string, unknown>;
  parameters?: Array<Record<string, unknown>>;
  created_by?: string;
  created_at?: string;
  updated_at?: string;
};

export type StepMeta = {
  actor_id?: string;
  actor_type?: string;
  idempotency_key?: string;
  pack_id?: string;
  capability?: string;
  risk_tags?: string[];
  requires?: string[];
  labels?: Record<string, string>;
};

export type Step = {
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
  meta?: StepMeta;
  on_error?: string;
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
};

export type WorkflowRun = {
  id: string;
  workflow_id: string;
  org_id: string;
  team_id: string;
  input: Record<string, unknown>;
  context?: Record<string, unknown>;
  status: string;
  started_at?: string;
  completed_at?: string;
  output?: Record<string, unknown>;
  error?: Record<string, unknown>;
  steps?: Record<string, StepRun>;
  total_cost?: number;
  triggered_by?: string;
  created_at?: string;
  updated_at?: string;
  labels?: Record<string, string>;
  metadata?: Record<string, string>;
  idempotency_key?: string;
  rerun_of?: string;
  rerun_step?: string;
  dry_run?: boolean;
};

export type StepRun = {
  step_id: string;
  status: string;
  started_at?: string;
  completed_at?: string;
  next_attempt_at?: string;
  attempts?: number;
  input?: Record<string, unknown>;
  output?: unknown;
  error?: Record<string, unknown>;
  job_id?: string;
  item?: unknown;
  children?: Record<string, StepRun>;
};

export type TimelineEvent = {
  time: string;
  type: string;
  run_id?: string;
  workflow_id?: string;
  step_id?: string;
  job_id?: string;
  status?: string;
  result_ptr?: string;
  message?: string;
  data?: Record<string, unknown>;
};

export type JobRecord = {
  id: string;
  trace_id?: string;
  updated_at: number;
  state: string;
  topic?: string;
  tenant?: string;
  team?: string;
  principal?: string;
  actor_id?: string;
  actor_type?: string;
  idempotency_key?: string;
  capability?: string;
  risk_tags?: string[];
  requires?: string[];
  pack_id?: string;
  attempts?: number;
  safety_decision?: string;
  safety_reason?: string;
  safety_rule_id?: string;
  safety_snapshot?: string;
  deadline_unix?: number;
};

export type JobDetail = {
  id: string;
  state: string;
  trace_id?: string;
  topic?: string;
  tenant?: string;
  actor_id?: string;
  actor_type?: string;
  idempotency_key?: string;
  capability?: string;
  pack_id?: string;
  risk_tags?: string[];
  requires?: string[];
  attempts?: number;
  result_ptr?: string;
  context_ptr?: string;
  context?: Record<string, unknown>;
  result?: Record<string, unknown>;
  error_message?: string;
  error_status?: string;
  error_code?: string;
  last_state?: string;
  safety_decision?: string;
  safety_reason?: string;
  safety_rule_id?: string;
  safety_snapshot?: string;
  safety_constraints?: Record<string, unknown>;
  safety_job_hash?: string;
  approval_required?: boolean;
  approval_ref?: string;
  approval_by?: string;
  approval_role?: string;
  approval_at?: number;
  approval_reason?: string;
  approval_note?: string;
  approval_policy_snapshot?: string;
  approval_job_hash?: string;
  labels?: Record<string, string>;
  workflow_id?: string;
  run_id?: string;
  step_id?: string;
};

export type DLQEntry = {
  job_id: string;
  topic?: string;
  status?: string;
  reason?: string;
  reason_code?: string;
  last_state?: string;
  attempts?: number;
  created_at: string;
};

export type DLQResponse = {
  items: DLQEntry[];
  next_cursor?: number | null;
};

export type Heartbeat = {
  worker_id?: string;
  pool?: string;
  cpu_load?: number;
  memory_load?: number;
  topic?: string;
  updated_at?: string;
  [key: string]: unknown;
};

export type ApprovalItem = {
  job: JobRecord;
  decision?: string;
  policy_snapshot?: string;
  policy_rule_id?: string;
  policy_reason?: string;
  constraints?: Record<string, unknown>;
  job_hash?: string;
  approval_required?: boolean;
  approval_ref?: string;
};

export type SafetyDecisionRecord = {
  decision?: string;
  reason?: string;
  rule_id?: string;
  policy_snapshot?: string;
  constraints?: Record<string, unknown>;
  approval_required?: boolean;
  approval_ref?: string;
  checked_at?: number;
};

export type ApprovalsResponse = {
  items: ApprovalItem[];
  next_cursor?: number | null;
};

export type WorkflowRunsResponse = {
  items: WorkflowRun[];
  next_cursor?: number | null;
};

export type JobsResponse = {
  items: JobRecord[];
  next_cursor?: number | null;
};

export type ConfigDocument = {
  scope: string;
  scope_id: string;
  data: Record<string, unknown>;
  revision: number;
  updated_at: string;
  meta?: Record<string, string>;
};

export type EffectiveConfigSnapshot = {
  version: string;
  hash: string;
  data: Record<string, unknown>;
};

export type PolicyCheckResponse = Record<string, unknown>;

export type PackRecord = {
  id: string;
  version: string;
  status: string;
  installed_at?: string;
  installed_by?: string;
  manifest?: {
    metadata?: {
      id?: string;
      version?: string;
      title?: string;
      description?: string;
    };
    compatibility?: {
      protocolVersion?: number;
      minCoreVersion?: string;
      maxCoreVersion?: string;
    };
    topics?: Array<{
      name?: string;
      requires?: string[];
      riskTags?: string[];
      capability?: string;
    }>;
  };
  resources?: {
    schemas?: Record<string, string>;
    workflows?: Record<string, string>;
  };
  overlays?: {
    config?: Array<{
      name?: string;
      scope?: string;
      scope_id?: string;
      key?: string;
      patch?: Record<string, unknown>;
    }>;
    policy?: Array<{
      name?: string;
      fragment_id?: string;
    }>;
  };
  tests?: {
    policySimulations?: Array<{
      name?: string;
      request?: Record<string, unknown>;
      expectDecision?: string;
    }>;
  };
};

export type PackListResponse = {
  items: PackRecord[];
};

export type PackVerifyResult = {
  name: string;
  expected: string;
  got: string;
  reason: string;
  ok: boolean;
};

export type PackVerifyResponse = {
  pack_id: string;
  results: PackVerifyResult[];
};

export type PolicyBundlesResponse = {
  bundles: Record<string, unknown>;
  items?: PolicyBundleSummary[];
  updated_at?: string;
};

export type PolicyBundleSummary = {
  id: string;
  enabled: boolean;
  source: string;
  author?: string;
  message?: string;
  created_at?: string;
  updated_at?: string;
  version?: string;
  installed_at?: string;
  sha256?: string;
};

export type PolicyBundleDetail = {
  id: string;
  content: string;
  enabled: boolean;
  author?: string;
  message?: string;
  created_at?: string;
  updated_at?: string;
};

export type PolicyBundleSimulateRequest = {
  request: Record<string, unknown>;
  content?: string;
};

export type PolicyBundleSnapshotSummary = {
  id: string;
  created_at: string;
  note?: string;
};

export type PolicyBundleSnapshotsResponse = {
  items: PolicyBundleSnapshotSummary[];
};

export type PolicyBundleSnapshot = {
  id: string;
  created_at: string;
  note?: string;
  bundles: Record<string, unknown>;
};

export type PolicyPublishResponse = {
  snapshot_before?: string;
  snapshot_after?: string;
  published?: string[];
};

export type PolicyRollbackResponse = {
  snapshot_before?: string;
  snapshot_after?: string;
  rollback_to?: string;
};

export type PolicyAuditEntry = {
  id: string;
  action: string;
  actor_id?: string;
  role?: string;
  bundle_ids?: string[];
  message?: string;
  snapshot_before?: string;
  snapshot_after?: string;
  created_at: string;
};

export type PolicyAuditResponse = {
  items: PolicyAuditEntry[];
};

export type PolicyRuleSource = {
  fragment_id: string;
  pack_id?: string;
  overlay_name?: string;
  version?: string;
  installed_at?: string;
  sha256?: string;
};

export type PolicyRule = {
  id?: string;
  decision?: string;
  reason?: string;
  match?: Record<string, unknown>;
  constraints?: Record<string, unknown>;
  source?: PolicyRuleSource;
  [key: string]: unknown;
};

export type PolicyRuleError = {
  fragment_id: string;
  error: string;
};

export type PolicyRulesResponse = {
  items: PolicyRule[];
  errors?: PolicyRuleError[];
};

export type BusPacket = {
  traceId?: string;
  senderId?: string;
  createdAt?: string;
  protocolVersion?: number;
  payload?: {
    jobRequest?: {
      jobId?: string;
      topic?: string;
      tenantId?: string;
      principalId?: string;
      workflowId?: string;
      labels?: Record<string, string>;
      riskTags?: string[];
      requires?: string[];
      packId?: string;
    };
    jobResult?: {
      jobId?: string;
      status?: string;
      errorCode?: string;
      errorMessage?: string;
      resultPtr?: string;
    };
    jobProgress?: {
      jobId?: string;
      status?: string;
      progress?: number;
      message?: string;
    };
    heartbeat?: {
      workerId?: string;
      pool?: string;
      cpuLoad?: number;
      memoryLoad?: number;
    };
    alert?: {
      level?: string;
      message?: string;
      code?: string;
    };
    jobCancel?: {
      jobId?: string;
      reason?: string;
    };
  };
};
