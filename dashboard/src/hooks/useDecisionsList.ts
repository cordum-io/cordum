import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import { listPolicyDecisions } from "@/api/generated/policy/policy";
import type { DecisionListResponse } from "@/api/generated/model/decisionListResponse";
import type { ListPolicyDecisionsParams } from "@/api/generated/model/listPolicyDecisionsParams";
import { queryKeys } from "@/lib/queryKeys";

// React Query wrapper around the generated `listPolicyDecisions` mutation
// (Backend 5b's `GET /api/v1/policy/decisions`). Filter shape mirrors
// `ListPolicyDecisionsParams` verbatim so DecisionsFilterBar can pass nuqs
// URL state straight through. Results are paginated via the response's
// `next_cursor`; the page-and-cursor handling lives in the caller (filter
// bar tracks current cursor + DecisionsPage feeds it back).

const STALE_MS = 10_000;

export function useDecisionsList(
  params: ListPolicyDecisionsParams,
): UseQueryResult<DecisionListResponse> {
  return useQuery<DecisionListResponse>({
    queryKey: queryKeys.policyStudioDecisions.list(params),
    queryFn: ({ signal }) => listPolicyDecisions(params, signal),
    staleTime: STALE_MS,
  });
}
