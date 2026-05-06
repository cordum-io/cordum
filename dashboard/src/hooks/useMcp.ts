// React Query hooks for the MCP governance dashboard.
//
// Wraps the two backend endpoints added in task-134647cd:
//   GET /api/v1/mcp/usage    — agents×tools heatmap aggregation
//   GET /api/v1/mcp/outbound — paginated outbound call log
//
// Approval queue + mutations live in `useMcpApprovals.ts` and are
// re-exported here so the MCP page only has to import from one
// module. This file deliberately stays narrow: shaping query params,
// driving React Query, surfacing typed responses. No render logic, no
// state.
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import type {
  MCPOutboundResponse,
  MCPUsageResponse,
  SignatureStatus,
} from "../api/types";

export {
  isMcpApprovalsUnavailableError,
  useApproveMcp,
  useMcpApproval,
  useMcpApprovals as useMcpPendingApprovals,
  useRejectMcp,
  shortArgsHash,
} from "./useMcpApprovals";
export type { McpApproval, McpApprovalStatus } from "./useMcpApprovals";

// ---------------------------------------------------------------------------
// Query-key factory — kept colocated so a future cache-invalidation
// caller doesn't have to chase the constants across files.
// ---------------------------------------------------------------------------

export const mcpQueryKeys = {
  usage: (params: UsageQueryParams) => ["mcp", "usage", params] as const,
  outbound: (params: OutboundQueryParams) => ["mcp", "outbound", params] as const,
};

// ---------------------------------------------------------------------------
// Usage heatmap
// ---------------------------------------------------------------------------

export interface UsageQueryParams {
  sinceMs?: number;
  untilMs?: number;
  agent?: string;
  tool?: string;
}

export interface UseMcpUsageOptions {
  enabled?: boolean;
  refetchIntervalMs?: number;
}

export function useMcpUsage(params: UsageQueryParams, opts: UseMcpUsageOptions = {}) {
  return useQuery<MCPUsageResponse>({
    queryKey: mcpQueryKeys.usage(params),
    queryFn: () => get<MCPUsageResponse>(`/mcp/usage${buildQuery(params)}`),
    staleTime: 30_000,
    refetchInterval: opts.refetchIntervalMs ?? 60_000,
    refetchIntervalInBackground: false,
    enabled: opts.enabled ?? true,
  });
}

// ---------------------------------------------------------------------------
// Outbound call log (infinite query for cursor pagination)
// ---------------------------------------------------------------------------

export interface OutboundQueryParams {
  sinceMs?: number;
  untilMs?: number;
  agent?: string;
  server?: string;
  sigStatus?: SignatureStatus | "all";
  limit?: number;
}

export interface UseMcpOutboundOptions {
  enabled?: boolean;
  refetchIntervalMs?: number;
}

export function useMcpOutbound(params: OutboundQueryParams, opts: UseMcpOutboundOptions = {}) {
  return useInfiniteQuery<MCPOutboundResponse>({
    queryKey: mcpQueryKeys.outbound(params),
    queryFn: ({ pageParam }) => {
      const cursor = typeof pageParam === "string" ? pageParam : "";
      return get<MCPOutboundResponse>(
        `/mcp/outbound${buildQuery({ ...params, cursor: cursor || undefined })}`,
      );
    },
    initialPageParam: "",
    getNextPageParam: (last) => {
      // Empty next_cursor signals end-of-range; React Query treats
      // undefined as "no more pages" so explicit undefined is correct.
      return last.next_cursor && last.next_cursor !== "" ? last.next_cursor : undefined;
    },
    staleTime: 30_000,
    refetchInterval: opts.refetchIntervalMs ?? 60_000,
    refetchIntervalInBackground: false,
    enabled: opts.enabled ?? true,
  });
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function buildQuery(params: object): string {
  const mapping: Record<string, string> = {
    sinceMs: "since",
    untilMs: "until",
    sigStatus: "sig_status",
  };
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === "") continue;
    const wireKey = mapping[key] ?? key;
    search.set(wireKey, String(value));
  }
  const qs = search.toString();
  return qs ? `?${qs}` : "";
}
