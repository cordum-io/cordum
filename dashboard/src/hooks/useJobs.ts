import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, post } from "../api/client";
import { logger } from "../lib/logger";
import type { Job, JobStatus, SafetyDecision, ApiResponse } from "../api/types";
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
  if (filters.state?.length === 1) {
    params.set("state", stateToBackend(filters.state[0]));
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

// ---------------------------------------------------------------------------
// Queries
// ---------------------------------------------------------------------------

export function useJobs(filters: JobFilters = {}) {
  return useQuery<ApiResponse<Job[]>>({
    queryKey: ["jobs", filters],
    queryFn: async () => {
      const res = await get<{ items: BackendJobRecord[]; next_cursor?: number | null }>(
        `/jobs${buildParams(filters)}`,
      );
      let items = (res.items ?? []).map(mapJobRecord);
      if (filters.state && filters.state.length > 1) {
        items = items.filter((j) => filters.state?.includes(j.status));
      }
      if (filters.decision && filters.decision.length > 0) {
        items = items.filter((j) =>
          j.safetyDecision && filters.decision?.includes(j.safetyDecision.type),
        );
      }
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
    queryKey: ["job", id],
    queryFn: async () => {
      const res = await get<BackendJobDetail>(`/jobs/${id}`);
      return mapJobDetail(res);
    },
    enabled: !!id,
    staleTime: 5_000,
  });
}

export function useJobDecisions(id: string) {
  return useQuery<SafetyDecision[]>({
    queryKey: ["job", id, "decisions"],
    queryFn: async () => {
      const res = await get<Array<Record<string, unknown>>>(`/jobs/${id}/decisions`);
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

export function useCancelJob() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (id) => {
      logger.info("jobs", "Cancelling job", { id });
      return post<void>(`/jobs/${id}/cancel`);
    },
    onSuccess: (_data, id) => {
      logger.info("jobs", "Job cancelled", { id });
      queryClient.invalidateQueries({ queryKey: ["jobs"] });
      queryClient.invalidateQueries({ queryKey: ["job", id] });
    },
    onError: (err, id) => {
      logger.error("jobs", "Cancel job failed", { id, error: err.message });
    },
  });
}

export function useRetryJob() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (id) => {
      logger.info("jobs", "Retrying job", { id });
      return post<void>(`/dlq/${id}/retry`);
    },
    onSuccess: (_data, id) => {
      logger.info("jobs", "Job retried", { id });
      queryClient.invalidateQueries({ queryKey: ["jobs"] });
      queryClient.invalidateQueries({ queryKey: ["job", id] });
    },
    onError: (err, id) => {
      logger.error("jobs", "Retry job failed", { id, error: err.message });
    },
  });
}
