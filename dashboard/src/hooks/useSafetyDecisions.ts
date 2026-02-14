import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import { mapJobRecord, type BackendJobRecord } from "../api/transform";
import type { JobStatus } from "../api/types";
import { useEventStore, type SafetyDecisionEvent } from "../state/events";

const MAX_DECISIONS = 100;
const REFRESH_INTERVAL_MS = 8_000;

function toEpochMs(timestamp: string): number {
  const parsed = Date.parse(timestamp);
  if (Number.isFinite(parsed)) return parsed;
  return 0;
}

function decisionFingerprint(event: SafetyDecisionEvent): string {
  return [
    event.timestamp,
    event.topic,
    event.decision,
    event.matchedRule ?? "",
    event.evalTimeMs ?? "",
  ].join("|");
}

function inferDecisionFromJobStatus(status: JobStatus): SafetyDecisionEvent["decision"] | null {
  switch (status) {
    case "denied":
      return "deny";
    case "approval_required":
      return "require_approval";
    default:
      return null;
  }
}

function mapJobRecordToSafetyDecision(record: BackendJobRecord): SafetyDecisionEvent | null {
  const mapped = mapJobRecord(record);
  const decision = mapped.safetyDecision?.type ?? inferDecisionFromJobStatus(mapped.status);
  if (!decision) return null;
  return {
    id: `job:${mapped.id}:${mapped.updatedAt}`,
    timestamp: mapped.updatedAt,
    topic: mapped.topic || "job.unknown",
    decision,
    matchedRule: mapped.safetyDecision?.matchedRule ?? record.safety_rule_id,
    evalTimeMs: mapped.safetyDecision?.evalTimeMs,
  };
}

function mergeSafetyDecisionEvents(
  live: SafetyDecisionEvent[],
  history: SafetyDecisionEvent[],
  limit: number,
): SafetyDecisionEvent[] {
  const sorted = [...live, ...history]
    .filter((event) => !!event?.timestamp && !!event?.decision)
    .sort((a, b) => toEpochMs(b.timestamp) - toEpochMs(a.timestamp));

  const out: SafetyDecisionEvent[] = [];
  const seen = new Set<string>();
  for (const event of sorted) {
    const key = decisionFingerprint(event);
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(event);
    if (out.length >= limit) break;
  }
  return out;
}

export function useSafetyDecisions(limit = MAX_DECISIONS) {
  const boundedLimit = Math.max(1, Math.min(limit, MAX_DECISIONS));
  const liveDecisions = useEventStore((s) => s.safetyDecisions);

  const historyQuery = useQuery<SafetyDecisionEvent[]>({
    queryKey: ["jobs", "safety-decisions", boundedLimit],
    queryFn: async () => {
      const res = await get<{ items: BackendJobRecord[] }>(`/jobs?limit=${boundedLimit}`);
      return (res.items ?? [])
        .map(mapJobRecordToSafetyDecision)
        .filter((item): item is SafetyDecisionEvent => item !== null);
    },
    staleTime: 5_000,
    refetchInterval: REFRESH_INTERVAL_MS,
  });

  const historyDecisions = historyQuery.data ?? [];
  const decisions = useMemo(
    () => mergeSafetyDecisionEvents(liveDecisions, historyDecisions, boundedLimit),
    [historyDecisions, liveDecisions, boundedLimit],
  );

  return {
    decisions,
    liveDecisions,
    historyDecisions,
    isLoading: historyQuery.isLoading && decisions.length === 0,
    isFetching: historyQuery.isFetching,
    isError: historyQuery.isError,
    error: historyQuery.error,
    refetch: historyQuery.refetch,
  };
}

/** @internal exported for unit tests */
export const __safetyDecisionInternal = {
  MAX_DECISIONS,
  REFRESH_INTERVAL_MS,
  toEpochMs,
  decisionFingerprint,
  inferDecisionFromJobStatus,
  mapJobRecordToSafetyDecision,
  mergeSafetyDecisionEvents,
};
