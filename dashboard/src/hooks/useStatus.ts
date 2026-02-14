import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import type { Worker, Job, WorkflowRun, ApiResponse } from "../api/types";
import {
  mapHeartbeatToWorker,
  mapJobRecord,
  mapWorkflowRun,
  normalizeJobStatus,
  type BackendHeartbeat,
  type BackendJobRecord,
  type BackendWorkflowRun,
} from "../api/transform";

// ---------------------------------------------------------------------------
// System status
// ---------------------------------------------------------------------------

export interface GatewayStatus {
  time?: string;
  uptime_seconds?: number;
  build?: {
    version?: string;
    commit?: string;
    date?: string;
  };
  nats?: {
    connected?: boolean;
    status?: string;
    url?: string;
  };
  redis?: {
    ok?: boolean;
    error?: string;
  };
  workers?: {
    count?: number;
  };
  license?: Record<string, unknown>;
  pipeline?: {
    pending?: number;
    dispatched?: number;
    running?: number;
    succeeded?: number;
    failed?: number;
  };
}

type PipelineMetrics = NonNullable<GatewayStatus["pipeline"]>;
const PIPELINE_FALLBACK_LIMIT = 300;

function hasPipeline(status?: GatewayStatus): status is GatewayStatus & { pipeline: PipelineMetrics } {
  return !!status?.pipeline && typeof status.pipeline === "object";
}

function pipelineMetric(value?: number): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return 0;
  }
  return value;
}

function pipelineTotal(pipeline?: PipelineMetrics): number {
  if (!pipeline) {
    return 0;
  }
  return (
    pipelineMetric(pipeline.pending) +
    pipelineMetric(pipeline.dispatched) +
    pipelineMetric(pipeline.running) +
    pipelineMetric(pipeline.succeeded) +
    pipelineMetric(pipeline.failed)
  );
}

function pipelineFromJobs(records: BackendJobRecord[]): PipelineMetrics {
  const out: PipelineMetrics = {
    pending: 0,
    dispatched: 0,
    running: 0,
    succeeded: 0,
    failed: 0,
  };

  for (const record of records) {
    const state = normalizeJobStatus(record.state);
    switch (state) {
      case "pending":
      case "scheduled":
      case "approval_required":
        out.pending = (out.pending ?? 0) + 1;
        break;
      case "dispatched":
        out.dispatched = (out.dispatched ?? 0) + 1;
        break;
      case "running":
        out.running = (out.running ?? 0) + 1;
        break;
      case "succeeded":
        out.succeeded = (out.succeeded ?? 0) + 1;
        break;
      case "failed":
      case "cancelled":
      case "timeout":
      case "denied":
      case "output_quarantined":
        out.failed = (out.failed ?? 0) + 1;
        break;
      default:
        break;
    }
  }

  return out;
}

export function useStatus() {
  return useQuery<GatewayStatus>({
    queryKey: ["status"],
    queryFn: () => get<GatewayStatus>("/status"),
    refetchInterval: 10_000,
    staleTime: 8_000,
  });
}

export function usePipelineMetrics() {
  const statusQuery = useStatus();
  const statusPipeline = hasPipeline(statusQuery.data) ? statusQuery.data.pipeline : undefined;
  const statusTotal = pipelineTotal(statusPipeline);
  const shouldProbeFallback = !statusQuery.isLoading && (!statusPipeline || statusTotal === 0);

  const fallbackQuery = useQuery<PipelineMetrics>({
    queryKey: ["status", "pipeline-fallback", PIPELINE_FALLBACK_LIMIT],
    enabled: shouldProbeFallback,
    queryFn: async () => {
      const res = await get<{ items: BackendJobRecord[] }>(`/jobs?limit=${PIPELINE_FALLBACK_LIMIT}`);
      return pipelineFromJobs(res.items ?? []);
    },
    refetchInterval: 10_000,
    staleTime: 8_000,
  });

  const fallbackTotal = pipelineTotal(fallbackQuery.data);
  const preferFallback = statusTotal === 0 && fallbackTotal > 0;

  const data = preferFallback ? fallbackQuery.data : statusPipeline ?? fallbackQuery.data;
  const source = preferFallback ? "jobs_fallback" : statusPipeline ? "gateway" : fallbackQuery.data ? "jobs_fallback" : "unavailable";

  return {
    data,
    source,
    isLoading: statusQuery.isLoading || (shouldProbeFallback && fallbackQuery.isLoading),
    isError: !statusPipeline && fallbackQuery.isError,
    refetch: preferFallback || !statusPipeline ? fallbackQuery.refetch : statusQuery.refetch,
  };
}

// ---------------------------------------------------------------------------
// Workers summary
// ---------------------------------------------------------------------------

export function useWorkersSummary() {
  return useQuery<ApiResponse<Worker[]>>({
    queryKey: ["workers"],
    queryFn: async () => {
      const res = await get<BackendHeartbeat[]>("/workers");
      const items = (res ?? [])
        .map(mapHeartbeatToWorker)
        .filter((w): w is Worker => !!w);
      return { items };
    },
    staleTime: 15_000,
  });
}

// ---------------------------------------------------------------------------
// Recent jobs
// ---------------------------------------------------------------------------

export function useRecentJobs(limit = 10) {
  return useQuery<ApiResponse<Job[]>>({
    queryKey: ["jobs", "recent", limit],
    queryFn: async () => {
      const res = await get<{ items: BackendJobRecord[] }>(`/jobs?limit=${limit}`);
      return { items: (res.items ?? []).map(mapJobRecord) };
    },
    staleTime: 10_000,
  });
}

// ---------------------------------------------------------------------------
// Recent workflow runs
// ---------------------------------------------------------------------------

export function useRecentRuns(limit = 10) {
  return useQuery<ApiResponse<WorkflowRun[]>>({
    queryKey: ["runs", "recent", limit],
    queryFn: async () => {
      const res = await get<{ items: BackendWorkflowRun[] }>(`/workflow-runs?limit=${limit}`);
      return { items: (res.items ?? []).map(mapWorkflowRun) };
    },
    staleTime: 10_000,
  });
}

/** @internal exported for unit tests */
export const __statusInternal = {
  PIPELINE_FALLBACK_LIMIT,
  hasPipeline,
  pipelineMetric,
  pipelineTotal,
  pipelineFromJobs,
};
