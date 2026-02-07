import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import type { Worker, Job, WorkflowRun, ApiResponse } from "../api/types";
import {
  mapHeartbeatToWorker,
  mapJobRecord,
  mapWorkflowRun,
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

export function useStatus() {
  return useQuery<GatewayStatus>({
    queryKey: ["status"],
    queryFn: () => get<GatewayStatus>("/status"),
    refetchInterval: 10_000,
    staleTime: 8_000,
  });
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
