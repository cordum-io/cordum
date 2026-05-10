import { useMemo } from "react";
import {
  useInfiniteQuery,
  type UseInfiniteQueryResult,
} from "@tanstack/react-query";
import { listPolicyDecisions } from "@/api/generated/policy/policy";
import type { Decision } from "@/api/generated/model/decision";
import type { DecisionListResponse } from "@/api/generated/model/decisionListResponse";
import type { ListPolicyDecisionsParams } from "@/api/generated/model/listPolicyDecisionsParams";
import { queryKeys } from "@/lib/queryKeys";

// React Query wrapper around the generated `listPolicyDecisions` query
// (Backend 5b's `GET /api/v1/policy/decisions`). Cursor pagination via
// the response's `next_cursor` -- callers render a "Load more" button and
// invoke `fetchNextPage()`. Filter shape mirrors `ListPolicyDecisionsParams`
// verbatim so DecisionsFilterBar can pass nuqs URL state straight through.

const STALE_MS = 10_000;

export interface UseDecisionsListResult {
  items: Decision[];
  hasMore: boolean;
  fetchNextPage: UseInfiniteQueryResult<unknown>["fetchNextPage"];
  isFetching: boolean;
  isFetchingNextPage: boolean;
  isError: boolean;
  refetch: UseInfiniteQueryResult<unknown>["refetch"];
}

export function useDecisionsList(
  params: ListPolicyDecisionsParams,
): UseDecisionsListResult {
  const query = useInfiniteQuery<DecisionListResponse>({
    queryKey: queryKeys.policyStudioDecisions.list(params),
    queryFn: ({ signal, pageParam }) =>
      listPolicyDecisions(
        { ...params, cursor: (pageParam as string | undefined) || undefined },
        signal,
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) =>
      lastPage.has_more && lastPage.next_cursor ? lastPage.next_cursor : undefined,
    staleTime: STALE_MS,
  });

  const items = useMemo<Decision[]>(() => {
    if (!query.data) return [];
    return query.data.pages.flatMap((page) => page.items);
  }, [query.data]);

  return {
    items,
    hasMore: query.hasNextPage ?? false,
    fetchNextPage: query.fetchNextPage,
    isFetching: query.isFetching,
    isFetchingNextPage: query.isFetchingNextPage,
    isError: query.isError,
    refetch: query.refetch,
  };
}
