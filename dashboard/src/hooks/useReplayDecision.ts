import { useMutation, useQueryClient, type UseMutationResult } from "@tanstack/react-query";
import { replayPolicyDecisions } from "@/api/generated/policy/policy";
import type { Decision } from "@/api/generated/model/decision";
import { DecisionType } from "@/api/generated/model/decisionType";
import type { PolicyReplayRequest } from "@/api/generated/model/policyReplayRequest";
import type { PolicyReplayResponse } from "@/api/generated/model/policyReplayResponse";
import { logger } from "@/lib/logger";

export interface ReplayDecisionResult {
  was: DecisionType;
  now: DecisionType;
  bundleVersion: string;
  changed: boolean;
}

// /api/v1/policy/replay is bulk time-range replay rather than per-decision —
// the canonical shape (handlers_policy_replay.go) takes {from, to, filters,
// use_current_policy, max_jobs}. To approximate "what would THIS decision
// be now?" we send a 1-second window around decision.timestamp,
// constrain `filters.original_decision` to the decision's own type, and
// cap max_jobs at 1. The result lets us derive was/now without backend
// churn; spec § "no 1:1 per-decision lane" is acknowledged in chat
// (msg-cde49c4f).
const WINDOW_MS = 1_000;

function buildRequest(decision: Decision): PolicyReplayRequest {
  const ts = Date.parse(decision.timestamp);
  const fromISO = new Date(ts).toISOString();
  const toISO = new Date(ts + WINDOW_MS).toISOString();
  return {
    from: fromISO,
    to: toISO,
    filters: { original_decision: decision.type },
    use_current_policy: true,
    max_jobs: 1,
  };
}

function deriveResult(decision: Decision, response: PolicyReplayResponse): ReplayDecisionResult {
  const bundleVersion = response.policy_snapshot ?? decision.bundle_version ?? "";
  // changes[] only carries decisions whose outcome flipped; if the entry
  // is unchanged the response surfaces the count via `summary.unchanged`.
  // Either signal is sufficient; if the backend ever returns both empty
  // we fall back to "no change" so the UI renders deterministically.
  const change = response.changes?.[0];
  if (change && change.new_decision) {
    const now = change.new_decision as DecisionType;
    return {
      was: decision.type,
      now,
      bundleVersion,
      changed: now !== decision.type,
    };
  }
  return {
    was: decision.type,
    now: decision.type,
    bundleVersion,
    changed: false,
  };
}

export function useReplayDecision(): UseMutationResult<
  ReplayDecisionResult,
  Error,
  Decision
> {
  const queryClient = useQueryClient();
  return useMutation<ReplayDecisionResult, Error, Decision>({
    mutationFn: async (decision: Decision) => {
      const request = buildRequest(decision);
      const response = await replayPolicyDecisions(request);
      logger.info("decisions-replay", "replay completed", {
        replay_id: response.replay_id,
        rule_id: decision.rule_id,
      });
      return deriveResult(decision, response);
    },
    onSuccess: () => {
      // The active decisions cache may render stale outcomes after a
      // policy replay — invalidate so list/stream refetch.
      void queryClient.invalidateQueries({
        queryKey: ["policy-studio-decisions"],
      });
    },
    onError: (err) => {
      logger.warn("decisions-replay", "replay failed", { err: String(err) });
    },
  });
}
