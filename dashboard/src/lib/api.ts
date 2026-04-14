import { get, post, put, del, patch } from "../api/client";
import type { User, Approval, DLQEntry, ApprovalContext, PolicyReplayRequest, PolicyReplayResponse, PolicyAnalyticsRequest, PolicyAnalyticsResponse } from "../api/types";
import { mapDLQEntry, mapApprovalItem, type BackendDLQEntry, type BackendApprovalItem } from "../api/transform";
import type {
  PolicyBundlesResponse,
  PolicyBundleDetail,
  PolicyBundleSnapshot,
  PolicyBundleSnapshotsResponse,
  PolicyBundleSnapshotSummary,
  PolicyPublishResponse,
  PolicyRollbackResponse,
  PolicyRulesResponse,
  PolicyAuditResponse,
  PolicyCheckResponse,
  PolicyBundleSimulateRequest,
  PolicySnapshotsListResponse,
  WorkflowRunsResponse,
  RawWorkflowRun,
  RawWorkflow,
  JobsResponse,
  JobDetail,
  Heartbeat,
  ConfigDocument,
  PackListResponse,
  ArtifactResponse,
  Lock,
  MemoryResult,
  JobRecord,
  ChatMessagePayload,
} from "../types/api";
import { API_PATHS } from "./constants";

type QueryParams = Record<string, string | number | boolean | null | undefined>;

function buildQueryString(params?: QueryParams): string {
  if (!params) return "";
  const entries = Object.entries(params)
    .filter((pair): pair is [string, string | number | boolean] => pair[1] != null)
    .map(([k, v]) => [k, String(v)]);
  return entries.length > 0 ? "?" + new URLSearchParams(entries).toString() : "";
}

interface SessionResponse {
  user: User;
}

export interface ApprovalsResponse {
  items: Approval[];
  next_cursor?: number | null;
}

interface DLQResponse {
  items: DLQEntry[];
  next_cursor?: number | string | null;
}

interface WorkflowListResponse {
  items: RawWorkflow[];
}

interface PoolListResponse {
  items: PoolSummary[];
}

interface PoolSummary {
  name: string;
  workers: number;
  active_jobs: number;
  capacity: number;
  utilization: number;
  topics?: string[];
  worker_list?: Heartbeat[];
  captured_at?: string;
}

interface StatusResponse {
  version?: string;
  uptime_sec?: number;
  services?: Record<string, unknown>;
  [key: string]: unknown;
}

interface SchemaEntry {
  id?: string;
  name?: string;
  version?: string;
  [key: string]: unknown;
}

interface ApprovalActionBody {
  reason?: string;
  note?: string;
  comment?: string;
}

interface RerunBody {
  from_step?: string;
  dry_run?: boolean;
}

interface PublishBody {
  note?: string;
  dry_run?: boolean;
}

interface RollbackBody {
  snapshot_id: string;
  note?: string;
}

interface SnapshotCaptureBody {
  note?: string;
}

interface ConfigBody {
  data?: Record<string, unknown>;
  meta?: Record<string, string>;
}

interface ChatSendBody {
  content: string;
  role?: string;
  stepId?: string;
  jobId?: string;
  metadata?: Record<string, unknown>;
}

export function wsUrl(path?: string, params?: Record<string, string | undefined>): string {
  const base = window.location.origin.replace(/^http/, "ws");
  const p = path || API_PATHS.stream;
  const qs = params
    ? "?" + Object.entries(params)
        .filter(([, v]) => v != null && v !== "")
        .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v!)}`)
        .join("&")
    : "";
  return `${base}${p}${qs}`;
}

export function wsProtocols(apiKey?: string | null): string[] {
  return apiKey ? [`cordum-api-key.${btoa(apiKey)}`] : [];
}

export const api = {
  getSession(): Promise<SessionResponse> {
    return get<SessionResponse>("/auth/session");
  },

  logout(): Promise<void> {
    return post<void>("/auth/logout");
  },

  listApprovals(limit: number, cursor?: string | number): Promise<ApprovalsResponse> {
    const cursorStr = cursor != null ? String(cursor) : undefined;
    const q = cursorStr ? `/approvals?limit=${limit}&cursor=${encodeURIComponent(cursorStr)}` : `/approvals?limit=${limit}`;
    return get<{ items: BackendApprovalItem[]; next_cursor?: number | null }>(q).then((res) => ({
      items: (res.items ?? [])
        .map(mapApprovalItem)
        .filter((v): v is Approval => !!v),
      next_cursor: res.next_cursor,
    }));
  },

  listDLQPage(limit: number, cursor?: string | number): Promise<DLQResponse> {
    const cursorStr = cursor != null ? String(cursor) : undefined;
    const q = cursorStr ? `/dlq/page?limit=${limit}&cursor=${encodeURIComponent(cursorStr)}` : `/dlq/page?limit=${limit}`;
    return get<{ items: BackendDLQEntry[]; next_cursor?: number | string | null }>(q).then((res) => ({
      items: (res.items ?? []).map(mapDLQEntry),
      next_cursor: res.next_cursor,
    }));
  },

  // ---------------------------------------------------------------------------
  // Policy methods
  // ---------------------------------------------------------------------------

  listPolicySnapshots(): Promise<PolicySnapshotsListResponse> {
    return get<PolicySnapshotsListResponse>("/policy/snapshots");
  },

  getPolicyBundles(): Promise<PolicyBundlesResponse> {
    return get<PolicyBundlesResponse>("/policy/bundles");
  },

  listPolicyRules(): Promise<PolicyRulesResponse> {
    return get<PolicyRulesResponse>("/policy/rules");
  },

  listPolicyBundleSnapshots(): Promise<PolicyBundleSnapshotsResponse> {
    return get<PolicyBundleSnapshotsResponse>("/policy/bundles/snapshots");
  },

  listPolicyAudit(): Promise<PolicyAuditResponse> {
    return get<PolicyAuditResponse>("/policy/audit");
  },

  getPolicyBundleSnapshot(id: string): Promise<PolicyBundleSnapshot> {
    return get<PolicyBundleSnapshot>(`/policy/bundles/snapshots/${id}`);
  },

  getPolicyBundle(id: string): Promise<PolicyBundleDetail> {
    return get<PolicyBundleDetail>(`/policy/bundles/${id}`);
  },

  putPolicyBundle(id: string, body: Partial<PolicyBundleDetail>): Promise<PolicyBundleDetail> {
    return put<PolicyBundleDetail>(`/policy/bundles/${id}`, body);
  },

  publishPolicyBundles(body: PublishBody): Promise<PolicyPublishResponse> {
    return post<PolicyPublishResponse>("/policy/bundles/publish", body);
  },

  rollbackPolicyBundles(body: RollbackBody): Promise<PolicyRollbackResponse> {
    return post<PolicyRollbackResponse>("/policy/bundles/rollback", body);
  },

  capturePolicyBundleSnapshot(body: SnapshotCaptureBody): Promise<PolicyBundleSnapshotSummary> {
    return post<PolicyBundleSnapshotSummary>("/policy/bundles/snapshots", body);
  },

  simulatePolicyBundle(id: string, body: PolicyBundleSimulateRequest): Promise<PolicyCheckResponse> {
    return post<PolicyCheckResponse>(`/policy/bundles/${id}/simulate`, body);
  },

  policySimulate(body: Record<string, unknown>): Promise<PolicyCheckResponse> {
    return post<PolicyCheckResponse>("/policy/simulate", body);
  },

  policyExplain(body: Record<string, unknown>): Promise<PolicyCheckResponse> {
    return post<PolicyCheckResponse>("/policy/explain", body);
  },

  policyEvaluate(body: Record<string, unknown>): Promise<PolicyCheckResponse> {
    return post<PolicyCheckResponse>("/policy/evaluate", body);
  },

  policyReplay(body: PolicyReplayRequest): Promise<PolicyReplayResponse> {
    return post<PolicyReplayResponse>("/policy/replay", body);
  },

  policyAnalytics(body: PolicyAnalyticsRequest): Promise<PolicyAnalyticsResponse> {
    return post<PolicyAnalyticsResponse>("/policy/analytics", body);
  },

  // ---------------------------------------------------------------------------
  // Job / Approval methods
  // ---------------------------------------------------------------------------

  approveJob(id: string, body?: ApprovalActionBody): Promise<void> {
    return post<void>(`/approvals/${id}/approve`, body);
  },

  rejectJob(id: string, body?: ApprovalActionBody): Promise<void> {
    return post<void>(`/approvals/${id}/reject`, body);
  },

  getApprovalContext(jobId: string): Promise<ApprovalContext> {
    return get<ApprovalContext>(`/approvals/${jobId}/context`);
  },

  listJobs(params?: QueryParams): Promise<JobsResponse> {
    return get<JobsResponse>(`/jobs${buildQueryString(params)}`);
  },

  getJob(id: string): Promise<JobDetail> {
    return get<JobDetail>(`/jobs/${id}`);
  },

  // ---------------------------------------------------------------------------
  // Workflow / Run methods
  // ---------------------------------------------------------------------------

  listWorkflowRuns(params?: QueryParams): Promise<WorkflowRunsResponse> {
    return get<WorkflowRunsResponse>(`/workflows/runs${buildQueryString(params)}`);
  },

  getRun(id: string): Promise<RawWorkflowRun> {
    return get<RawWorkflowRun>(`/workflows/runs/${id}`);
  },

  cancelRun(workflowIdOrRunId: string, runId?: string): Promise<void> {
    const id = runId ?? workflowIdOrRunId;
    return post<void>(`/workflows/runs/${id}/cancel`);
  },

  rerunRun(id: string, body?: RerunBody): Promise<RawWorkflowRun> {
    return post<RawWorkflowRun>(`/workflows/runs/${id}/rerun`, body);
  },

  listWorkflows(params?: QueryParams): Promise<WorkflowListResponse> {
    return get<WorkflowListResponse>(`/workflows${buildQueryString(params)}`);
  },

  // ---------------------------------------------------------------------------
  // Worker / Pool methods
  // ---------------------------------------------------------------------------

  listWorkers(params?: QueryParams): Promise<Heartbeat[]> {
    return get<Heartbeat[]>(`/workers${buildQueryString(params)}`);
  },

  getWorker(id: string): Promise<Heartbeat> {
    return get<Heartbeat>(`/workers/${encodeURIComponent(id)}`);
  },

  getWorkerJobs(workerId: string, limit = 20): Promise<JobsResponse> {
    return get<JobsResponse>(`/workers/${encodeURIComponent(workerId)}/jobs?limit=${limit}`);
  },

  listPools(): Promise<PoolListResponse> {
    return get<PoolListResponse>("/pools");
  },

  getPool(name: string): Promise<PoolSummary> {
    return get<PoolSummary>(`/pools/${encodeURIComponent(name)}`);
  },

  createPool(name: string, data: { requires?: string[]; description?: string }) {
    return put<{ name: string; status: string; requires: string[]; description: string }>(
      `/pools/${encodeURIComponent(name)}`,
      data,
    );
  },

  updatePool(name: string, data: { requires?: string[]; description?: string; status?: string }) {
    return patch<{ name: string; status: string; requires: string[]; description: string }>(
      `/pools/${encodeURIComponent(name)}`,
      data,
    );
  },

  deletePool(name: string, force = false) {
    const qs = force ? "?force=true" : "";
    return del<void>(`/pools/${encodeURIComponent(name)}${qs}`);
  },

  drainPool(name: string, opts?: { timeout_seconds?: number }) {
    return post<{ name: string; status: string; drain_started_at: string; drain_timeout_seconds: number }>(
      `/pools/${encodeURIComponent(name)}/drain`,
      opts || {},
    );
  },

  addTopicToPool(poolName: string, topic: string) {
    return put<void>(`/pools/${encodeURIComponent(poolName)}/topics/${encodeURIComponent(topic)}`, {});
  },

  removeTopicFromPool(poolName: string, topic: string) {
    return del<void>(`/pools/${encodeURIComponent(poolName)}/topics/${encodeURIComponent(topic)}`);
  },

  getStatus(): Promise<StatusResponse> {
    return get<StatusResponse>("/status");
  },

  // ---------------------------------------------------------------------------
  // Config methods
  // ---------------------------------------------------------------------------

  getConfig(scope?: string, scopeId?: string): Promise<ConfigDocument> {
    const parts = [scope, scopeId].filter(Boolean);
    const path = parts.length > 0 ? `/config/${parts.join("/")}` : "/config";
    return get<ConfigDocument>(path);
  },

  setConfig(scope: string, scopeId: string, data?: Record<string, unknown>, meta?: Record<string, string>): Promise<ConfigDocument> {
    return put<ConfigDocument>(`/config/${scope}/${scopeId}`, { data, meta } satisfies ConfigBody);
  },

  // ---------------------------------------------------------------------------
  // Schema methods
  // ---------------------------------------------------------------------------

  listSchemas(params?: QueryParams): Promise<SchemaEntry[]> {
    return get<SchemaEntry[]>(`/schemas${buildQueryString(params)}`);
  },

  // ---------------------------------------------------------------------------
  // Pack methods
  // ---------------------------------------------------------------------------

  listPacks(params?: QueryParams): Promise<PackListResponse> {
    return get<PackListResponse>(`/packs${buildQueryString(params)}`);
  },

  // ---------------------------------------------------------------------------
  // Artifact / Lock / Memory methods
  // ---------------------------------------------------------------------------

  getArtifact(id: string): Promise<ArtifactResponse> {
    return get<ArtifactResponse>(`/artifacts/${id}`);
  },

  putArtifact(id: string, body: unknown): Promise<ArtifactResponse> {
    return put<ArtifactResponse>(`/artifacts/${id}`, body);
  },

  getLock(id: string): Promise<Lock> {
    return get<Lock>(`/locks/${id}`);
  },

  acquireLock(resource: string, owner?: string, ttlMs?: number): Promise<Lock> {
    return post<Lock>("/locks", { resource, owner, ttl_ms: ttlMs });
  },

  releaseLock(resource: string, owner?: string): Promise<void> {
    return del<void>(`/locks/${encodeURIComponent(resource)}${owner ? `?owner=${encodeURIComponent(owner)}` : ""}`);
  },

  renewLock(resource: string, owner?: string, ttlMs?: number): Promise<Lock> {
    return post<Lock>(`/locks/${encodeURIComponent(resource)}/renew`, { owner, ttl_ms: ttlMs });
  },

  getMemory(ptr?: string, key?: string): Promise<MemoryResult> {
    if (ptr) return get<MemoryResult>(`/memory/${encodeURIComponent(ptr)}`);
    if (key) return get<MemoryResult>(`/memory?key=${encodeURIComponent(key)}`);
    return get<MemoryResult>("/memory");
  },

  // ---------------------------------------------------------------------------
  // Trace methods
  // ---------------------------------------------------------------------------

  getTrace(id: string): Promise<JobRecord[]> {
    return get<JobRecord[]>(`/traces/${id}`);
  },

  // ---------------------------------------------------------------------------
  // Chat methods
  // ---------------------------------------------------------------------------

  getRunChat(runId: string): Promise<ChatMessagePayload[]> {
    return get<ChatMessagePayload[]>(`/workflows/runs/${runId}/chat`);
  },

  sendChatMessage(runId: string, body: ChatSendBody): Promise<ChatMessagePayload> {
    return post<ChatMessagePayload>(`/workflows/runs/${runId}/chat`, body);
  },

  // ---------------------------------------------------------------------------
  // DLQ methods
  // ---------------------------------------------------------------------------

  retryDLQ(id: string): Promise<void> {
    return post<void>(`/dlq/${id}/retry`);
  },

  deleteDLQ(id: string): Promise<void> {
    return del<void>(`/dlq/${id}`);
  },
};
