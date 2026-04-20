import { useMemo } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import { FEATURE_FLAGS } from "../config/flags";
import type {
  GovernanceDecision,
  GovernanceDecisionsResponse,
  GovernanceVerdict,
} from "../api/types";
import {
  mapGovernanceDecision,
  type BackendGovernanceDecision,
} from "../api/transform";
import { getGovernanceMockResponse } from "../mocks/handlers/governance";

export interface GovernanceDecisionFilters {
  verdict?: GovernanceVerdict;
  ruleId?: string;
  agentId?: string;
  since?: string;
  until?: string;
}

export interface UseGovernanceDecisionsArgs {
  jobId?: string;
  runId?: string;
  filters?: GovernanceDecisionFilters;
  limit?: number;
}

interface BackendGovernanceDecisionsResponse {
  items?: BackendGovernanceDecision[];
  nextCursor?: string | null;
  next_cursor?: string | null;
}

function buildGovernanceDecisionQueryString(
  args: UseGovernanceDecisionsArgs,
  cursor?: string,
): string {
  const params = new URLSearchParams();
  const limit = Math.max(1, args.limit ?? 50);

  if (args.jobId) params.set("job_id", args.jobId);
  if (args.runId) params.set("run_id", args.runId);
  if (args.filters?.verdict) params.set("verdict", args.filters.verdict);
  if (args.filters?.ruleId) params.set("rule_id", args.filters.ruleId);
  if (args.filters?.agentId) params.set("agent_id", args.filters.agentId);
  if (args.filters?.since) params.set("since", args.filters.since);
  if (args.filters?.until) params.set("until", args.filters.until);
  params.set("limit", String(limit));
  if (cursor) params.set("cursor", cursor);

  return params.toString();
}

function normalizeGovernancePage(
  response: BackendGovernanceDecisionsResponse,
): GovernanceDecisionsResponse {
  return {
    items: (response.items ?? [])
      .map(mapGovernanceDecision)
      .filter((item): item is GovernanceDecision => item !== null),
    nextCursor: response.nextCursor ?? response.next_cursor ?? undefined,
  };
}

function sortGovernanceChronologically(
  items: GovernanceDecision[],
): GovernanceDecision[] {
  return [...items].sort(
    (a, b) => Date.parse(a.timestamp) - Date.parse(b.timestamp),
  );
}

export function useGovernanceDecisions(
  args: UseGovernanceDecisionsArgs = {},
) {
  const enabled = Boolean(args.jobId || args.runId);

  return useInfiniteQuery({
    queryKey: [
      "governance",
      "decisions",
      {
        jobId: args.jobId,
        runId: args.runId,
        filters: args.filters,
      },
    ],
    queryFn: async ({ pageParam }) => {
      if (FEATURE_FLAGS.governanceTimelineMocks) {
        return getGovernanceMockResponse(
          {
            jobId: args.jobId,
            runId: args.runId,
            filters: args.filters,
            limit: args.limit,
          },
          typeof pageParam === "string" ? pageParam : undefined,
        );
      }
      const query = buildGovernanceDecisionQueryString(
        args,
        typeof pageParam === "string" ? pageParam : undefined,
      );
      const response = await get<BackendGovernanceDecisionsResponse>(
        `/governance/decisions?${query}`,
      );
      return normalizeGovernancePage(response);
    },
    getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
    initialPageParam: undefined as string | undefined,
    staleTime: 30_000,
    enabled,
  });
}

export function useGovernanceDecisionsFlat(
  args: UseGovernanceDecisionsArgs = {},
) {
  const query = useGovernanceDecisions(args);

  const items = useMemo(
    () =>
      sortGovernanceChronologically(
        query.data?.pages.flatMap((page) => page.items) ?? [],
      ),
    [query.data],
  );

  return {
    ...query,
    items,
  };
}

/** @internal exported for unit tests */
export const __governanceDecisionsInternal = {
  buildGovernanceDecisionQueryString,
  normalizeGovernancePage,
  sortGovernanceChronologically,
};
