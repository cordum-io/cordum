import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { del, get, post } from "../api/client";
import { logger } from "../lib/logger";
import { queryKeys } from "../lib/queryKeys";
import { useToastStore } from "../state/toast";
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
  let seconds: number;
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

function toPositiveInt(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value) && value > 0) {
    return Math.floor(value);
  }
  if (typeof value === "string") {
    const parsed = Number.parseInt(value.trim(), 10);
    if (Number.isFinite(parsed) && parsed > 0) {
      return parsed;
    }
  }
  return undefined;
}

function parseSwitchCaseEntry(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object") return null;
  const record = value as Record<string, unknown>;
  const matchValue = record.match ?? record.when ?? record.value ?? record.matchValue;
  const stepRaw = record.next ?? record.step ?? record.target ?? record.step_id ?? record.goto ?? record.stepId;
  const stepId = typeof stepRaw === "string" ? stepRaw.trim() : "";
  if (!stepId) return null;
  return {
    match: matchValue == null ? "" : matchValue,
    next: stepId,
  };
}

function parseSwitchCasesInput(value: unknown): Array<Record<string, unknown>> {
  if (Array.isArray(value)) {
    return value
      .map((entry) => parseSwitchCaseEntry(entry))
      .filter((entry): entry is Record<string, unknown> => entry !== null);
  }
  if (value && typeof value === "object") {
    return Object.entries(value as Record<string, unknown>)
      .map(([match, stepRaw]) => {
        const stepId = typeof stepRaw === "string" ? stepRaw.trim() : "";
        if (!stepId) return null;
        return { match, next: stepId } as Record<string, unknown>;
      })
      .filter((entry): entry is Record<string, unknown> => entry !== null);
  }
  if (typeof value === "string" && value.trim()) {
    try {
      const parsed = JSON.parse(value);
      return parseSwitchCasesInput(parsed);
    } catch {
      logger.debug("workflows", "JSON parse failed for switch cases input");
      return [];
    }
  }
  return [];
}

function parseDateToISO(value: string): string | undefined {
  const ms = Date.parse(value);
  if (Number.isNaN(ms)) return undefined;
  const iso = new Date(ms).toISOString();
  return iso;
}

function parseMappingValue(value: unknown): Record<string, unknown> | string | undefined {
  if (value == null) return undefined;
  if (typeof value === "string") {
    const trimmed = value.trim();
    if (!trimmed) return undefined;
    try {
      const parsed = JSON.parse(trimmed);
      if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
        return parsed as Record<string, unknown>;
      }
    } catch {
      logger.debug("workflows", "JSON parse failed in parseMappingValue, treating as string");
      return trimmed;
    }
    return trimmed;
  }
  if (typeof value === "object" && !Array.isArray(value)) {
    return value as Record<string, unknown>;
  }
  return undefined;
}

function buildStepPayload(step: Workflow["steps"][number]): Record<string, unknown> {
  const config = (step.config ?? {}) as Record<string, unknown>;
  const JOB_SUBTYPES = new Set(["agent-task", "pack-action", "tool-call", "job"]);
  const frontendType = step.type || "job";
  const backendType = typeof config.backendType === "string" && config.backendType.trim()
    ? config.backendType.trim()
    : JOB_SUBTYPES.has(frontendType)
      ? "job"
      : frontendType === "sub-workflow"
        ? "subworkflow"
        : frontendType;
  const payload: Record<string, unknown> = {
    id: step.id,
    name: step.name,
    type: backendType,
  };

  // Prefer direct backend fields, fall back to legacy config bag
  const deps = step.depends_on ?? step.dependsOn;
  if (Array.isArray(deps) && deps.length > 0) {
    payload.depends_on = deps;
  }

  const topic = step.topic ?? (typeof config.topic === "string" ? config.topic : undefined);
  if (topic?.trim()) payload.topic = topic.trim();

  const workerId = step.worker_id ?? (typeof config.workerId === "string" ? config.workerId : undefined);
  if (workerId?.trim()) payload.worker_id = workerId.trim();

  const condition = step.condition ?? (typeof config.expression === "string" ? config.expression : undefined);
  if (condition?.trim()) payload.condition = condition.trim();

  const forEach = step.for_each ?? (typeof config.forEach === "string" ? config.forEach : undefined);
  if (forEach?.trim()) payload.for_each = forEach.trim();

  const maxParallel = step.max_parallel ?? (typeof config.parallelism === "number" ? config.parallelism : undefined);
  if (typeof maxParallel === "number" && maxParallel > 0) payload.max_parallel = Math.floor(maxParallel);

  // Timeout: prefer direct field, fall back to config duration parsing
  const timeoutSec = step.timeout_sec ?? parseDurationSeconds(config.timeout);
  if (timeoutSec !== undefined) payload.timeout_sec = timeoutSec;

  // Delay
  const delaySec = step.delay_sec ?? parseDurationSeconds(config.duration);
  if (delaySec !== undefined) {
    payload.delay_sec = delaySec;
  } else {
    const delayUntil = step.delay_until ?? (typeof config.duration === "string" ? parseDateToISO(config.duration) : undefined);
    if (delayUntil) payload.delay_until = delayUntil;
  }

  // Retry
  if (step.retry?.max_retries) {
    payload.retry = step.retry;
  } else if (typeof config.retryMax === "number" && config.retryMax > 0) {
    payload.retry = { max_retries: Math.floor(config.retryMax) };
  }

  // Schemas
  const inputSchema = step.input_schema ?? (config.inputSchema as Record<string, unknown> | undefined);
  if (inputSchema && typeof inputSchema === "object") payload.input_schema = inputSchema;
  const inputSchemaId = step.input_schema_id ?? (typeof config.inputSchemaId === "string" ? config.inputSchemaId : undefined);
  if (inputSchemaId?.trim()) payload.input_schema_id = inputSchemaId.trim();
  const outputSchema = step.output_schema ?? (config.outputSchema as Record<string, unknown> | undefined);
  if (outputSchema && typeof outputSchema === "object") payload.output_schema = outputSchema;
  const outputSchemaId = step.output_schema_id ?? (typeof config.outputSchemaId === "string" ? config.outputSchemaId : undefined);
  if (outputSchemaId?.trim()) payload.output_schema_id = outputSchemaId.trim();
  const outputPath = step.output_path ?? (typeof config.outputPath === "string" ? config.outputPath : undefined);
  if (outputPath?.trim()) payload.output_path = outputPath.trim();

  // Route labels
  const routeLabels = step.route_labels ?? (config.routeLabels as Record<string, string> | undefined);
  if (routeLabels && typeof routeLabels === "object") payload.route_labels = routeLabels;

  // Input: prefer direct field, merge with legacy config fields
  const input: Record<string, unknown> = {};
  const stepInput = step.input ?? (config.input as Record<string, unknown> | undefined);
  if (stepInput && typeof stepInput === "object") Object.assign(input, stepInput);
  if (frontendType === "parallel" || payload.type === "parallel") {
    const parallelSteps = toStringArray(
      (stepInput as Record<string, unknown> | undefined)?.steps ?? config.parallelSteps ?? config.steps,
    );
    if (parallelSteps.length > 0) {
      input.steps = parallelSteps;
    }
    const strategy =
      typeof (stepInput as Record<string, unknown> | undefined)?.strategy === "string"
        ? ((stepInput as Record<string, unknown>).strategy as string).trim()
        : typeof config.completionStrategy === "string"
          ? config.completionStrategy.trim()
          : "";
    if (strategy) {
      input.strategy = strategy;
    }
    const required = toPositiveInt(
      (stepInput as Record<string, unknown> | undefined)?.required ?? config.requiredCount,
    );
    if ((strategy || input.strategy) === "n_of_m" && required !== undefined) {
      input.required = required;
    }
  }
  if (frontendType === "sub-workflow" || payload.type === "subworkflow") {
    const workflowId =
      typeof (stepInput as Record<string, unknown> | undefined)?.workflow_id === "string"
        ? ((stepInput as Record<string, unknown>).workflow_id as string).trim()
        : typeof config.workflowId === "string"
          ? config.workflowId.trim()
          : "";
    if (workflowId) input.workflow_id = workflowId;

    const inputMapping = parseMappingValue(
      (stepInput as Record<string, unknown> | undefined)?.input_mapping ?? config.inputMapping,
    );
    if (inputMapping !== undefined) input.input_mapping = inputMapping;

    const outputMapping = parseMappingValue(
      (stepInput as Record<string, unknown> | undefined)?.output_mapping ?? config.outputMapping,
    );
    if (outputMapping !== undefined) input.output_mapping = outputMapping;
  }
  if (frontendType === "switch" || payload.type === "switch") {
    const switchCases = parseSwitchCasesInput(
      (stepInput as Record<string, unknown> | undefined)?.cases ??
        config.switchCases ??
        config.cases,
    );
    if (switchCases.length > 0) {
      input.cases = switchCases;
    }
    const defaultBranch =
      (typeof (stepInput as Record<string, unknown> | undefined)?.default === "string"
        ? ((stepInput as Record<string, unknown>).default as string).trim()
        : typeof (stepInput as Record<string, unknown> | undefined)?.default_step === "string"
          ? ((stepInput as Record<string, unknown>).default_step as string).trim()
          : typeof config.defaultBranch === "string"
            ? config.defaultBranch.trim()
            : "");
    if (defaultBranch) {
      input.default = defaultBranch;
    }
  }
  if (typeof config.messageTemplate === "string" && config.messageTemplate.trim()) input.message = config.messageTemplate.trim();
  if (typeof config.channel === "string" && config.channel.trim()) input.component = config.channel.trim();
  if (typeof config.prompt === "string" && config.prompt.trim()) input.prompt = config.prompt.trim();
  const budgetInput: Record<string, number> = {};
  if (typeof config.maxInputTokens === "number" && config.maxInputTokens > 0) budgetInput.input_tokens = config.maxInputTokens;
  if (typeof config.maxOutputTokens === "number" && config.maxOutputTokens > 0) budgetInput.output_tokens = config.maxOutputTokens;
  if (typeof config.maxTotalTokens === "number" && config.maxTotalTokens > 0) budgetInput.total_tokens = config.maxTotalTokens;
  if (Object.keys(budgetInput).length > 0) input.budget = budgetInput;
  if (Object.keys(input).length > 0) payload.input = input;

  // Meta: prefer direct field, merge with legacy config fields
  let meta: Record<string, unknown> = {};
  const stepMeta = step.meta ?? (config.meta as Record<string, unknown> | undefined);
  if (stepMeta && typeof stepMeta === "object") meta = { ...stepMeta };
  const caps = toStringArray(config.capabilities ?? config.capability);
  const requires = toStringArray(config.requires);
  const riskTags = toStringArray(config.riskTags ?? config.risk_tags);
  if (caps.length > 0) {
    meta.capability = caps[0];
    const combined = [...caps.slice(1), ...requires].filter(Boolean);
    if (combined.length > 0) meta.requires = combined;
  } else if (requires.length > 0) {
    meta.requires = requires;
  }
  if (riskTags.length > 0) meta.risk_tags = riskTags;
  if (config.labels && typeof config.labels === "object") meta.labels = config.labels as Record<string, string>;
  if (typeof config.packId === "string") meta.pack_id = config.packId;
  if (typeof config.actorId === "string") meta.actor_id = config.actorId;
  if (typeof config.actorType === "string") meta.actor_type = config.actorType;
  if (typeof config.adapterId === "string" && config.adapterId) meta.adapter_id = config.adapterId;
  if (typeof config.memoryId === "string" && config.memoryId) meta.memory_id = config.memoryId;
  if (typeof config.contextMode === "string" && config.contextMode) meta.context_mode = config.contextMode;
  if (typeof config.allowSummarization === "boolean") meta.allow_summarization = config.allowSummarization;
  if (typeof config.allowRetrieval === "boolean") meta.allow_retrieval = config.allowRetrieval;
  if (typeof config.deadlineMs === "number" && config.deadlineMs > 0) meta.deadline_ms = config.deadlineMs;
  if (typeof config.priority === "string" && config.priority) meta.priority = config.priority;
  if (Object.keys(budgetInput).length > 0 && !meta.budget) meta.budget = budgetInput;
  if (Object.keys(meta).length > 0) payload.meta = meta;

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

  // Prefer direct timeout_sec, fall back to legacy timeout
  const timeoutSec =
    typeof input.timeout_sec === "number"
      ? input.timeout_sec
      : typeof input.timeout === "number"
        ? input.timeout
        : typeof meta.timeout === "number"
          ? (meta.timeout as number)
          : undefined;
  if (typeof timeoutSec === "number" && timeoutSec > 0) {
    payload.timeout_sec = Math.floor(timeoutSec);
  }

  // Prefer direct fields, fall back to metadata
  const inputSchema = input.input_schema ?? meta.inputSchema;
  if (inputSchema) payload.input_schema = inputSchema;
  if (meta.parameters) payload.parameters = meta.parameters;
  const config = input.config ?? meta.config;
  if (config) payload.config = config;

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
    queryKey: queryKeys.workflows.list(params),
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
    queryKey: queryKeys.workflows.detail(id),
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
    mutationFn: (payload: Partial<Workflow> & { id?: string }) => {
      logger.info("workflows", "Creating workflow", { name: payload.name });
      return post<WorkflowIdResponse>("/workflows", toWorkflowUpsertPayload(payload ?? {}));
    },
    onSuccess: (data) => {
      logger.info("workflows", "Workflow created", { id: data?.id });
      useToastStore.getState().addToast({ type: "success", title: "Workflow created" });
      queryClient.invalidateQueries({ queryKey: queryKeys.workflows.all });
      if (data?.id) {
        queryClient.invalidateQueries({ queryKey: queryKeys.workflows.detail(data.id) });
      }
    },
    onError: (err) => {
      logger.error("workflows", "Create workflow failed", { error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to create workflow", description: err.message });
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
      logger.info("workflows", "Updating workflow", { id: payload.id });
      return post<WorkflowIdResponse>("/workflows", toWorkflowUpsertPayload(payload));
    },
    onSuccess: (data, variables) => {
      logger.info("workflows", "Workflow updated", { id: data?.id || variables?.id });
      useToastStore.getState().addToast({ type: "success", title: "Workflow saved" });
      queryClient.invalidateQueries({ queryKey: queryKeys.workflows.all });
      const workflowId = data?.id || variables?.id;
      if (workflowId) {
        queryClient.invalidateQueries({ queryKey: queryKeys.workflows.detail(workflowId) });
      }
    },
    onError: (err, variables) => {
      logger.error("workflows", "Update workflow failed", { id: variables?.id, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to save workflow", description: err.message });
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
      logger.info("workflows", "Deleting workflow", { id: workflowId });
      return del<void>(`/workflows/${workflowId}`);
    },
    onSuccess: (_data, workflowId) => {
      logger.info("workflows", "Workflow deleted", { id: workflowId });
      useToastStore.getState().addToast({ type: "success", title: "Workflow deleted" });
      queryClient.invalidateQueries({ queryKey: queryKeys.workflows.all });
      if (workflowId) {
        queryClient.invalidateQueries({ queryKey: queryKeys.workflows.detail(workflowId) });
      }
    },
    onError: (err, workflowId) => {
      logger.error("workflows", "Delete workflow failed", { id: workflowId, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to delete workflow", description: err.message });
    },
  });
}

export function useRuns(workflowId: string | null | undefined, params?: WorkflowRunsParams) {
  return useQuery<WorkflowRun[]>({
    queryKey: queryKeys.workflowRuns.byWorkflow(workflowId, params),
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
    queryKey: queryKeys.workflowRuns.allRuns(filters),
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
    queryKey: queryKeys.workflowRuns.detail(runId),
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
    queryKey: queryKeys.workflowRuns.timeline(runId, params?.limit),
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
      logger.info("workflows", "Starting run", { workflowId: input.workflowId, dryRun: input.dryRun });
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
      logger.info("workflows", "Run started", { workflowId: variables?.workflowId, runId: data?.run_id });
      if (!variables?.dryRun) {
        useToastStore.getState().addToast({ type: "success", title: "Run started" });
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.workflowRuns.all });
      if (variables?.workflowId) {
        queryClient.invalidateQueries({ queryKey: queryKeys.workflowRuns.byWorkflow(variables.workflowId) });
      }
      if (data?.run_id) {
        queryClient.invalidateQueries({ queryKey: queryKeys.workflowRuns.detail(data.run_id) });
      }
    },
    onError: (err, variables) => {
      logger.error("workflows", "Start run failed", { workflowId: variables?.workflowId, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to start run", description: err.message });
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
      if (!variables?.dryRun) {
        useToastStore.getState().addToast({ type: "success", title: "Run restarted" });
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.workflowRuns.all });
      if (variables?.runId) {
        queryClient.invalidateQueries({ queryKey: queryKeys.workflowRuns.detail(variables.runId) });
      }
      if (data?.run_id) {
        queryClient.invalidateQueries({ queryKey: queryKeys.workflowRuns.detail(data.run_id) });
      }
    },
    onError: (err) => {
      useToastStore.getState().addToast({ type: "error", title: "Failed to rerun", description: err.message });
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
]);

function getAttentionPriority(run: WorkflowRun): number {
  const steps = run.steps ?? [];
  // Priority 0: Any step waiting for approval
  if (steps.some((s) => s.status === "waiting")) return 0;
  // Priority 1: Any step failed
  if (steps.some((s) => s.status === "failed" || s.status === "timed_out")) return 1;
  // Priority 2: Currently running
  if (run.status === "running") return 2;
  // Priority 3: Pending
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
    queryKey: queryKeys.workflowRuns.active(),
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
    (r) => r.status === "succeeded",
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
    queryKey: queryKeys.workflowRuns.byWorkflow(workflowId, { limit: 20 }),
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
      logger.info("workflows", "Cancelling run", { workflowId: input.workflowId, runId: input.runId });
      return post<void>(`/workflows/${input.workflowId}/runs/${input.runId}/cancel`);
    },
    onSuccess: (_data, variables) => {
      logger.info("workflows", "Run cancelled", { workflowId: variables?.workflowId, runId: variables?.runId });
      useToastStore.getState().addToast({ type: "success", title: "Run cancelled" });
      queryClient.invalidateQueries({ queryKey: queryKeys.workflowRuns.all });
      if (variables?.workflowId) {
        queryClient.invalidateQueries({ queryKey: queryKeys.workflowRuns.byWorkflow(variables.workflowId) });
      }
      if (variables?.runId) {
        queryClient.invalidateQueries({ queryKey: queryKeys.workflowRuns.detail(variables.runId) });
      }
    },
    onError: (err, variables) => {
      logger.error("workflows", "Cancel run failed", { runId: variables?.runId, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to cancel run", description: err.message });
    },
  });
}

// ---------------------------------------------------------------------------
// Delete run
// ---------------------------------------------------------------------------

export interface DeleteRunInput {
  workflowId: string;
  runId: string;
}

export function useDeleteRun() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: DeleteRunInput) => {
      if (!input?.workflowId || !input?.runId) {
        throw new Error("workflow id and run id are required");
      }
      logger.info("workflows", "Deleting run", { workflowId: input.workflowId, runId: input.runId });
      return del<void>(`/workflows/${input.workflowId}/runs/${input.runId}`);
    },
    onSuccess: (_data, variables) => {
      logger.info("workflows", "Run deleted", { workflowId: variables?.workflowId, runId: variables?.runId });
      useToastStore.getState().addToast({ type: "success", title: "Run deleted" });
      queryClient.invalidateQueries({ queryKey: queryKeys.workflowRuns.all });
      if (variables?.workflowId) {
        queryClient.invalidateQueries({ queryKey: queryKeys.workflowRuns.byWorkflow(variables.workflowId) });
      }
    },
    onError: (err, variables) => {
      logger.error("workflows", "Delete run failed", { runId: variables?.runId, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to delete run", description: err.message });
    },
  });
}

export function useDeleteRuns() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (inputs: DeleteRunInput[]) => {
      if (!inputs?.length) {
        throw new Error("at least one run is required");
      }
      logger.info("workflows", "Deleting runs", { count: inputs.length });
      return Promise.all(
        inputs.map((input) => del<void>(`/workflows/${input.workflowId}/runs/${input.runId}`)),
      );
    },
    onSuccess: (_data, variables) => {
      logger.info("workflows", "Runs deleted", { count: variables?.length });
      useToastStore.getState().addToast({ type: "success", title: `${variables?.length ?? 0} run(s) deleted` });
      queryClient.invalidateQueries({ queryKey: queryKeys.workflowRuns.all });
      const workflowIds = new Set(variables?.map((v) => v.workflowId));
      for (const wid of workflowIds) {
        queryClient.invalidateQueries({ queryKey: queryKeys.workflowRuns.byWorkflow(wid) });
      }
    },
    onError: (err) => {
      logger.error("workflows", "Bulk delete runs failed", { error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to delete runs", description: err.message });
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

/** @internal exported for unit tests */
export const __workflowsInternal = {
  buildQuery,
  toStringArray,
  parseDurationSeconds,
  parseDateToISO,
  buildStepPayload,
  toWorkflowUpsertPayload,
  getAttentionPriority,
  sortByAttention,
  computeWorkflowStats,
};
