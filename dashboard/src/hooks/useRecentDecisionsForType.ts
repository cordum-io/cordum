import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import { getPolicyAudit } from "@/api/generated/policy/policy";
import type { PolicyAuditEntry } from "@/api/generated/model/policyAuditEntry";
import type { PolicyAuditEnvelope } from "@/api/generated/model/policyAuditEnvelope";
import { RuleType } from "@/api/generated/model/ruleType";

const SIMULATOR_EVENT_LIMIT = 100;
const SIMULATOR_CACHE_MS = 60_000;

export interface RecentDecisionsResult {
  entries: PolicyAuditEntry[];
  total: number;
  hasMore: boolean;
}

/**
 * Maps a unified RuleType to the audit endpoint's `type` filter so the
 * simulator only pulls entries the active rule could plausibly match.
 * Backend's `type=output` is the legacy output-rule audit log; for the
 * Policy Studio surfaces we map velocity/input/edge to the parent
 * `resource_type` the gateway emits on those events.
 */
function ruleTypeToAuditFilter(type: RuleType): string {
  switch (type) {
    case RuleType.input:
      return "input";
    case RuleType.output:
      return "output";
    case RuleType.velocity:
      return "velocity";
    case RuleType.edge:
      return "edge";
  }
}

/**
 * Read-only fetch of the last 100 audit/decision events for the active
 * rule type. Cached for 60s so toggling between Form and YAML tabs
 * doesn't re-trigger the network call. The simulator panel composes
 * this with `useEvaluateRule` to evaluate the draft against each event.
 *
 * Scope filtering happens client-side in the simulator panel (the audit
 * endpoint has no `scope` query param); we still pull `limit=100` per
 * type because the simulator's "100-event" target operates on the
 * type-bucket, not the scope-bucket.
 */
export function useRecentDecisionsForType(
  ruleType: RuleType | null | undefined,
): UseQueryResult<RecentDecisionsResult> {
  return useQuery<RecentDecisionsResult>({
    queryKey: ["policy-studio-simulator", "recent-decisions", ruleType ?? "missing"],
    enabled: typeof ruleType === "string" && ruleType.length > 0,
    queryFn: async () => {
      const env = await getPolicyAudit({
        limit: SIMULATOR_EVENT_LIMIT,
        type: ruleType ? ruleTypeToAuditFilter(ruleType) : undefined,
      });
      return normalizeEnvelope(env);
    },
    staleTime: SIMULATOR_CACHE_MS,
  });
}

function normalizeEnvelope(env: PolicyAuditEnvelope): RecentDecisionsResult {
  const entries = Array.isArray(env.items) ? env.items : [];
  return {
    entries,
    total: typeof env.total === "number" ? env.total : entries.length,
    hasMore: env.has_more === true,
  };
}

export const SIMULATOR_EVENT_LIMIT_VALUE = SIMULATOR_EVENT_LIMIT;
export const SIMULATOR_CACHE_MS_VALUE = SIMULATOR_CACHE_MS;
