// useGovernanceHealth — React Query hook for the Command Center
// governance score widget.
//
// 60s staleTime matches the backend's per-tenant in-memory cache so the
// dashboard polls no faster than the upstream recomputes. A 403 from
// the endpoint (non-admin caller) resolves to `{data: undefined, error}`
// without a toast — the widget chooses to render nothing in that case.
import { useQuery } from "@tanstack/react-query";
import { get, ApiError } from "../api/client";
import type { GovernanceHealth } from "../api/types";

export const governanceHealthQueryKey = ["governance-health"] as const;

export function useGovernanceHealth() {
  return useQuery<GovernanceHealth, ApiError>({
    queryKey: governanceHealthQueryKey,
    queryFn: () => get<GovernanceHealth>("/governance/health"),
    staleTime: 60_000,
    refetchInterval: 60_000,
    retry: (failureCount, error) => {
      // 401/403 mean the caller isn't an admin — do not retry, do not
      // spam logs. Other network errors get 1 retry.
      if (error instanceof ApiError && (error.status === 401 || error.status === 403)) {
        return false;
      }
      return failureCount < 1;
    },
  });
}
