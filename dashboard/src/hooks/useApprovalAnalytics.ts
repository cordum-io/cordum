import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import {
  mapApprovalAnalytics,
  type BackendApprovalAnalyticsResponse,
} from "../api/transform";
import type {
  ApprovalAnalyticsGroupBy,
  ApprovalAnalyticsResponse,
  ApprovalAnalyticsWindow,
} from "../api/types";

export interface UseApprovalAnalyticsArgs {
  window: ApprovalAnalyticsWindow;
  groupBy?: ApprovalAnalyticsGroupBy;
  limit?: number;
}

function buildQuery(args: UseApprovalAnalyticsArgs): string {
  const params = new URLSearchParams();
  params.set("window", args.window);
  if (args.groupBy) params.set("group_by", args.groupBy);
  if (typeof args.limit === "number") {
    params.set("limit", String(Math.max(1, Math.min(50, args.limit))));
  }
  return params.toString();
}

export function useApprovalAnalytics(args: UseApprovalAnalyticsArgs) {
  return useQuery<ApprovalAnalyticsResponse, Error>({
    queryKey: [
      "governance",
      "approvals",
      "analytics",
      { window: args.window, groupBy: args.groupBy ?? "overall", limit: args.limit },
    ],
    queryFn: async () => {
      const res = await get<BackendApprovalAnalyticsResponse>(
        `/governance/approvals/analytics?${buildQuery(args)}`,
      );
      return mapApprovalAnalytics(res);
    },
    // Matches the server-side 30s per-tenant cache so a dashboard
    // that polls faster than TTL sees cached responses instead of
    // thrashing the decision-log index.
    staleTime: 30_000,
    refetchOnWindowFocus: true,
  });
}

export const __approvalAnalyticsInternal = { buildQuery };
