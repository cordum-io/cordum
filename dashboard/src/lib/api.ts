import { get, post, put, del } from "../api/client";
import type { User, Approval, DLQEntry } from "../api/types";
import { mapDLQEntry, mapApprovalItem, type BackendDLQEntry, type BackendApprovalItem } from "../api/transform";

interface SessionResponse {
  user: User;
}

interface ApprovalsResponse {
  items: Approval[];
  next_cursor?: number | null;
}

interface DLQResponse {
  items: DLQEntry[];
  next_cursor?: number | string | null;
}

export function wsUrl(path?: string, params?: Record<string, string | undefined>): string {
  const base = window.location.origin.replace(/^http/, "ws");
  const p = path || "/api/v1/stream";
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

  listPolicySnapshots(): Promise<any[]> {
    return get<any[]>("/policy/snapshots");
  },

  getPolicyBundles(): Promise<any[]> {
    return get<any[]>("/policy/bundles");
  },

  listPolicyRules(): Promise<any[]> {
    return get<any[]>("/policy/rules");
  },

  listPolicyBundleSnapshots(): Promise<any[]> {
    return get<any[]>("/policy/bundles/snapshots");
  },

  listPolicyAudit(): Promise<any[]> {
    return get<any[]>("/policy/audit");
  },

  getPolicyBundleSnapshot(id: string): Promise<any> {
    return get<any>(`/policy/bundles/snapshots/${id}`);
  },

  getPolicyBundle(id: string): Promise<any> {
    return get<any>(`/policy/bundles/${id}`);
  },

  putPolicyBundle(id: string, body: any): Promise<any> {
    return put<any>(`/policy/bundles/${id}`, body);
  },

  publishPolicyBundles(body: any): Promise<any> {
    return post<any>("/policy/bundles/publish", body);
  },

  rollbackPolicyBundles(body: any): Promise<any> {
    return post<any>("/policy/bundles/rollback", body);
  },

  capturePolicyBundleSnapshot(body: any): Promise<any> {
    return post<any>("/policy/bundles/snapshots", body);
  },

  simulatePolicyBundle(id: string, body: any): Promise<any> {
    return post<any>(`/policy/bundles/${id}/simulate`, body);
  },

  policySimulate(body: any): Promise<any> {
    return post<any>("/policy/simulate", body);
  },

  policyExplain(body: any): Promise<any> {
    return post<any>("/policy/explain", body);
  },

  policyEvaluate(body: any): Promise<any> {
    return post<any>("/policy/evaluate", body);
  },

  // ---------------------------------------------------------------------------
  // Job / Approval methods
  // ---------------------------------------------------------------------------

  approveJob(id: string, body?: any): Promise<void> {
    return post<void>(`/approvals/${id}/approve`, body);
  },

  rejectJob(id: string, body?: any): Promise<void> {
    return post<void>(`/approvals/${id}/reject`, body);
  },

  listJobs(params?: any): Promise<any> {
    const qs = params ? "?" + new URLSearchParams(params).toString() : "";
    return get<any>(`/jobs${qs}`);
  },

  getJob(id: string): Promise<any> {
    return get<any>(`/jobs/${id}`);
  },

  // ---------------------------------------------------------------------------
  // Workflow / Run methods
  // ---------------------------------------------------------------------------

  listWorkflowRuns(params?: any): Promise<any> {
    const qs = params ? "?" + new URLSearchParams(params).toString() : "";
    return get<any>(`/workflows/runs${qs}`);
  },

  getRun(id: string): Promise<any> {
    return get<any>(`/workflows/runs/${id}`);
  },

  cancelRun(workflowIdOrRunId: string, runId?: string): Promise<void> {
    const id = runId ?? workflowIdOrRunId;
    return post<void>(`/workflows/runs/${id}/cancel`);
  },

  rerunRun(id: string, body?: any): Promise<any> {
    return post<any>(`/workflows/runs/${id}/rerun`, body);
  },

  listWorkflows(params?: any): Promise<any> {
    const qs = params ? "?" + new URLSearchParams(params).toString() : "";
    return get<any>(`/workflows${qs}`);
  },

  // ---------------------------------------------------------------------------
  // Worker / Pool methods
  // ---------------------------------------------------------------------------

  listWorkers(params?: any): Promise<any> {
    const qs = params ? "?" + new URLSearchParams(params).toString() : "";
    return get<any>(`/workers${qs}`);
  },

  getWorker(id: string): Promise<any> {
    return get<any>(`/workers/${encodeURIComponent(id)}`);
  },

  getWorkerJobs(workerId: string, limit = 20): Promise<any> {
    return get<any>(`/workers/${encodeURIComponent(workerId)}/jobs?limit=${limit}`);
  },

  listPools(): Promise<any> {
    return get<any>("/pools");
  },

  getPool(name: string): Promise<any> {
    return get<any>(`/pools/${encodeURIComponent(name)}`);
  },

  getStatus(): Promise<any> {
    return get<any>("/status");
  },

  // ---------------------------------------------------------------------------
  // Config methods
  // ---------------------------------------------------------------------------

  getConfig(scope?: string, scopeId?: string): Promise<any> {
    const parts = [scope, scopeId].filter(Boolean);
    const path = parts.length > 0 ? `/config/${parts.join("/")}` : "/config";
    return get<any>(path);
  },

  setConfig(scope: string, scopeId: string, data?: any, meta?: any): Promise<any> {
    return put<any>(`/config/${scope}/${scopeId}`, { data, meta });
  },

  // ---------------------------------------------------------------------------
  // Schema methods
  // ---------------------------------------------------------------------------

  listSchemas(params?: any): Promise<any> {
    const qs = params ? "?" + new URLSearchParams(params).toString() : "";
    return get<any>(`/schemas${qs}`);
  },

  // ---------------------------------------------------------------------------
  // Pack methods
  // ---------------------------------------------------------------------------

  listPacks(params?: any): Promise<any> {
    const qs = params ? "?" + new URLSearchParams(params).toString() : "";
    return get<any>(`/packs${qs}`);
  },

  // ---------------------------------------------------------------------------
  // Artifact / Lock / Memory methods
  // ---------------------------------------------------------------------------

  getArtifact(id: string): Promise<any> {
    return get<any>(`/artifacts/${id}`);
  },

  putArtifact(id: string, body: any): Promise<any> {
    return put<any>(`/artifacts/${id}`, body);
  },

  getLock(id: string): Promise<any> {
    return get<any>(`/locks/${id}`);
  },

  acquireLock(resource: string, owner?: string, ttlMs?: number): Promise<any> {
    return post<any>("/locks", { resource, owner, ttl_ms: ttlMs });
  },

  releaseLock(resource: string, owner?: string): Promise<void> {
    return del<void>(`/locks/${encodeURIComponent(resource)}${owner ? `?owner=${encodeURIComponent(owner)}` : ""}`);
  },

  renewLock(resource: string, owner?: string, ttlMs?: number): Promise<any> {
    return post<any>(`/locks/${encodeURIComponent(resource)}/renew`, { owner, ttl_ms: ttlMs });
  },

  getMemory(ptr?: string, key?: string): Promise<any> {
    if (ptr) return get<any>(`/memory/${encodeURIComponent(ptr)}`);
    if (key) return get<any>(`/memory?key=${encodeURIComponent(key)}`);
    return get<any>("/memory");
  },

  // ---------------------------------------------------------------------------
  // Trace methods
  // ---------------------------------------------------------------------------

  getTrace(id: string): Promise<any> {
    return get<any>(`/traces/${id}`);
  },

  // ---------------------------------------------------------------------------
  // Chat methods
  // ---------------------------------------------------------------------------

  getRunChat(runId: string): Promise<any> {
    return get<any>(`/workflows/runs/${runId}/chat`);
  },

  sendChatMessage(runId: string, body: any): Promise<any> {
    return post<any>(`/workflows/runs/${runId}/chat`, body);
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
