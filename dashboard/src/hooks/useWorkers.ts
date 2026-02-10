import { useEffect } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { get } from "../api/client";
import type { Job, Worker } from "../api/types";
import {
  mapHeartbeatToWorker,
  mapJobRecord,
  type BackendHeartbeat,
  type BackendJobRecord,
} from "../api/transform";

// ---------------------------------------------------------------------------
// List all workers (15s polling)
// ---------------------------------------------------------------------------

export function useWorkers() {
  return useQuery<Worker[]>({
    queryKey: ["workers"],
    queryFn: async () => {
      const res = await get<BackendHeartbeat[]>("/workers");
      return (res ?? [])
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
      const res = await get<BackendHeartbeat[]>("/workers");
      const match = (res ?? [])
        .map(mapHeartbeatToWorker)
        .find((w): w is Worker => !!w && w.id === id);
      if (!match) throw new Error("worker not found");
      return match;
    },
    enabled: !!id,
  });
}

// ---------------------------------------------------------------------------
// Worker recent jobs
// ---------------------------------------------------------------------------

export function useWorkerJobs(workerId: string | null | undefined) {
  return useQuery<Job[]>({
    queryKey: ["worker-jobs", workerId],
    queryFn: async () => {
      // Backend has no worker_id filter on /jobs — fetch recent jobs
      // and return them as "recent system jobs" for the drawer context
      const res = await get<{ items: BackendJobRecord[] }>("/jobs?limit=20");
      return (res.items ?? []).map(mapJobRecord);
    },
    enabled: !!workerId,
    staleTime: 15_000,
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
      }
    }

    window.addEventListener("cordum:event", handler);
    return () => window.removeEventListener("cordum:event", handler);
  }, [queryClient]);
}
