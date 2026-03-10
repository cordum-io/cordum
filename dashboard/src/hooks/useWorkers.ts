import { useEffect } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { get } from "../api/client";
import type { Job, Worker, Pool } from "../api/types";
import {
  mapHeartbeatToWorker,
  mapPoolResponse,
  type BackendHeartbeat,
  type BackendPoolSummary,
} from "../api/transform";

// ---------------------------------------------------------------------------
// List all workers (15s polling)
// ---------------------------------------------------------------------------

export function useWorkers() {
  return useQuery<Worker[]>({
    queryKey: ["workers"],
    queryFn: async () => {
      const res = await get<{ items?: BackendHeartbeat[] } | BackendHeartbeat[]>(
        "/workers",
      );
      const items = Array.isArray(res) ? res : (res.items ?? []);
      return items
        .map(mapHeartbeatToWorker)
        .filter((w): w is Worker => !!w);
    },
    refetchInterval: 15_000,
  });
}

// ---------------------------------------------------------------------------
// Single worker detail
// ---------------------------------------------------------------------------

export function useWorker(id: string | null | undefined) {
  return useQuery<Worker>({
    queryKey: ["worker", id],
    queryFn: async () => {
      if (!id) throw new Error("worker id is required");
      const res = await get<BackendHeartbeat>(`/workers/${encodeURIComponent(id)}`);
      const worker = mapHeartbeatToWorker(res);
      if (!worker) throw new Error("worker not found");
      return worker;
    },
    enabled: !!id,
    staleTime: 10_000,
  });
}

// ---------------------------------------------------------------------------
// Worker recent jobs
// ---------------------------------------------------------------------------

export function useWorkerJobs(workerId: string | null | undefined) {
  return useQuery<Job[]>({
    queryKey: ["worker-jobs", workerId],
    queryFn: async () => {
      if (!workerId) return [];
      const res = await get<{ items?: any[] }>(`/workers/${encodeURIComponent(workerId)}/jobs?limit=20`);
      return res.items ?? [];
    },
    enabled: !!workerId,
    staleTime: 15_000,
  });
}

// ---------------------------------------------------------------------------
// Pool hooks
// ---------------------------------------------------------------------------

export function usePools() {
  return useQuery<Pool[]>({
    queryKey: ["pools"],
    queryFn: async () => {
      const res = await get<{ items?: BackendPoolSummary[] }>("/pools");
      return (res.items ?? []).map((bp) => mapPoolResponse(bp));
    },
    staleTime: 15_000,
    refetchInterval: 30_000,
  });
}

export function usePool(name: string | null | undefined) {
  return useQuery<Pool>({
    queryKey: ["pool", name],
    queryFn: async () => {
      if (!name) throw new Error("pool name is required");
      const res = await get<BackendPoolSummary>(`/pools/${encodeURIComponent(name)}`);
      return mapPoolResponse(res);
    },
    enabled: !!name,
  });
}

// ---------------------------------------------------------------------------
// WebSocket event invalidation
// ---------------------------------------------------------------------------
// Listens for "cordum:event" CustomEvents on window. When a worker.* event
// arrives, the workers query cache is invalidated so React Query refetches.
// Wire the WebSocket handler to dispatch:
//   window.dispatchEvent(new CustomEvent("cordum:event", { detail: { type: "worker.heartbeat" } }))
// ---------------------------------------------------------------------------

export function useWorkerEvents() {
  const queryClient = useQueryClient();

  useEffect(() => {
    function handler(e: Event) {
      const detail = (e as CustomEvent).detail;
      if (detail?.type?.startsWith("worker.")) {
        queryClient.invalidateQueries({ queryKey: ["workers"] });
        queryClient.invalidateQueries({ queryKey: ["pools"] });
      }
    }

    window.addEventListener("cordum:event", handler);
    return () => window.removeEventListener("cordum:event", handler);
  }, [queryClient]);
}
