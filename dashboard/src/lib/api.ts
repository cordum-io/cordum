import { useConfigStore } from "../state/config";
import type { ChatMessage, ChatResponse } from "../types/chat";
import type {
  ApprovalsResponse,
  ArtifactResponse,
  ConfigDocument,
  DLQEntry,
  DLQResponse,
  EffectiveConfigSnapshot,
  Heartbeat,
  JobDetail,
  JobsResponse,
  Lock,
  MarketplaceResponse,
  MemoryResult,
  PackListResponse,
  PackRecord,
  PackVerifyResponse,
  PolicyBundleSnapshot,
  PolicyBundleSnapshotsResponse,
  PolicyBundlesResponse,
  PolicyCheckResponse,
  PolicyPublishResponse,
  PolicyRollbackResponse,
  PolicyAuditResponse,
  PolicyBundleDetail,
  PolicyBundleSimulateRequest,
  PolicyRulesResponse,
  SafetyDecisionRecord,
  TimelineEvent,
  AuthConfig,
  AuthLoginResponse,
  Workflow,
  WorkflowRun,
  WorkflowRunsResponse,
} from "../types/api";

const DEFAULT_TIMEOUT_MS = 15_000;

type JsonBody = Record<string, unknown> | Array<unknown>;

type RequestOptions = Omit<RequestInit, "body"> & {
  query?: Record<string, string | number | boolean | undefined | null>;
  timeoutMs?: number;
  body?: BodyInit | JsonBody;
};

function resolveBaseUrl(): string {
  const { apiBaseUrl } = useConfigStore.getState();
  if (!apiBaseUrl) {
    return window.location.origin;
  }
  if (apiBaseUrl.startsWith("http://") || apiBaseUrl.startsWith("https://")) {
    return apiBaseUrl.replace(/\/$/, "");
  }
  return `${window.location.origin}${apiBaseUrl.startsWith("/") ? "" : "/"}${apiBaseUrl}`;
}

function buildUrl(path: string, query?: RequestOptions["query"]): string {
  const base = resolveBaseUrl();
  const url = new URL(path, base);
  if (query) {
    Object.entries(query).forEach(([key, value]) => {
      if (value === undefined || value === null || value === "") {
        return;
      }
      url.searchParams.set(key, String(value));
    });
  }
  return url.toString();
}

async function apiRequest<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const config = useConfigStore.getState();
  const headers = new Headers(options.headers || {});
  headers.set("Accept", "application/json");
  if (config.apiKey) {
    headers.set("X-API-Key", config.apiKey);
  }
  if (config.principalId) {
    headers.set("X-Principal-Id", config.principalId);
  }
  if (config.principalRole) {
    headers.set("X-Principal-Role", config.principalRole);
  }

  let body = options.body;
  const isPlainObject =
    body &&
    typeof body === "object" &&
    !(body instanceof FormData) &&
    !(body instanceof URLSearchParams) &&
    !(body instanceof Blob) &&
    !(body instanceof ArrayBuffer) &&
    !(ArrayBuffer.isView(body));
  if (isPlainObject) {
    headers.set("Content-Type", "application/json");
    body = JSON.stringify(body);
  }

  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), options.timeoutMs ?? DEFAULT_TIMEOUT_MS);
  try {
    const res = await fetch(buildUrl(path, options.query), {
      ...options,
      headers,
      body: body as BodyInit | null | undefined,
      signal: controller.signal,
    });
    if (res.status === 401) {
      const updater = useConfigStore.getState().update;
      if (typeof updater === "function") {
        updater({ apiKey: "", principalId: "", principalRole: "" });
      }
    }
    if (!res.ok) {
      const message = await res.text();
      throw new Error(message || `Request failed (${res.status})`);
    }
    if (res.status === 204) {
      return undefined as T;
    }
    return (await res.json()) as T;
  } finally {
    window.clearTimeout(timeout);
  }
}

export const api = {
  getAuthConfig: () => apiRequest<AuthConfig>("/api/v1/auth/config"),
  login: (payload: { username: string; password: string; tenant?: string }) =>
    apiRequest<AuthLoginResponse>("/api/v1/auth/login", { method: "POST", body: payload }),
  logout: () => apiRequest<void>("/api/v1/auth/logout", { method: "POST" }),
  getSession: () => apiRequest<AuthLoginResponse>("/api/v1/auth/session"),
  listWorkflows: () => apiRequest<Workflow[]>("/api/v1/workflows"),
  getWorkflow: (id: string) => apiRequest<Workflow>(`/api/v1/workflows/${id}`),
  createWorkflow: (payload: Record<string, unknown>) =>
    apiRequest<{ id: string }>("/api/v1/workflows", { method: "POST", body: payload }),
  deleteWorkflow: (id: string) => apiRequest<void>(`/api/v1/workflows/${id}`, { method: "DELETE" }),
  listRunsByWorkflow: (id: string) => apiRequest<WorkflowRun[]>(`/api/v1/workflows/${id}/runs`),
  listWorkflowRuns: (params?: {
    limit?: number;
    cursor?: number;
    status?: string;
    workflow_id?: string;
    org_id?: string;
    team_id?: string;
    updated_after?: number;
    updated_before?: number;
  }) => apiRequest<WorkflowRunsResponse>("/api/v1/workflow-runs", { query: params }),
  getRun: (id: string) => apiRequest<WorkflowRun>(`/api/v1/workflow-runs/${id}`),
  getRunTimeline: (id: string, limit = 200) =>
    apiRequest<TimelineEvent[]>(`/api/v1/workflow-runs/${id}/timeline`, {
      query: { limit },
    }),
  getRunChat: (runId: string, limit = 100, cursor?: number) =>
    apiRequest<ChatResponse>(`/api/v1/workflow-runs/${runId}/chat`, {
      query: { limit, cursor },
    }),
  sendChatMessage: (runId: string, payload: { content: string; metadata?: Record<string, unknown> }) =>
    apiRequest<ChatMessage>(`/api/v1/workflow-runs/${runId}/chat`, {
      method: "POST",
      body: payload,
    }),
  startRun: (workflowId: string, payload: Record<string, unknown>, query?: Record<string, string>) =>
    apiRequest<{ run_id: string }>(`/api/v1/workflows/${workflowId}/runs`, {
      method: "POST",
      body: payload,
      query,
    }),
  cancelRun: (workflowId: string, runId: string) =>
    apiRequest<void>(`/api/v1/workflows/${workflowId}/runs/${runId}/cancel`, { method: "POST" }),
  rerunRun: (runId: string, options?: { fromStep?: string; dryRun?: boolean }) =>
    apiRequest<{ run_id: string }>(`/api/v1/workflow-runs/${runId}/rerun`, {
      method: "POST",
      body: options ? { from_step: options.fromStep, dry_run: options.dryRun } : undefined,
    }),
  approveStep: (workflowId: string, runId: string, stepId: string, approved: boolean) =>
    apiRequest<void>(`/api/v1/workflows/${workflowId}/runs/${runId}/steps/${stepId}/approve`, {
      method: "POST",
      body: { approved },
    }),
  listApprovals: (limit = 100, cursor?: number) =>
    apiRequest<ApprovalsResponse>("/api/v1/approvals", {
      query: { limit, cursor },
    }),
  approveJob: (jobId: string, payload?: { reason?: string; note?: string }) =>
    apiRequest<{ job_id: string }>(`/api/v1/approvals/${jobId}/approve`, { method: "POST", body: payload }),
  rejectJob: (jobId: string, payload?: { reason?: string; note?: string }) =>
    apiRequest<{ job_id: string }>(`/api/v1/approvals/${jobId}/reject`, { method: "POST", body: payload }),
  remediateJob: (jobId: string, remediationId?: string) =>
    apiRequest<{ job_id: string; trace_id?: string }>(`/api/v1/jobs/${jobId}/remediate`, {
      method: "POST",
      body: remediationId ? { remediation_id: remediationId } : undefined,
    }),
  listPacks: () => apiRequest<PackListResponse>("/api/v1/packs"),
  getPack: (id: string) => apiRequest<PackRecord>(`/api/v1/packs/${id}`),
  listMarketplacePacks: () => apiRequest<MarketplaceResponse>("/api/v1/marketplace/packs"),
  installPack: (bundle: File, options?: { force?: boolean; upgrade?: boolean; inactive?: boolean }) => {
    const form = new FormData();
    form.append("bundle", bundle);
    if (options?.force) {
      form.append("force", "true");
    }
    if (options?.upgrade) {
      form.append("upgrade", "true");
    }
    if (options?.inactive) {
      form.append("inactive", "true");
    }
    return apiRequest<PackRecord>("/api/v1/packs/install", { method: "POST", body: form });
  },
  installMarketplacePack: (payload: {
    catalog_id?: string;
    pack_id?: string;
    version?: string;
    url?: string;
    sha256?: string;
    force?: boolean;
    upgrade?: boolean;
    inactive?: boolean;
  }) => apiRequest<PackRecord>("/api/v1/marketplace/install", { method: "POST", body: payload }),
  uninstallPack: (id: string, purge?: boolean) =>
    apiRequest<PackRecord>(`/api/v1/packs/${id}/uninstall`, { method: "POST", body: { purge: Boolean(purge) } }),
  verifyPack: (id: string) => apiRequest<PackVerifyResponse>(`/api/v1/packs/${id}/verify`, { method: "POST" }),
  listDLQ: (limit = 100) => apiRequest<DLQEntry[]>("/api/v1/dlq", { query: { limit } }),
  listDLQPage: (limit = 100, cursor?: number) =>
    apiRequest<DLQResponse>("/api/v1/dlq/page", { query: { limit, cursor } }),
  retryDLQ: (jobId: string) => apiRequest<{ job_id: string }>(`/api/v1/dlq/${jobId}/retry`, { method: "POST" }),
  deleteDLQ: (jobId: string) => apiRequest<void>(`/api/v1/dlq/${jobId}`, { method: "DELETE" }),
  listWorkers: () => apiRequest<Heartbeat[]>("/api/v1/workers"),
  getStatus: () => apiRequest<Record<string, unknown>>("/api/v1/status"),
  listJobs: (params?: {
    limit?: number;
    cursor?: number;
    state?: string;
    topic?: string;
    tenant?: string;
    team?: string;
    trace_id?: string;
    updated_after?: number;
    updated_before?: number;
  }) => apiRequest<JobsResponse>("/api/v1/jobs", { query: params }),
  getJob: (id: string) => apiRequest<JobDetail>(`/api/v1/jobs/${id}`),
  listJobDecisions: (jobId: string, limit = 50) =>
    apiRequest<SafetyDecisionRecord[]>(`/api/v1/jobs/${jobId}/decisions`, { query: { limit } }),
  listSchemas: () => apiRequest<Record<string, unknown>[]>("/api/v1/schemas"),
  getConfig: (scope: string, scopeId: string) =>
    apiRequest<ConfigDocument>("/api/v1/config", { query: { scope, scope_id: scopeId } }),
  getEffectiveConfig: (params?: {
    org_id?: string;
    team_id?: string;
    workflow_id?: string;
    step_id?: string;
  }) => apiRequest<EffectiveConfigSnapshot>("/api/v1/config/effective", { query: params }),
  policySimulate: (payload: Record<string, unknown>) =>
    apiRequest<PolicyCheckResponse>("/api/v1/policy/simulate", { method: "POST", body: payload }),
  policyExplain: (payload: Record<string, unknown>) =>
    apiRequest<PolicyCheckResponse>("/api/v1/policy/explain", { method: "POST", body: payload }),
  policyEvaluate: (payload: Record<string, unknown>) =>
    apiRequest<PolicyCheckResponse>("/api/v1/policy/evaluate", { method: "POST", body: payload }),
  listPolicySnapshots: () => apiRequest<Record<string, unknown>>(`/api/v1/policy/snapshots`),
  listPolicyRules: () => apiRequest<PolicyRulesResponse>("/api/v1/policy/rules"),
  getPolicyBundles: () => apiRequest<PolicyBundlesResponse>("/api/v1/policy/bundles"),
  getPolicyBundle: (id: string) =>
    apiRequest<PolicyBundleDetail>(`/api/v1/policy/bundles/${encodeBundleId(id)}`),
  putPolicyBundle: (id: string, payload: { content: string; enabled?: boolean; author?: string; message?: string }) =>
    apiRequest<{ id: string; updated_at: string }>(`/api/v1/policy/bundles/${encodeBundleId(id)}`, {
      method: "PUT",
      body: payload,
    }),
  simulatePolicyBundle: (id: string, payload: PolicyBundleSimulateRequest) =>
    apiRequest<PolicyCheckResponse>(`/api/v1/policy/bundles/${encodeBundleId(id)}/simulate`, {
      method: "POST",
      body: payload,
    }),
  publishPolicyBundles: (payload: { bundle_ids?: string[]; author?: string; message?: string; note?: string }) =>
    apiRequest<PolicyPublishResponse>("/api/v1/policy/publish", { method: "POST", body: payload }),
  rollbackPolicyBundles: (payload: { snapshot_id: string; author?: string; message?: string; note?: string }) =>
    apiRequest<PolicyRollbackResponse>("/api/v1/policy/rollback", { method: "POST", body: payload }),
  listPolicyAudit: () => apiRequest<PolicyAuditResponse>("/api/v1/policy/audit"),
  listPolicyBundleSnapshots: () => apiRequest<PolicyBundleSnapshotsResponse>("/api/v1/policy/bundles/snapshots"),
  capturePolicyBundleSnapshot: (payload?: { note?: string }) =>
    apiRequest<PolicyBundleSnapshot>("/api/v1/policy/bundles/snapshots", { method: "POST", body: payload }),
  getPolicyBundleSnapshot: (id: string) =>
    apiRequest<PolicyBundleSnapshot>(`/api/v1/policy/bundles/snapshots/${id}`),
  getTrace: (id: string) => apiRequest<Record<string, unknown>[]>(`/api/v1/traces/${id}`),
  
  // Power User Tools
  getLock: (resource: string) => apiRequest<Lock>("/api/v1/locks", { query: { resource } }),
  acquireLock: (resource: string, owner: string, ttlMs: number, mode: "exclusive" | "shared" = "exclusive") =>
    apiRequest<Lock>("/api/v1/locks/acquire", {
      method: "POST",
      body: { resource, owner, ttl_ms: ttlMs, mode },
    }),
  releaseLock: (resource: string, owner: string) =>
    apiRequest<{ lock: Lock; released: boolean }>("/api/v1/locks/release", {
      method: "POST",
      body: { resource, owner },
    }),
  renewLock: (resource: string, owner: string, ttlMs: number) =>
    apiRequest<Lock>("/api/v1/locks/renew", {
      method: "POST",
      body: { resource, owner, ttl_ms: ttlMs },
    }),
  getMemory: (ptr?: string, key?: string) =>
    apiRequest<MemoryResult>("/api/v1/memory", { query: { ptr, key } }),
  getArtifact: (ptr: string) => apiRequest<ArtifactResponse>(`/api/v1/artifacts/${encodeURIComponent(ptr)}`),
  putArtifact: (content: string, contentType: string, retention: "short" | "standard" | "audit" = "standard", labels?: Record<string, string>) =>
    apiRequest<ArtifactResponse>("/api/v1/artifacts", {
      method: "POST",
      body: { content, content_type: contentType, retention, labels },
    }),
};

function encodeBundleId(id: string): string {
  return encodeURIComponent(id.split("/").join("~"));
}

export function wsUrl(path: string): string {
  const base = resolveBaseUrl();
  const protocol = base.startsWith("https://") ? "wss" : "ws";
  const url = new URL(path, base.replace(/^https?/, protocol));
  return url.toString();
}

export function wsProtocols(apiKey?: string): string[] {
  const token = encodeWsApiKey(apiKey);
  if (!token) {
    return [];
  }
  return ["cordum-api-key", token];
}

function encodeWsApiKey(apiKey?: string): string {
  if (!apiKey) {
    return "";
  }
  try {
    const base64 = btoa(apiKey);
    return base64.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
  } catch {
    return "";
  }
}
