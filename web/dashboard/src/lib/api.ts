import { useSettingsStore } from "../state/settingsStore";
import { useAuthStore } from "../state/authStore";

export type HealthResponse = string;

export type WorkerHeartbeat = {
  worker_id: string;
  region?: string;
  type?: string;
  cpu_load?: number;
  gpu_utilization?: number;
  active_jobs?: number;
  capabilities?: string[];
  pool?: string;
  max_parallel_jobs?: number;
  labels?: Record<string, string>;
};

export type JobState =
  | "PENDING"
  | "SCHEDULED"
  | "DISPATCHED"
  | "RUNNING"
  | "SUCCEEDED"
  | "FAILED"
  | "CANCELLED"
  | "TIMEOUT"
  | "DENIED";

export type JobRecord = {
  id: string;
  trace_id?: string;
  updated_at: number; // unix seconds
  state: JobState;
  topic?: string;
  tenant?: string;
  team?: string;
  principal?: string;
  safety_decision?: string;
  safety_reason?: string;
  deadline_unix?: number; // unix seconds
};

export type JobsResponse = {
  items: JobRecord[];
  next_cursor: number | null; // unix seconds cursor (same unit as JobRecord.updated_at)
};

export type JobDetailResponse = {
  id: string;
  state: JobState;
  trace_id?: string;
  context_ptr?: string;
  context?: unknown;
  topic?: string;
  tenant?: string;
  safety_decision?: string;
  safety_reason?: string;
  error_message?: string;
  error_status?: string;
  result_ptr?: string;
  result?: unknown;
};

export type MemoryPointerKind = "context" | "result" | "memory";

export type MemoryPointerResponse = {
  pointer: string;
  key: string;
  kind: MemoryPointerKind;
  size_bytes: number;
  base64: string;
  text?: string;
  json?: unknown;
};

export type SubmitJobRequest = {
  prompt: string;
  topic: string;
  adapter_id?: string;
  priority?: string;
  labels?: Record<string, string>;
  // Optional advanced fields supported by the gateway; omit to keep default behavior.
  memory_id?: string;
  context_mode?: string;
};

export type SubmitJobResponse = {
  job_id: string;
  trace_id: string;
};

export type CancelJobResponse = {
  id: string;
  state: JobState | string;
  reason?: string;
};

export type WorkflowStepType =
  | "llm"
  | "worker"
  | "http"
  | "container"
  | "script"
  | "approval"
  | "input"
  | "condition"
  | "switch"
  | "parallel"
  | "loop"
  | "delay"
  | "notify"
  | "transform"
  | "storage"
  | "subworkflow";

export type WorkflowStepDefinition = {
  id: string;
  name: string;
  type: WorkflowStepType;
  worker_id?: string;
  topic?: string;
  depends_on?: string[];
  condition?: string;
  for_each?: string;
  max_parallel?: number;
  input?: Record<string, unknown>;
  output_path?: string;
  on_error?: string;
  retry?: {
    max_retries?: number;
    initial_backoff_sec?: number;
    max_backoff_sec?: number;
    multiplier?: number;
  };
  timeout_sec?: number;
  route_labels?: Record<string, string>;
};

export type WorkflowDefinition = {
  id: string;
  org_id?: string;
  team_id?: string;
  name: string;
  description?: string;
  version?: string;
  timeout_sec?: number;
  steps: Record<string, WorkflowStepDefinition>;
  config?: Record<string, unknown>;
  input_schema?: Record<string, unknown>;
  parameters?: Record<string, unknown>[];
  created_by?: string;
  created_at?: string;
  updated_at?: string;
};

export type WorkflowRunStatus =
  | "pending"
  | "running"
  | "waiting"
  | "succeeded"
  | "failed"
  | "cancelled"
  | "timed_out";

export type WorkflowStepStatus =
  | "pending"
  | "running"
  | "waiting"
  | "succeeded"
  | "failed"
  | "cancelled"
  | "timed_out";

export type WorkflowStepRun = {
  step_id: string;
  status: WorkflowStepStatus;
  started_at?: string | null;
  completed_at?: string | null;
  next_attempt_at?: string | null;
  attempts?: number;
  input?: Record<string, unknown>;
  output?: unknown;
  error?: Record<string, unknown>;
  job_id?: string;
  item?: unknown;
  children?: Record<string, WorkflowStepRun>;
};

export type WorkflowRun = {
  id: string;
  workflow_id: string;
  org_id?: string;
  team_id?: string;
  input?: Record<string, unknown>;
  context?: Record<string, unknown>;
  status: WorkflowRunStatus;
  started_at?: string | null;
  completed_at?: string | null;
  output?: Record<string, unknown>;
  error?: Record<string, unknown>;
  steps?: Record<string, WorkflowStepRun>;
  total_cost?: number;
  triggered_by?: string;
  created_at?: string;
  updated_at?: string;
  labels?: Record<string, string>;
  metadata?: Record<string, string>;
};

function getApiBase(): string {
  return useSettingsStore.getState().apiBase;
}

function getApiKey(): string {
  return useSettingsStore.getState().apiKey;
}

async function apiFetch(path: string, init?: RequestInit): Promise<Response> {
  const apiBase = getApiBase();
  const url = apiBase + path;
  const headers = new Headers(init?.headers);
  const apiKey = getApiKey();
  if (apiKey) {
    headers.set("X-API-Key", apiKey);
  }

  const isProtected = path.startsWith("/api/");
  const authStatus = useAuthStore.getState().status;
  if (isProtected && (authStatus === "missing_api_key" || authStatus === "invalid_api_key")) {
    throw new Error(
      authStatus === "missing_api_key"
        ? "unauthorized (401) — missing API key; set it in Settings"
        : "unauthorized (401) — invalid API key; update it in Settings",
    );
  }

  const res = await fetch(url, { ...init, headers });
  if (isProtected) {
    if (res.status === 401) {
      useAuthStore.getState().markUnauthorized({ hasKey: Boolean(apiKey) });
    } else if (res.ok) {
      useAuthStore.getState().markAuthorized();
    }
  }

  if (res.status === 401) {
    throw new Error(Boolean(apiKey) ? "unauthorized (401) — invalid API key" : "unauthorized (401) — missing API key");
  }
  return res;
}

export async function fetchHealth(): Promise<HealthResponse> {
  const res = await apiFetch("/health");
  if (!res.ok) {
    throw new Error(`health check failed: ${res.status}`);
  }
  return res.text();
}

export async function fetchWorkers(): Promise<WorkerHeartbeat[]> {
  const res = await apiFetch("/api/v1/workers");
  if (!res.ok) {
    throw new Error(`workers fetch failed: ${res.status}`);
  }
  return res.json();
}

export async function fetchJobs(params?: {
  state?: string;
  topic?: string;
  tenant?: string;
  team?: string;
  limit?: number;
  cursor?: number;
}): Promise<JobsResponse> {
  const qs = new URLSearchParams();
  if (params?.state) qs.set("state", params.state);
  if (params?.topic) qs.set("topic", params.topic);
  if (params?.tenant) qs.set("tenant", params.tenant);
  if (params?.team) qs.set("team", params.team);
  if (params?.limit) qs.set("limit", String(params.limit));
  if (params?.cursor) qs.set("cursor", String(params.cursor));

  const res = await apiFetch(`/api/v1/jobs${qs.size ? `?${qs.toString()}` : ""}`);
  if (!res.ok) {
    throw new Error(`jobs fetch failed: ${res.status}`);
  }
  return res.json();
}

export async function fetchJob(jobID: string): Promise<JobDetailResponse> {
  const res = await apiFetch(`/api/v1/jobs/${encodeURIComponent(jobID)}`);
  if (!res.ok) {
    throw new Error(`job fetch failed: ${res.status}`);
  }
  return res.json();
}

export async function cancelJob(jobID: string): Promise<CancelJobResponse> {
  const res = await apiFetch(`/api/v1/jobs/${encodeURIComponent(jobID)}/cancel`, { method: "POST" });
  if (!res.ok) {
    const txt = await res.text().catch(() => "");
    throw new Error(`cancel failed: ${res.status} ${txt}`.trim());
  }
  return res.json();
}

export async function fetchMemoryPointer(pointer: string): Promise<MemoryPointerResponse> {
  const res = await apiFetch(`/api/v1/memory?ptr=${encodeURIComponent(pointer)}`);
  if (!res.ok) {
    const txt = await res.text().catch(() => "");
    throw new Error(`memory fetch failed: ${res.status} ${txt}`.trim());
  }
  return res.json();
}

export async function submitJob(req: SubmitJobRequest): Promise<SubmitJobResponse> {
  const res = await apiFetch("/api/v1/jobs", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
  if (!res.ok) {
    const txt = await res.text().catch(() => "");
    throw new Error(`submit failed: ${res.status} ${txt}`);
  }
  return res.json();
}

export async function fetchTrace(traceID: string): Promise<JobRecord[]> {
  const res = await apiFetch(`/api/v1/traces/${encodeURIComponent(traceID)}`);
  if (!res.ok) {
    throw new Error(`trace fetch failed: ${res.status}`);
  }
  return res.json();
}

export async function fetchWorkflows(params?: { org_id?: string }): Promise<WorkflowDefinition[]> {
  const qs = new URLSearchParams();
  if (params?.org_id) qs.set("org_id", params.org_id);
  const res = await apiFetch(`/api/v1/workflows${qs.size ? `?${qs.toString()}` : ""}`);
  if (!res.ok) {
    throw new Error(`workflows fetch failed: ${res.status}`);
  }
  return res.json();
}

export async function fetchWorkflow(workflowID: string): Promise<WorkflowDefinition> {
  const res = await apiFetch(`/api/v1/workflows/${encodeURIComponent(workflowID)}`);
  if (!res.ok) {
    throw new Error(`workflow fetch failed: ${res.status}`);
  }
  return res.json();
}

export async function upsertWorkflow(def: Partial<WorkflowDefinition> & { id?: string }): Promise<{ id: string }> {
  const res = await apiFetch("/api/v1/workflows", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(def),
  });
  if (!res.ok) {
    const txt = await res.text().catch(() => "");
    throw new Error(`workflow upsert failed: ${res.status} ${txt}`);
  }
  return res.json();
}

export async function startWorkflowRun(
  workflowID: string,
  input: Record<string, unknown>,
  params?: { org_id?: string; team_id?: string },
): Promise<{ run_id: string }> {
  const qs = new URLSearchParams();
  if (params?.org_id) qs.set("org_id", params.org_id);
  if (params?.team_id) qs.set("team_id", params.team_id);
  const res = await apiFetch(`/api/v1/workflows/${encodeURIComponent(workflowID)}/runs${qs.size ? `?${qs.toString()}` : ""}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input ?? {}),
  });
  if (!res.ok) {
    const txt = await res.text().catch(() => "");
    throw new Error(`start run failed: ${res.status} ${txt}`);
  }
  return res.json();
}

export async function fetchWorkflowRuns(workflowID: string): Promise<WorkflowRun[]> {
  const res = await apiFetch(`/api/v1/workflows/${encodeURIComponent(workflowID)}/runs`);
  if (!res.ok) {
    throw new Error(`runs fetch failed: ${res.status}`);
  }
  return res.json();
}

export async function fetchWorkflowRun(runID: string): Promise<WorkflowRun> {
  const res = await apiFetch(`/api/v1/workflow-runs/${encodeURIComponent(runID)}`);
  if (!res.ok) {
    throw new Error(`run fetch failed: ${res.status}`);
  }
  return res.json();
}

export async function approveWorkflowStep(workflowID: string, runID: string, stepID: string, approved: boolean): Promise<void> {
  const res = await apiFetch(
    `/api/v1/workflows/${encodeURIComponent(workflowID)}/runs/${encodeURIComponent(runID)}/steps/${encodeURIComponent(stepID)}/approve`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ approved }),
    },
  );
  if (!res.ok) {
    const txt = await res.text().catch(() => "");
    throw new Error(`approve failed: ${res.status} ${txt}`);
  }
}

export async function cancelWorkflowRun(workflowID: string, runID: string): Promise<void> {
  const res = await apiFetch(`/api/v1/workflows/${encodeURIComponent(workflowID)}/runs/${encodeURIComponent(runID)}/cancel`, {
    method: "POST",
  });
  if (!res.ok) {
    const txt = await res.text().catch(() => "");
    throw new Error(`cancel failed: ${res.status} ${txt}`);
  }
}
