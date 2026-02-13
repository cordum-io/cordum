import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useEventStore } from "../state/events";
import type { StreamEvent, WorkflowRun, RunStatus } from "../api/types";
import type { WorkflowRunListResponse } from "./useWorkflows";
import { logger } from "../lib/logger";

// ---------------------------------------------------------------------------
// Event type detection
// ---------------------------------------------------------------------------

const RUN_EVENT_TYPES = new Set([
  "job.result",
  "job.result.succeeded",
  "job.result.failed",
  "job.result.cancelled",
  "job.submit",
  "job.progress",
  "run.status_changed",
  "run.step_status_changed",
  "workflow.run_completed",
  "approval.requested",
]);

function isRunEvent(event: StreamEvent): boolean {
  for (const prefix of RUN_EVENT_TYPES) {
    if (event.type === prefix || event.type.startsWith(`${prefix}.`)) return true;
  }
  return false;
}

function extractRunId(event: StreamEvent): string | undefined {
  const p = event.payload ?? {};
  return (p.runId ?? p.run_id ?? p.workflowRunId ?? p.workflow_run_id) as string | undefined;
}

function extractStepId(event: StreamEvent): string | undefined {
  const p = event.payload ?? {};
  return (p.stepId ?? p.step_id) as string | undefined;
}

function extractStatus(event: StreamEvent): RunStatus | undefined {
  const p = event.payload ?? {};
  const raw = (p.status ?? p.newStatus ?? p.new_status) as string | undefined;
  return raw as RunStatus | undefined;
}

function extractJobId(event: StreamEvent): string | undefined {
  const p = event.payload ?? {};
  return (p.jobId ?? p.job_id) as string | undefined;
}

// ---------------------------------------------------------------------------
// Cache patcher
// ---------------------------------------------------------------------------

function patchRunInCache(
  queryClient: ReturnType<typeof useQueryClient>,
  eventRunId: string,
  patcher: (run: WorkflowRun) => WorkflowRun,
) {
  // Patch individual run cache
  queryClient.setQueriesData<WorkflowRun>(
    { queryKey: ["workflow-run"] },
    (old) => {
      if (!old || old.id !== eventRunId) return old;
      return patcher(old);
    },
  );

  // Patch run arrays (workflow-specific runs)
  queryClient.setQueriesData<WorkflowRun[]>(
    { queryKey: ["workflow-runs"] },
    (old) => {
      if (!old || !Array.isArray(old)) return old;
      let changed = false;
      const updated = old.map((r) => {
        if (r.id !== eventRunId) return r;
        changed = true;
        return patcher(r);
      });
      return changed ? updated : old;
    },
  );

  // Patch active runs response shape
  queryClient.setQueriesData<WorkflowRunListResponse>(
    { queryKey: ["workflow-runs", "active"] },
    (old) => {
      if (!old?.items) return old;
      let changed = false;
      const updated = old.items.map((r) => {
        if (r.id !== eventRunId) return r;
        changed = true;
        return patcher(r);
      });
      return changed ? { ...old, items: updated } : old;
    },
  );
}

// ---------------------------------------------------------------------------
// Hook: useRunStream
// ---------------------------------------------------------------------------

/**
 * Subscribes to the WebSocket event store and optimistically patches
 * React Query cached run data for instant DAG/strip updates.
 *
 * @param runId - Filter to a specific run, or null to listen to all runs
 */
export function useRunStream(runId: string | null | undefined): void {
  const queryClient = useQueryClient();
  const lastSeenRef = useRef<string | null>(null);

  useEffect(() => {
    const unsub = useEventStore.subscribe((state) => {
      const events = state.events;
      if (events.length === 0) return;

      const latest = events[0];
      // Skip if we already processed this event
      if (latest.id === lastSeenRef.current) return;
      lastSeenRef.current = latest.id;

      if (!isRunEvent(latest)) return;

      const eventRunId = extractRunId(latest);
      if (!eventRunId) return;

      // If filtering by runId, skip events for other runs
      if (runId && eventRunId !== runId) return;

      logger.debug("run-stream", "Processing run event", {
        type: latest.type,
        runId: eventRunId,
      });

      const stepId = extractStepId(latest);
      const newStatus = extractStatus(latest);
      const jobId = extractJobId(latest);

      // Step-level status change
      if (stepId && newStatus) {
        patchRunInCache(queryClient, eventRunId, (run) => ({
          ...run,
          updatedAt: latest.timestamp,
          steps: run.steps.map((s) => {
            if (s.id !== stepId) return s;
            return {
              ...s,
              status: newStatus,
              completedAt:
                newStatus === "succeeded" || newStatus === "failed" || newStatus === "timed_out"
                  ? latest.timestamp
                  : s.completedAt,
              error:
                newStatus === "failed"
                  ? ((latest.payload?.errorMessage as string) ?? s.error)
                  : s.error,
            };
          }),
        }));
        return;
      }

      // Job result — map back to step via jobId match
      if (jobId && latest.type.startsWith("job.result") && newStatus) {
        patchRunInCache(queryClient, eventRunId, (run) => ({
          ...run,
          updatedAt: latest.timestamp,
          steps: run.steps.map((s) => {
            const sJobId =
              (s.config?.jobId as string) ??
              (s.output?.jobId as string) ??
              (s.output?.job_id as string);
            if (sJobId !== jobId) return s;
            return {
              ...s,
              status: newStatus,
              completedAt:
                newStatus === "succeeded" || newStatus === "failed"
                  ? latest.timestamp
                  : s.completedAt,
              error:
                newStatus === "failed"
                  ? ((latest.payload?.errorMessage as string) ?? s.error)
                  : s.error,
            };
          }),
        }));
        return;
      }

      // Run-level status change
      if (newStatus && !stepId) {
        patchRunInCache(queryClient, eventRunId, (run) => ({
          ...run,
          status: newStatus,
          updatedAt: latest.timestamp,
          completedAt:
            newStatus === "succeeded" || newStatus === "failed" || newStatus === "cancelled"
              ? latest.timestamp
              : run.completedAt,
        }));
        return;
      }
    });

    return unsub;
  }, [runId, queryClient]);
}

/** @internal exported for unit tests */
export const __runStreamInternal = {
  isRunEvent,
  extractRunId,
  extractStepId,
  extractStatus,
  extractJobId,
  patchRunInCache,
};
