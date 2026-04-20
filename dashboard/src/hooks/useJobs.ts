import { useQuery, useMutation, useQueryClient, type QueryKey } from "@tanstack/react-query";
import { get, post } from "../api/client";
import { logger } from "../lib/logger";
import { queryKeys } from "../lib/queryKeys";
import { useToastStore } from "../state/toast";
import type {
  Job,
  JobStatus,
  SafetyDecision,
  ApiResponse,
  RemediateJobInput,
  RemediateJobResponse,
  SubmitJobInput,
  SubmitJobResponse,
} from "../api/types";
import {
  mapJobDetail,
  mapJobRecord,
  mapSafetyDecision,
  type BackendJobDetail,
  type BackendJobRecord,
} from "../api/transform";

// ---------------------------------------------------------------------------
// Filters
// ---------------------------------------------------------------------------

export interface JobFilters {
  state?: JobStatus[];
  topic?: string;
  pool?: string;
  decision?: string[];
  timeRange?: string;
  tenant?: string;
  team?: string;
  limit?: number;
  cursor?: number;
  updatedAfter?: number;
  updatedBefore?: number;
}

function stateToBackend(state: JobStatus): string {
  switch (state) {
    case "pending":
      return "PENDING";
    case "scheduled":
      return "SCHEDULED";
    case "dispatched":
      return "DISPATCHED";
    case "running":
      return "RUNNING";
    case "succeeded":
      return "SUCCEEDED";
    case "failed":
      return "FAILED";
    case "cancelled":
      return "CANCELLED";
    case "approval_required":
      return "APPROVAL_REQUIRED";
    case "denied":
      return "DENIED";
    case "timeout":
      return "TIMEOUT";
    case "output_quarantined":
      return "OUTPUT_QUARANTINED";
    default:
      return (state as string).toUpperCase();
  }
}

function rangeToMicros(range?: string): { after?: number; before?: number } {
  if (!range) return {};
  const now = Date.now();
  let deltaMs = 0;
  switch (range) {
    case "1h":
      deltaMs = 60 * 60 * 1000;
      break;
    case "24h":
      deltaMs = 24 * 60 * 60 * 1000;
      break;
    case "7d":
      deltaMs = 7 * 24 * 60 * 60 * 1000;
      break;
    case "30d":
      deltaMs = 30 * 24 * 60 * 60 * 1000;
      break;
    default:
      deltaMs = 0;
  }
  if (!deltaMs) return {};
  const after = (now - deltaMs) * 1000;
  const before = now * 1000;
  return { after, before };
}

function buildParams(filters: JobFilters): string {
  const params = new URLSearchParams();
  if (filters.state?.length) {
    for (const s of filters.state) {
      params.append("state", stateToBackend(s));
    }
  }
  if (filters.topic) params.set("topic", filters.topic);
  if (filters.tenant) params.set("tenant", filters.tenant);
  if (filters.team) params.set("team", filters.team);
  if (filters.limit !== undefined) params.set("limit", String(filters.limit));
  if (filters.cursor !== undefined && filters.cursor > 0) {
    params.set("cursor", String(filters.cursor));
  }
  if (filters.updatedAfter !== undefined && filters.updatedAfter > 0) {
    params.set("updated_after", String(filters.updatedAfter));
  }
  if (filters.updatedBefore !== undefined && filters.updatedBefore > 0) {
    params.set("updated_before", String(filters.updatedBefore));
  }
  if (filters.timeRange) {
    const range = rangeToMicros(filters.timeRange);
    if (range.after) params.set("updated_after", String(range.after));
    if (range.before) params.set("updated_before", String(range.before));
  }
  const qs = params.toString();
  return qs ? `?${qs}` : "";
}

function filterJobsForClient(items: Job[], filters: JobFilters): Job[] {
  let filtered = items;
  if (filters.state && filters.state.length > 0) {
    filtered = filtered.filter((job) => filters.state?.includes(job.status));
  }
  if (filters.decision && filters.decision.length > 0) {
    filtered = filtered.filter((job) =>
      job.safetyDecision && filters.decision?.includes(job.safetyDecision.type),
    );
  }
  return filtered;
}

function applyOptimisticCancelToList(
  old: ApiResponse<Job[]> | undefined,
  id: string,
): ApiResponse<Job[]> | undefined {
  if (!old?.items) return old;
  return {
    ...old,
    items: old.items.map((job) =>
      job.id === id ? { ...job, status: "cancelled" as JobStatus } : job,
    ),
  };
}

function applyOptimisticCancelToDetail(
  old: Job | undefined,
): Job | undefined {
  return old ? { ...old, status: "cancelled" as JobStatus } : old;
}

function validateRemediateJobId(jobId: string): string {
  const trimmedJobID = jobId.trim();
  if (!trimmedJobID) {
    throw new Error("job id is required");
  }
  return trimmedJobID;
}

// ---------------------------------------------------------------------------
// Queries
// ---------------------------------------------------------------------------

export function useJobs(filters: JobFilters = {}) {
  return useQuery<ApiResponse<Job[]>>({
    queryKey: queryKeys.jobs.list(filters),
    queryFn: async () => {
      const res = await get<{ items: BackendJobRecord[]; next_cursor?: number | null }>(
        `/jobs${buildParams(filters)}`,
      );
      const mapped = (res.items ?? []).map(mapJobRecord);
      const items = filterJobsForClient(mapped, filters);
      return {
        items,
        next_cursor: res.next_cursor ?? null,
      };
    },
    staleTime: 10_000,
  });
}

export function useJob(id: string) {
  return useQuery<Job>({
    queryKey: queryKeys.jobs.detail(id),
    queryFn: async () => {
      const res = await get<BackendJobDetail>(`/jobs/${encodeURIComponent(id)}`);
      return mapJobDetail(res);
    },
    enabled: !!id,
    staleTime: 5_000,
  });
}

export function useJobDecisions(id: string) {
  return useQuery<SafetyDecision[]>({
    queryKey: queryKeys.jobs.decisions(id),
    queryFn: async () => {
      const res = await get<Array<Record<string, unknown>>>(`/jobs/${encodeURIComponent(id)}/decisions`);
      return (res ?? []).map((r) =>
        mapSafetyDecision(
          typeof r.decision === "string" ? r.decision : undefined,
          typeof r.reason === "string" ? r.reason : undefined,
          typeof r.rule_id === "string" ? r.rule_id : undefined,
        ),
      ).filter((v): v is SafetyDecision => !!v);
    },
    enabled: !!id,
    staleTime: 30_000,
  });
}

// ---------------------------------------------------------------------------
// Mutations
// ---------------------------------------------------------------------------

export function useSubmitJob() {
  const queryClient = useQueryClient();
  return useMutation<SubmitJobResponse, Error, SubmitJobInput>({
    mutationFn: (input) => {
      logger.info("jobs", "Submitting job", {
        topic: input.topic,
        priority: input.priority ?? "normal",
      });
      return post<SubmitJobResponse>("/jobs", input);
    },
    onSuccess: (result) => {
      logger.info("jobs", "Job submitted", {
        job_id: result.job_id,
        trace_id: result.trace_id,
      });
      queryClient.invalidateQueries({ queryKey: queryKeys.jobs.all });
    },
    onError: (err, input) => {
      logger.error("jobs", "Job submission failed", {
        topic: input.topic,
        error: err.message,
      });
      useToastStore.getState().addToast({ type: "error", title: "Job submission failed", description: err.message });
    },
  });
}

export function useCancelJob() {
  const queryClient = useQueryClient();
  type CancelSnapshot = { previousList: [QueryKey, ApiResponse<Job[]> | undefined][]; previousDetail: Job | undefined; id: string };
  return useMutation<void, Error, string, CancelSnapshot>({
    mutationFn: (id) => {
      logger.info("jobs", "Cancelling job", { id });
      return post<void>(`/jobs/${encodeURIComponent(id)}/cancel`);
    },
    onMutate: async (id) => {
      await queryClient.cancelQueries({ queryKey: queryKeys.jobs.all });
      await queryClient.cancelQueries({ queryKey: queryKeys.jobs.detail(id) });
      const previousList = queryClient.getQueriesData<ApiResponse<Job[]>>({ queryKey: queryKeys.jobs.all });
      const previousDetail = queryClient.getQueryData<Job>(queryKeys.jobs.detail(id));
      queryClient.setQueriesData<ApiResponse<Job[]>>(
        { queryKey: queryKeys.jobs.all },
        (old) => applyOptimisticCancelToList(old, id),
      );
      queryClient.setQueryData<Job>(queryKeys.jobs.detail(id), (old) =>
        applyOptimisticCancelToDetail(old),
      );
      return { previousList, previousDetail, id };
    },
    onSuccess: (_data, id) => {
      logger.info("jobs", "Job cancelled", { id });
      useToastStore.getState().addToast({ type: "success", title: "Job cancelled" });
    },
    onError: (err, id, context) => {
      if (context?.previousList) {
        for (const [key, data] of context.previousList) {
          queryClient.setQueryData(key, data);
        }
      }
      if (context?.previousDetail) {
        queryClient.setQueryData(queryKeys.jobs.detail(context.id), context.previousDetail);
      }
      logger.error("jobs", "Cancel job failed", { id, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to cancel job", description: err.message });
    },
    onSettled: (_data, _err, id) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.jobs.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.jobs.detail(id) });
    },
  });
}

export function useRetryJob() {
  const queryClient = useQueryClient();
  return useMutation<SubmitJobResponse, Error, { id: string; topic: string }>({
    mutationFn: ({ id, topic }) => {
      logger.info("jobs", "Retrying job via resubmit", { id, topic });
      return post<SubmitJobResponse>("/jobs", {
        topic,
        prompt: "",
        labels: { retry: "true", retry_of_job: id },
      });
    },
    onSuccess: (result, { id }) => {
      logger.info("jobs", "Job retried", { originalId: id, newJobId: result.job_id });
      useToastStore.getState().addToast({ type: "success", title: "Retrying job" });
      queryClient.invalidateQueries({ queryKey: queryKeys.jobs.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.jobs.detail(id) });
    },
    onError: (err, { id }) => {
      logger.error("jobs", "Retry job failed", { id, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to retry job", description: err.message });
    },
  });
}

export function useRemediateJob() {
  const queryClient = useQueryClient();
  return useMutation<
    RemediateJobResponse,
    Error,
    { jobId: string; input: RemediateJobInput }
  >({
    mutationFn: ({ jobId, input }) => {
      const trimmedJobID = validateRemediateJobId(jobId);
      logger.info("jobs", "Remediating job", {
        jobId: trimmedJobID,
      });
      return post<RemediateJobResponse>(`/jobs/${encodeURIComponent(trimmedJobID)}/remediate`, input);
    },
    onSuccess: (_result, variables) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.jobs.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.jobs.detail(variables.jobId) });
    },
    onError: (err, variables) => {
      logger.error("jobs", "Remediate job failed", {
        jobId: variables.jobId,
        error: err.message,
      });
      useToastStore.getState().addToast({ type: "error", title: "Remediation failed", description: err.message });
    },
  });
}

/** @internal exported for unit tests */
export const __jobsInternal = {
  stateToBackend,
  rangeToMicros,
  buildParams,
  filterJobsForClient,
  applyOptimisticCancelToList,
  applyOptimisticCancelToDetail,
  validateRemediateJobId,
};
