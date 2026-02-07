import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { del, get, post } from "../api/client";
import type { RunStatus, Workflow, WorkflowRun } from "../api/types";
import {
  mapWorkflow,
  mapWorkflowRun,
  type BackendWorkflow,
  type BackendWorkflowRun,
} from "../api/transform";

export interface WorkflowListParams {
  orgId?: string;
  limit?: number;
  cursor?: number;
}

export interface WorkflowRunsParams {
  limit?: number;
}

export interface AllRunsParams {
  limit?: number;
  cursor?: number;
  status?: RunStatus;
  workflowId?: string;
  orgId?: string;
  teamId?: string;
  updatedAfter?: number;
  updatedBefore?: number;
}

export interface RunTimelineParams {
  limit?: number;
}

export interface StartRunInput {
  workflowId: string;
  input?: Record<string, unknown>;
  orgId?: string;
  teamId?: string;
  dryRun?: boolean;
}

export interface RerunRunInput {
  runId: string;
  fromStep?: string;
  dryRun?: boolean;
}

export interface CancelRunInput {
  workflowId: string;
  runId: string;
}

export interface WorkflowRunListResponse {
  items: WorkflowRun[];
  next_cursor?: number | null;
}

export interface RunTimelineEvent {
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
}

interface WorkflowIdResponse {
  id: string;
}

interface RunIdResponse {
  run_id: string;
}

function buildQuery(params: Record<string, unknown>): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === "") {
      continue;
    }
    if (Array.isArray(value)) {
      for (const entry of value) {
        if (entry === undefined || entry === null || entry === "") {
          continue;
        }
        search.append(key, String(entry));
      }
      continue;
    }
    search.set(key, String(value));
  }
  const query = search.toString();
  return query ? `?${query}` : "";
}

function toStringArray(value: unknown): string[] {
  if (Array.isArray(value)) {
    return value.map((v) => String(v).trim()).filter(Boolean);
  }
  if (typeof value === "string") {
    return value
      .split(",")
      .map((v) => v.trim())
      .filter(Boolean);
  }
  return [];
}

function parseDurationSeconds(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value > 0 ? Math.round(value) : undefined;
  }
  if (typeof value !== "string") return undefined;
  const trimmed = value.trim();
  if (!trimmed) return undefined;
  const match = trimmed.match(/^(\d+(?:\.\d+)?)\s*(ms|s|m|h|d)?$/i);
  if (!match) return undefined;
  const amount = Number.parseFloat(match[1]);
  if (!Number.isFinite(amount)) return undefined;
  const unit = (match[2] || "s").toLowerCase();
  let seconds = amount;
  switch (unit) {
    case "ms":
      seconds = amount / 1000;
      break;
    case "s":
      seconds = amount;
      break;
    case "m":
      seconds = amount * 60;
      break;
    case "h":
      seconds = amount * 3600;
      break;
    case "d":
      seconds = amount * 86400;
      break;
    default:
      seconds = amount;
  }
  if (!Number.isFinite(seconds) || seconds <= 0) return undefined;
  return Math.round(seconds);
}

function parseDateToISO(value: string): string | undefined {
  const ms = Date.parse(value);
  if (Number.isNaN(ms)) return undefined;
  const iso = new Date(ms).toISOString();
  return iso;
}

function buildStepPayload(step: Workflow["steps"][number]): Record<string, unknown> {
  const config = (step.config ?? {}) as Record<string, unknown>;
  const payload: Record<string, unknown> = {
    id: step.id,
    name: step.name,
    type:
      typeof config.backendType === "string" && config.backendType.trim()
        ? config.backendType.trim()
        : step.type || "job",
  };

  if (Array.isArray(step.dependsOn) && step.dependsOn.length > 0) {
    payload.depends_on = step.dependsOn;
  }

  if (typeof config.topic === "string" && config.topic.trim()) {
    payload.topic = config.topic.trim();
  }
  if (typeof config.workerId === "string" && config.workerId.trim()) {
    payload.worker_id = config.workerId.trim();
  }
  if (typeof config.expression === "string" && config.expression.trim()) {
    payload.condition = config.expression.trim();
  }
  if (typeof config.forEach === "string" && config.forEach.trim()) {
    payload.for_each = config.forEach.trim();
  }
  if (typeof config.parallelism === "number" && config.parallelism > 0) {
    payload.max_parallel = Math.floor(config.parallelism);
  }

  const timeoutSec = parseDurationSeconds(config.timeout);
  if (timeoutSec !== undefined) {
    payload.timeout_sec = timeoutSec;
  }

  const delaySec = parseDurationSeconds(config.duration);
  if (delaySec !== undefined) {
    payload.delay_sec = delaySec;
  } else if (typeof config.duration === "string") {
    const iso = parseDateToISO(config.duration);
    if (iso) {
      payload.delay_until = iso;
    }
  }

  if (typeof config.retryMax === "number" && config.retryMax > 0) {
    payload.retry = { max_retries: Math.floor(config.retryMax) };
  }

  if (config.inputSchema && typeof config.inputSchema === "object") {
    payload.input_schema = config.inputSchema;
  }
  if (typeof config.inputSchemaId === "string" && config.inputSchemaId.trim()) {
    payload.input_schema_id = config.inputSchemaId.trim();
  }
  if (config.outputSchema && typeof config.outputSchema === "object") {
    payload.output_schema = config.outputSchema;
  }
  if (typeof config.outputSchemaId === "string" && config.outputSchemaId.trim()) {
    payload.output_schema_id = config.outputSchemaId.trim();
  }
  if (typeof config.outputPath === "string" && config.outputPath.trim()) {
    payload.output_path = config.outputPath.trim();
  }

  if (config.routeLabels && typeof config.routeLabels === "object") {
    payload.route_labels = config.routeLabels as Record<string, string>;
  }

  const input: Record<string, unknown> = {};
  if (config.input && typeof config.input === "object") {
    Object.assign(input, config.input as Record<string, unknown>);
  }
  if (typeof config.messageTemplate === "string" && config.messageTemplate.trim()) {
    input.message = config.messageTemplate.trim();
  }
  if (typeof config.channel === "string" && config.channel.trim()) {
    input.component = config.channel.trim();
  }
  if (Object.keys(input).length > 0) {
    payload.input = input;
  }

  let meta: Record<string, unknown> = {};
  if (config.meta && typeof config.meta === "object") {
    meta = { ...(config.meta as Record<string, unknown>) };
  }
  const caps = toStringArray(config.capabilities ?? config.capability);
  const requires = toStringArray(config.requires);
  const riskTags = toStringArray(config.riskTags ?? config.risk_tags);
  if (caps.length > 0) {
    meta.capability = caps[0];
    const combined = [...caps.slice(1), ...requires].filter(Boolean);
    if (combined.length > 0) {
      meta.requires = combined;
    }
  } else if (requires.length > 0) {
    meta.requires = requires;
  }
  if (riskTags.length > 0) {
    meta.risk_tags = riskTags;
  }
  if (config.labels && typeof config.labels === "object") {
    meta.labels = config.labels as Record<string, string>;
  }
  if (typeof config.packId === "string") meta.pack_id = config.packId;
  if (typeof config.actorId === "string") meta.actor_id = config.actorId;
  if (typeof config.actorType === "string") meta.actor_type = config.actorType;
  if (Object.keys(meta).length > 0) {
    payload.meta = meta;
  }

  return payload;
}

function toWorkflowUpsertPayload(input: Partial<Workflow> & { id?: string }): Record<string, unknown> {
  const meta = (input.metadata ?? {}) as Record<string, unknown>;
  const payload: Record<string, unknown> = {};

  if (input.id) payload.id = input.id;
  if (input.name) payload.name = input.name;

  const description = (input.description ?? meta.description) as string | undefined;
  if (description) payload.description = description;

  const orgId = (input.orgId ?? meta.orgId ?? meta.org_id) as string | undefined;
  if (orgId) payload.org_id = orgId;

  const teamId = (input.teamId ?? meta.teamId ?? meta.team_id) as string | undefined;
  if (teamId) payload.team_id = teamId;

  const version = (input.version ?? meta.version) as string | undefined;
  if (version) payload.version = version;

  const timeout =
    typeof input.timeout === "number"
      ? input.timeout
      : typeof meta.timeout === "number"
        ? meta.timeout
        : undefined;
  if (typeof timeout === "number" && timeout > 0) {
    payload.timeout_sec = Math.floor(timeout);
  }

  if (meta.inputSchema) payload.input_schema = meta.inputSchema;
  if (meta.parameters) payload.parameters = meta.parameters;
  if (meta.config) payload.config = meta.config;

  if (Array.isArray(input.steps)) {
    const steps: Record<string, unknown> = {};
    for (const step of input.steps) {
      if (!step.id) continue;
      steps[step.id] = buildStepPayload(step);
    }
    payload.steps = steps;
  }

  return payload;
}

export function useWorkflows(params?: WorkflowListParams) {
  return useQuery<Workflow[]>({
    queryKey: ["workflows", params ?? {}],
    queryFn: async () => {
      const res = await get<BackendWorkflow[]>(
        `/workflows${buildQuery({
          org_id: params?.orgId,
        })}`,
      );
      return (res ?? []).map(mapWorkflow);
    },
  });
}

export function useWorkflow(id: string | null | undefined) {
  return useQuery<Workflow>({
    queryKey: ["workflow", id],
    queryFn: () => {
      if (!id) {
        throw new Error("workflow id is required");
      }
      return get<BackendWorkflow>(`/workflows/${id}`).then(mapWorkflow);
    },
    enabled: !!id,
  });
}

export function useCreateWorkflow() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (payload: Partial<Workflow> & { id?: string }) =>
      post<WorkflowIdResponse>("/workflows", toWorkflowUpsertPayload(payload ?? {})),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ["workflows"] });
      if (data?.id) {
        queryClient.invalidateQueries({ queryKey: ["workflow", data.id] });
      }
    },
  });
}

export function useUpdateWorkflow() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (payload: Partial<Workflow> & { id: string }) => {
      if (!payload?.id) {
        throw new Error("workflow id is required");
      }
      return post<WorkflowIdResponse>("/workflows", toWorkflowUpsertPayload(payload));
    },
    onSuccess: (data, variables) => {
      queryClient.invalidateQueries({ queryKey: ["workflows"] });
      const workflowId = data?.id || variables?.id;
      if (workflowId) {
        queryClient.invalidateQueries({ queryKey: ["workflow", workflowId] });
      }
    },
  });
}

export function useDeleteWorkflow() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (workflowId: string) => {
      if (!workflowId) {
        throw new Error("workflow id is required");
      }
      return del<void>(`/workflows/${workflowId}`);
    },
    onSuccess: (_data, workflowId) => {
      queryClient.invalidateQueries({ queryKey: ["workflows"] });
      if (workflowId) {
        queryClient.invalidateQueries({ queryKey: ["workflow", workflowId] });
      }
    },
  });
}

export function useRuns(workflowId: string | null | undefined, params?: WorkflowRunsParams) {
  return useQuery<WorkflowRun[]>({
    queryKey: ["workflow-runs", workflowId, params ?? {}],
    queryFn: () => {
      if (!workflowId) {
        throw new Error("workflow id is required");
      }
      return get<BackendWorkflowRun[]>(
        `/workflows/${workflowId}/runs${buildQuery({
          limit: params?.limit,
        })}`,
      ).then((runs) => (runs ?? []).map(mapWorkflowRun));
    },
    enabled: !!workflowId,
  });
}

export function useAllRuns(filters?: AllRunsParams) {
  return useQuery<WorkflowRunListResponse>({
    queryKey: ["workflow-runs", "all", filters ?? {}],
    queryFn: async () => {
      const res = await get<{ items: BackendWorkflowRun[]; next_cursor?: number | null }>(
        `/workflow-runs${buildQuery({
          limit: filters?.limit,
          cursor: filters?.cursor,
          status: filters?.status,
          workflow_id: filters?.workflowId,
          org_id: filters?.orgId,
          team_id: filters?.teamId,
          updated_after: filters?.updatedAfter,
          updated_before: filters?.updatedBefore,
        })}`,
      );
      return {
        items: (res.items ?? []).map(mapWorkflowRun),
        next_cursor: res.next_cursor ?? null,
      };
    },
  });
}

export function useRun(runId: string | null | undefined) {
  return useQuery<WorkflowRun>({
    queryKey: ["workflow-run", runId],
    queryFn: () => {
      if (!runId) {
        throw new Error("run id is required");
      }
      return get<BackendWorkflowRun>(`/workflow-runs/${runId}`).then(mapWorkflowRun);
    },
    enabled: !!runId,
  });
}

export function useRunTimeline(runId: string | null | undefined, params?: RunTimelineParams) {
  return useQuery<RunTimelineEvent[]>({
    queryKey: ["workflow-run", runId, "timeline", params?.limit ?? "default"],
    queryFn: () => {
      if (!runId) {
        throw new Error("run id is required");
      }
      return get<Array<Record<string, unknown>>>(
        `/workflow-runs/${runId}/timeline${buildQuery({
          limit: params?.limit,
        })}`,
      ).then((events) =>
        (events ?? []).map((e) => ({
          time: String(e.time ?? e.timestamp ?? ""),
          type: String(e.type ?? ""),
          run_id: e.run_id as string | undefined,
          workflow_id: e.workflow_id as string | undefined,
          step_id: e.step_id as string | undefined,
          job_id: e.job_id as string | undefined,
          status: e.status as string | undefined,
          result_ptr: e.result_ptr as string | undefined,
          message: e.message as string | undefined,
          data: (e.data as Record<string, unknown>) ?? undefined,
        })),
      );
    },
    enabled: !!runId,
  });
}

export function useStartRun() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: StartRunInput) => {
      if (!input?.workflowId) {
        throw new Error("workflow id is required");
      }
      return post<RunIdResponse>(
        `/workflows/${input.workflowId}/runs${buildQuery({
          org_id: input.orgId,
          team_id: input.teamId,
          dry_run: input.dryRun ? "true" : undefined,
        })}`,
        input.input ?? {},
      );
    },
    onSuccess: (data, variables) => {
      queryClient.invalidateQueries({ queryKey: ["workflow-runs"] });
      if (variables?.workflowId) {
        queryClient.invalidateQueries({ queryKey: ["workflow-runs", variables.workflowId] });
      }
      if (data?.run_id) {
        queryClient.invalidateQueries({ queryKey: ["workflow-run", data.run_id] });
      }
    },
  });
}

export function useRerunRun() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: RerunRunInput) => {
      if (!input?.runId) {
        throw new Error("run id is required");
      }
      const payload = {
        from_step: input.fromStep?.trim() || undefined,
        dry_run: input.dryRun ?? undefined,
      };
      return post<RunIdResponse>(`/workflow-runs/${input.runId}/rerun`, payload);
    },
    onSuccess: (data, variables) => {
      queryClient.invalidateQueries({ queryKey: ["workflow-runs"] });
      if (variables?.runId) {
        queryClient.invalidateQueries({ queryKey: ["workflow-run", variables.runId] });
      }
      if (data?.run_id) {
        queryClient.invalidateQueries({ queryKey: ["workflow-run", data.run_id] });
      }
    },
  });
}

// ---------------------------------------------------------------------------
// Active runs with attention-first sorting
// ---------------------------------------------------------------------------

const ACTIVE_STATUSES = new Set<string>([
  "running",
  "pending",
  "waiting",
  "in_progress",
  "queued",
  "blocked",
]);

function getAttentionPriority(run: WorkflowRun): number {
  const steps = run.steps ?? [];
  // Priority 0: Any step waiting for approval
  if (steps.some((s) => s.status === "waiting" || s.status === "blocked")) return 0;
  // Priority 1: Any step failed
  if (steps.some((s) => s.status === "failed" || s.status === "timed_out")) return 1;
  // Priority 2: Currently running
  if (run.status === "running" || run.status === "in_progress") return 2;
  // Priority 3: Pending/queued
  return 3;
}

function sortByAttention(runs: WorkflowRun[]): WorkflowRun[] {
  return [...runs]
    .filter((r) => ACTIVE_STATUSES.has(r.status))
    .sort((a, b) => {
      const pa = getAttentionPriority(a);
      const pb = getAttentionPriority(b);
      if (pa !== pb) return pa - pb;
      // Within same priority, oldest first (longest running = most likely stuck)
      const ta = new Date(a.startedAt || a.createdAt || "").getTime() || 0;
      const tb = new Date(b.startedAt || b.createdAt || "").getTime() || 0;
      return ta - tb;
    });
}

export function useActiveRuns() {
  return useQuery<WorkflowRunListResponse, Error, WorkflowRun[]>({
    queryKey: ["workflow-runs", "active"],
    queryFn: async () => {
      const res = await get<{ items: BackendWorkflowRun[]; next_cursor?: number | null }>(
        `/workflow-runs${buildQuery({ limit: 50 })}`,
      );
      return {
        items: (res.items ?? []).map(mapWorkflowRun),
        next_cursor: res.next_cursor ?? null,
      };
    },
    select: (data) => sortByAttention(data.items),
    refetchInterval: 10_000,
    staleTime: 5_000,
  });
}

// ---------------------------------------------------------------------------
// Workflow stats (client-side from run history)
// ---------------------------------------------------------------------------

const TERMINAL_STATUSES = new Set<string>([
  "succeeded",
  "completed",
  "failed",
  "cancelled",
  "timed_out",
]);

export interface WorkflowStats {
  successRate: number;
  lastRunStatus: RunStatus | null;
  lastRunTime: string | null;
  sparkline: RunStatus[];
}

function computeWorkflowStats(runs: WorkflowRun[]): WorkflowStats {
  if (runs.length === 0) {
    return { successRate: 0, lastRunStatus: null, lastRunTime: null, sparkline: [] };
  }
  const terminal = runs.filter((r) => TERMINAL_STATUSES.has(r.status));
  const succeeded = terminal.filter(
    (r) => r.status === "succeeded" || r.status === "completed",
  ).length;
  const successRate = terminal.length > 0 ? Math.round((succeeded / terminal.length) * 100) : 0;
  return {
    successRate,
    lastRunStatus: runs[0].status,
    lastRunTime: runs[0].startedAt ?? runs[0].createdAt ?? null,
    sparkline: runs.map((r) => r.status),
  };
}

export function useWorkflowStats(workflowId: string | null | undefined) {
  return useQuery<WorkflowRun[], Error, WorkflowStats>({
    queryKey: ["workflow-runs", workflowId, { limit: 20 }],
    queryFn: () => {
      if (!workflowId) throw new Error("workflow id is required");
      return get<BackendWorkflowRun[]>(
        `/workflows/${workflowId}/runs${buildQuery({ limit: 20 })}`,
      ).then((runs) => (runs ?? []).map(mapWorkflowRun));
    },
    enabled: !!workflowId,
    select: computeWorkflowStats,
    staleTime: 30_000,
  });
}

export function useCancelRun() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: CancelRunInput) => {
      if (!input?.workflowId || !input?.runId) {
        throw new Error("workflow id and run id are required");
      }
      return post<void>(`/workflows/${input.workflowId}/runs/${input.runId}/cancel`);
    },
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: ["workflow-runs"] });
      if (variables?.workflowId) {
        queryClient.invalidateQueries({ queryKey: ["workflow-runs", variables.workflowId] });
      }
      if (variables?.runId) {
        queryClient.invalidateQueries({ queryKey: ["workflow-run", variables.runId] });
      }
    },
  });
}

// ---------------------------------------------------------------------------
// Dry-run simulation
// ---------------------------------------------------------------------------

export interface DryRunStepResult {
  step_id: string;
  step_type: string;
  decision: string;
  reason: string;
  rule_id?: string;
}

export interface DryRunResult {
  steps: DryRunStepResult[];
}

export interface DryRunInput {
  workflowId: string;
  input?: Record<string, unknown>;
  environment?: Record<string, unknown>;
}

export function useDryRun() {
  return useMutation({
    mutationFn: (params: DryRunInput) => {
      if (!params?.workflowId) {
        throw new Error("workflow id is required");
      }
      return post<DryRunResult>(`/workflows/${params.workflowId}/dry-run`, {
        input: params.input ?? {},
        environment: params.environment,
      });
    },
  });
}
